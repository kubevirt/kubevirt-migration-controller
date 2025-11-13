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
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	vmIndexKey                 = "spec.virtualMachines.name"
	migrationNameIndexKey      = "spec.virtualMachineStorageMigrationPlanRef.name"
	RefreshStartTimeAnnotation = "migration.kubevirt.io/refresh-start-time"
	RefreshEndTimeAnnotation   = "migration.kubevirt.io/refresh-end-time"
)

// MigPlanReconciler reconciles a MigPlan object
type StorageMigPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	record.EventRecorder
	Log logr.Logger
}

// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=migplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=migplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=migplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=list;watch
// +kubebuilder:rbac:groups=kubevirt.io,resources=kubevirts,verbs=list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MigPlan object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *StorageMigPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log
	log.V(5).Info("Reconciling VirtualMachineStorageMigrationPlan", "name", req.NamespacedName)
	// Fetch the MigPlan instance
	plan := &migrations.VirtualMachineStorageMigrationPlan{}
	err := r.Get(context.TODO(), req.NamespacedName, plan)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	planCopy := plan.DeepCopy()
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
	}

	if plan.Status.HasCriticalCondition() {
		plan.Status.SetCondition(readyCondition(corev1.ConditionFalse, "plan has one or more critical conditions"))
	} else {
		plan.Status.SetCondition(readyCondition(corev1.ConditionTrue, "plan is ready"))
	}
	// Update the ready/completed migrations based on the status of the storage migrations
	if err := r.processMigrations(ctx, plan); err != nil {
		return reconcile.Result{}, err
	}

	planStatusCopy := plan.Status.DeepCopy()
	if !apiequality.Semantic.DeepEqual(plan.Status, planCopy.Status) {
		log.V(5).Info("Updating MigPlan status")
		if err := r.Status().Update(context.TODO(), plan); err != nil {
			return reconcile.Result{}, err
		}
	}
	plan.Status = *planStatusCopy

	if r.shouldUpdateRefresh(plan) {
		r.setRefreshAnnotations(plan)
		if err := r.Update(context.TODO(), plan); err != nil {
			return reconcile.Result{}, err
		}
	}

	log.V(5).Info("Reconciling MigPlan completed")
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
	} else {
		plan.Status.SetCondition(progressCondition(corev1.ConditionTrue, "in progress storage migrations found"))
	}

	if err := r.updateReadyCompletedMigrations(plan, storageMigrationList.Items[len(storageMigrationList.Items)-1]); err != nil {
		return err
	}

	if len(plan.Status.CompletedMigrations) == len(plan.Spec.VirtualMachines) {
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
	plan.Status.ReadyMigrations = readyMigrations
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
		return false
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
	c, err := controller.New("kubevirt-migplan-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to MigPlan
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.VirtualMachineStorageMigrationPlan{},
		&handler.TypedEnqueueRequestForObject[*migrations.VirtualMachineStorageMigrationPlan]{},
		predicate.TypedFuncs[*migrations.VirtualMachineStorageMigrationPlan]{
			CreateFunc: func(e event.TypedCreateEvent[*migrations.VirtualMachineStorageMigrationPlan]) bool { return true },
			DeleteFunc: func(e event.TypedDeleteEvent[*migrations.VirtualMachineStorageMigrationPlan]) bool { return true },
			UpdateFunc: func(e event.TypedUpdateEvent[*migrations.VirtualMachineStorageMigrationPlan]) bool { return true },
		})); err != nil {
		return err
	}

	// Index the vmIndexKey field on VirtualMachineStorageMigrationPlans
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &migrations.VirtualMachineStorageMigrationPlan{}, vmIndexKey, func(rawObj client.Object) []string {
		vmStorageMigrationPlan := rawObj.(*migrations.VirtualMachineStorageMigrationPlan)
		vmNames := []string{}
		for _, vm := range vmStorageMigrationPlan.Spec.VirtualMachines {
			vmNames = append(vmNames, vm.Name)
		}
		// The indexer stores an entry for each value in the returned slice
		return vmNames
	}); err != nil {
		return err
	}

	// Watch for changes to VMs
	if err := c.Watch(source.Kind(mgr.GetCache(), &virtv1.VirtualMachine{},
		// Map function that enqueues requests for VirtualMachineStorageMigrationPlans that have the VM in their spec
		handler.TypedEnqueueRequestsFromMapFunc(r.getVirtualMachineMigrationPlansForVM),
		predicate.TypedFuncs[*virtv1.VirtualMachine]{
			CreateFunc: func(e event.TypedCreateEvent[*virtv1.VirtualMachine]) bool { return true },
			DeleteFunc: func(e event.TypedDeleteEvent[*virtv1.VirtualMachine]) bool { return true },
			UpdateFunc: func(e event.TypedUpdateEvent[*virtv1.VirtualMachine]) bool { return true },
		},
	)); err != nil {
		return err
	}

	// Index the migrationNameIndexKey field on VirtualMachineStorageMigrations
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &migrations.VirtualMachineStorageMigration{}, migrationNameIndexKey, func(rawObj client.Object) []string {
		migration := rawObj.(*migrations.VirtualMachineStorageMigration)
		if migration.Spec.VirtualMachineStorageMigrationPlanRef == nil || migration.Spec.VirtualMachineStorageMigrationPlanRef.Name == "" {
			return nil
		}
		return []string{migration.Spec.VirtualMachineStorageMigrationPlanRef.Name}
	}); err != nil {
		return err
	}

	// Watch for changes to VirtualMachineStorageMigrations
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.VirtualMachineStorageMigration{},
		handler.TypedEnqueueRequestsFromMapFunc(r.getVirtualMachineStorageMigrationsPlanForStorageMigration),
		predicate.TypedFuncs[*migrations.VirtualMachineStorageMigration]{
			CreateFunc: func(e event.TypedCreateEvent[*migrations.VirtualMachineStorageMigration]) bool { return true },
			DeleteFunc: func(e event.TypedDeleteEvent[*migrations.VirtualMachineStorageMigration]) bool { return true },
			UpdateFunc: func(e event.TypedUpdateEvent[*migrations.VirtualMachineStorageMigration]) bool { return true },
		},
	)); err != nil {
		return err
	}
	return nil
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
