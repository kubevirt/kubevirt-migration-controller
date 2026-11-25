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

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

func (t *Task) isVMOnSourceVolumes(ctx context.Context, vmName string) (bool, error) {
	vm := &virtv1.VirtualMachine{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: vmName}, vm); err != nil {
		return false, err
	}
	for _, planVM := range t.getCancellableMigrations() {
		if planVM.Name != vmName {
			continue
		}
		return isVMOnSourceVolumesForPlan(vm, planVM), nil
	}
	return false, nil
}

// vmUnchangedByPlanMigration reports VMs that never switched to this plan's target
// volumes. They may still run on PVCs from a prior completed migration and must
// not be reverted using this plan's recorded source names.
func (t *Task) vmUnchangedByPlanMigration(ctx context.Context, vmName string) (bool, error) {
	vm := &virtv1.VirtualMachine{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: vmName}, vm); err != nil {
		return false, err
	}
	for _, planVM := range t.getCancellableMigrations() {
		if planVM.Name != vmName {
			continue
		}
		if vmReferencesTargetVolumes(vm, planVM) {
			return false, nil
		}
		return !isVMOnSourceVolumesForPlan(vm, planVM), nil
	}
	return false, nil
}

func isVMOnSourceVolumesForPlan(vm *virtv1.VirtualMachine, planVM migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) bool {
	if len(planVM.SourcePVCs) == 0 {
		return false
	}
	// Target claim names are authoritative: if the VM still references any
	// planned destination, we are mid-flight regardless of SourcePVCs (which
	// can be corrupted to target names after a plan status race).
	if vmReferencesTargetVolumes(vm, planVM) {
		return false
	}
	// All planned volumes must already reference the original source names.
	for _, sourcePVC := range planVM.SourcePVCs {
		matched := false
		for _, vmVolume := range vm.Spec.Template.Spec.Volumes {
			if sourcePVC.VolumeName != "" && vmVolume.Name != sourcePVC.VolumeName {
				continue
			}
			claimName := volumeClaimName(vmVolume)
			if claimName == "" {
				continue
			}
			if claimName == sourcePVC.Name {
				matched = true
				break
			}
			if sourcePVC.VolumeName != "" {
				// Correct volume name but still on the target claim.
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func vmReferencesTargetVolumes(vm *virtv1.VirtualMachine, planVM migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) bool {
	targets := make(map[string]struct{}, len(planVM.TargetMigrationPVCs))
	for _, targetPVC := range planVM.TargetMigrationPVCs {
		if targetPVC.DestinationPVC.Name == nil {
			continue
		}
		targets[*targetPVC.DestinationPVC.Name] = struct{}{}
	}
	if len(targets) == 0 {
		return false
	}
	for _, vmVolume := range vm.Spec.Template.Spec.Volumes {
		if _, ok := targets[volumeClaimName(vmVolume)]; ok {
			return true
		}
	}
	for _, dvt := range vm.Spec.DataVolumeTemplates {
		if _, ok := targets[dvt.Name]; ok {
			return true
		}
	}
	return false
}

func volumeClaimName(volume virtv1.Volume) string {
	switch {
	case volume.DataVolume != nil:
		return volume.DataVolume.Name
	case volume.PersistentVolumeClaim != nil:
		return volume.PersistentVolumeClaim.ClaimName
	default:
		return ""
	}
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
					if targetPVC.DestinationPVC.Name == nil {
						continue
					}
					dv := &cdiv1.DataVolume{}
					if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: *targetPVC.DestinationPVC.Name}, dv); err != nil {
						if k8serrors.IsNotFound(err) {
							continue
						}
						return false, err
					}
					t.Log.V(5).Info("deleting target DV", "dv", dv.Name)
					if err := t.Client.Delete(ctx, dv); err != nil {
						if k8serrors.IsNotFound(err) {
							continue
						}
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
			if vmReferencesTargetVolumes(vm, planVM) {
				// VM still on this plan's targets — revert back to source.
			} else if !isVMOnSourceVolumesForPlan(vm, planVM) {
				t.Log.Info("Skipping volume revert; VM was not moved by this plan",
					"vm", vmName)
				return nil
			} else {
				t.Log.V(4).Info("VM already on plan source volumes", "vm", vmName)
				return nil
			}
			if err := t.recoverCorruptedSourcePVCs(vm, &planVM); err != nil {
				return err
			}
			revertPlan, err := t.revertPlanVolumes(&planVM)
			if err != nil {
				return err
			}
			if err := t.updateVMForStorageMigration(ctx, vm, *revertPlan); err != nil {
				return err
			}
			return nil
		}
	}
	return fmt.Errorf("no cancellable plan VM found to revert volumes for %s", vmName)
}

// recoverCorruptedSourcePVCs restores SourcePVCs[].Name when they were overwritten
// with target names (plan status race). Prefer VolumeUpdateState, then keep names
// that already differ from the destination.
func (t *Task) recoverCorruptedSourcePVCs(vm *virtv1.VirtualMachine, planVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) error {
	if len(planVM.SourcePVCs) != len(planVM.TargetMigrationPVCs) {
		return fmt.Errorf("source PVCs (%d) and target PVCs (%d) length mismatch for vm %s",
			len(planVM.SourcePVCs), len(planVM.TargetMigrationPVCs), planVM.Name)
	}
	for i := range planVM.SourcePVCs {
		if planVM.TargetMigrationPVCs[i].DestinationPVC.Name == nil {
			continue
		}
		targetName := *planVM.TargetMigrationPVCs[i].DestinationPVC.Name
		if planVM.SourcePVCs[i].Name != targetName {
			continue
		}
		recovered := sourceClaimFromVolumeUpdateState(vm, targetName)
		if recovered == "" {
			return fmt.Errorf("plan source PVC for vm %s volume %s matches target %s and VolumeUpdateState has no original source",
				planVM.Name, planVM.SourcePVCs[i].VolumeName, targetName)
		}
		t.Log.Info("Recovered corrupted plan source PVC from VolumeUpdateState",
			"vm", planVM.Name, "volume", planVM.SourcePVCs[i].VolumeName, "source", recovered, "target", targetName)
		planVM.SourcePVCs[i].Name = recovered
		planVM.SourcePVCs[i].SourcePVC.Name = recovered
	}
	return nil
}

func sourceClaimFromVolumeUpdateState(vm *virtv1.VirtualMachine, targetClaim string) string {
	if vm.Status.VolumeUpdateState == nil || vm.Status.VolumeUpdateState.VolumeMigrationState == nil {
		return ""
	}
	for _, migrated := range vm.Status.VolumeUpdateState.VolumeMigrationState.MigratedVolumes {
		if migrated.DestinationPVCInfo == nil || migrated.SourcePVCInfo == nil {
			continue
		}
		if migrated.DestinationPVCInfo.ClaimName == targetClaim {
			return migrated.SourcePVCInfo.ClaimName
		}
	}
	return ""
}

func (t *Task) revertPlanVolumes(planVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) (*migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, error) {
	if len(planVM.SourcePVCs) != len(planVM.TargetMigrationPVCs) {
		return nil, fmt.Errorf("source PVCs (%d) and target PVCs (%d) length mismatch for vm %s",
			len(planVM.SourcePVCs), len(planVM.TargetMigrationPVCs), planVM.Name)
	}
	revertPlan := planVM.DeepCopy()

	for i := range revertPlan.SourcePVCs {
		if planVM.TargetMigrationPVCs[i].DestinationPVC.Name == nil {
			continue
		}
		sourceName := revertPlan.SourcePVCs[i].Name
		targetName := *planVM.TargetMigrationPVCs[i].DestinationPVC.Name
		if sourceName == targetName {
			return nil, fmt.Errorf("cannot revert volumes for vm %s: source and target both %s", planVM.Name, sourceName)
		}
		t.Log.V(5).Info("reverting source PVC", "sourcePVC", sourceName, "targetPVC", targetName)
		// prepareVMForStorageMigration matches DataVolumeTemplates by SourcePVCs[].Name.
		// Mid-flight the VM already uses target names, so treat targets as the current
		// names and original sources as the revert destinations.
		revertPlan.SourcePVCs[i].Name = targetName
		revertPlan.SourcePVCs[i].SourcePVC.Name = targetName
		revertPlan.TargetMigrationPVCs[i].DestinationPVC.Name = ptr.To(sourceName)
	}
	t.Log.Info("Reverted plan volumes", "revertPlan", revertPlan)
	return revertPlan, nil
}

func (t *Task) getCancellableMigrations() []migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine {
	cancelMigrations := make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	if t.Plan != nil {
		cancelMigrations = append(cancelMigrations, t.Plan.Status.ReadyMigrations...)
		cancelMigrations = append(cancelMigrations, t.Plan.Status.InProgressMigrations...)
	} else {
		t.Log.V(1).Info("No plan found to get cancellable migrations")
	}
	return cancelMigrations
}
