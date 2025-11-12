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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	vmIndexKey                 = "spec.virtualMachines.name"
	RefreshStartTimeAnnotation = "migration.kubevirt.io/refresh-start-time"
	RefreshEndTimeAnnotation   = "migration.kubevirt.io/refresh-end-time"
)

// MigPlanReconciler reconciles a MigPlan object
type StorageMigPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	record.EventRecorder
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
	log := logf.FromContext(ctx)
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

	if plan.Status.Suffix == nil {
		// Generate suffix
		suffix := rand.String(4)
		plan.Status.Suffix = &suffix
	}

	// Validations.
	if err := r.validate(ctx, plan); err != nil {
		log.Error(err, "Failed to validate VirtualMachineStorageMigrationPlan")
		plan.Status.SetReconcileFailed(err)
	}

	if plan.Status.HasCriticalCondition() {
		plan.Status.SetCondition(migrations.Condition{
			Type:     migrations.Ready,
			Status:   corev1.ConditionFalse,
			Category: migrations.Required,
			Message:  "plan has one or more critical conditions",
		})
	} else {
		plan.Status.SetCondition(migrations.Condition{
			Type:     migrations.Ready,
			Status:   corev1.ConditionTrue,
			Category: migrations.Required,
			Message:  "plan is ready",
		})
	}

	if r.shouldUpdateRefresh(plan) {
		r.setRefreshAnnotations(plan)
		if err := r.Update(context.TODO(), plan); err != nil {
			return reconcile.Result{}, err
		}
	}
	if !apiequality.Semantic.DeepEqual(plan.Status, planCopy.Status) {
		log.V(5).Info("Updating MigPlan status")
		if err := r.Status().Update(context.TODO(), plan); err != nil {
			return reconcile.Result{}, err
		}
	}

	log.V(5).Info("Reconciling MigPlan completed")
	return ctrl.Result{}, nil
}

func (r *StorageMigPlanReconciler) shouldUpdateRefresh(plan *migrations.VirtualMachineStorageMigrationPlan) bool {
	if _, ok := plan.Annotations[RefreshStartTimeAnnotation]; !ok {
		return false
	}
	if _, ok := plan.Annotations[RefreshEndTimeAnnotation]; !ok {
		return true
	}
	return false
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
			UpdateFunc: func(e event.TypedUpdateEvent[*migrations.VirtualMachineStorageMigrationPlan]) bool {
				return !apiequality.Semantic.DeepEqual(e.ObjectOld.Spec, e.ObjectNew.Spec) ||
					!apiequality.Semantic.DeepEqual(e.ObjectOld.DeletionTimestamp, e.ObjectNew.DeletionTimestamp)
			},
		},
	)); err != nil {
		return err
	}

	// Index the vmIndexKey field on MigPlans
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
		// Map function that enqueues requests for MigPlans that have the VM in their spec
		handler.TypedEnqueueRequestsFromMapFunc[*virtv1.VirtualMachine, reconcile.Request](r.getMigPlansForVM),
		predicate.TypedFuncs[*virtv1.VirtualMachine]{
			CreateFunc: func(e event.TypedCreateEvent[*virtv1.VirtualMachine]) bool { return true },
			DeleteFunc: func(e event.TypedDeleteEvent[*virtv1.VirtualMachine]) bool { return true },
			UpdateFunc: func(e event.TypedUpdateEvent[*virtv1.VirtualMachine]) bool { return true },
		},
	)); err != nil {
		return err
	}
	// Watch for changes to MigMigrations
	return nil
}

func (r *StorageMigPlanReconciler) getMigPlansForVM(ctx context.Context, vm *virtv1.VirtualMachine) []reconcile.Request {
	log := logf.FromContext(ctx)
	vmStorageMigrationPlanList := &migrations.VirtualMachineStorageMigrationPlanList{}
	requests := []reconcile.Request{}
	if err := r.List(ctx, vmStorageMigrationPlanList, client.MatchingFields{vmIndexKey: vm.Name}); err != nil {
		log.Error(err, "Failed to list VirtualMachineStorageMigrationPlans for VM", "name", vm.Name)
		return nil
	}
	for _, migplan := range vmStorageMigrationPlanList.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: migplan.Name, Namespace: migplan.Namespace}})
	}
	return requests
}
