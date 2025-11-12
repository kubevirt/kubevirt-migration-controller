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
package storagemigplan

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"k8s.io/utils/ptr"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
)

// Types
const (
	KubeVirtNotInstalledSourceCluster      = "KubeVirtNotInstalledSourceCluster"
	KubeVirtVersionNotSupported            = "KubeVirtVersionNotSupported"
	KubeVirtStorageLiveMigrationNotEnabled = "KubeVirtStorageLiveMigrationNotEnabled"
	StorageMigrationNotPossible            = "StorageMigrationNotPossible"
	InvalidPVCs                            = "InvalidPVCs"
	NotAllVirtualMachinesReady             = "NotAllVirtualMachinesReady"
)

// Messages
const (
	KubeVirtNotInstalledSourceClusterMessage      = "KubeVirt is not installed on the source cluster"
	KubeVirtVersionNotSupportedMessage            = "KubeVirt version does not support storage live migration, Virtual Machines will be stopped instead"
	KubeVirtStorageLiveMigrationNotEnabledMessage = "KubeVirt storage live migration is not enabled, Virtual Machines will be stopped instead"
)

// Valid kubevirt feature gates
const (
	VolumesUpdateStrategy = "VolumesUpdateStrategy"
	VolumeMigrationConfig = "VolumeMigration"
	VMLiveUpdateFeatures  = "VMLiveUpdateFeatures"
	storageProfile        = "auto"
)

var (
	suffixMatcher = regexp.MustCompile(`(.*)-mig-([\d|[:alpha:]]{4})$`)
)

// Validate the plan resource.
func (r *StorageMigPlanReconciler) validate(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) error {
	if err := r.validateLiveMigrationPossible(ctx, plan); err != nil {
		return fmt.Errorf("err checking if live migration is possible: %w", err)
	}

	return nil
}

func (r *StorageMigPlanReconciler) validateLiveMigrationPossible(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) error {
	if err := r.validateKubeVirtInstalled(ctx, plan); err != nil {
		return err
	}
	if plan.Status.HasCriticalCondition() {
		return nil
	}
	if err := r.validateStorageMigrationPossible(ctx, plan); err != nil {
		return err
	}
	return nil
}

func (r *StorageMigPlanReconciler) validateStorageMigrationPossible(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) error {
	// Loop over the virtual machines in the plan and validate if the storage migration is possible.
	plan.Status.ReadyMigrations = make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	plan.Status.InvalidMigrations = make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	plan.Status.CompletedMigrations = make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	plan.Status.InProgressMigrations = make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	plan.Status.FailedMigrations = make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	for _, vm := range plan.Spec.VirtualMachines {
		if reason, message, err := componenthelpers.ValidateStorageMigrationPossibleForVM(ctx, r.Client, vm.Name, plan.Namespace); err != nil {
			return err
		} else if message != "" || reason != "" {
			log := logf.FromContext(ctx)
			log.Info("Setting StorageMigrationNotPossible condition", "reason", reason, "message", message)
			plan.Status.SetCondition(migrations.Condition{
				Type:     StorageMigrationNotPossible,
				Status:   corev1.ConditionTrue,
				Reason:   reason,
				Category: migrations.Critical,
				Message:  message,
			})
			return nil
		}

		statusVM, err := r.createStatusVM(ctx, &vm, plan.Namespace, plan.Status.Suffix)
		if err != nil {
			return err
		}

		// Add the virtual machine to the ready migrations if it is not completed.
		if !r.isVMCompleted(plan, &vm) && !r.isVMInProgress(plan, &vm) && !r.isVMFailed(plan, &vm) {
			plan.Status.ReadyMigrations = append(plan.Status.ReadyMigrations, statusVM)
		}
	}
	// Validate the PVCs are valid
	if reason, message, err := r.validatePVCs(ctx, plan); err != nil {
		return err
	} else if message != "" {
		plan.Status.SetCondition(migrations.Condition{
			Type:     InvalidPVCs,
			Status:   corev1.ConditionTrue,
			Reason:   reason,
			Category: migrations.Critical,
			Message:  message,
		})
		return nil
	} else {
		plan.Status.DeleteCondition(InvalidPVCs)
	}
	if len(plan.Status.ReadyMigrations) > 0 {
		// Remove the storage migration not possible condition for the virtual machine.
		plan.Status.DeleteCondition(StorageMigrationNotPossible)
	}
	if len(plan.Status.ReadyMigrations)+len(plan.Status.InProgressMigrations)+len(plan.Status.CompletedMigrations)+len(plan.Status.FailedMigrations) != len(plan.Spec.VirtualMachines) {
		plan.Status.SetCondition(migrations.Condition{
			Type:     NotAllVirtualMachinesReady,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotReady,
			Category: migrations.Warn,
			Message:  "Some virtual machines are not ready for storage migration",
		})
	}
	return nil
}

func (r *StorageMigPlanReconciler) isVMCompleted(plan *migrations.VirtualMachineStorageMigrationPlan, vm *migrations.VirtualMachineStorageMigrationPlanVirtualMachine) bool {
	for _, completedVM := range plan.Status.CompletedMigrations {
		if completedVM.Name == vm.Name {
			return true
		}
	}
	return false
}

func (r *StorageMigPlanReconciler) isVMInProgress(plan *migrations.VirtualMachineStorageMigrationPlan, vm *migrations.VirtualMachineStorageMigrationPlanVirtualMachine) bool {
	for _, inProgressVM := range plan.Status.InProgressMigrations {
		if inProgressVM.Name == vm.Name {
			return true
		}
	}
	return false
}

func (r *StorageMigPlanReconciler) isVMFailed(plan *migrations.VirtualMachineStorageMigrationPlan, vm *migrations.VirtualMachineStorageMigrationPlanVirtualMachine) bool {
	for _, failedVM := range plan.Status.FailedMigrations {
		if failedVM.Name == vm.Name {
			return true
		}
	}
	return false
}

func (r *StorageMigPlanReconciler) createStatusVM(ctx context.Context, migPlanVM *migrations.VirtualMachineStorageMigrationPlanVirtualMachine, namespace string, suffix *string) (migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, error) {
	statusVM := migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{
		VirtualMachineStorageMigrationPlanVirtualMachine: *migPlanVM.DeepCopy(),
	}

	vm, err := componenthelpers.GetVirtualMachineFromName(ctx, r.Client, migPlanVM.Name, namespace)
	if err != nil {
		return migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{}, err
	}

	for i, pvc := range statusVM.TargetMigrationPVCs {
		sourcePVC, err := r.getSourcePVC(ctx, vm, pvc.VolumeName)
		if err != nil {
			return migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{}, err
		}
		statusVM.SourcePVCs = append(statusVM.SourcePVCs, migrations.VirtualMachineStorageMigrationPlanSourcePVC{
			VolumeName: pvc.VolumeName,
			Name:       sourcePVC.Name,
			Namespace:  sourcePVC.Namespace,
			SourcePVC:  *sourcePVC,
		})
		currentTarget := statusVM.TargetMigrationPVCs[i].DestinationPVC.Name
		newTargetName, err := r.targetPVCName(currentTarget, sourcePVC.Name, suffix)
		if err != nil {
			return migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine{}, err
		}
		statusVM.TargetMigrationPVCs[i].DestinationPVC.Name = ptr.To[string](newTargetName)
	}
	return statusVM, nil
}

func (r *StorageMigPlanReconciler) getSourcePVC(ctx context.Context, vm *virtv1.VirtualMachine, volumeName string) (*corev1.PersistentVolumeClaim, error) {
	pvcName := ""
	for _, volume := range vm.Spec.Template.Spec.Volumes {
		if volume.Name == volumeName {
			if volume.VolumeSource.PersistentVolumeClaim != nil {
				pvcName = volume.VolumeSource.PersistentVolumeClaim.ClaimName
				break
			}
			if volume.VolumeSource.DataVolume != nil {
				pvcName = volume.VolumeSource.DataVolume.Name
				break
			}
		}
	}
	if pvcName == "" {
		return nil, fmt.Errorf("pvc not found for volume %s", volumeName)
	}
	pvc := &corev1.PersistentVolumeClaim{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: vm.Namespace, Name: pvcName}, pvc); err != nil {
		return nil, err
	}
	return pvc, nil
}

func (r *StorageMigPlanReconciler) targetPVCName(name *string, sourcePVCName string, suffix *string) (string, error) {
	if name != nil {
		return *name, nil
	}
	sourcePVCName = trimSuffix(sourcePVCName)
	if suffix != nil {
		return fmt.Sprintf("%s-mig-%s", sourcePVCName, *suffix), nil
	}
	return "", fmt.Errorf("no name provided and no suffix set")
}

func trimSuffix(pvcName string) string {
	suffix := "-new"
	if suffixMatcher.MatchString(pvcName) {
		suffixFixCols := suffixMatcher.FindStringSubmatch(pvcName)
		suffix = "-mig-" + suffixFixCols[2]
	}
	return strings.TrimSuffix(pvcName, suffix)
}

func (r *StorageMigPlanReconciler) validatePVCs(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) (string, string, error) {
	readyMigrations := make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	invalidMigrations := make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	lastReason := ""
	lastMessage := ""
	for _, vm := range plan.Status.ReadyMigrations {
		issueFound := false
		for i, pvc := range vm.TargetMigrationPVCs {
			if pvc.DestinationPVC.StorageClassName != nil && *pvc.DestinationPVC.StorageClassName != "" {
				// Validate the storage class exists in the cluster
				if reason, message, err := r.validateStorageClassExists(ctx, *pvc.DestinationPVC.StorageClassName); err != nil {
					return "", "", err
				} else if message != "" {
					lastReason = reason
					lastMessage = message
					issueFound = true
					break
				}
			} else {
				// Validate a default storage class exists in the cluster.
				if defaultStorageClass, err := r.getDefaultStorageClass(ctx); err != nil {
					return "", "", err
				} else if defaultStorageClass != "" {
					// Set the targetPVC storage class to the default storage class.
					vm.TargetMigrationPVCs[i].DestinationPVC.StorageClassName = ptr.To[string](defaultStorageClass)
				} else {
					lastReason = migrations.NotFound
					lastMessage = "no default storage class found"
					issueFound = true
					break
				}
			}
			if vm.TargetMigrationPVCs[i].DestinationPVC.Name != nil && *vm.TargetMigrationPVCs[i].DestinationPVC.Name == vm.SourcePVCs[i].Name {
				lastReason = migrations.Conflict
				lastMessage = fmt.Sprintf("VM %s has a destination PVC name for volume %s that is the same as the source PVC name", vm.Name, vm.TargetMigrationPVCs[i].VolumeName)
				issueFound = true
				break
			}
		}
		if !issueFound {
			readyMigrations = append(readyMigrations, vm)
		} else {
			invalidMigrations = append(invalidMigrations, vm)
		}
	}
	plan.Status.ReadyMigrations = readyMigrations
	plan.Status.InvalidMigrations = invalidMigrations
	return lastReason, lastMessage, nil
}

func (r *StorageMigPlanReconciler) validateStorageClassExists(ctx context.Context, storageClass string) (string, string, error) {
	if err := r.Client.Get(ctx, types.NamespacedName{Name: storageClass}, &storagev1.StorageClass{}); err != nil {
		if k8serrors.IsNotFound(err) {
			return migrations.NotFound, fmt.Sprintf("storage class %s not found", storageClass), nil
		}
		return "", "", err
	}
	return "", "", nil
}

// Get the default storage class. The default virt storage class is preferred over the default k8s storage class.
func (r *StorageMigPlanReconciler) getDefaultStorageClass(ctx context.Context) (string, error) {
	virtStorageClass, err := componenthelpers.GetDefaultVirtStorageClass(ctx, r.Client)
	if err != nil {
		return "", err
	}
	if virtStorageClass != nil {
		return *virtStorageClass, nil
	}
	defaultStorageClass, err := componenthelpers.GetDefaultStorageClass(ctx, r.Client)
	if err != nil {
		return "", err
	}
	if defaultStorageClass != nil {
		return *defaultStorageClass, nil
	}
	return "", nil
}

func (r *StorageMigPlanReconciler) validateKubeVirtInstalled(ctx context.Context, plan *migrations.VirtualMachineStorageMigrationPlan) error {
	log := logf.FromContext(ctx)
	kubevirtList := &virtv1.KubeVirtList{}
	if err := r.Client.List(ctx, kubevirtList); err != nil {
		if meta.IsNoMatchError(err) {
			plan.Status.SetCondition(migrations.Condition{
				Type:     KubeVirtNotInstalledSourceCluster,
				Status:   corev1.ConditionTrue,
				Reason:   migrations.NotFound,
				Category: migrations.Critical,
				Message:  KubeVirtNotInstalledSourceClusterMessage,
			})
			return nil
		}
		return fmt.Errorf("error listing kubevirts: %w", err)
	}
	if len(kubevirtList.Items) == 0 || len(kubevirtList.Items) > 1 {
		plan.Status.SetCondition(migrations.Condition{
			Type:     KubeVirtNotInstalledSourceCluster,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotFound,
			Category: migrations.Critical,
			Message:  KubeVirtNotInstalledSourceClusterMessage,
		})
		return nil
	}
	kubevirt := kubevirtList.Items[0]
	operatorVersion := kubevirt.Status.OperatorVersion
	major, minor, bugfix, err := parseKubeVirtOperatorSemver(operatorVersion)
	if err != nil {
		plan.Status.SetCondition(migrations.Condition{
			Type:     KubeVirtVersionNotSupported,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotSupported,
			Category: migrations.Critical,
			Message:  KubeVirtVersionNotSupportedMessage,
		})
		return nil
	}
	log.V(3).Info("KubeVirt operator version", "major", major, "minor", minor, "bugfix", bugfix)
	// Check if kubevirt operator version is at least 1.3.0 if live migration is enabled.
	if major < 1 || (major == 1 && minor < 3) {
		log.Info("KubeVirt operator version is not supported", "version", operatorVersion)
		plan.Status.SetCondition(migrations.Condition{
			Type:     KubeVirtVersionNotSupported,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotSupported,
			Category: migrations.Critical,
			Message:  KubeVirtVersionNotSupportedMessage,
		})
		return nil
	}
	// Check if the appropriate feature gates are enabled
	if kubevirt.Spec.Configuration.VMRolloutStrategy == nil ||
		*kubevirt.Spec.Configuration.VMRolloutStrategy != virtv1.VMRolloutStrategyLiveUpdate ||
		kubevirt.Spec.Configuration.DeveloperConfiguration == nil ||
		isStorageLiveMigrationDisabled(&kubevirt, major, minor) {
		plan.Status.SetCondition(migrations.Condition{
			Type:     KubeVirtStorageLiveMigrationNotEnabled,
			Status:   corev1.ConditionTrue,
			Reason:   migrations.NotSupported,
			Category: migrations.Critical,
			Message:  KubeVirtStorageLiveMigrationNotEnabledMessage,
		})
		return nil
	}
	return nil
}

func parseKubeVirtOperatorSemver(operatorVersion string) (int, int, int, error) {
	// example versions: v1.1.1-106-g0be1a2073, or: v1.3.0-beta.0.202+f8efa57713ba76-dirty
	tokens := strings.Split(operatorVersion, ".")
	if len(tokens) < 3 {
		return -1, -1, -1, fmt.Errorf("version string was not in semver format, != 3 tokens")
	}

	if tokens[0][0] == 'v' {
		tokens[0] = tokens[0][1:]
	}
	major, err := strconv.Atoi(tokens[0])
	if err != nil {
		return -1, -1, -1, fmt.Errorf("major version could not be parsed as integer")
	}

	minor, err := strconv.Atoi(tokens[1])
	if err != nil {
		return -1, -1, -1, fmt.Errorf("minor version could not be parsed as integer")
	}

	bugfixTokens := strings.Split(tokens[2], "-")
	bugfix, err := strconv.Atoi(bugfixTokens[0])
	if err != nil {
		return -1, -1, -1, fmt.Errorf("bugfix version could not be parsed as integer")
	}

	return major, minor, bugfix, nil
}

func isStorageLiveMigrationDisabled(kubevirt *virtv1.KubeVirt, major, minor int) bool {
	if major == 1 && minor >= 5 || major > 1 {
		// Those are all GA from 1.5 and onwards
		// https://github.com/kubevirt/kubevirt/releases/tag/v1.5.0
		return false
	}

	return !slices.Contains(kubevirt.Spec.Configuration.DeveloperConfiguration.FeatureGates, VolumesUpdateStrategy) ||
		!slices.Contains(kubevirt.Spec.Configuration.DeveloperConfiguration.FeatureGates, VolumeMigrationConfig) ||
		!slices.Contains(kubevirt.Spec.Configuration.DeveloperConfiguration.FeatureGates, VMLiveUpdateFeatures)
}
