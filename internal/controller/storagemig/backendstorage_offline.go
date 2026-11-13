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

// Offline migration of the VMState PVC (the KubeVirt-owned PVC holding vTPM/EFI/CBT state).
//
// KubeVirt tracks this PVC by the "persistent-state-for=<vm>" label, not via the VM's volume list.
// A running VM's PVC moves with its live migration; a stopped VM has no live migration, so otherwise
// the VMState PVC is left on the source storage class. We handle the stopped case the same way as
// KubeVirt's VMRestore (reconcileBackendVolume): clone the PVC to the target storage class with CDI,
// then move the label onto the clone so KubeVirt adopts it on next start.
//
// TODO (kubevirt v1.10): adapt the label handoff to set virtualMachineState.source instead.

package storagemig

import (
	"context"
	"fmt"

	"github.com/openshift/library-go/pkg/build/naming"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// formerBackendStoragePVCLabel tags the original PVC once its "persistent-state-for" label moved to
// the clone, so we can find it again to revert (cancel) or delete it (deleteSource).
// TODO (kubevirt v1.10): removed with the label-based handoff.
const formerBackendStoragePVCLabel = "migrations.kubevirt.io/former-backend-storage"

// offlineBackendStorageCloneName returns the deterministic clone name "persistent-state-for-<vm>-mig-<suffix>".
func (t *Task) offlineBackendStorageCloneName(vmName string) (string, error) {
	if t.Plan == nil || t.Plan.Status.Suffix == nil || *t.Plan.Status.Suffix == "" {
		return "", fmt.Errorf("plan suffix not set; cannot name VMState clone for %s", vmName)
	}
	base := componenthelpers.BackendStoragePVCLabel + "-" + vmName
	migSuffix := fmt.Sprintf("mig-%s", *t.Plan.Status.Suffix)
	return naming.GetName(base, migSuffix, kvalidation.DNS1035LabelMaxLength), nil
}

// offlineBackendStorageTarget returns the VM's current VMState PVC and the storage class to migrate it
// to. ok=false means nothing to do: no backend storage, no VMState PVC yet, the disks target more than
// one storage class, or the PVC is already on the target.
func (t *Task) offlineBackendStorageTarget(ctx context.Context, vm *virtv1.VirtualMachine, planVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) (*corev1.PersistentVolumeClaim, string, bool, error) {
	if !componenthelpers.VMNeedsBackendStorage(vm) {
		return nil, "", false, nil
	}
	targetSC, ok := componenthelpers.SingleTargetStorageClass(planVM)
	if !ok {
		t.Log.V(3).Info("Skipping offline VMState migration: no single target StorageClass", "vm", vm.Name)
		return nil, "", false, nil
	}
	sourcePVC, err := componenthelpers.GetBackendStoragePVC(ctx, t.Client, vm.Name, t.Owner.Namespace)
	if err != nil {
		return nil, "", false, err
	}
	if sourcePVC == nil {
		return nil, "", false, nil
	}
	if sourcePVC.Spec.StorageClassName != nil && *sourcePVC.Spec.StorageClassName == targetSC {
		return nil, "", false, nil // already migrated
	}
	return sourcePVC, targetSC, true, nil
}

// startOfflineBackendStorageMigration starts the CDI clone of the VMState PVC onto the target storage
// class. Idempotent. The clone is created without the "persistent-state-for" label so KubeVirt never
// sees two current VMState PVCs; the label is moved later in reconcileOfflineBackendStorageMigration.
func (t *Task) startOfflineBackendStorageMigration(ctx context.Context, vm *virtv1.VirtualMachine, planVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) error {
	sourcePVC, targetSC, ok, err := t.offlineBackendStorageTarget(ctx, vm, planVM)
	if err != nil || !ok {
		return err
	}
	cloneName, err := t.offlineBackendStorageCloneName(vm.Name)
	if err != nil {
		return err
	}
	// Clone already exists or is in progress.
	dv := &cdiv1.DataVolume{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: cloneName}, dv); err == nil {
		return nil
	} else if !k8serrors.IsNotFound(err) {
		return err
	}

	size, err := t.determineSourcePVCSize(ctx, sourcePVC)
	if err != nil {
		return err
	}
	volumeMode := corev1.PersistentVolumeFilesystem // VMState PVCs are always Filesystem.
	targetPVC := migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC{
		VolumeName: componenthelpers.BackendStoragePVCLabel,
		DestinationPVC: migrations.VirtualMachineStorageMigrationPlanDestinationPVC{
			Name:             &cloneName,
			StorageClassName: &targetSC,
			VolumeMode:       &volumeMode,
			AccessModes:      backendStorageAccessModes(sourcePVC),
		},
	}
	t.Log.V(3).Info("Cloning VMState PVC for offline storage migration", "vm", vm.Name, "source", sourcePVC.Name, "target", cloneName, "storageClass", targetSC)
	// nil labels so the clone does not inherit the source's "persistent-state-for" label.
	return t.createTargetDV(ctx, targetPVC, size, nil, nil, &sourcePVC.Name)
}

// reconcileOfflineBackendStorageMigration waits for the clone to populate, then moves the label to it.
// Returns true when migrated (or nothing to do), false while the clone is still populating.
func (t *Task) reconcileOfflineBackendStorageMigration(ctx context.Context, vmName string) (bool, error) {
	vm := &virtv1.VirtualMachine{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: vmName}, vm); err != nil {
		return false, err
	}
	if !componenthelpers.VMNeedsBackendStorage(vm) {
		return true, nil
	}
	if _, ok := componenthelpers.SingleTargetStorageClass(t.planVMOrEmpty(vmName)); !ok {
		return true, nil
	}
	cloneName, err := t.offlineBackendStorageCloneName(vmName)
	if err != nil {
		return false, err
	}

	// Only swap the label while the VM is stopped; if a VMI exists it's using the current PVC, so skip.
	if running, err := componenthelpers.VMIExists(ctx, t.Client, vmName, t.Owner.Namespace); err != nil {
		return false, err
	} else if running {
		t.Log.V(2).Info("VM started during offline VMState migration; skipping label handoff", "vm", vmName)
		return true, nil
	}

	clonePVC := &corev1.PersistentVolumeClaim{}
	err = t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: cloneName}, clonePVC)
	if k8serrors.IsNotFound(err) {
		// No clone yet; start it and requeue.
		planVM := t.getPlanVMByName(vmName)
		if planVM == nil {
			return true, nil
		}
		if _, _, ok, err := t.offlineBackendStorageTarget(ctx, vm, planVM); err != nil {
			return false, err
		} else if !ok {
			return true, nil
		}
		if err := t.startOfflineBackendStorageMigration(ctx, vm, planVM); err != nil {
			return false, err
		}
		return false, nil
	} else if err != nil {
		return false, err
	}

	// Wait until the clone is populated before handing the label over.
	dv := &cdiv1.DataVolume{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: cloneName}, dv); err == nil {
		if dv.Status.Phase != cdiv1.Succeeded && dv.Status.Phase != cdiv1.WaitForFirstConsumer {
			return false, nil
		}
	} else if !k8serrors.IsNotFound(err) {
		return false, err
	}

	if err := t.handoffBackendStorageLabel(ctx, vm, clonePVC); err != nil {
		return false, err
	}
	return true, nil
}

// handoffBackendStorageLabel moves the "persistent-state-for" label from the original PVC to the clone.
// Idempotent and safe to re-run if interrupted. The VM is stopped (caller-guaranteed), so the brief
// moment where no PVC holds the label is harmless.
func (t *Task) handoffBackendStorageLabel(ctx context.Context, vm *virtv1.VirtualMachine, clonePVC *corev1.PersistentVolumeClaim) error {
	// Own the clone with the VM (matching native VMState PVCs) and drop the leftover DataVolume.
	if err := t.reownCloneToVM(ctx, vm, clonePVC); err != nil {
		return err
	}
	dv := &cdiv1.DataVolume{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: clonePVC.Namespace, Name: clonePVC.Name}, dv); err == nil {
		if err := t.Client.Delete(ctx, dv); err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else if !k8serrors.IsNotFound(err) {
		return err
	}

	// Remove the label from the original PVC (if it still holds it), then add it to the clone.
	current, err := componenthelpers.GetBackendStoragePVC(ctx, t.Client, vm.Name, t.Owner.Namespace)
	if err != nil {
		return err
	}
	if current != nil && current.Name != clonePVC.Name {
		if err := t.setBackendStorageLabels(ctx, current, func(labels map[string]string) {
			delete(labels, componenthelpers.BackendStoragePVCLabel)
			labels[formerBackendStoragePVCLabel] = vm.Name
		}); err != nil {
			return err
		}
	}

	if clonePVC.Labels[componenthelpers.BackendStoragePVCLabel] != vm.Name {
		if err := t.setBackendStorageLabels(ctx, clonePVC, func(labels map[string]string) {
			labels[componenthelpers.BackendStoragePVCLabel] = vm.Name
		}); err != nil {
			return err
		}
	}
	t.Log.V(3).Info("Handed off VMState PVC to clone on target StorageClass", "vm", vm.Name, "clone", clonePVC.Name)
	return nil
}

// cancelOfflineBackendStorageMigration reverts the migration: restore the label on the original PVC
// (if it was moved) and delete the clone and its DataVolume. Idempotent.
func (t *Task) cancelOfflineBackendStorageMigration(ctx context.Context, vmName string) error {
	former, err := t.findFormerBackendStoragePVC(ctx, vmName)
	if err != nil {
		return err
	}
	if former != nil {
		if err := t.setBackendStorageLabels(ctx, former, func(labels map[string]string) {
			delete(labels, formerBackendStoragePVCLabel)
			labels[componenthelpers.BackendStoragePVCLabel] = vmName
		}); err != nil {
			return err
		}
	}
	cloneName, err := t.offlineBackendStorageCloneName(vmName)
	if err != nil {
		return nil //nolint:nilerr // no suffix means no clone was ever created
	}
	return t.deleteBackendStorageClone(ctx, cloneName)
}

// deleteFormerBackendStoragePVC deletes the original PVC after a completed migration under the
// deleteSource policy. No-op if there is none.
func (t *Task) deleteFormerBackendStoragePVC(ctx context.Context, vmName string) error {
	former, err := t.findFormerBackendStoragePVC(ctx, vmName)
	if err != nil {
		return err
	}
	if former == nil {
		return nil
	}
	t.Log.V(3).Info("Deleting former VMState PVC due to deleteSource retention", "vm", vmName, "pvc", former.Name)
	if err := t.Client.Delete(ctx, former); err != nil && !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}

func (t *Task) planVMOrEmpty(vmName string) *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine {
	if planVM := t.getPlanVMByName(vmName); planVM != nil {
		return planVM
	}
	return &migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{}
}

func (t *Task) reownCloneToVM(ctx context.Context, vm *virtv1.VirtualMachine, clonePVC *corev1.PersistentVolumeClaim) error {
	controller := true
	vmOwner := metav1.OwnerReference{
		APIVersion: virtv1.GroupVersion.String(),
		Kind:       "VirtualMachine",
		Name:       vm.Name,
		UID:        vm.UID,
		Controller: &controller,
	}
	for _, ref := range clonePVC.OwnerReferences {
		if ref.UID == vm.UID {
			return nil // already owned by the VM
		}
	}
	updated := clonePVC.DeepCopy()
	updated.OwnerReferences = []metav1.OwnerReference{vmOwner}
	if err := t.Client.Patch(ctx, updated, k8sclient.MergeFrom(clonePVC)); err != nil {
		return err
	}
	clonePVC.OwnerReferences = updated.OwnerReferences
	return nil
}

func (t *Task) setBackendStorageLabels(ctx context.Context, pvc *corev1.PersistentVolumeClaim, mutate func(map[string]string)) error {
	updated := pvc.DeepCopy()
	if updated.Labels == nil {
		updated.Labels = map[string]string{}
	}
	mutate(updated.Labels)
	if err := t.Client.Patch(ctx, updated, k8sclient.MergeFrom(pvc)); err != nil {
		return err
	}
	pvc.Labels = updated.Labels
	return nil
}

func (t *Task) findFormerBackendStoragePVC(ctx context.Context, vmName string) (*corev1.PersistentVolumeClaim, error) {
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := t.Client.List(ctx, pvcList,
		k8sclient.InNamespace(t.Owner.Namespace),
		k8sclient.MatchingLabels{formerBackendStoragePVCLabel: vmName},
	); err != nil {
		return nil, err
	}
	if len(pvcList.Items) == 0 {
		return nil, nil
	}
	return &pvcList.Items[0], nil
}

func (t *Task) deleteBackendStorageClone(ctx context.Context, cloneName string) error {
	dv := &cdiv1.DataVolume{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: cloneName}, dv); err == nil {
		if err := t.Client.Delete(ctx, dv); err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else if !k8serrors.IsNotFound(err) {
		return err
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: cloneName}, pvc); err == nil {
		if err := t.Client.Delete(ctx, pvc); err != nil && !k8serrors.IsNotFound(err) {
			return err
		}
	} else if !k8serrors.IsNotFound(err) {
		return err
	}
	return nil
}

func backendStorageAccessModes(pvc *corev1.PersistentVolumeClaim) []migrations.VirtualMachineStorageMigrationPlanAccessMode {
	modes := make([]migrations.VirtualMachineStorageMigrationPlanAccessMode, 0, len(pvc.Spec.AccessModes))
	for _, m := range pvc.Spec.AccessModes {
		modes = append(modes, migrations.VirtualMachineStorageMigrationPlanAccessMode(m))
	}
	return modes
}
