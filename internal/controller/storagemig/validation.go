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
	"fmt"
	"path"

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

// Validate the migration resource.
func (r *StorageMigrationReconciler) validate(plan *migrations.VirtualMachineStorageMigrationPlan, migration *migrations.VirtualMachineStorageMigration) {
	log := r.Log
	// verify the migration is owned by a plan
	ownerReference := migration.FindOwnerReference()
	if ownerReference == nil {
		log.Info("cannot find owner reference")
		migration.Status.SetCondition(migrations.Condition{
			Type:     migrations.InvalidPlanRef,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotFound,
			Category: migrations.Critical,
			Message:  "cannot find owner reference",
		})
		return
	} else {
		migration.Status.DeleteCondition(migrations.InvalidPlanRef)
	}
	if migration.Spec.VirtualMachineStorageMigrationPlanRef.UID != "" {
		if ownerReference.UID != migration.Spec.VirtualMachineStorageMigrationPlanRef.UID {
			log.Info("uid mismatch", "owner reference uid", ownerReference.UID, "migration uid", migration.Spec.VirtualMachineStorageMigrationPlanRef.UID)
			migration.Status.SetCondition(migrations.Condition{
				Type:     migrations.InvalidPlanRef,
				Status:   corev1.ConditionTrue,
				Reason:   migrations.NotFound,
				Category: migrations.Critical,
				Message:  fmt.Sprintf("migration is not owned by the plan %s, uid mismatch", migration.Spec.VirtualMachineStorageMigrationPlanRef.Name),
			})
			return
		} else {
			migration.Status.DeleteCondition(migrations.InvalidPlanRef)
		}
	}
	r.validatePlan(plan, migration)
}

// Validate the referenced plan.
func (r *StorageMigrationReconciler) validatePlan(plan *migrations.VirtualMachineStorageMigrationPlan, migration *migrations.VirtualMachineStorageMigration) {
	// Check if the plan has any critical conditions
	if plan.Status.HasCriticalCondition() {
		migration.Status.SetCondition(migrations.Condition{
			Type:     migrations.PlanNotReady,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotReady,
			Category: migrations.Critical,
			Message: fmt.Sprintf("The referenced `virtualMachineStorageMigrationPlanRef` has critical conditions, subject: %s",
				path.Join(migration.Spec.VirtualMachineStorageMigrationPlanRef.Namespace, migration.Spec.VirtualMachineStorageMigrationPlanRef.Name)),
		})
	} else {
		migration.Status.DeleteCondition(migrations.PlanNotReady)
	}
}
