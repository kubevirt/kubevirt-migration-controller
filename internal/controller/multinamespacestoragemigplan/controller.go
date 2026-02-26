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
package multinamespacestoragemigplan

import (
	"context"
	"fmt"
	"slices"
	"strings"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	logr "github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	multiNamespaceStorageMigPlanNameLabel      = "migration.kubevirt.io/multi-namespace-storage-mig-plan-name"
	multiNamespaceStorageMigPlanNamespaceLabel = "migration.kubevirt.io/multi-namespace-storage-mig-plan-namespace"
	multiNamespaceStorageMigPlanUIDLabel       = "migration.kubevirt.io/multi-namespace-storage-mig-plan-uid"

	multiNamespaceStorageMigPlanNameAnnotation      = "migration.kubevirt.io/multi-namespace-storage-mig-plan-name"
	multiNamespaceStorageMigPlanNamespaceAnnotation = "migration.kubevirt.io/multi-namespace-storage-mig-plan-namespace"

	InvalidNamespace = "InvalidNamespace"
)

// MultiNamespaceStorageMigPlanReconciler reconciles a MultiNamespaceVirtualMachineStorageMigrationPlan object
type MultiNamespaceStorageMigPlanReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	record.EventRecorder
	Log logr.Logger
}

// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=multinamespacevirtualmachinestoragemigrationplans,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=multinamespacevirtualmachinestoragemigrationplans/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=multinamespacevirtualmachinestoragemigrationplans/finalizers,verbs=update
// +kubebuilder:rbac:groups=migrations.kubevirt.io,resources=virtualmachinestoragemigrationplans,verbs=get;list;watch;create;update;patch
func (r *MultiNamespaceStorageMigPlanReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log
	log.V(5).Info("Reconciling MultiNamespaceVirtualMachineStorageMigrationPlan", "name", req.NamespacedName)
	// Fetch the MultiNamespaceVirtualMachineStorageMigrationPlan instance
	plan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
	err := r.Get(ctx, req.NamespacedName, plan)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}
	originalPlan := plan.DeepCopy()

	invalidNamespaceFound := false
	for _, namespace := range plan.Spec.Namespaces {
		if message, err := r.validateNamespace(ctx, &namespace); err != nil {
			return reconcile.Result{}, err
		} else if message != "" {
			plan.Status.SetCondition(migrations.Condition{
				Type:    InvalidNamespace,
				Status:  corev1.ConditionTrue,
				Message: message,
			})
			invalidNamespaceFound = true
		}
		namespacedPlan, err := r.getNamespacePlan(ctx, plan.Name, &namespace)
		if err != nil {
			return reconcile.Result{}, err
		}
		if namespacedPlan == nil {
			log.V(5).Info("Creating namespace plan", "namespace", namespace.Name)
			err = r.createNamespacePlan(ctx, plan, &namespace)
			if err != nil {
				return reconcile.Result{}, err
			}
		} else {
			log.V(5).Info("Updating namespace plan status", "namespace", namespace.Name)
			// Update the status of the multi-namespace storage migration plan
			r.updateNamespacePlanStatus(plan, namespacedPlan)
		}
	}

	if !invalidNamespaceFound {
		plan.Status.DeleteCondition(InvalidNamespace)
	}

	planStatusCopy := plan.Status.DeepCopy()
	if !apiequality.Semantic.DeepEqual(plan.Status, originalPlan.Status) {
		log.V(5).Info("Updating MultiNamespaceVirtualMachineStorageMigrationPlan status")
		if err := r.Status().Update(ctx, plan); err != nil {
			return reconcile.Result{}, err
		}
	}
	plan.Status = *planStatusCopy

	if !apiequality.Semantic.DeepEqual(originalPlan, plan) {
		if err := r.Update(ctx, plan); err != nil {
			return reconcile.Result{}, err
		}
	}
	log.V(5).Info("Reconciling MultiNamespaceVirtualMachineStorageMigrationPlan completed")
	return ctrl.Result{}, nil
}

func (r *MultiNamespaceStorageMigPlanReconciler) updateNamespacePlanStatus(plan *migrations.MultiNamespaceVirtualMachineStorageMigrationPlan, namespacedPlan *migrations.VirtualMachineStorageMigrationPlan) {
	slices.SortFunc(plan.Status.Namespaces, func(a, b migrations.VirtualMachineStorageMigrationPlanNamespaceStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
	updatedNamespaces := make([]migrations.VirtualMachineStorageMigrationPlanNamespaceStatus, 0)
	namespaceFound := false
	for _, namespace := range plan.Status.Namespaces {
		if namespace.Name == namespacedPlan.Namespace {
			updatedNamespaces = append(updatedNamespaces, migrations.VirtualMachineStorageMigrationPlanNamespaceStatus{
				Name:                                     namespace.Name,
				VirtualMachineStorageMigrationPlanStatus: namespacedPlan.Status.DeepCopy(),
			})
			namespaceFound = true
		} else {
			updatedNamespaces = append(updatedNamespaces, namespace)
		}
	}
	if !namespaceFound {
		updatedNamespaces = append(updatedNamespaces, migrations.VirtualMachineStorageMigrationPlanNamespaceStatus{
			Name:                                     namespacedPlan.Namespace,
			VirtualMachineStorageMigrationPlanStatus: namespacedPlan.Status.DeepCopy(),
		})
	}
	r.Log.Info("Updated namespaces", "namespaces", updatedNamespaces)
	plan.Status.Namespaces = updatedNamespaces
	slices.SortFunc(plan.Status.Namespaces, func(a, b migrations.VirtualMachineStorageMigrationPlanNamespaceStatus) int {
		return strings.Compare(a.Name, b.Name)
	})
}

func (r *MultiNamespaceStorageMigPlanReconciler) getNamespacePlan(ctx context.Context, planName string, planNamespace *migrations.VirtualMachineStorageMigrationPlanNamespaceSpec) (*migrations.VirtualMachineStorageMigrationPlan, error) {
	plan := &migrations.VirtualMachineStorageMigrationPlan{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: migrations.GetNamespacedPlanName(planName, planNamespace.Name), Namespace: planNamespace.Name}, plan); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return plan, nil
}

func (r *MultiNamespaceStorageMigPlanReconciler) validateNamespace(ctx context.Context, planNamespace *migrations.VirtualMachineStorageMigrationPlanNamespaceSpec) (string, error) {
	if planNamespace.VirtualMachineStorageMigrationPlanSpec == nil {
		return fmt.Sprintf("namespace %s has no plan", planNamespace.Name), nil
	}
	namespace := &corev1.Namespace{}
	if err := r.Client.Get(ctx, types.NamespacedName{Name: planNamespace.Name}, namespace); err != nil {
		if k8serrors.IsNotFound(err) {
			return fmt.Sprintf("namespace %s not found", planNamespace.Name), nil
		}
		return "", err
	}
	if namespace.DeletionTimestamp != nil {
		return fmt.Sprintf("namespace %s is being deleted", planNamespace.Name), nil
	}
	return "", nil
}

func (r *MultiNamespaceStorageMigPlanReconciler) createNamespacePlan(ctx context.Context, plan *migrations.MultiNamespaceVirtualMachineStorageMigrationPlan, namespace *migrations.VirtualMachineStorageMigrationPlanNamespaceSpec) error {
	spec := namespace.VirtualMachineStorageMigrationPlanSpec.DeepCopy()
	// When the multinamespace plan has RetentionPolicy set (e.g. deleteSource), apply it to the child plan
	if plan.Spec.RetentionPolicy != nil {
		spec.RetentionPolicy = plan.Spec.RetentionPolicy
	}
	namespacePlan := &migrations.VirtualMachineStorageMigrationPlan{
		ObjectMeta: metav1.ObjectMeta{
			Name:      migrations.GetNamespacedPlanName(plan.Name, namespace.Name),
			Namespace: namespace.Name,
			Labels: map[string]string{
				multiNamespaceStorageMigPlanUIDLabel: string(plan.UID),
			},
			Annotations: map[string]string{
				multiNamespaceStorageMigPlanNameAnnotation:      plan.Name,
				multiNamespaceStorageMigPlanNamespaceAnnotation: namespace.Name,
			},
		},
		Spec: *spec,
	}

	if err := r.Create(ctx, namespacePlan); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

func (r *MultiNamespaceStorageMigPlanReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Create a new controller
	c, err := controller.New("kubevirt-multinamespace-storage-migplan-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to MultiNamespaceVirtualMachineStorageMigrationPlan
	if err := c.Watch(source.Kind(
		mgr.GetCache(),
		&migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{},
		&handler.TypedEnqueueRequestForObject[*migrations.MultiNamespaceVirtualMachineStorageMigrationPlan]{})); err != nil {
		return err
	}

	// Watch for changes to owned VirtualMachineStorageMigrationPlan
	if err := c.Watch(source.Kind(
		mgr.GetCache(),
		&migrations.VirtualMachineStorageMigrationPlan{},
		handler.TypedEnqueueRequestsFromMapFunc(r.getMultiNamespaceVirtualMachineStorageMigrationsPlanForStorageMigration))); err != nil {
		return err
	}
	return nil
}

func (r *MultiNamespaceStorageMigPlanReconciler) getMultiNamespaceVirtualMachineStorageMigrationsPlanForStorageMigration(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) []reconcile.Request {
	// backwards compatibility with us using labels here
	nameLabel := plan.Labels[multiNamespaceStorageMigPlanNameLabel]
	namespaceLabel := plan.Labels[multiNamespaceStorageMigPlanNamespaceLabel]
	if nameLabel == "" || namespaceLabel == "" {
		nameLabel = plan.Annotations[multiNamespaceStorageMigPlanNameAnnotation]
		namespaceLabel = plan.Annotations[multiNamespaceStorageMigPlanNamespaceAnnotation]
	}
	multiNamespaceStorageMigPlan := &migrations.MultiNamespaceVirtualMachineStorageMigrationPlan{}
	if err := r.Get(ctx, types.NamespacedName{Name: nameLabel, Namespace: namespaceLabel}, multiNamespaceStorageMigPlan); err != nil {
		r.Log.Error(err, "failed to get MultiNamespaceVirtualMachineStorageMigrationPlan for VirtualMachineStorageMigrationPlan", "multi-namespace-storage-mig-plan-name", nameLabel, "multi-namespace-storage-mig-plan-namespace", namespaceLabel)
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Name: multiNamespaceStorageMigPlan.Name, Namespace: multiNamespaceStorageMigPlan.Namespace}},
	}
}
