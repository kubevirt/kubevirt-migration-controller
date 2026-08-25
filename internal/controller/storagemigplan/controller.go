/*
Copyright 2025 The KubeVirt Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package storagemigplan

import (
	"context"
	"fmt"
	"slices"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"

	logr "github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	vmIndexKey                 = "spec.virtualMachines.name"
	migrationNameIndexKey      = "spec.virtualMachineStorageMigrationPlanRef.name"
	RefreshStartTimeAnnotation = "migration.kubevirt.io/refresh-start-time"
	RefreshEndTimeAnnotation   = "migration.kubevirt.io/refresh-end-time"
)

// StorageMigPlanReconciler reconciles a VirtualMachineStorageMigrationPlan object
type StorageMigPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	record.EventRecorder
	Log logr.Logger
}

// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrationplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrationplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrationplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=list;watch
// +kubebuilder:rbac:groups=kubevirt.io,resources=kubevirts,verbs=list;watch
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrations,verbs=get;list;watch
func (r *StorageMigPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log
	log.V(5).Info("Reconciling VirtualMachineStorageMigrationPlan", "name", req.NamespacedName)
	// Fetch the MigPlan instance
	plan := &migrations.VirtualMachineStorageMigrationPlan{}
	err := r.Get(ctx, req.NamespacedName, plan)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	origPlan := plan.DeepCopy()

	if plan.DeletionTimestamp == nil {
		completed, err := r.isPlanCompleted(ctx, plan)
		if err != nil {
			return reconcile.Result{}, err
		}
		if completed {
			log.V(3).Info("Skipping reconcile for completed plan", "plan", plan.Name)
			return reconcile.Result{}, nil
		}
	}

	if plan.DeletionTimestamp != nil {
		active, err := r.hasActiveMigrations(ctx, plan)
		if err != nil {
			return reconcile.Result{}, err
		}
		if active {
			log.Info("Plan deletion blocked: active migrations exist")
			// Emit DeletionBlocked condition/event only once. Without this guard every
			// reconcile while deletion is held would spam Events and rewrite status.
			if !plan.Status.HasCondition(migrations.DeletionBlocked) {
				plan.Status.SetCondition(migrations.Condition{
					Type:     migrations.DeletionBlocked,
					Status:   corev1.ConditionTrue,
					Category: migrations.Advisory,
					Message:  "plan deletion is blocked by active migrations; in-flight migrations will continue until complete",
				})
				r.Event(plan, corev1.EventTypeNormal, "DeletionBlocked", "plan deletion is blocked by active migrations")
			}
		} else {
			plan.RemoveFinalizer(migrations.VirtualMachineStorageMigrationPlanFinalizer)
		}
	} else {
		if !migrations.HasFinalizer(plan, migrations.VirtualMachineStorageMigrationPlanFinalizer) {
			plan.AddFinalizer(migrations.VirtualMachineStorageMigrationPlanFinalizer)
		}

		plan.Status.CompletedOutOf = fmt.Sprintf("%d/%d", len(plan.Status.CompletedMigrations), len(plan.Spec.VirtualMachines))

		if plan.Status.Suffix == nil {
			// Generate suffix
			suffix := rand.String(4)
			plan.Status.Suffix = &suffix
		}

		// Validations.
		if err := r.validate(ctx, plan); err != nil {
			r.Log.Error(err, "Failed to validate VirtualMachineStorageMigrationPlan")
			plan.Status.SetReconcileFailed(err)
		} else {
			plan.Status.DeleteCondition(migrations.ReconcileFailed)
		}

		if plan.Status.HasCriticalCondition() {
			plan.Status.SetCondition(readyCondition(corev1.ConditionFalse, "plan has one or more critical conditions"))
		} else if len(plan.Status.ReadyMigrations) > 0 {
			plan.Status.SetCondition(readyCondition(corev1.ConditionTrue, "plan is ready"))
		} else {
			plan.Status.SetCondition(readyCondition(corev1.ConditionFalse, "no virtual machines are ready for storage migration"))
		}
		// Update the ready/completed migrations based on the status of the storage migrations
		if err := r.processMigrations(ctx, plan); err != nil {
			return reconcile.Result{}, err
		}

		if r.shouldUpdateRefresh(plan) {
			r.setRefreshAnnotations(plan)
		}
	}

	log.V(5).Info("Reconciling MigPlan completed")
	return r.persistPlan(ctx, origPlan, plan)
}

// persistPlan writes metadata then status once each, only when changed.
// Metadata is written first so Status().Update sees a fresh resourceVersion.
// Restore desired status after Update — the main-resource response can replace
// the in-memory object with an empty status (status is a separate subresource).
func (r *StorageMigPlanReconciler) persistPlan(ctx context.Context, orig, plan *migrations.VirtualMachineStorageMigrationPlan) (ctrl.Result, error) {
	desiredStatus := plan.Status.DeepCopy()
	compareStatus := orig.Status.DeepCopy()
	compareStatus.CopyConditionTimestampsFrom(desiredStatus)
	statusChanged := !apiequality.Semantic.DeepEqual(*compareStatus, *desiredStatus)
	metaChanged := !apiequality.Semantic.DeepEqual(orig.ObjectMeta, plan.ObjectMeta)

	if metaChanged {
		r.Log.V(5).Info("Updating MigPlan object metadata", "finalizers", plan.Finalizers, "annotations", plan.Annotations)
		if err := r.Update(ctx, plan); err != nil {
			return ctrl.Result{}, err
		}
		plan.Status = *desiredStatus
	}
	if statusChanged {
		r.Log.V(5).Info("Updating MigPlan status")
		if err := r.Status().Update(ctx, plan); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *StorageMigPlanReconciler) processMigrations(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) error {
	storageMigrationList := &migrations.VirtualMachineStorageMigrationList{}
	if err := r.List(ctx, storageMigrationList, client.MatchingFields{migrationNameIndexKey: plan.Name}); err != nil {
		if !k8serrors.IsNotFound(err) {
			r.Log.V(3).Info("No matching storage migrations found", "plan", plan.Name)
			return nil
		}
		return err
	}
	slices.SortFunc(storageMigrationList.Items, compareStorageMigrations)
	if len(storageMigrationList.Items) == 0 {
		plan.Status.SetCondition(progressCondition(corev1.ConditionFalse, "no storage migrations found"))
		return nil
	} else if plan.Status.HasCondition(migrations.Ready) {
		plan.Status.SetCondition(progressCondition(corev1.ConditionTrue, "in progress storage migrations found"))
	} else {
		plan.Status.SetCondition(progressCondition(corev1.ConditionFalse, "plan is not ready"))
	}

	if err := r.updateReadyCompletedMigrations(plan, storageMigrationList.Items[len(storageMigrationList.Items)-1]); err != nil {
		return err
	}

	if len(plan.Status.CompletedMigrations) == len(plan.Spec.VirtualMachines) && len(plan.Spec.VirtualMachines) > 0 {
		plan.Status.SetCondition(progressCondition(corev1.ConditionFalse, "all storage migrations completed"))
		plan.Status.SetCondition(readyCondition(corev1.ConditionFalse, "all storage migrations completed"))
	}
	return nil
}

func readyCondition(status corev1.ConditionStatus, message string) migrations.Condition {
	return migrations.Condition{
		Type:     migrations.Ready,
		Status:   status,
		Category: migrations.Required,
		Message:  message,
	}
}

func progressCondition(status corev1.ConditionStatus, message string) migrations.Condition {
	return migrations.Condition{
		Type:     migrations.Progressing,
		Status:   status,
		Category: migrations.Required,
		Message:  message,
	}
}

func (r *StorageMigPlanReconciler) updateReadyCompletedMigrations(plan *migrations.VirtualMachineStorageMigrationPlan, lastMigration migrations.VirtualMachineStorageMigration) error {
	readyMigrations := []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{}
	inProgressMigrations := []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{}
	completedVMs := make(map[string]struct{})
	for _, completedVM := range lastMigration.Status.CompletedMigrations {
		completedVMs[completedVM] = struct{}{}
	}
	for _, vm := range plan.Status.ReadyMigrations {
		if _, ok := completedVMs[vm.Name]; !ok {
			readyMigrations = append(readyMigrations, vm)
		} else {
			plan.Status.CompletedMigrations = append(plan.Status.CompletedMigrations, vm)
		}
	}
	for _, vm := range plan.Status.InProgressMigrations {
		if _, ok := completedVMs[vm.Name]; !ok {
			inProgressMigrations = append(inProgressMigrations, vm)
		} else {
			plan.Status.CompletedMigrations = append(plan.Status.CompletedMigrations, vm)
		}
	}
	plan.Status.ReadyMigrations = readyMigrations
	plan.Status.InProgressMigrations = inProgressMigrations
	return nil
}

func compareStorageMigrations(a, b migrations.VirtualMachineStorageMigration) int {
	if a.Status.Phase != b.Status.Phase {
		if a.Status.Phase == migrations.Completed {
			return -1
		}
		if b.Status.Phase == migrations.Completed {
			return 1
		}
	}
	return a.CreationTimestamp.Time.Compare(b.CreationTimestamp.Time)
}

func (r *StorageMigPlanReconciler) shouldUpdateRefresh(plan *migrations.VirtualMachineStorageMigrationPlan) bool {
	if _, ok := plan.Annotations[RefreshStartTimeAnnotation]; !ok {
		return false
	}
	if _, ok := plan.Annotations[RefreshEndTimeAnnotation]; ok {
		var startTime time.Time
		var endTime time.Time
		var err error
		if startTime, err = time.Parse(time.RFC3339Nano, plan.Annotations[RefreshStartTimeAnnotation]); err != nil {
			return true
		}
		if endTime, err = time.Parse(time.RFC3339Nano, plan.Annotations[RefreshEndTimeAnnotation]); err != nil {
			return true
		}
		if endTime.After(startTime) {
			return false
		}
	}
	return true
}

func (r *StorageMigPlanReconciler) setRefreshAnnotations(plan *migrations.VirtualMachineStorageMigrationPlan) {
	if plan.Annotations == nil {
		plan.Annotations = make(map[string]string)
	}
	plan.Annotations[RefreshEndTimeAnnotation] = time.Now().Format(time.RFC3339Nano)
}

// SetupWithManager sets up the controller with the Manager.
func (r *StorageMigPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller
	c, err := controller.New("kubevirt-storage-migplan-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to VirtualMachineStorageMigrationPlan
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.VirtualMachineStorageMigrationPlan{},
		&handler.TypedEnqueueRequestForObject[*migrations.VirtualMachineStorageMigrationPlan]{})); err != nil {
		return err
	}

	// Index fields used by List MatchingFields queries.
	if err := IndexFields(mgr.GetFieldIndexer()); err != nil {
		return err
	}

	// Watch for changes to VMs
	if err := c.Watch(source.Kind(mgr.GetCache(), &virtv1.VirtualMachine{},
		// Map function that enqueues requests for VirtualMachineStorageMigrationPlans that have the VM in their spec
		handler.TypedEnqueueRequestsFromMapFunc(r.getVirtualMachineMigrationPlansForVM))); err != nil {
		return err
	}

	// Watch for changes to VirtualMachineStorageMigrations
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.VirtualMachineStorageMigration{},
		handler.TypedEnqueueRequestsFromMapFunc(r.getVirtualMachineStorageMigrationsPlanForStorageMigration))); err != nil {
		return err
	}
	return nil
}

func planCompletedByStatus(plan *migrations.VirtualMachineStorageMigrationPlan) bool {
	if len(plan.Spec.VirtualMachines) == 0 {
		return false
	}
	return len(plan.Status.CompletedMigrations) == len(plan.Spec.VirtualMachines)
}

// isPlanCompleted reports whether every VM in the plan is recorded as completed
// and no child migration is still active.
func (r *StorageMigPlanReconciler) isPlanCompleted(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) (bool, error) {
	if !planCompletedByStatus(plan) {
		return false, nil
	}
	active, err := r.hasActiveMigrations(ctx, plan)
	if err != nil {
		return false, err
	}
	return !active, nil
}

// IndexFields registers field indexes required by StorageMigPlanReconciler List queries.
func IndexFields(indexer client.FieldIndexer) error {
	if err := indexer.IndexField(context.Background(), &migrations.VirtualMachineStorageMigrationPlan{}, vmIndexKey, func(rawObj client.Object) []string {
		vmStorageMigrationPlan := rawObj.(*migrations.VirtualMachineStorageMigrationPlan)
		vmNames := make([]string, 0, len(vmStorageMigrationPlan.Spec.VirtualMachines))
		for _, vm := range vmStorageMigrationPlan.Spec.VirtualMachines {
			vmNames = append(vmNames, vm.Name)
		}
		return vmNames
	}); err != nil {
		return err
	}
	return indexer.IndexField(context.Background(), &migrations.VirtualMachineStorageMigration{}, migrationNameIndexKey, func(rawObj client.Object) []string {
		migration := rawObj.(*migrations.VirtualMachineStorageMigration)
		if migration.Spec.VirtualMachineStorageMigrationPlanRef == nil || migration.Spec.VirtualMachineStorageMigrationPlanRef.Name == "" {
			return nil
		}
		return []string{migration.Spec.VirtualMachineStorageMigrationPlanRef.Name}
	})
}

func (r *StorageMigPlanReconciler) getVirtualMachineStorageMigrationsPlanForStorageMigration(ctx context.Context, migration *migrations.VirtualMachineStorageMigration) []reconcile.Request {
	if migration.Spec.VirtualMachineStorageMigrationPlanRef == nil || migration.Spec.VirtualMachineStorageMigrationPlanRef.Name == "" {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: migration.Spec.VirtualMachineStorageMigrationPlanRef.Name, Namespace: migration.Namespace}},
	}
}

func (r *StorageMigPlanReconciler) getVirtualMachineMigrationPlansForVM(ctx context.Context, vm *virtv1.VirtualMachine) []reconcile.Request {
	vmStorageMigrationPlanList := &migrations.VirtualMachineStorageMigrationPlanList{}
	requests := []reconcile.Request{}
	if err := r.List(ctx, vmStorageMigrationPlanList, client.MatchingFields{vmIndexKey: vm.Name}); err != nil {
		r.Log.Error(err, "Failed to list VirtualMachineStorageMigrationPlans for VM", "name", vm.Name)
		return nil
	}
	r.Log.V(5).Info("found virtual machine storage migration plans for VM", "vm name", vm.Name, "list", vmStorageMigrationPlanList.Items)
	for _, migplan := range vmStorageMigrationPlanList.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: migplan.Name, Namespace: migplan.Namespace}})
	}
	return requests
}

func (r *StorageMigPlanReconciler) hasActiveMigrations(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) (bool, error) {
	migrationsForPlan, err := r.listMigrationsForPlan(ctx, plan)
	if err != nil {
		return false, err
	}
	for _, m := range migrationsForPlan {
		if m.Status.Phase != migrations.Completed && m.Status.Phase != migrations.Canceled {
			r.Log.V(3).Info("Found active migration", "migration", m.Name, "phase", m.Status.Phase)
			return true, nil
		}
	}
	r.Log.V(3).Info("No active migrations found for plan", "plan", plan.Name)
	return false, nil
}

func (r *StorageMigPlanReconciler) listMigrationsForPlan(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) ([]migrations.VirtualMachineStorageMigration, error) {
	storageMigrationList := &migrations.VirtualMachineStorageMigrationList{}
	if err := r.List(ctx, storageMigrationList, client.MatchingFields{migrationNameIndexKey: plan.Name}); err != nil {
		return nil, err
	}
	return storageMigrationList.Items, nil
}
