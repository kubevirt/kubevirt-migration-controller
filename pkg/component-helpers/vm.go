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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	StorageLiveMigratable = "StorageLiveMigratable"
)

// VMIExists returns true if a VirtualMachineInstance exists for the given VM name (VMI has the same name as the VM).
// If the VMI exists, we use live migration; if it does not exist, the VM is not running and we use offline migration.
func VMIExists(ctx context.Context, client k8sclient.Client, vmName, namespace string) (bool, error) {
	vmi := &virtv1.VirtualMachineInstance{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: vmName}, vmi); err != nil {
		if !k8serrors.IsNotFound(err) {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// IsVMRunning returns true if the VM is running: a VMI exists for it and the VMI's IsRunning() method returns true.
// If the VMI does not exist, the VM is not running.
func IsVMRunning(ctx context.Context, client k8sclient.Client, vmName, namespace string) (bool, error) {
	vmi := &virtv1.VirtualMachineInstance{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: vmName}, vmi); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return vmi.IsRunning(), nil
}

func ValidateStorageMigrationPossibleForVM(ctx context.Context,
	client k8sclient.Client,
	vmName string,
	namespace string) (string, string, error) {
	// Check the conditions for the virtual machine.
	vm := virtv1.VirtualMachine{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: vmName}, &vm); err != nil {
		if k8serrors.IsNotFound(err) {
			return migrations.NotFound, "virtual machine not found", nil
		}
		return "", "", err
	}
	// Offline migration: VM not running is fine — we will clone DVs. Mark as ready.
	running, err := IsVMRunning(ctx, client, vmName, namespace)
	if err != nil {
		return "", "", err
	}
	if !running {
		return "", "", nil
	}
	// Live migration: VM is running, so require Ready and live-migration conditions.
	if !vm.Status.Ready {
		return migrations.NotReady, "virtual machine is not ready", nil
	}
	for _, condition := range vm.Status.Conditions {
		if condition.Type == StorageLiveMigratable && condition.Status == corev1.ConditionFalse {
			return condition.Reason, condition.Message, nil
		}
		if condition.Type == virtv1.VirtualMachineRestartRequired && condition.Status == corev1.ConditionTrue {
			return condition.Reason, condition.Message, nil
		}
	}
	return "", "", nil
}

func ValidatePVCsMatchForVM(ctx context.Context,
	client k8sclient.Client,
	migplanVM *migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine,
	namespace string) (string, string, error) {
	vm := &virtv1.VirtualMachine{}
	log := logf.FromContext(ctx)
	if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: migplanVM.Name}, vm); err != nil {
		if k8serrors.IsNotFound(err) {
			return migrations.NotFound, "virtual machine not found", nil
		}
		return "", "", err
	}

	vmVolumeMap := make(map[string]virtv1.Volume)
	for _, volume := range vm.Spec.Template.Spec.Volumes {
		vmVolumeMap[volume.Name] = volume
	}
	for _, volume := range migplanVM.SourcePVCs {
		log.Info("Validating volume", "volume", volume.VolumeName)
		if _, ok := vmVolumeMap[volume.VolumeName]; !ok {
			return migrations.NotFound, fmt.Sprintf("volume %s not found in virtual machine", volume.VolumeName), nil
		}
		log.Info("Volume found in virtual machine", "volume", volume.VolumeName)
	}
	return "", "", nil
}

func GetVirtualMachineFromName(ctx context.Context,
	client k8sclient.Client,
	vmName string,
	namespace string) (*virtv1.VirtualMachine, error) {
	vm := &virtv1.VirtualMachine{}
	if err := client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: vmName}, vm); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return vm, nil
}

func HasDVOwnerRef(pvc *corev1.PersistentVolumeClaim) bool {
	for _, ownerRef := range pvc.OwnerReferences {
		if ownerRef.Kind == "DataVolume" {
			return true
		}
	}
	return false
}
