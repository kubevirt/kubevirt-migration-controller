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

package multinamespacestoragemig

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
)

const (
	multiNamespaceStorageMigrationNameLabel      = "migrations.kubevirt.io/multi-namespace-storage-mig-name"
	multiNamespaceStorageMigrationNamespaceLabel = "migrations.kubevirt.io/multi-namespace-storage-mig-namespace"
	multiNamespaceStorageMigrationUIDLabel       = "migration.kubevirt.io/multi-namespace-storage-mig-uid"

	multiNamespaceStorageMigrationNameAnnotation      = "migration.kubevirt.io/multi-namespace-storage-mig-name"
	multiNamespaceStorageMigrationNamespaceAnnotation = "migration.kubevirt.io/multi-namespace-storage-mig-namespace"
)

// MigMigrationReconciler reconciles a MigMigration object
type MultiNamespaceStorageMigrationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	record.EventRecorder
	Log    logr.Logger
	Config *rest.Config
}

// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=multinamespacevirtualmachinestoragemigrations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=multinamespacevirtualmachinestoragemigrations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=multinamespacevirtualmachinestoragemigrations/finalizers,verbs=update
func (r *MultiNamespaceStorageMigrationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log
	log.V(5).Info("Reconciling MultiNamespaceVirtualMachineStorageMigration", "name", req.NamespacedName.Name)
	// Fetch the MultiNamespaceVirtualMachineStorageMigration instance
	migration := &migrations.MultiNamespaceVirtualMachineStorageMigration{}

	if err := r.Get(context.TODO(), req.NamespacedName, migration); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	origMigration := migration.DeepCopy()

	if migration.Spec.MultiNamespaceVirtualMachineStorageMigrationPlanRef != nil {
		migration.Spec.MultiNamespaceVirtualMachineStorageMigrationPlanRef.Namespace = migration.Namespace
	} else {
		log.V(3).Info("No multi-namespace plan reference found")
		return reconcile.Result{}, nil
	}
	plan, err := componenthelpers.GetMultiNamespaceStorageMigrationPlan(ctx, r.Client, migration.Spec.MultiNamespaceVirtualMachineStorageMigrationPlanRef)
	if err != nil {
		return reconcile.Result{}, err
	}

	if plan != nil {
		migration.Status.DeleteCondition(migrations.InvalidPlanRef)
	} else {
		migration.Status.SetCondition(migrations.Condition{
			Type:     migrations.InvalidPlanRef,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotFound,
			Category: migrations.Critical,
			Message: fmt.Sprintf("The referenced `multiNamespaceVirtualMachineStorageMigrationPlanRef` does not exist, subject: %s.",
				path.Join(migration.Spec.MultiNamespaceVirtualMachineStorageMigrationPlanRef.Namespace, migration.Spec.MultiNamespaceVirtualMachineStorageMigrationPlanRef.Name)),
		})
	}

	// Migrate
	if !migration.Status.HasBlockerCondition() {
		log.V(5).Info("Starting MultiNamespaceVirtualMachineStorageMigration")
		migration.Status.Namespaces = make([]migrations.MultiNamespaceVirtualMachineStorageMigrationNamespaceStatus, 0)
		for _, namespacePlan := range plan.Spec.Namespaces {
			virtualMachineStorageMigration, err := r.getVirtualMachineStorageMigration(ctx, plan, &namespacePlan)
			if err != nil {
				return reconcile.Result{}, err
			}
			if virtualMachineStorageMigration == nil {
				log.V(5).Info("Creating virtual machine storage migration", "namespace", namespacePlan.Name)
				if err := r.createNamespacedMigration(ctx, plan.Name, migration, &namespacePlan); err != nil {
					return reconcile.Result{}, err
				}
				return reconcile.Result{}, nil
			} else {
				r.updateNamespaceMigrationStatus(migration, virtualMachineStorageMigration, namespacePlan.Name)
			}
		}
		slices.SortFunc(migration.Status.Namespaces, func(a, b migrations.MultiNamespaceVirtualMachineStorageMigrationNamespaceStatus) int {
			return strings.Compare(a.Name, b.Name)
		})
	}

	// Apply changes to the status.
	if !apiequality.Semantic.DeepEqual(migration.Status, origMigration.Status) {
		log.V(5).Info("Updating VirtualMachineStorageMigration status")
		if err := r.Status().Update(context.TODO(), migration); err != nil {
			return reconcile.Result{}, err
		}
	}

	log.V(5).Info("Reconciling VirtualMachineStorageMigration completed")
	return ctrl.Result{}, nil
}

func (r *MultiNamespaceStorageMigrationReconciler) updateNamespaceMigrationStatus(migration *migrations.MultiNamespaceVirtualMachineStorageMigration, virtualMachineStorageMigration *migrations.VirtualMachineStorageMigration, namespace string) {
	migration.Status.Namespaces = append(migration.Status.Namespaces, migrations.MultiNamespaceVirtualMachineStorageMigrationNamespaceStatus{
		Name:                                 namespace,
		VirtualMachineStorageMigrationStatus: virtualMachineStorageMigration.Status.DeepCopy(),
	})
}

func (r *MultiNamespaceStorageMigrationReconciler) getVirtualMachineStorageMigration(ctx context.Context, plan *migrations.MultiNamespaceVirtualMachineStorageMigrationPlan, namespace *migrations.VirtualMachineStorageMigrationPlanNamespaceSpec) (*migrations.VirtualMachineStorageMigration, error) {
	virtualMachineStorageMigration := &migrations.VirtualMachineStorageMigration{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: migrations.GetNamespacedPlanName(plan.Name, namespace.Name), Namespace: namespace.Name}, virtualMachineStorageMigration); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return virtualMachineStorageMigration, nil
}

func (r *MultiNamespaceStorageMigrationReconciler) createNamespacedMigration(ctx context.Context, planName string, migration *migrations.MultiNamespaceVirtualMachineStorageMigration, namespacePlan *migrations.VirtualMachineStorageMigrationPlanNamespaceSpec) error {
	virtualMachineStorageMigration := &migrations.VirtualMachineStorageMigration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migrations.GetNamespacedPlanName(planName, namespacePlan.Name),
			Namespace: namespacePlan.Name,
			Labels: map[string]string{
				multiNamespaceStorageMigrationUIDLabel: string(migration.UID),
			},
			Annotations: map[string]string{
				multiNamespaceStorageMigrationNameAnnotation:      migration.Name,
				multiNamespaceStorageMigrationNamespaceAnnotation: migration.Namespace,
			},
		},
		Spec: migrations.VirtualMachineStorageMigrationSpec{
			VirtualMachineStorageMigrationPlanRef: &corev1.ObjectReference{
				Namespace: namespacePlan.Name,
				Name:      migrations.GetNamespacedPlanName(planName, namespacePlan.Name),
			},
		},
	}
	if err := r.Create(ctx, virtualMachineStorageMigration); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *MultiNamespaceStorageMigrationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller
	c, err := controller.New("kubevirt-multinamespace-storage-migration-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}
	r.Config = mgr.GetConfig()

	// Watch for changes to MultiNamespaceVirtualMachineStorageMigration
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.MultiNamespaceVirtualMachineStorageMigration{},
		&handler.TypedEnqueueRequestForObject[*migrations.MultiNamespaceVirtualMachineStorageMigration]{})); err != nil {
		return err
	}

	// Watch for changes to VirtualMachineStorageMigrations
	if err := c.Watch(source.Kind(mgr.GetCache(), &migrations.VirtualMachineStorageMigration{},
		handler.TypedEnqueueRequestsFromMapFunc(r.getVirtualMachineStorageMigrationsForMultiNamespaceStorageMigration))); err != nil {
		return err
	}
	return nil
}

func (r *MultiNamespaceStorageMigrationReconciler) getVirtualMachineStorageMigrationsForMultiNamespaceStorageMigration(ctx context.Context, migration *migrations.VirtualMachineStorageMigration) []reconcile.Request {
	// backwards compatibility with us using labels here
	nameLabel := migration.Labels[multiNamespaceStorageMigrationNameLabel]
	namespaceLabel := migration.Labels[multiNamespaceStorageMigrationNamespaceLabel]
	if nameLabel == "" || namespaceLabel == "" {
		nameLabel = migration.Annotations[multiNamespaceStorageMigrationNameAnnotation]
		namespaceLabel = migration.Annotations[multiNamespaceStorageMigrationNamespaceAnnotation]
	}
	multiNamespaceMigration := &migrations.MultiNamespaceVirtualMachineStorageMigration{}
	if err := r.Get(ctx, types.NamespacedName{Name: nameLabel, Namespace: namespaceLabel}, multiNamespaceMigration); err != nil {
		r.Log.Error(err, "failed to get MultiNamespaceVirtualMachineStorageMigration for VirtualMachineStorageMigration", "multi-namespace-storage-mig-name", nameLabel, "multi-namespace-storage-mig-namespace", namespaceLabel)
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: multiNamespaceMigration.Name, Namespace: multiNamespaceMigration.Namespace}},
	}
}
