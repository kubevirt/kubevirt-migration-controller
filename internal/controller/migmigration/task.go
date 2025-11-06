package migmigration

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"

	migrationsv1alpha1 "kubevirt.io/kubevirt-migration-controller/api/v1alpha1"
)

// Requeue
const (
	FastReQ = time.Millisecond * 100
	PollReQ = time.Second * 3
	NoReQ   = time.Duration(0)
)

// Get a progress report.
// Returns: phase, n, total.
func (r Itinerary) progressReport(phaseName string) (string, int, int) {
	n := 0
	total := len(r.Phases)
	for i, phase := range r.Phases {
		if string(phase) == phaseName {
			n = i + 1
			break
		}
	}

	return phaseName, n, total
}

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
	Scheme    *runtime.Scheme
	Log       logr.Logger
	Client    k8sclient.Client
	Owner     *migrationsv1alpha1.MigMigration
	Plan      *migrationsv1alpha1.MigPlan
	Requeue   time.Duration
	Itinerary *Itinerary
	Errors    []string
}

// Run the task.
// Each call will:
//  1. Run the current phase.
//  2. Update the phase to the next phase.
//  3. Set the Requeue (as appropriate).
//  4. Return.
func (t *Task) Run(ctx context.Context) error {
	// Set stage, phase, phase description, migplan name
	t.Log = t.Log.WithValues("phase", t.Owner.Status.Phase)
	t.Requeue = FastReQ

	t.init()

	// Run the current phase.
	switch Phase(t.Owner.Status.Phase) {
	case Started:
		// Set finalizer on migration
		t.Owner.AddFinalizer(migrationsv1alpha1.MigMigrationFinalizer)
		t.Owner.Status.Phase = string(BeginLiveMigration)
	case BeginLiveMigration:
		// if t.hasDirectVolumes() {
		// 	err := t.createDirectVolumeMigration(nil)
		// 	if err != nil {
		// 		return err
		// 	}
		// }
		// if err := t.next(); err != nil {
		// 	return err
		// }
	// case WaitForLiveMigrationToComplete:
	// if err := t.next(); err != nil {
	// 	return err
	// }
	// case WaitForDirectVolumeMigrationToComplete:
	// dvm, err := t.getDirectVolumeMigration()
	// if err != nil {
	// 	return err
	// }
	// // if no dvm, continue to next task
	// if dvm == nil {
	// 	if err = t.next(); err != nil {
	// 		return err
	// 	}
	// 	break
	// }
	// if err := t.waitForDVMToComplete(nil); err != nil {
	// 	return err
	// }
	// case SwapPVCReferences:
	// 	t.Log.Info("Swapping PVC references")
	// 	reasons, err := t.swapPVCReferences(ctx)
	// 	if err != nil {
	// 		return err
	// 	}
	// 	if len(reasons) > 0 {
	// 		t.Log.Info("PVC references NOTTTT swapped successfully")
	// 		t.fail(MigrationFailed, reasons)
	// 	} else {
	// 		t.Log.Info("PVC references swapped successfully")
	// 		if err = t.next(); err != nil {
	// 			return err
	// 		}
	// 	}
	case Canceled:
		t.Owner.Status.DeleteCondition(string(Canceling))
		t.Owner.Status.SetCondition(migrationsv1alpha1.Condition{
			Type:     string(Canceled),
			Status:   True,
			Reason:   Cancel,
			Category: Advisory,
			Message:  "The migration has been canceled.",
			Durable:  true,
		})
		// if err = t.next(); err != nil {
		// 	return err
		// }
	case Completed:
	default:
		t.Requeue = NoReQ
		// if err = t.next(); err != nil {
		// 	return err
		// }
	}

	if t.Owner.Status.Phase == string(Completed) {
		t.Requeue = NoReQ
		t.Log.Info("[COMPLETED]")
	}

	return nil
}

// Initialize.
func (t *Task) init() {
	t.Log.V(4).Info("Running task init")
	t.Requeue = FastReQ
	if t.canceled() {
		t.Itinerary = &CancelItinerary
	} else {
		t.Itinerary = &ExecuteItinerary
	}

	t.Owner.Status.Itinerary = t.Itinerary.Name
	t.Owner.Status.Phase = string(t.Itinerary.Phases[0])
}

// func (t *Task) updatePipeline() {
// 	t.Log.V(4).Info("Updating pipeline view of progress")
// 	currentStep := t.Owner.Status.FindStep(t.Step)
// 	for _, step := range t.Owner.Status.Pipeline {
// 		if currentStep != step && step.MarkedStarted() {
// 			step.MarkCompleted()
// 		}
// 	}
// 	// mark steps skipped
// 	for _, step := range t.Owner.Status.Pipeline {
// 		if step == currentStep {
// 			break
// 		} else if !step.MarkedStarted() {
// 			step.Skipped = true
// 		}
// 	}
// 	if currentStep != nil {
// 		currentStep.MarkStarted()
// 		currentStep.Phase = t.Phase
// 		if currentStep.Name == StepDirectVolume {
// 			return
// 		}
// 		if desc, found := PhaseDescriptions[t.Phase]; found {
// 			currentStep.Message = desc
// 		} else {
// 			currentStep.Message = ""
// 		}
// 		if t.Phase == Completed {
// 			currentStep.MarkCompleted()
// 		}
// 	}
// 	t.Owner.Status.ReflectPipeline()
// }

// func (t *Task) setProgress(progress []string) {
// 	currentStep := t.Owner.Status.FindStep(t.Step)
// 	if currentStep != nil {
// 		currentStep.Progress = progress
// 	}
// }

// Advance the task to the next phase.
// func (t *Task) next() error {
// 	// Write time taken to complete phase
// 	cond := t.Owner.Status.FindCondition(migrationsv1alpha1.Running)
// 	if cond != nil {
// 		elapsed := time.Since(cond.LastTransitionTime.Time)
// 		t.Log.Info("Phase completed", "phaseElapsed", elapsed)
// 	}

// 	current := -1
// 	for i, phase := range t.Itinerary.Phases {
// 		if phase != t.Phase {
// 			continue
// 		}
// 		current = i
// 		break
// 	}
// 	if current == -1 {
// 		t.Phase = Completed
// 		return nil
// 	}
// 	// for n := current + 1; n < len(t.Itinerary.Phases); n++ {
// 	// 	next := t.Itinerary.Phases[n]
// 	// 	t.Phase = next.Name
// 	// 	t.Step = next.Step
// 	// 	return nil
// 	// }
// 	t.Phase = Completed
// 	return nil
// }

// Phase fail.
// func (t *Task) fail(nextPhase Phase, reasons []string) {
// 	t.addErrors(reasons)
// 	t.Owner.AddErrors(t.Errors)
// 	t.Log.Info("Marking migration as FAILED. See Status.Errors",
// 		"migrationErrors", t.Owner.Status.Errors)
// 	t.Owner.Status.SetCondition(migrationsv1alpha1.Condition{
// 		Type:     migrationsv1alpha1.Failed,
// 		Status:   True,
// 		Reason:   string(t.Phase),
// 		Category: Advisory,
// 		Message:  "The migration has failed.  See: Errors.",
// 		Durable:  true,
// 	})
// 	// t.failCurrentStep()
// 	t.Phase = nextPhase
// }

// Marks current step failed
// func (t *Task) failCurrentStep() {
// 	currentStep := t.Owner.Status.FindStep(t.Step)
// 	if currentStep != nil {
// 		currentStep.Failed = true
// 	}
// }

// Add errors.
func (t *Task) addErrors(errors []string) {
	t.Errors = append(t.Errors, errors...)
}

// Migration UID.
func (t *Task) UID() string {
	return string(t.Owner.UID)
}

// Get whether the migration has failed
func (t *Task) failed() bool {
	return t.Owner.HasErrors() || t.Owner.Status.HasCondition(migrationsv1alpha1.Failed)
}

// Get whether the migration is cancelled.
func (t *Task) canceled() bool {
	return t.Owner.Status.HasAnyCondition(string(Canceled), string(Canceling))
}

// Get whether the migration is rollback.
// func (t *Task) rollback() bool {
// 	return t.Owner.Spec.Rollback
// }

// Get whether the migration is stage.
// func (t *Task) stage() bool {
// 	return t.Owner.Spec.Stage
// }

// Get whether the migration is state transfer
// func (t *Task) migrateState() bool {
// 	return t.Owner.Spec.MigrateState
// }

// isStorageConversionMigration tells whether the migratoin is for storage conversion
// func (t *Task) isStorageConversionMigration() (bool, error) {
// 	if t.migrateState() || t.rollback() {
// 		// for srcNs, destNs := range t.PlanResources.MigPlan.GetNamespaceMapping() {
// 		// 	if srcNs != destNs {
// 		// 		return false, nil
// 		// 	}
// 		// }
// 		return true, nil
// 	}
// 	return false, nil
// }

// Get whether to retain annotations
// func (t *Task) keepAnnotations() bool {
// 	return t.Owner.Spec.KeepAnnotations
// }

// Get a client for the source cluster.
// func (t *Task) getSourceRestConfig() (*rest.Config, error) {
// 	// return t.PlanResources.SrcMigCluster.BuildRestConfig(t.Client)
// 	return nil, nil
// }

// Get a client for the source cluster.
// func (t *Task) getDestinationRestConfig() (*rest.Config, error) {
// 	// return t.PlanResources.DestMigCluster.BuildRestConfig(t.Client)
// 	return nil, nil
// }

// Get the persistent volumes included in the plan which are included in the
// stage backup/restore process
// This function will only return PVs that are being copied via restic or
// snapshot and any PVs selected for move.
// func (t *Task) getStagePVs() migrationsv1alpha1.PersistentVolumes {
// 	directVolumesEnabled := true
// 	volumes := []migrationsv1alpha1.PV{}
// 	for _, pv := range t.PlanResources.MigPlan.Spec.PersistentVolumes.List {
// 		// If the pv is skipped or if its a filesystem copy with DVM enabled then
// 		// don't include it in a stage PV
// 		if pv.Selection.Action == migrationsv1alpha1.PvSkipAction ||
// 			(directVolumesEnabled && pv.Selection.Action == migrationsv1alpha1.PvCopyAction) {
// 			continue
// 		}
// 		volumes = append(volumes, pv)
// 	}
// 	pvList := t.PlanResources.MigPlan.Spec.PersistentVolumes.DeepCopy()
// 	pvList.List = volumes
// 	return *pvList
// }

// Get the persistentVolumeClaims / action mapping included in the plan which are not skipped.
// func (t *Task) getPVCs() map[k8sclient.ObjectKey]migrationsv1alpha1.PV {
// 	claims := map[k8sclient.ObjectKey]migrationsv1alpha1.PV{}
// 	for _, pv := range t.getStagePVs().List {
// 		claimKey := k8sclient.ObjectKey{
// 			Name:      pv.PVC.GetSourceName(),
// 			Namespace: pv.PVC.Namespace,
// 		}
// 		claims[claimKey] = pv
// 	}
// 	return claims
// }

// Get whether the associated plan lists not skipped PVs.
// First return value is PVs overall, and second is limited to Move or snapshot copy PVs
// func (t *Task) hasPVs() bool {
// 	for _, pv := range t.PlanResources.MigPlan.Spec.PersistentVolumes.List {
// 		if pv.Selection.Action != migrationsv1alpha1.PvSkipAction {
// 			return true
// 		}
// 	}
// 	return false
// }

// Get whether the associated plan has PVs to be directly migrated
// func (t *Task) hasDirectVolumes() bool {
// 	return t.getDirectVolumeClaimList() != nil
// }

// Get whether the verification is desired
// func (t *Task) hasVerify() bool {
// 	return t.Owner.Spec.Verify
// }

// Returns true if the IndirectVolumeMigration override on the plan is not set (plan is configured to do direct migration)
// There must exist a set of direct volumes for this to return true
// func (t *Task) directVolumeMigration() bool {
// 	return t.hasDirectVolumes()
// }

// func (t *Task) liveVolumeMigration() bool {
// 	// For rollbacks on stopped VMs, we can just swap PVC references
// 	return true
// }

// GetStepForPhase returns which high level step current phase belongs to
// func (r *Itinerary) GetStepForPhase(phaseName string) string {
// 	for _, phase := range r.Phases {
// 		if phaseName == string(phase) {
// 		}
// 	}
// 	return ""
// }

// Emits an INFO level warning message (no stack trace) letting the
// user know an error was encountered with a description of the phase
// where available. Stack trace will be printed shortly after this.
// This is meant to help contextualize the stack trace for the user.
// func (t *Task) logErrorForPhase(phaseName string, err error) {
// 	t.Log.Info("Exited Phase with error.",
// 		"phase", phaseName,
// 		"phaseDescription", t.getPhaseDescription(phaseName),
// 		"error", err.Error())
// }

// Get the extended phase description for a phase.
// func (t *Task) getPhaseDescription(phaseName string) string {
// 	// Log the extended description of current phase
// 	if phaseDescription, found := PhaseDescriptions[t.Phase]; found {
// 		return phaseDescription
// 	}
// 	t.Log.Info("Missing phase description for phase: " + phaseName)
// 	// If no description available, just return phase name.
// 	return phaseName
// }

// func (t *Task) waitForDVMToComplete(_ *migrationsv1alpha1.DirectVolumeMigration) error {
// 	// Check if DVM is complete and report progress
// 	if time.Since(t.Owner.CreationTimestamp.Time) > 2*time.Minute {
// 		// TODO: dummy wait to simulate dvm processing until the controller is ready
// 		if err := t.next(); err != nil {
// 			return err
// 		}
// 	} else {
// 		t.Requeue = PollReQ
// 	}
// 	return nil
// }
