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
	"strings"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	virtv1 "kubevirt.io/api/core/v1"
	cdiv1 "kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
	componenthelpers "kubevirt.io/kubevirt-migration-controller/pkg/component-helpers"
)

// Requeue
const (
	FastReQ = time.Millisecond * 100
	PollReQ = time.Second * 20
	NoReQ   = time.Duration(0)

	virtLauncherPodLabelSelectorKey   = "kubevirt.io"
	virtLauncherPodLabelSelectorValue = "virt-launcher"
)

type Task struct {
	Config  *rest.Config
	Scheme  *runtime.Scheme
	Log     logr.Logger
	Client  k8sclient.Client
	Owner   *migrations.VirtualMachineStorageMigration
	Plan    *migrations.VirtualMachineStorageMigrationPlan
	Requeue time.Duration
	Errors  []string
}

// Run the task.
// Each call will:
//  1. Run the current phase.
//  2. Update the phase to the next phase.
//  3. Set the Requeue (as appropriate).
//  4. Return.
func (t *Task) Run(ctx context.Context) error {
	// Set stage, phase, phase description, migplan name
	t.Requeue = NoReQ

	t.init()
	log := t.Log

	t.maybeTransitionToCanceling()

	// Run the current phase.
	switch t.Owner.Status.Phase {
	case migrations.Started:
		log.V(5).Info("Processing Started phase")
		// Set finalizer on migration
		t.Owner.AddFinalizer(migrations.VirtualMachineStorageMigrationFinalizer)
		t.Owner.Status.Phase = migrations.RefreshStorageMigrationPlan
	case migrations.RefreshStorageMigrationPlan:
		log.V(5).Info("Processing RefreshStorageMigrationPlan phase")
		if err := t.refreshReadyVirtualMachines(ctx); err != nil {
			return err
		}
		t.Owner.Status.Phase = migrations.WaitForStorageMigrationPlanRefreshCompletion
	case migrations.WaitForStorageMigrationPlanRefreshCompletion:
		log.V(5).Info("Processing WaitForStorageMigrationPlanRefreshCompletion phase")
		if completed, err := t.refreshCompletedVirtualMachines(ctx); err != nil {
			return err
		} else if !completed {
			t.Requeue = PollReQ
			return nil
		}
		t.Owner.Status.Phase = migrations.BeginLiveMigration
	case migrations.BeginLiveMigration:
		log.V(5).Info("Processing BeginLiveMigration phase")
		err := t.handleBeginLiveMigrationPhase(ctx)
		if err != nil {
			return err
		}
	case migrations.WaitForLiveMigrationToComplete:
		log.V(5).Info("Processing WaitForLiveMigrationToComplete phase")
		err := t.handleWaitForLiveMigrationToCompletePhase(ctx)
		if err != nil {
			return err
		}
	case migrations.CleanupMigrationResources:
		return t.handleCleanupMigrationResourcesPhase(ctx)
	case migrations.Canceling:
		log.V(5).Info("Processing Canceling phase")
		err := t.handleCancelingPhase(ctx)
		if err != nil {
			return err
		}
	case migrations.CleanupCancelledMigrations:
		return t.handleCleanupCancelledMigrationsPhase(ctx)
	case migrations.Canceled:
		t.handleCanceledPhase()
	default:
		t.Requeue = NoReQ
	}
	return nil
}

// maybeTransitionToCanceling moves a deleting migration into Canceling unless
// plan deletion is intentionally blocked by the plan finalizer (cascade delete).
func (t *Task) maybeTransitionToCanceling() {
	if t.Owner.DeletionTimestamp == nil ||
		t.Owner.Status.Phase == migrations.Canceled ||
		t.Owner.Status.Phase == migrations.Completed ||
		t.Owner.Status.Phase == migrations.CleanupCancelledMigrations {
		return
	}
	// Migrations always get a controller owner reference to their plan
	// (see StorageMigrationReconciler.setOwnerReference). Deleting the plan therefore
	// cascade-marks owned migrations for deletion. When the plan still has its finalizer,
	// the plan controller is holding deletion until migrations finish — keep running
	// instead of canceling. Direct migration deletes (or plan delete after the finalizer
	// is gone) still cancel as usual.
	if t.Plan != nil && t.Plan.DeletionTimestamp != nil &&
		migrations.HasFinalizer(t.Plan, migrations.VirtualMachineStorageMigrationPlanFinalizer) {
		t.Log.V(4).Info("Migration deletion deferred: plan deletion is blocked until migrations complete",
			"migration", t.Owner.Name, "phase", t.Owner.Status.Phase)
		return
	}
	t.Log.V(4).Info("Cancelling migration", "migration", t.Owner.Name, "phase", t.Owner.Status.Phase)
	t.Owner.Status.Phase = migrations.Canceling
}

func (t *Task) handleCleanupMigrationResourcesPhase(ctx context.Context) error {
	t.Log.V(3).Info("Processing CleanupMigrationResources phase")
	allCleaned, err := t.cleanupMigrationResources(ctx, t.Owner.Status.CompletedMigrations)
	if err != nil {
		return err
	}
	if !allCleaned {
		t.Requeue = PollReQ
		return nil
	}
	if t.Plan != nil && t.Plan.Spec.RetentionPolicy != nil && *t.Plan.Spec.RetentionPolicy == migrations.RetentionPolicyDeleteSource {
		t.Log.V(3).Info("Deleting source DataVolume and PVCs due to retentionPolicy deleteSource")
		if err := t.deleteSourceDataVolumesAndPVCs(ctx, t.Owner.Status.CompletedMigrations); err != nil {
			return err
		}
	}
	t.Owner.RemoveFinalizer(migrations.VirtualMachineStorageMigrationFinalizer)
	t.Owner.Status.Phase = migrations.Completed
	return nil
}

func (t *Task) handleCleanupCancelledMigrationsPhase(ctx context.Context) error {
	t.Log.V(5).Info("Processing CleanupCancelledMigrations phase")
	allCleaned, err := t.cleanupCancelledMigrationResources(ctx, t.Owner.Status.CancelledMigrations, t.Owner.Status.CompletedMigrations)
	if err != nil {
		return err
	}
	if !allCleaned {
		t.Log.V(4).Info("some cancelled migration resources are not cleaned up, requeuing")
		t.Requeue = PollReQ
		return nil
	}
	t.Owner.Status.Phase = migrations.Canceled
	return nil
}

func (t *Task) handleCanceledPhase() {
	t.Log.V(5).Info("Processing Canceled phase")
	t.Owner.Status.DeleteCondition(string(migrations.Canceling))
	t.Owner.Status.SetCondition(migrations.Condition{
		Type:     string(migrations.Canceled),
		Status:   corev1.ConditionTrue,
		Reason:   Cancel,
		Category: migrations.Advisory,
		Message:  "The migration has been canceled.",
	})
	t.Owner.RemoveFinalizer(migrations.VirtualMachineStorageMigrationFinalizer)
}

func (t *Task) handleBeginLiveMigrationPhase(ctx context.Context) error {
	t.Log.V(5).Info("Processing BeginLiveMigration phase", "readyMigrations", len(t.Plan.Status.ReadyMigrations))
	t.Log.V(5).Info("Processing BeginLiveMigration phase", "inProgressMigrations", len(t.Plan.Status.InProgressMigrations))
	checkMigrations := make([]migrations.VirtualMachineStorageMigrationPlanStatusVirtualMachine, 0)
	checkMigrations = append(checkMigrations, t.Plan.Status.ReadyMigrations...)
	checkMigrations = append(checkMigrations, t.Plan.Status.InProgressMigrations...)
	for _, planVM := range checkMigrations {
		vmiExists, err := componenthelpers.VMIExists(ctx, t.Client, planVM.Name, t.Owner.Namespace)
		if err != nil {
			return err
		}
		if vmiExists {
			if can, err := t.canVMStorageMigrate(ctx, planVM.Name); err != nil {
				return err
			} else if !can {
				t.Log.V(3).Info("VM cannot storage migrate", "vm", planVM.Name)
				continue
			}
			if err := t.liveMigrateVM(ctx, planVM); err != nil {
				return err
			}
			t.Log.V(3).Info("VM live migration is running", "vm", planVM.Name)
			t.Owner.Status.RunningMigrations = append(t.Owner.Status.RunningMigrations, migrations.RunningVirtualMachineMigration{
				Name: planVM.Name,
			})
		} else {
			if err := t.offlineMigrateVM(ctx, planVM); err != nil {
				return err
			}
			t.Log.V(3).Info("VM offline migration is running", "vm", planVM.Name)
			t.Owner.Status.RunningMigrations = append(t.Owner.Status.RunningMigrations, migrations.RunningVirtualMachineMigration{
				Name: planVM.Name,
			})
		}
	}
	t.Owner.Status.Phase = migrations.WaitForLiveMigrationToComplete
	return nil
}

func (t *Task) handleWaitForLiveMigrationToCompletePhase(ctx context.Context) error {
	runningMigrations := make([]migrations.RunningVirtualMachineMigration, 0)
	waitingVMs := make([]string, 0)
	for _, vm := range t.Owner.Status.RunningMigrations {
		vmiExists, err := componenthelpers.VMIExists(ctx, t.Client, vm.Name, t.Owner.Namespace)
		if err != nil {
			return err
		}
		offline := !vmiExists

		if offline {
			t.Log.V(5).Info("Checking if offline migration is completed", "vm", vm.Name)

			waiting, err := t.isOfflineMigrationWaitingForFirstConsumer(ctx, vm.Name)
			if err != nil {
				return err
			}
			if waiting {
				waitingVMs = append(waitingVMs, vm.Name)
				runningMigrations = append(runningMigrations, vm)
				continue
			}

			completed, err := t.isOfflineMigrationCompleted(ctx, vm.Name)
			if err != nil {
				return err
			}
			if !completed {
				runningMigrations = append(runningMigrations, vm)
				continue
			}
		} else {
			t.Log.V(5).Info("Checking if live migration is completed", "vm", vm.Name)

			completed, err := t.isLiveMigrationCompleted(ctx, vm.Name)
			if err != nil {
				return err
			}
			if !completed {
				runningMigrations = append(runningMigrations, vm)
				progress, err := t.getLastObservedProgressPercent(ctx, vm.Name, t.Owner.Namespace)
				if err != nil {
					return err
				}
				if progress != "" {
					runningMigrations[len(runningMigrations)-1].Progress = progress
				}
				continue
			}
		}

		t.Owner.Status.CompletedMigrations = append(t.Owner.Status.CompletedMigrations, vm.Name)
	}
	// SetCondition preserves LastTransitionTime when status/message are unchanged; only delete when clear.
	if len(waitingVMs) > 0 {
		t.Owner.Status.SetCondition(migrations.Condition{
			Type:     migrations.OfflineMigrationWaiting,
			Status:   corev1.ConditionTrue,
			Category: migrations.Warn,
			Reason:   "WaitForFirstConsumer",
			Message: fmt.Sprintf(
				"One or more offline VMs are waiting for first consumer (WaitForFirstConsumer storage class). Start the following VMs to allow data copying to complete the plan: %s",
				strings.Join(waitingVMs, ", "),
			),
		})
	} else {
		t.Owner.Status.DeleteCondition(migrations.OfflineMigrationWaiting)
	}
	t.Owner.Status.RunningMigrations = runningMigrations
	if len(runningMigrations) == 0 {
		t.Owner.Status.Phase = migrations.CleanupMigrationResources
	}
	t.Requeue = PollReQ
	return nil
}

func (t *Task) handleCancelingPhase(ctx context.Context) error {
	runningMigrations := make([]migrations.RunningVirtualMachineMigration, 0)
	cancelledMigrations := make([]string, 0)
	for _, vm := range t.Owner.Status.RunningMigrations {
		vmiExists, err := componenthelpers.VMIExists(ctx, t.Client, vm.Name, t.Owner.Namespace)
		if err != nil {
			return err
		}
		if !vmiExists {
			// Offline: revert VM volumes and mark as cancelled; DVs are deleted in CleanupCancelledMigrations.
			t.Log.V(4).Info("Cancelling offline migration", "vm", vm.Name)
			if err := t.cancelLiveMigration(ctx, vm.Name); err != nil {
				return err
			}
			cancelledMigrations = append(cancelledMigrations, vm.Name)
			continue
		}

		onSource, err := t.isVMOnSourceVolumes(ctx, vm.Name)
		if err != nil {
			return err
		}
		if onSource {
			// Spec points at source, but an in-flight VMIM (abort or reverse migrate)
			// may still be using the target — wait until it finishes before cleanup.
			active, err := t.hasActiveVMIM(ctx, vm.Name)
			if err != nil {
				return err
			}
			if active {
				t.Log.V(4).Info("Waiting for active VMIM to finish after volume revert", "vm", vm.Name)
				runningMigrations = append(runningMigrations, vm)
				continue
			}
			t.Log.V(4).Info("Live migration volumes reverted to source", "vm", vm.Name)
			cancelledMigrations = append(cancelledMigrations, vm.Name)
			continue
		}

		// Still on target volumes — revert even if the VMIM already completed.
		// Otherwise a race where live migration finishes just as we cancel would
		// leave the VM on the destination PVC while we GC the migration CR.
		t.Log.V(4).Info("Reverting live migration volumes to source", "vm", vm.Name)
		if err := t.cancelLiveMigration(ctx, vm.Name); err != nil {
			return err
		}
		runningMigrations = append(runningMigrations, vm)
	}
	t.Owner.Status.RunningMigrations = runningMigrations
	t.Owner.Status.CancelledMigrations = cancelledMigrations
	if len(runningMigrations) == 0 {
		t.Owner.Status.Phase = migrations.CleanupCancelledMigrations
	}
	t.Requeue = PollReQ
	return nil
}

func (t *Task) hasActiveVMIM(ctx context.Context, vmName string) (bool, error) {
	vmimList := &virtv1.VirtualMachineInstanceMigrationList{}
	if err := t.Client.List(ctx, vmimList, k8sclient.InNamespace(t.Owner.Namespace)); err != nil {
		return false, err
	}
	for _, vmim := range vmimList.Items {
		if vmim.Spec.VMIName != vmName {
			continue
		}
		if vmim.CreationTimestamp.Before(&t.Owner.CreationTimestamp) {
			continue
		}
		switch vmim.Status.Phase {
		case virtv1.MigrationSucceeded, virtv1.MigrationFailed:
			continue
		default:
			return true, nil
		}
	}
	return false, nil
}

func (t *Task) isLiveMigrationCompleted(ctx context.Context, vmName string) (bool, error) {
	// In order to determine if the live migration is complete, we need to check the VMIM status.
	vmimList := &virtv1.VirtualMachineInstanceMigrationList{}
	if err := t.Client.List(ctx, vmimList, k8sclient.InNamespace(t.Owner.Namespace)); err != nil {
		return false, err
	}
	var activeVMIM *virtv1.VirtualMachineInstanceMigration
	for _, vmim := range vmimList.Items {
		if vmim.Spec.VMIName == vmName && vmim.Status.Phase != virtv1.MigrationFailed && !vmim.CreationTimestamp.Before(&t.Owner.CreationTimestamp) {
			t.Log.V(5).Info("Found active VMIM", "vmim", vmim.Name)
			activeVMIM = &vmim
			break
		}
	}
	if activeVMIM == nil {
		return false, nil
	}
	t.Log.V(5).Info("is active VMIM completed", "completed", activeVMIM.Status.MigrationState != nil && activeVMIM.Status.MigrationState.Completed && !activeVMIM.Status.MigrationState.Failed)
	return activeVMIM.Status.MigrationState != nil && activeVMIM.Status.MigrationState.Completed && !activeVMIM.Status.MigrationState.Failed, nil
}

func (t *Task) cleanupMigrationResources(ctx context.Context, completedMigrationsVMNames []string) (allCleaned bool, err error) {
	if allCleaned, err := t.cleanupCompletedPods(ctx, completedMigrationsVMNames); err != nil {
		return false, err
	} else if !allCleaned {
		return false, nil
	}
	if err := t.cleanupCompletedVMIMs(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (t *Task) cleanupCompletedVMIMs(ctx context.Context) error {
	vmimList := &virtv1.VirtualMachineInstanceMigrationList{}
	if err := t.Client.List(ctx, vmimList, k8sclient.InNamespace(t.Owner.Namespace)); err != nil {
		return err
	}

	completedMigrations := make(map[string]struct{})
	for _, migration := range t.Owner.Status.CompletedMigrations {
		completedMigrations[migration] = struct{}{}
	}
	for _, vmim := range vmimList.Items {
		_, ok := completedMigrations[vmim.Spec.VMIName]
		if vmim.Status.Phase == virtv1.MigrationSucceeded && ok {
			t.Log.V(5).Info("Cleaning up migration resource", "vmim", vmim.Name)
			if err := t.Client.Delete(ctx, &vmim); err != nil {
				if !k8serrors.IsNotFound(err) {
					return err
				}
			}
		} else if vmim.Status.Phase == virtv1.MigrationFailed {
			t.Log.V(2).Info("WARNING: Not cleaning up failed VMIM", "vmim", vmim.Name)
		}
	}
	return nil
}

// gatherSourcePVCsForCompletedMigrations returns source PVCs for completed VMs (offline from migration
// status, live from plan status) and how many live VMs are still missing from the plan.
func (t *Task) gatherSourcePVCsForCompletedMigrations(completedVMNames []string) ([]migrations.VirtualMachineStorageMigrationPlanSourcePVC, int) {
	completedSet := make(map[string]struct{})
	for _, name := range completedVMNames {
		completedSet[name] = struct{}{}
	}

	offlineHandled := make(map[string]struct{})
	var sourcePVCs []migrations.VirtualMachineStorageMigrationPlanSourcePVC

	// Offline migrations: use source PVCs recorded in the migration status — always reliable.
	for _, info := range t.Owner.Status.OfflineMigrations {
		if _, ok := completedSet[info.VMName]; !ok {
			continue
		}
		if len(info.SourcePVCs) == 0 {
			continue
		}
		offlineHandled[info.VMName] = struct{}{}
		for _, pvc := range info.SourcePVCs {
			sourcePVCs = append(sourcePVCs, migrations.VirtualMachineStorageMigrationPlanSourcePVC{
				Name:      pvc.Name,
				Namespace: pvc.Namespace,
			})
		}
	}

	// Live migrations: use the plan's CompletedMigrations list (may need to wait for plan controller).
	planCompletedVMs := make(map[string]bool)
	if t.Plan != nil {
		for _, vm := range t.Plan.Status.CompletedMigrations {
			if _, ok := completedSet[vm.Name]; ok {
				if _, ok := offlineHandled[vm.Name]; !ok {
					if len(vm.SourcePVCs) == 0 {
						continue
					}
					planCompletedVMs[vm.Name] = true
					sourcePVCs = append(sourcePVCs, vm.SourcePVCs...)
				}
			}
		}
	}

	// Count live-migration VMs whose source PVCs are not yet in the plan.
	missingCount := 0
	for vmName := range completedSet {
		if _, ok := offlineHandled[vmName]; ok {
			continue
		}
		if !planCompletedVMs[vmName] {
			missingCount++
		}
	}

	return sourcePVCs, missingCount
}

// deleteSourceDataVolumesAndPVCs deletes the source DataVolume (if it exists) or source PVC for each completed migration.
func (t *Task) deleteSourceDataVolumesAndPVCs(ctx context.Context, completedMigrationsVMNames []string) error {
	if t.Plan == nil {
		return nil
	}
	sourcePVCs, missingCount := t.gatherSourcePVCsForCompletedMigrations(completedMigrationsVMNames)
	t.Log.V(3).Info("Deleting source DataVolume and PVCs", "sourcePVCs", sourcePVCs)

	// If live-migration VMs have not yet appeared in plan.Status.CompletedMigrations,
	// requeue and wait for the plan controller to catch up.
	if missingCount > 0 {
		t.Log.Info("WARNING: retentionPolicy is deleteSource but source PVCs not yet available for completed migrations",
			"completedMigrations", completedMigrationsVMNames,
			"planCompletedCount", len(t.Plan.Status.CompletedMigrations),
			"missingVMCount", missingCount)
		t.Requeue = PollReQ
		return fmt.Errorf("source PVCs not yet available in plan status for %d completed migrations", missingCount)
	}

	// Deduplicate by namespace/name
	seen := make(map[string]struct{})
	for _, sourcePVC := range sourcePVCs {
		key := sourcePVC.Namespace + "/" + sourcePVC.Name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		// Try to delete DataVolume first (CDI creates PVC with same name as DataVolume).
		dv := &cdiv1.DataVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sourcePVC.Name,
				Namespace: sourcePVC.Namespace,
			},
		}
		t.Log.V(3).Info("deleting DataVolume", "name", dv.Name, "namespace", dv.Namespace)
		if err := t.Client.Delete(ctx, dv); err != nil {
			if !k8serrors.IsNotFound(err) {
				return err
			}
			t.Log.V(3).Info("DataVolume not found", "name", dv.Name, "namespace", dv.Namespace)
			// DataVolume not found, fall through to delete PVC
		} else {
			t.Log.V(3).Info("Deleted source DataVolume", "name", sourcePVC.Name, "namespace", sourcePVC.Namespace)
			continue
		}

		t.Log.V(3).Info("deleting PVC", "name", sourcePVC.Name, "namespace", sourcePVC.Namespace)
		// No DataVolume or it was already deleted; delete the source PVC.
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sourcePVC.Name,
				Namespace: sourcePVC.Namespace,
			},
		}
		if err := t.Client.Delete(ctx, pvc); err != nil {
			if !k8serrors.IsNotFound(err) {
				return err
			}
		} else {
			t.Log.V(3).Info("Deleted source PVC", "name", sourcePVC.Name, "namespace", sourcePVC.Namespace)
		}
	}
	return nil
}

func (t *Task) cleanupCompletedPods(ctx context.Context, completedMigrationsVMNames []string) (allCleaned bool, err error) {
	podList := &corev1.PodList{}
	labelSelector := map[string]string{virtLauncherPodLabelSelectorKey: virtLauncherPodLabelSelectorValue}
	if err := t.Client.List(ctx, podList, k8sclient.InNamespace(t.Owner.Namespace), k8sclient.MatchingLabels(labelSelector)); err != nil {
		return false, err
	}
	for _, pod := range podList.Items {
		switch pod.Status.Phase {
		case corev1.PodSucceeded:
			t.Log.V(5).Info("Cleaning up migration resource", "pod", pod.Name)
			if err := t.Client.Delete(ctx, &pod); err != nil {
				if !k8serrors.IsNotFound(err) {
					return false, err
				}
			}
		case corev1.PodFailed:
			t.Log.V(2).Info("WARNING: Not cleaning up failed pod", "pod", pod.Name)
		}
	}
	for _, completedMigrationVMName := range completedMigrationsVMNames {
		vmi := &virtv1.VirtualMachineInstance{}
		if err := t.Client.Get(ctx, k8sclient.ObjectKey{Namespace: t.Owner.Namespace, Name: completedMigrationVMName}, vmi); err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return false, err
		}
		if len(vmi.Status.ActivePods) > 1 {
			// Not all pods are cleaned up, so we need to requeue.
			return false, nil
		}
	}
	return true, nil
}

// Initialize.
func (t *Task) init() {
	t.Log.V(5).Info("Running task init")
	if t.Owner.Status.Phase == "" {
		t.Owner.Status.Phase = migrations.Started
	}
}
