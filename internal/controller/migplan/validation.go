package migplan

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	virtv1 "kubevirt.io/api/core/v1"
	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

// Types
const (
	Suspended                                  = "Suspended"
	InvalidSourceClusterRef                    = "InvalidSourceClusterRef"
	InvalidDestinationClusterRef               = "InvalidDestinationClusterRef"
	InvalidStorageRef                          = "InvalidStorageRef"
	SourceClusterNotReady                      = "SourceClusterNotReady"
	DestinationClusterNotReady                 = "DestinationClusterNotReady"
	ClusterVersionMismatch                     = "ClusterVersionMismatch"
	SourceClusterNoRegistryPath                = "SourceClusterNoRegistryPath"
	DestinationClusterNoRegistryPath           = "DestinationClusterNoRegistryPath"
	StorageNotReady                            = "StorageNotReady"
	StorageClassConversionUnavailable          = "StorageClassConversionUnavailable"
	NsListEmpty                                = "NamespaceListEmpty"
	InvalidDestinationCluster                  = "InvalidDestinationCluster"
	NsNotFoundOnSourceCluster                  = "NamespaceNotFoundOnSourceCluster"
	NsNotFoundOnDestinationCluster             = "NamespaceNotFoundOnDestinationCluster"
	NsLimitExceeded                            = "NamespaceLimitExceeded"
	NsLengthExceeded                           = "NamespaceLengthExceeded"
	NsNotDNSCompliant                          = "NamespaceNotDNSCompliant"
	NsHaveNodeSelectors                        = "NamespacesHaveNodeSelectors"
	DuplicateNsOnSourceCluster                 = "DuplicateNamespaceOnSourceCluster"
	DuplicateNsOnDestinationCluster            = "DuplicateNamespaceOnDestinationCluster"
	PodLimitExceeded                           = "PodLimitExceeded"
	SourceClusterProxySecretMisconfigured      = "SourceClusterProxySecretMisconfigured"
	DestinationClusterProxySecretMisconfigured = "DestinationClusterProxySecretMisconfigured"
	PlanConflict                               = "PlanConflict"
	PvNameConflict                             = "PvNameConflict"
	PvInvalidAction                            = "PvInvalidAction"
	PvNoSupportedAction                        = "PvNoSupportedAction"
	PvInvalidStorageClass                      = "PvInvalidStorageClass"
	PvInvalidAccessMode                        = "PvInvalidAccessMode"
	PvNoStorageClassSelection                  = "PvNoStorageClassSelection"
	PvWarnAccessModeUnavailable                = "PvWarnAccessModeUnavailable"
	PvInvalidCopyMethod                        = "PvInvalidCopyMethod"
	PvCapacityAdjustmentRequired               = "PvCapacityAdjustmentRequired"
	PvUsageAnalysisFailed                      = "PvUsageAnalysisFailed"
	PvNoCopyMethodSelection                    = "PvNoCopyMethodSelection"
	PvWarnCopyMethodSnapshot                   = "PvWarnCopyMethodSnapshot"
	NfsNotAccessible                           = "NfsNotAccessible"
	NfsAccessCannotBeValidated                 = "NfsAccessCannotBeValidated"
	PvLimitExceeded                            = "PvLimitExceeded"
	StorageEnsured                             = "StorageEnsured"
	RegistriesEnsured                          = "RegistriesEnsured"
	RegistriesHealthy                          = "RegistriesHealthy"
	PvsDiscovered                              = "PvsDiscovered"
	Closed                                     = "Closed"
	SourcePodsNotHealthy                       = "SourcePodsNotHealthy"
	GVKsIncompatible                           = "GVKsIncompatible"
	InvalidHookRef                             = "InvalidHookRef"
	InvalidResourceList                        = "InvalidResourceList"
	HookNotReady                               = "HookNotReady"
	InvalidHookNSName                          = "InvalidHookNSName"
	InvalidHookSAName                          = "InvalidHookSAName"
	HookPhaseUnknown                           = "HookPhaseUnknown"
	HookPhaseDuplicate                         = "HookPhaseDuplicate"
	IntraClusterMigration                      = "IntraClusterMigration"
	KubeVirtNotInstalledSourceCluster          = "KubeVirtNotInstalledSourceCluster"
	KubeVirtVersionNotSupported                = "KubeVirtVersionNotSupported"
	KubeVirtStorageLiveMigrationNotEnabled     = "KubeVirtStorageLiveMigrationNotEnabled"
	StorageMigrationNotPossible                = "StorageMigrationNotPossible"
	StorageLiveMigratable                      = "StorageLiveMigratable"
)

// Categories
const (
	Advisory = migrationsv1alpha1.Advisory
	Critical = migrationsv1alpha1.Critical
	Error    = migrationsv1alpha1.Error
	Warn     = migrationsv1alpha1.Warn
)

// Reasons
const (
	NotSet                 = "NotSet"
	NotFound               = "NotFound"
	NotReady               = "NotReady"
	KeyNotFound            = "KeyNotFound"
	NotDistinct            = "NotDistinct"
	LimitExceeded          = "LimitExceeded"
	LengthExceeded         = "LengthExceeded"
	NotDNSCompliant        = "NotDNSCompliant"
	NotDone                = "NotDone"
	IsDone                 = "Done"
	Conflict               = "Conflict"
	NotHealthy             = "NotHealthy"
	NodeSelectorsDetected  = "NodeSelectorsDetected"
	DuplicateNs            = "DuplicateNamespaces"
	ConflictingNamespaces  = "ConflictingNamespaces"
	ConflictingPermissions = "ConflictingPermissions"
	NotSupported           = "NotSupported"
)

// Messages
const (
	KubeVirtNotInstalledSourceClusterMessage      = "KubeVirt is not installed on the source cluster"
	KubeVirtVersionNotSupportedMessage            = "KubeVirt version does not support storage live migration, Virtual Machines will be stopped instead"
	KubeVirtStorageLiveMigrationNotEnabledMessage = "KubeVirt storage live migration is not enabled, Virtual Machines will be stopped instead"
)

// Statuses
const (
	True  = migrationsv1alpha1.True
	False = migrationsv1alpha1.False
)

// Valid kubevirt feature gates
const (
	VolumesUpdateStrategy = "VolumesUpdateStrategy"
	VolumeMigrationConfig = "VolumeMigration"
	VMLiveUpdateFeatures  = "VMLiveUpdateFeatures"
	storageProfile        = "auto"
)

// Validate the plan resource.
func (r *MigPlanReconciler) validate(ctx context.Context, plan *migrationsv1alpha1.MigPlan) error {
	if err := r.validateLiveMigrationPossible(ctx, plan); err != nil {
		return fmt.Errorf("err checking if live migration is possible: %w", err)
	}

	return nil
}

func (r *MigPlanReconciler) validateLiveMigrationPossible(ctx context.Context, plan *migrationsv1alpha1.MigPlan) error {
	if err := r.validateKubeVirtInstalled(ctx, plan); err != nil {
		return err
	}
	if err := r.validateStorageMigrationPossible(ctx, plan); err != nil {
		return err
	}
	return nil
}

func (r *MigPlanReconciler) validateStorageMigrationPossible(ctx context.Context, plan *migrationsv1alpha1.MigPlan) error {
	// Loop over the virtual machines in the plan and validate if the storage migration is possible.
	for _, vm := range plan.Spec.VirtualMachines {
		if reason, message, err := r.validateStorageMigrationPossibleForVM(ctx, &vm, plan.Namespace); err != nil {
			return err
		} else if message != "" {
			plan.Status.SetCondition(migrationsv1alpha1.Condition{
				Type:     StorageMigrationNotPossible,
				Status:   True,
				Reason:   reason,
				Category: Critical,
				Message:  message,
			})
			return nil
		}
	}
	// Remove the storage migration not possible condition for the virtual machine.
	plan.Status.DeleteCondition(StorageMigrationNotPossible)
	return nil
}

func (r *MigPlanReconciler) validateStorageMigrationPossibleForVM(ctx context.Context, planVM *migrationsv1alpha1.MigPlanVirtualMachine, namespace string) (string, string, error) {
	// Check the conditions for the virtual machine.
	vm := virtv1.VirtualMachine{}
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: planVM.Name}, &vm); err != nil {
		return "", "", err
	}
	if !vm.Status.Ready {
		return NotReady, "virtual machine is not ready", nil
	}
	for _, condition := range vm.Status.Conditions {
		if condition.Type == StorageLiveMigratable && condition.Status == False {
			return condition.Reason, condition.Message, nil
		}
	}
	return "", "", nil
}

func (r *MigPlanReconciler) validateKubeVirtInstalled(ctx context.Context, plan *migrationsv1alpha1.MigPlan) error {
	log := logf.FromContext(ctx)
	kubevirtList := &virtv1.KubeVirtList{}
	if err := r.Client.List(ctx, kubevirtList); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("error listing kubevirts: %w", err)
	}
	if len(kubevirtList.Items) == 0 || len(kubevirtList.Items) > 1 {
		plan.Status.SetCondition(migrationsv1alpha1.Condition{
			Type:     KubeVirtNotInstalledSourceCluster,
			Status:   True,
			Reason:   NotFound,
			Category: Critical,
			Message:  KubeVirtNotInstalledSourceClusterMessage,
		})
		return nil
	}
	kubevirt := kubevirtList.Items[0]
	operatorVersion := kubevirt.Status.OperatorVersion
	major, minor, bugfix, err := parseKubeVirtOperatorSemver(operatorVersion)
	if err != nil {
		plan.Status.SetCondition(migrationsv1alpha1.Condition{
			Type:     KubeVirtVersionNotSupported,
			Status:   True,
			Reason:   NotSupported,
			Category: Critical,
			Message:  KubeVirtVersionNotSupportedMessage,
		})
		return nil
	}
	log.V(3).Info("KubeVirt operator version", "major", major, "minor", minor, "bugfix", bugfix)
	// Check if kubevirt operator version is at least 1.3.0 if live migration is enabled.
	if major < 1 || (major == 1 && minor < 3) {
		plan.Status.SetCondition(migrationsv1alpha1.Condition{
			Type:     KubeVirtVersionNotSupported,
			Status:   True,
			Reason:   NotSupported,
			Category: Critical,
			Message:  KubeVirtVersionNotSupportedMessage,
		})
		return nil
	}
	// Check if the appropriate feature gates are enabled
	if kubevirt.Spec.Configuration.VMRolloutStrategy == nil ||
		*kubevirt.Spec.Configuration.VMRolloutStrategy != virtv1.VMRolloutStrategyLiveUpdate ||
		kubevirt.Spec.Configuration.DeveloperConfiguration == nil ||
		isStorageLiveMigrationDisabled(&kubevirt, major, minor) {
		plan.Status.SetCondition(migrationsv1alpha1.Condition{
			Type:     KubeVirtStorageLiveMigrationNotEnabled,
			Status:   True,
			Reason:   NotSupported,
			Category: Critical,
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
