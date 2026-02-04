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
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openshift/library-go/pkg/build/naming"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/utils/ptr"
	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	"kubevirt.io/kubevirt-migration-controller/internal/controller/storagemigplan"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func (t *Task) canVMStorageMigrate(ctx context.Context, vmName string) (bool, error) {
	reason, _, err := componenthelpers.ValidateStorageMigrationPossibleForVM(ctx, t.Client, vmName, t.Owner.Namespace)
	if err != nil {
		return false, err
	}
	if reason != "" {
		return false, nil
	}
	return true, nil
}

func (t *Task) liveMigrateVM(ctx context.Context, planVM migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) error {
	// Get the virtual machine
	vm := &virtv1.VirtualMachine{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: planVM.Name}, vm); err != nil {
		return err
	}
	// Ensure the PVCs we are migrating are available in the VM.
	if _, message, err := componenthelpers.ValidatePVCsMatchForVM(ctx, t.Client, &planVM, t.Owner.Namespace); err != nil {
		return err
	} else if message != "" {
		return errors.New(message)
	}

	// Create the target DVs for the PVCs.
	for i, pvc := range planVM.TargetMigrationPVCs {
		// Determine the size of the source PVC.
		// Requires a get call since objectmeta is pruned on the embedded corev1.PersistentVolumeClaim in the planVM
		apiPVC := &corev1.PersistentVolumeClaim{}
		if err := t.Client.Get(ctx, types.NamespacedName{Namespace: planVM.SourcePVCs[i].Namespace, Name: planVM.SourcePVCs[i].Name}, apiPVC); err != nil {
			return err
		}
		if size, err := t.determineSourcePVCSize(ctx, apiPVC); err != nil {
			return err
		} else {
			pvcObjectMeta := apiPVC.ObjectMeta
			if err := t.createTargetDV(ctx, pvc, size, pvcObjectMeta.Labels, pvcObjectMeta.Annotations); err != nil {
				return err
			}
		}
	}

	t.Log.Info("Updating VM for storage migration", "vm", vm.Name)
	// Update the VM to use the new PVCs.
	if err := t.updateVMForStorageMigration(ctx, vm, planVM); err != nil {
		return err
	}
	return nil
}

func (t *Task) determineSourcePVCSize(ctx context.Context, sourcePVC *corev1.PersistentVolumeClaim) (resource.Quantity, error) {
	if componenthelpers.HasDVOwnerRef(sourcePVC) {
		return t.determineDataVolumeSize(ctx, sourcePVC)
	}
	return sourcePVC.Spec.Resources.Requests[corev1.ResourceStorage], nil
}

func (t *Task) determineDataVolumeSize(ctx context.Context, sourcePVC *corev1.PersistentVolumeClaim) (resource.Quantity, error) {
	// TODO: Figure out how to handle the situation where someone has resized the PVC
	// but not the DataVolume (its immutable). We should use the same mechanism as CDI to determine the size (size check pod/annotation).
	dataVolume := &cdiv1.DataVolume{}
	if err := t.Client.Get(ctx, types.NamespacedName{Namespace: t.Owner.Namespace, Name: sourcePVC.Name}, dataVolume); err != nil {
		return resource.Quantity{}, err
	}
	if dataVolume.Spec.PVC != nil {
		return dataVolume.Spec.PVC.Resources.Requests[corev1.ResourceStorage], nil
	} else if dataVolume.Spec.Storage != nil {
		size := dataVolume.Spec.Storage.Resources.Requests[corev1.ResourceStorage]
		if sourcePVC.Spec.VolumeMode != nil && *sourcePVC.Spec.VolumeMode == corev1.PersistentVolumeBlock {
			// Aligning this with MTC behavior for feature parity
			// But ultimately this will be investigated for libvirt issues
			// https://github.com/migtools/mig-controller/blob/a3ff4c526d36af61e20bcf542ea95d97cb9bedcf/pkg/controller/directvolumemigration/pvcs.go#L110
			size = sourcePVC.Status.Capacity[corev1.ResourceStorage]
		}
		return size, nil
	}
	// Cannot figure out the size from the DataVolume, get the size from the PVC.
	return sourcePVC.Spec.Resources.Requests[corev1.ResourceStorage], nil
}

func (t *Task) createTargetDV(ctx context.Context, pvc migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC, size resource.Quantity, labels map[string]string, annotations map[string]string) error {
	targetPvc := pvc.DestinationPVC

	if annotations != nil {
		cleanTargetAnnotations(annotations)
	}
	dv := &cdiv1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name:        *targetPvc.Name,
			Namespace:   t.Owner.Namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Spec: cdiv1.DataVolumeSpec{
			Source: &cdiv1.DataVolumeSource{
				Blank: &cdiv1.DataVolumeBlankImage{},
			},
			Storage: &cdiv1.StorageSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: size,
					},
				},
				StorageClassName: targetPvc.StorageClassName,
			},
		},
	}
	if targetPvc.VolumeMode != nil && (*targetPvc.VolumeMode == corev1.PersistentVolumeBlock || *targetPvc.VolumeMode == corev1.PersistentVolumeFilesystem) {
		dv.Spec.Storage.VolumeMode = targetPvc.VolumeMode
	} else if targetPvc.VolumeMode != nil && *targetPvc.VolumeMode == "Auto" {
		dv.Spec.Storage.VolumeMode = nil
	}
	for _, accessMode := range targetPvc.AccessModes {
		// Only set the access mode on the DV if it is set in the MigPlan, otherwise let CDI figure it out.
		if accessMode == migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadWriteOnce) || accessMode == migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadWriteMany) || accessMode == migrations.VirtualMachineStorageMigrationPlanAccessMode(corev1.ReadOnlyMany) {
			dv.Spec.Storage.AccessModes = append(dv.Spec.Storage.AccessModes, corev1.PersistentVolumeAccessMode(accessMode))
		}
	}

	// Add the CDI allowClaimAdoption annotation to the DV. This allows the DV to adopt the PVC if it already exists.
	if dv.Annotations == nil {
		dv.Annotations = make(map[string]string)
	}
	dv.Annotations["cdi.kubevirt.io/allowClaimAdoption"] = "true"
	// Add the migration UID label to the DV. This allows the DV to be associated with the migration.
	if dv.Labels == nil {
		dv.Labels = make(map[string]string)
	}
	dv.Labels[migrations.VirtualMachineStorageMigrationUIDLabel] = string(t.Owner.UID)
	dv.Labels[migrations.VirtualMachineStorageMigrationPlanLabel] = naming.GetName(t.Plan.Name, "mig", kvalidation.DNS1035LabelMaxLength)
	dv.Labels[migrations.VirtualMachineStorageMigrationPlanUIDLabel] = string(t.Plan.UID)
	if err := t.Client.Create(ctx, dv); err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return nil
		}
		return err
	}
	return nil
}

func (t *Task) updateVMForStorageMigration(ctx context.Context, vm *virtv1.VirtualMachine, planVM migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine) error {
	if vm == nil || vm.Name == "" {
		return fmt.Errorf("vm is nil or empty")
	}

	planTargetPVCMap := make(map[string]migrations.VirtualMachineStorageMigrationPlanTargetMigrationPVC)
	for _, pvc := range planVM.TargetMigrationPVCs {
		planTargetPVCMap[pvc.VolumeName] = pvc
	}

	vmCopy := vm.DeepCopy()
	// Ensure the VM migration strategy is set properly.
	vmCopy.Spec.UpdateVolumesStrategy = ptr.To(virtv1.UpdateVolumesStrategyMigration)

	// Update the VM to use the new PVCs.
	for i, volume := range vm.Spec.Template.Spec.Volumes {
		if planPVC, ok := planTargetPVCMap[volume.Name]; ok {
			if volume.PersistentVolumeClaim != nil {
				vmCopy.Spec.Template.Spec.Volumes[i].PersistentVolumeClaim.ClaimName = *planPVC.DestinationPVC.Name
			}
			if volume.DataVolume != nil {
				vmCopy.Spec.Template.Spec.Volumes[i].DataVolume.Name = *planPVC.DestinationPVC.Name
			}
		}
	}

	planPVCNameMap := make(map[string]string)
	for _, pvc := range planVM.SourcePVCs {
		planPVCNameMap[pvc.Name] = pvc.VolumeName
	}
	// Update the DataVolumeTemplates to match the new PVCs.
	for i, dvTemplate := range vmCopy.Spec.DataVolumeTemplates {
		if _, ok := planPVCNameMap[dvTemplate.Name]; ok {
			volumeName := planPVCNameMap[dvTemplate.Name]
			if targetPVC, ok := planTargetPVCMap[volumeName]; ok {
				vmCopy.Spec.DataVolumeTemplates[i].Name = *targetPVC.DestinationPVC.Name
				if vmCopy.Spec.DataVolumeTemplates[i].Spec.Storage != nil {
					vmCopy.Spec.DataVolumeTemplates[i].Spec.Storage.StorageClassName = targetPVC.DestinationPVC.StorageClassName
				} else if vmCopy.Spec.DataVolumeTemplates[i].Spec.PVC != nil {
					vmCopy.Spec.DataVolumeTemplates[i].Spec.PVC.StorageClassName = targetPVC.DestinationPVC.StorageClassName
				}
			}
		}
	}
	if !apiequality.Semantic.DeepEqual(vm, vmCopy) {
		data, err := client.MergeFrom(vm).Data(vmCopy)
		if err != nil {
			return err
		}
		t.Log.V(5).Info("patch", "data", string(data))
		if err := t.Client.Patch(ctx, vmCopy, client.MergeFrom(vm)); err != nil {
			return err
		}
	}
	return nil
}

// Update the VirtualMachineStorageMigrationPlan to trigger a refresh of virtual machine statuses.
// This is used to ensure that the VirtualMachineStorageMigrationPlan is updated with the latest status of the virtual machines.
// This is done by adding a start time annotation to the plan and then waiting for the plan to refresh.
// The plan controller will put the end time annotation on the plan when the refresh is complete.
// If the end time is after the start time, then the refresh is complete and we can start the live migration.
func (t *Task) refreshReadyVirtualMachines(ctx context.Context) error {
	plan, err := componenthelpers.GetStorageMigrationPlan(ctx, t.Client, t.Owner.Spec.VirtualMachineStorageMigrationPlanRef)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("plan not found")
	}
	planCopy := plan.DeepCopy()
	// The mig plan reconcile will refresh all the information we want. So in order to force
	// a refresh we just need to touch the plan.
	if planCopy.Annotations == nil {
		planCopy.Annotations = make(map[string]string)
	}
	planCopy.Annotations[storagemigplan.RefreshStartTimeAnnotation] = time.Now().Format(time.RFC3339Nano)
	delete(planCopy.Annotations, storagemigplan.RefreshEndTimeAnnotation)
	t.Log.V(5).Info("new plan annotations", "annotations", planCopy.Annotations)
	if err := t.Client.Patch(ctx, planCopy, client.MergeFrom(plan)); err != nil {
		return err
	}
	t.Log.V(5).Info("refreshed plan annotations", "annotations", planCopy.Annotations)
	return nil
}

func (t *Task) refreshCompletedVirtualMachines(ctx context.Context) (bool, error) {
	log := logf.FromContext(ctx)
	plan, err := componenthelpers.GetStorageMigrationPlan(ctx, t.Client, t.Owner.Spec.VirtualMachineStorageMigrationPlanRef)
	if err != nil {
		return false, err
	}
	var startTime time.Time
	if startTimeString, ok := plan.Annotations[storagemigplan.RefreshStartTimeAnnotation]; !ok {
		return false, fmt.Errorf("refresh start time not found")
	} else {
		log.V(5).Info("refresh start time", "startTime", startTimeString)
		if startTime, err = time.Parse(time.RFC3339Nano, startTimeString); err != nil {
			return false, err
		}
	}
	if endTime, ok := plan.Annotations[storagemigplan.RefreshEndTimeAnnotation]; ok {
		log.V(5).Info("refresh end time", "endTime", endTime)
		if endTime, err := time.Parse(time.RFC3339Nano, endTime); err != nil {
			return false, err
		} else {
			if endTime.After(startTime) {
				log.V(5).Info("refresh completed", "endTime", endTime, "startTime", startTime)
				return true, nil
			} else {
				log.V(5).Info("refresh not completed", "endTime", endTime, "startTime", startTime)
			}
		}
	}
	return false, nil
}

func cleanTargetAnnotations(annotations map[string]string) {
	// Remove annotations indicating the PVC is bound or provisioned
	delete(annotations, "pv.kubernetes.io/bind-completed")
	delete(annotations, "volume.beta.kubernetes.io/storage-provisioner")
	delete(annotations, "pv.kubernetes.io/bound-by-controller")
	delete(annotations, "volume.kubernetes.io/storage-provisioner")
	// Remove any cdi related annotations from the PVC
	for k := range annotations {
		if strings.HasPrefix(k, "cdi.kubevirt.io") || strings.HasPrefix(k, "volume.kubernetes.io/selected-node") {
			delete(annotations, k)
		}
	}
}
