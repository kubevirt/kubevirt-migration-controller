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

package storagemig

import (
	"context"
	"fmt"
	"path"
	"slices"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	virtv1 "kubevirt.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
)

const (
	planNameIndexKey = "spec.virtualMachineStorageMigrationPlanRef"
)

// MigMigrationReconciler reconciles a MigMigration object
type StorageMigrationReconciler struct {
	client.Client
	UncachedClient client.Reader
	Scheme         *runtime.Scheme
	record.EventRecorder
	Log    logr.Logger
	Config *rest.Config
}

// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrations/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=list;watch;update
// +kubebuilder:rbac:groups=core,resources=pods,verbs=list;watch
// +kubebuilder:rbac:groups=kubevirt.io,resources=virtualmachines,verbs=get;list;watch
// +kubebuilder:rbac:groups=cdiv1.kubevirt.io,resources=datavolumes,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the MigMigration object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.4/pkg/reconcile
func (r *StorageMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log
	log.V(5).Info("Reconciling VirtualMachineStorageMigration", "name", req.NamespacedName.Name)
	// Fetch the MigMigration instance
	migration := &migrations.VirtualMachineStorageMigration{}

	if err := r.Get(context.TODO(), req.NamespacedName, migration); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if migration.DeletionTimestamp != nil {
		log.V(3).Info("VirtualMachineStorageMigration is being deleted")
		if migration.Status.Phase == migrations.Canceled || migration.Status.Phase == migrations.Completed {
			migration.RemoveFinalizer(migrations.VirtualMachineStorageMigrationFinalizer, log)
			if err := r.Update(context.TODO(), migration); err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{}, nil
		}
	}

	origMigration := migration.DeepCopy()

	// Completed.
	if migration.Status.Phase == migrations.Completed {
		log.V(3).Info("VirtualMachineStorageMigration is completed")
		return reconcile.Result{}, nil
	}

	if migration.Spec.VirtualMachineStorageMigrationPlanRef != nil {
		migration.Spec.VirtualMachineStorageMigrationPlanRef.Namespace = migration.Namespace
	} else {
		log.V(3).Info("No plan reference found")
		return reconcile.Result{}, nil
	}
	plan, err := componenthelpers.GetStorageMigrationPlan(ctx, r.Client, migration.Spec.VirtualMachineStorageMigrationPlanRef)
	if err != nil {
		return reconcile.Result{}, err
	}

	if plan != nil {
		migration.Status.DeleteCondition(migrations.InvalidPlanRef)

		// Owner Reference
		if err := r.setOwnerReference(plan, migration); err != nil {
			return reconcile.Result{}, err
		}

		// Validate
		r.validate(plan, migration)
	} else {
		migration.Status.SetCondition(migrations.Condition{
			Type:     migrations.InvalidPlanRef,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotFound,
			Category: migrations.Critical,
			Message: fmt.Sprintf("The referenced `virtualMachineStorageMigrationPlanRef` does not exist, subject: %s.",
				path.Join(migration.Spec.VirtualMachineStorageMigrationPlanRef.Namespace, migration.Spec.VirtualMachineStorageMigrationPlanRef.Name)),
		})
	}

	requeueAfter := NoReQ

	// Migrate
	if !migration.Status.HasBlockerCondition() || migration.DeletionTimestamp != nil {
		log.V(3).Info("Starting VirtualMachineStorageMigration", "deletionTimestamp", migration.DeletionTimestamp)
		var err error
		requeueAfter, err = r.migrate(ctx, plan, migration)
		if err != nil {
			return reconcile.Result{}, err
		}
	}

	// Make a copy of the migration object so we can keep the finalizers
	migrationCopy := migration.DeepCopy()
	// Apply changes to the status.
	if !apiequality.Semantic.DeepEqual(migration.Status, origMigration.Status) {
		log.V(5).Info("Updating VirtualMachineStorageMigration status", "phase", migration.Status.Phase)
		if err := r.Status().Update(context.TODO(), migration); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Apply changes to the migration.
	if !apiequality.Semantic.DeepEqual(migration.ObjectMeta, origMigration.ObjectMeta) {
		migration.Finalizers = migrationCopy.Finalizers
		log.V(5).Info("Updating VirtualMachineStorageMigration object metadata", "finalizers", migration.Finalizers)
		if err := r.Update(context.TODO(), migration); err != nil {
			return reconcile.Result{}, err
		}
	}

	log.V(5).Info("Reconciling VirtualMachineStorageMigration completed")
	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// Set the owner reference is set to the plan.
func (r *StorageMigrationReconciler) setOwnerReference(plan *migrations.VirtualMachineStorageMigrationPlan, migration *migrations.VirtualMachineStorageMigration) error {
	if plan == nil {
		r.Log.Info("no plan found")
		return fmt.Errorf("no plan found")
	}
	if err := controllerutil.SetControllerReference(plan, migration, r.Scheme); err != nil {
		r.Log.Error(err, "Failed to set owner reference")
		return err
	}
	r.Log.V(5).Info("migration owner reference", "owner reference", migration.OwnerReferences)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *StorageMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller
	c, err := controller.New("kubevirt-storage-migration-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}
	r.Config = mgr.GetConfig()

	// Watch for changes to MigMigration
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.VirtualMachineStorageMigration{},
		&handler.TypedEnqueueRequestForObject[*migrations.VirtualMachineStorageMigration]{})); err != nil {
		return err
	}

	// Index the plan name on MigMigrations
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &migrations.VirtualMachineStorageMigration{}, planNameIndexKey, func(rawObj client.Object) []string {
		migration := rawObj.(*migrations.VirtualMachineStorageMigration)
		if migration.Spec.VirtualMachineStorageMigrationPlanRef == nil {
			return nil
		}
		name := path.Join(migration.Namespace, migration.Spec.VirtualMachineStorageMigrationPlanRef.Name)
		return []string{name}
	}); err != nil {
		return err
	}

	// Watch for changes to MigPlans
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.VirtualMachineStorageMigrationPlan{},
		handler.TypedEnqueueRequestsFromMapFunc(r.getMigMigrationsForPlan))); err != nil {
		return err
	}

	// Watch for changes to VirtualMachineInstanceMigrations
	if err := c.Watch(source.Kind(mgr.GetCache(), &virtv1.VirtualMachineInstanceMigration{},
		handler.TypedEnqueueRequestsFromMapFunc(r.getMigMigrationsForVMIM))); err != nil {
		return err
	}

	return nil
}

func (r *StorageMigrationReconciler) getMigMigrationsForPlan(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) []reconcile.Request {
	migrations := &migrations.VirtualMachineStorageMigrationList{}
	requests := []reconcile.Request{}
	if err := r.List(ctx, migrations, client.MatchingFields{planNameIndexKey: path.Join(plan.Namespace, plan.Name)}); err != nil {
		return nil
	}
	for _, migration := range migrations.Items {
		requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: migration.Name, Namespace: migration.Namespace}})
	}
	return requests
}

func (r *StorageMigrationReconciler) getMigMigrationsForVMIM(ctx context.Context, vmim *virtv1.VirtualMachineInstanceMigration) []reconcile.Request {
	migrationList := &migrations.VirtualMachineStorageMigrationList{}
	requests := []reconcile.Request{}
	if err := r.List(ctx, migrationList, client.InNamespace(vmim.Namespace)); err != nil {
		return nil
	}
	for _, migration := range migrationList.Items {
		if slices.ContainsFunc(migration.Status.RunningMigrations, func(runningMigration migrations.RunningVirtualMachineMigration) bool {
			return runningMigration.Name == vmim.Spec.VMIName
		}) {
			requests = append(requests, reconcile.Request{NamespacedName: types.NamespacedName{Name: migration.Name, Namespace: migration.Namespace}})
		}
	}
	return requests
}
