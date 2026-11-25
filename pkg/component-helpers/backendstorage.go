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
package componenthelpers

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// BackendStoragePVCLabel is the label KubeVirt puts on the VMState PVC; its value is the VM name and
// it is also the PVC name prefix ("persistent-state-for-<vm>").
// TODO (kubevirt v1.10): declarative vmstate uses a different label ("kubevirt.io/vmStateOwner").
const BackendStoragePVCLabel = "persistent-state-for"

// VMNeedsBackendStorage reports whether a VM has backend storage (the VMState PVC holding vTPM, EFI
// NVRAM and CBT metadata): persistent vTPM, persistent EFI, or enabled CBT. Same check as KubeVirt's
// IsBackendStorageNeeded.
func VMNeedsBackendStorage(vm *virtv1.VirtualMachine) bool {
	if vm == nil || vm.Spec.Template == nil {
		return false
	}
	return hasPersistentTPM(&vm.Spec.Template.Spec) ||
		hasPersistentEFI(&vm.Spec.Template.Spec) ||
		hasCBTEnabled(vm.Status.ChangedBlockTracking)
}

func hasPersistentTPM(spec *virtv1.VirtualMachineInstanceSpec) bool {
	tpm := spec.Domain.Devices.TPM
	return tpm != nil && (tpm.Enabled == nil || *tpm.Enabled) && tpm.Persistent != nil && *tpm.Persistent
}

func hasPersistentEFI(spec *virtv1.VirtualMachineInstanceSpec) bool {
	fw := spec.Domain.Firmware
	return fw != nil && fw.Bootloader != nil && fw.Bootloader.EFI != nil &&
		fw.Bootloader.EFI.Persistent != nil && *fw.Bootloader.EFI.Persistent
}

func hasCBTEnabled(status *virtv1.ChangedBlockTrackingStatus) bool {
	return status != nil &&
		(status.State == virtv1.ChangedBlockTrackingInitializing || status.State == virtv1.ChangedBlockTrackingEnabled)
}

// SingleTargetStorageClass returns the storage class all of the VM's volumes target, or (false) if
// they differ or there are none. The VMState PVC has no per-volume mapping, so we only have a
// destination for it when the whole VM moves to one storage class.
// TODO (kubevirt v1.10): not needed; declarative vmstate has its own storage class field.
func SingleTargetStorageClass(
	planVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine,
) (string, bool) {
	sc := ""
	for _, pvc := range planVM.TargetMigrationPVCs {
		if pvc.DestinationPVC.StorageClassName == nil || *pvc.DestinationPVC.StorageClassName == "" {
			return "", false
		}
		if sc == "" {
			sc = *pvc.DestinationPVC.StorageClassName
		} else if sc != *pvc.DestinationPVC.StorageClassName {
			return "", false
		}
	}
	if sc == "" {
		return "", false
	}
	return sc, true
}

// GetBackendStoragePVC looks up a VM's VMState PVC by its "persistent-state-for=<vm>" label, or nil
// if there is none (e.g. the VM has never started).
func GetBackendStoragePVC(
	ctx context.Context, client k8sclient.Client, vmName, namespace string,
) (*corev1.PersistentVolumeClaim, error) {
	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := client.List(ctx, pvcList,
		k8sclient.InNamespace(namespace),
		k8sclient.MatchingLabels{BackendStoragePVCLabel: vmName},
	); err != nil {
		return nil, err
	}
	if len(pvcList.Items) == 0 {
		return nil, nil
	}
	return &pvcList.Items[0], nil
}

// GetVMStateStorageClass reads the cluster-wide vmStateStorageClass from the KubeVirt CR, or "" if
// unset or there isn't exactly one KubeVirt CR.
func GetVMStateStorageClass(ctx context.Context, client k8sclient.Client) (string, error) {
	kubevirtList := &virtv1.KubeVirtList{}
	if err := client.List(ctx, kubevirtList); err != nil {
		return "", err
	}
	if len(kubevirtList.Items) != 1 {
		return "", nil
	}
	return kubevirtList.Items[0].Spec.Configuration.VMStateStorageClass, nil
}
