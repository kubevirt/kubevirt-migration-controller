package storagemig

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	virtv1 "kubevirt.io/api/core/v1"
	migrations "kubevirt.io/kubevirt-migration-controller/api/migrationcontroller/v1alpha1"
)

// Requeue
const (
	FastReQ = time.Millisecond * 100
	PollReQ = time.Second * 3
	NoReQ   = time.Duration(0)

	virtLauncherPodLabelSelectorKey   = "kubevirt.io"
	virtLauncherPodLabelSelectorValue = "virt-launcher"
)

// Get a progress report.
// Returns: phase, n, total.
// func (r Itinerary) progressReport(phaseName string) (string, int, int) {
// 	n := 0
// 	total := len(r.Phases)
// 	for i, phase := range r.Phases {
// 		if string(phase) == phaseName {
// 			n = i + 1
// 			break
// 		}
// 	}

// 	return phaseName, n, total
// }

// A task that provides the complete migration workflow.
// Log - A controller's logger.
// Client - A controller's (local) client.
// Owner - A MigMigration resource.
// PlanResources - A PlanRefResources.
// Annotations - Map of annotations to applied to the backup & restore
// Phase - The task phase.
// Requeue - The requeueAfter duration. 0 indicates no requeue.
// Itinerary - The phase itinerary.
// Errors - Migration errors.
// Failed - Task phase has failed.
type Task struct {
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

	t.Log.Info("Running task.Run", "phase", t.Owner.Status.Phase)
	// Run the current phase.
	switch t.Owner.Status.Phase {
	case migrations.Started:
		// Set finalizer on migration
		t.Owner.AddFinalizer(migrations.VirtualMachineStorageMigrationFinalizer, t.Log)
		t.Owner.Status.Phase = migrations.RefreshStorageMigrationPlan
	case migrations.RefreshStorageMigrationPlan:
		if err := t.refreshReadyVirtualMachines(ctx); err != nil {
			return err
		}
		t.Owner.Status.Phase = migrations.WaitForStorageMigrationPlanRefreshCompletion
	case migrations.WaitForStorageMigrationPlanRefreshCompletion:
		if completed, err := t.refreshCompletedVirtualMachines(ctx); err != nil {
			return err
		} else if !completed {
			return nil
		}
		t.Owner.Status.Phase = migrations.BeginLiveMigration
	case migrations.BeginLiveMigration:
		t.Log.Info("Beginning live migration", "readyMigrations", len(t.Plan.Status.ReadyMigrations))
		for _, vm := range t.Plan.Status.ReadyMigrations {
			if can, err := t.canVMStorageMigrate(ctx, vm.Name); err != nil {
				return err
			} else if !can {
				t.Log.V(3).Info("VM cannot storage migrate", "vm", vm.Name)
				continue
			}
			if err := t.liveMigrateVM(ctx, vm); err != nil {
				return err
			}
			t.Log.V(3).Info("VM migration is running", "vm", vm.Name)
			t.Owner.Status.RunningMigrations = append(t.Owner.Status.RunningMigrations, vm.Name)
		}
		t.Owner.Status.Phase = migrations.WaitForLiveMigrationToComplete
	case migrations.WaitForLiveMigrationToComplete:
		runningMigrations := make([]string, 0)
		for _, vm := range t.Owner.Status.RunningMigrations {
			if ok, err := t.isLiveMigrationToComplete(ctx, vm); err != nil {
				return err
			} else if !ok {
				runningMigrations = append(runningMigrations, vm)
				continue
			}
			t.Owner.Status.CompletedMigrations = append(t.Owner.Status.CompletedMigrations, vm)
		}
		t.Owner.Status.RunningMigrations = runningMigrations
		if len(runningMigrations) == 0 {
			t.Owner.Status.Phase = migrations.CleanupMigrationResources
		}
		t.Requeue = PollReQ
	case migrations.CleanupMigrationResources:
		if err := t.cleanupMigrationResources(ctx); err != nil {
			return err
		}
		t.Owner.Status.Phase = migrations.Completed
	case migrations.Canceled:
		t.Owner.Status.DeleteCondition(string(migrations.Canceling))
		t.Owner.Status.SetCondition(migrations.Condition{
			Type:     string(migrations.Canceled),
			Status:   corev1.ConditionTrue,
			Reason:   Cancel,
			Category: migrations.Advisory,
			Message:  "The migration has been canceled.",
		})
	default:
		t.Requeue = NoReQ
	}
	return nil
}

func (t *Task) isLiveMigrationToComplete(ctx context.Context, vmName string) (bool, error) {
	// In order to determine if the live migration is complete, we need to check the VMIM status.
	vmimList := &virtv1.VirtualMachineInstanceMigrationList{}
	if err := t.Client.List(ctx, vmimList, k8sclient.InNamespace(t.Owner.Namespace)); err != nil {
		return false, err
	}
	var activeVMIM *virtv1.VirtualMachineInstanceMigration
	for _, vmim := range vmimList.Items {
		if vmim.Spec.VMIName == vmName && vmim.Status.Phase != virtv1.MigrationFailed {
			activeVMIM = &vmim
			break
		}
	}
	if activeVMIM == nil {
		return false, nil
	}
	return activeVMIM.Status.MigrationState != nil && activeVMIM.Status.MigrationState.Completed && !activeVMIM.Status.MigrationState.Failed, nil
}

func (t *Task) cleanupMigrationResources(ctx context.Context) error {
	podList := &corev1.PodList{}
	labelSelector := map[string]string{virtLauncherPodLabelSelectorKey: virtLauncherPodLabelSelectorValue}
	if err := t.Client.List(ctx, podList, k8sclient.InNamespace(t.Owner.Namespace), k8sclient.MatchingLabels(labelSelector)); err != nil {
		return err
	}
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodSucceeded {
			if err := t.Client.Delete(ctx, &pod); err != nil {
				if !k8serrors.IsNotFound(err) {
					return err
				}
			}
		}
	}
	return nil
}

// Initialize.
func (t *Task) init() {
	t.Log.V(4).Info("Running task init")
	if t.Owner.Status.Phase == "" {
		t.Owner.Status.Phase = migrations.Started
	}
}
