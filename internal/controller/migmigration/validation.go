package migmigration

import (
	"context"
	"fmt"
	"path"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

// Statuses
const (
	True  = migrationsv1alpha1.True
	False = migrationsv1alpha1.False
)

// Categories
const (
	Critical = migrationsv1alpha1.Critical
	Advisory = migrationsv1alpha1.Advisory
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
	UnhealthyNamespaces                = "UnhealthyNamespaces"
	InvalidPlanRef                     = "InvalidPlanRef"
	PlanNotReady                       = "PlanNotReady"
	PlanClosed                         = "PlanClosed"
	HasFinalMigration                  = "HasFinalMigration"
	Postponed                          = "Postponed"
	SucceededWithWarnings              = "SucceededWithWarnings"
	ResticErrors                       = "ResticErrors"
	ResticVerifyErrors                 = "ResticVerifyErrors"
	VeleroInitialBackupPartiallyFailed = "VeleroInitialBackupPartiallyFailed"
	VeleroStageBackupPartiallyFailed   = "VeleroStageBackupPartiallyFailed"
	VeleroStageRestorePartiallyFailed  = "VeleroStageRestorePartiallyFailed"
	VeleroFinalRestorePartiallyFailed  = "VeleroFinalRestorePartiallyFailed"
	DirectImageMigrationFailed         = "DirectImageMigrationFailed"
	StageNoOp                          = "StageNoOp"
	RegistriesHealthy                  = "RegistriesHealthy"
	RegistriesUnhealthy                = "RegistriesUnhealthy"
	StaleSrcVeleroCRsDeleted           = "StaleSrcVeleroCRsDeleted"
	StaleDestVeleroCRsDeleted          = "StaleDestVeleroCRsDeleted"
	StaleResticCRsDeleted              = "StaleResticCRsDeleted"
	DirectVolumeMigrationBlocked       = "DirectVolumeMigrationBlocked"
	InvalidSpec                        = "InvalidSpec"
	ConflictingPVCMappings             = "ConflictingPVCMappings"
)

// Validate the migration resource.
func (r *MigMigrationReconciler) validate(ctx context.Context, plan *migrationsv1alpha1.MigPlan, migration *migrationsv1alpha1.MigMigration) error {
	// verify the migration is owned by a plan
	ownerReference := migration.FindOwnerReference()
	if ownerReference == nil {
		return fmt.Errorf("migration is not owned by a plan")
	} else if ownerReference.UID != migration.Spec.MigPlanRef.UID {
		return fmt.Errorf("migration is not owned by the plan %s, uid mismatch", migration.Spec.MigPlanRef.Name)
	}
	return r.validatePlan(ctx, plan, migration)
}

// Validate the referenced plan.
func (r *MigMigrationReconciler) validatePlan(ctx context.Context, plan *migrationsv1alpha1.MigPlan, migration *migrationsv1alpha1.MigMigration) error {
	if plan == nil {
		migration.Status.SetCondition(migrationsv1alpha1.Condition{
			Type:     InvalidPlanRef,
			Status:   True,
			Reason:   NotFound,
			Category: Critical,
			Message: fmt.Sprintf("The referenced `migPlanRef` does not exist, subject: %s.",
				path.Join(migration.Spec.MigPlanRef.Namespace, migration.Spec.MigPlanRef.Name)),
		})
		return nil
	}
	// Check if the plan has any critical conditions
	if plan.Status.HasCriticalCondition() {
		migration.Status.SetCondition(migrationsv1alpha1.Condition{
			Type:     PlanNotReady,
			Status:   True,
			Category: Critical,
			Message: fmt.Sprintf("The referenced `migPlanRef` does not have a `Ready` condition, subject: %s.",
				path.Join(migration.Spec.MigPlanRef.Namespace, migration.Spec.MigPlanRef.Name)),
		})
		return nil
	}
	return nil
}
