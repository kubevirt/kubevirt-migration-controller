package storagemig

import (
	"fmt"
	"path"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

// Reasons
const (
	NotSet         = "NotSet"
	NotFound       = "NotFound"
	Cancel         = "Cancel"
	ErrorsDetected = "ErrorsDetected"
	NotSupported   = "NotSupported"
	PvNameConflict = "PvNameConflict"
	NotDistinct    = "NotDistinct"
)

// Types
const (
	InvalidPlanRef        = "InvalidPlanRef"
	PlanNotReady          = "PlanNotReady"
	PlanClosed            = "PlanClosed"
	SucceededWithWarnings = "SucceededWithWarnings"
	InvalidSpec           = "InvalidSpec"
)

// Validate the migration resource.
func (r *StorageMigrationReconciler) validate(plan *migrations.VirtualMachineStorageMigrationPlan, migration *migrations.VirtualMachineStorageMigration, log logr.Logger) {
	// verify the migration is owned by a plan
	ownerReference := migration.FindOwnerReference()
	if ownerReference == nil {
		log.Info("cannot find owner reference")
		migration.Status.SetCondition(migrations.Condition{
			Type:     InvalidPlanRef,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotFound,
			Category: migrations.Critical,
			Message:  "cannot find owner reference",
		})
		return
	} else {
		migration.Status.DeleteCondition(InvalidPlanRef)
	}
	if migration.Spec.VirtualMachineStorageMigrationPlanRef.UID != "" {
		if ownerReference.UID != migration.Spec.VirtualMachineStorageMigrationPlanRef.UID {
			log.Info("uid mismatch", "owner reference uid", ownerReference.UID, "migration uid", migration.Spec.VirtualMachineStorageMigrationPlanRef.UID)
			migration.Status.SetCondition(migrations.Condition{
				Type:     InvalidPlanRef,
				Status:   corev1.ConditionTrue,
				Reason:   migrations.NotFound,
				Category: migrations.Critical,
				Message:  fmt.Sprintf("migration is not owned by the plan %s, uid mismatch", migration.Spec.VirtualMachineStorageMigrationPlanRef.Name),
			})
			return
		} else {
			migration.Status.DeleteCondition(InvalidPlanRef)
		}
	}
	r.validatePlan(plan, migration)
}

// Validate the referenced plan.
func (r *StorageMigrationReconciler) validatePlan(plan *migrations.VirtualMachineStorageMigrationPlan, migration *migrations.VirtualMachineStorageMigration) {
	// Check if the plan has any critical conditions
	if plan.Status.HasCriticalCondition() {
		migration.Status.SetCondition(migrations.Condition{
			Type:     PlanNotReady,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotReady,
			Category: migrations.Critical,
			Message: fmt.Sprintf("The referenced `virtualMachineStorageMigrationPlanRef` has critical conditions, subject: %s",
				path.Join(migration.Spec.VirtualMachineStorageMigrationPlanRef.Namespace, migration.Spec.VirtualMachineStorageMigrationPlanRef.Name)),
		})
	} else {
		migration.Status.DeleteCondition(PlanNotReady)
	}
}
