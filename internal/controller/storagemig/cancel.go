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

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

func (t *Task) isLiveMigrationCanceling(ctx context.Context, vmName string) (bool, error) {
	vm := &virtv1.VirtualMachine{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: vmName}, vm); err != nil {
		return false, err
	}
	cancelMigrations := t.getCancellableMigrations()
	for _, planVM := range cancelMigrations {
		if planVM.Name == vmName {
			for _, vmVolume := range vm.Spec.Template.Spec.Volumes {
				if vmVolume.PersistentVolumeClaim != nil {
					for _, sourcePVC := range planVM.SourcePVCs {
						if sourcePVC.Name == vmVolume.PersistentVolumeClaim.ClaimName {
							return true, nil
						}
					}
				}
				if vmVolume.DataVolume != nil {
					for _, sourcePVC := range planVM.SourcePVCs {
						if sourcePVC.Name == vmVolume.DataVolume.Name {
							return true, nil
						}
					}
				}
			}
		}
	}
	return false, nil
}

func (t *Task) cleanupCancelledMigrationResources(ctx context.Context, cancelledMigrations []string, completedMigrations []string) (bool, error) {
	allCleaned := false
	var err error
	if allCleaned, err = t.cleanupMigrationResources(ctx, completedMigrations); err != nil {
		return false, err
	}

	for _, cancelledMigrationVMName := range cancelledMigrations {
		for _, planVM := range t.getCancellableMigrations() {
			if planVM.Name == cancelledMigrationVMName {
				// Get the target DV so we can delete them
				for _, targetPVC := range planVM.TargetMigrationPVCs {
					dv := &cdiv1.DataVolume{}
					if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: *targetPVC.DestinationPVC.Name}, dv); err != nil {
						return false, err
					}
					t.Log.V(5).Info("deleting target DV", "dv", dv.Name)
					if err := t.Client.Delete(ctx, dv); err != nil {
						return false, err
					}
				}
			}
		}
	}
	return allCleaned, nil
}

func (t *Task) cancelLiveMigration(ctx context.Context, vmName string) error {
	// In order to cancel the live migration, we need to update the VM back to the source volumes.
	vm := &virtv1.VirtualMachine{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: vmName}, vm); err != nil {
		return err
	}
	cancelMigrations := t.getCancellableMigrations()
	for _, planVM := range cancelMigrations {
		if planVM.Name == vmName {
			revertPlan := t.revertPlanVolumes(&planVM)
			if err := t.updateVMForStorageMigration(ctx, vm, *revertPlan); err != nil {
				return err
			}
			return nil
		}
	}
	t.Log.V(1).Info("No planVM found to revert for VM", "vm", vmName)
	return nil
}

func (t *Task) revertPlanVolumes(planVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine {
	revertPlan := planVM.DeepCopy()

	for i := range revertPlan.SourcePVCs {
		t.Log.V(5).Info("reverting source PVC", "sourcePVC", revertPlan.SourcePVCs[i].Name, "targetPVC", planVM.TargetMigrationPVCs[i].DestinationPVC.Name)
		sourceName := revertPlan.SourcePVCs[i].Name
		revertPlan.SourcePVCs[i].SourcePVC.Name = *planVM.TargetMigrationPVCs[i].DestinationPVC.Name
		revertPlan.TargetMigrationPVCs[i].DestinationPVC.Name = ptr.To(sourceName)
	}
	t.Log.Info("Reverted plan volumes", "revertPlan", revertPlan)
	return revertPlan
}

func (t *Task) getCancellableMigrations() []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine {
	cancelMigrations := make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	cancelMigrations = append(cancelMigrations, t.Plan.Status.ReadyMigrations...)
	cancelMigrations = append(cancelMigrations, t.Plan.Status.InProgressMigrations...)
	return cancelMigrations
}
