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

package migplan

import (
	"context"
	"reflect"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/tools/record"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

// MigPlanReconciler reconciles a MigPlan object
type MigPlanReconciler struct {
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
func (r *MigPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch the MigPlan instance
	plan := &migrationsv1alpha1.MigPlan{}
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
		plan.Status.SetReconcileFailed(err)
	}

	if !reflect.DeepEqual(plan.Status, planCopy.Status) {
		if err := r.Status().Update(context.TODO(), plan); err != nil {
			return reconcile.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MigPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller
	c, err := controller.New("kubevirt-migplan-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to MigPlan
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrationsv1alpha1.MigPlan{},
		&handler.TypedEnqueueRequestForObject[*migrationsv1alpha1.MigPlan]{},
		predicate.TypedFuncs[*migrationsv1alpha1.MigPlan]{
			CreateFunc: func(e event.TypedCreateEvent[*migrationsv1alpha1.MigPlan]) bool { return true },
			DeleteFunc: func(e event.TypedDeleteEvent[*migrationsv1alpha1.MigPlan]) bool { return true },
			UpdateFunc: func(e event.TypedUpdateEvent[*migrationsv1alpha1.MigPlan]) bool {
				return !reflect.DeepEqual(e.ObjectOld.Spec, e.ObjectNew.Spec) ||
					!reflect.DeepEqual(e.ObjectOld.DeletionTimestamp, e.ObjectNew.DeletionTimestamp)
			},
		},
	)); err != nil {
		return err
	}

	// Watch for changes to MigMigrations
	return nil
}
