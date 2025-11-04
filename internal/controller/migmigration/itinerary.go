package migmigration

// Itinerary names
const (
	StageItineraryName    = "Stage"
	ExecuteItineraryName  = "Final"
	CancelItineraryName   = "Cancel"
	FailedItineraryName   = "Failed"
	RollbackItineraryName = "Rollback"
)

// Itinerary defines itinerary
type Itinerary struct {
	Name   string
	Phases []Phase
}

var ExecuteItinerary = Itinerary{
	Name: ExecuteItineraryName,
	Phases: []Phase{
		{Name: Created, Step: StepPrepare},
		{Name: Started, Step: StepPrepare},
		{Name: StartRefresh, Step: StepPrepare},
		{Name: WaitForRefresh, Step: StepPrepare},
		{Name: CleanStaleAnnotations, Step: StepPrepare},
		{Name: WaitForRegistriesReady, Step: StepPrepare, all: IndirectImage | EnableImage | HasISs},
		{Name: QuiesceSourceVM, Step: StepDirectVolume, all: Quiesce},
		{Name: EnsureSrcQuiesced, Step: StepDirectVolume, all: Quiesce},
		{Name: AnnotateResources, Step: StepDirectVolume, all: HasStageBackup},
		{Name: CreateDirectVolumeMigration, Step: StepDirectVolume, all: DirectVolume | EnableVolume},
		{Name: WaitForDirectVolumeMigrationToComplete, Step: StepDirectVolume, all: DirectVolume | EnableVolume},
		{Name: SwapPVCReferences, Step: StepCleanup, all: StorageConversion | Quiesce},
		{Name: Verification, Step: StepCleanup, all: HasVerify},
		{Name: Completed, Step: StepCleanup},
	},
}

var CancelItinerary = Itinerary{
	Name: CancelItineraryName,
	Phases: []Phase{
		{Name: Canceling, Step: StepCleanupHelpers},
		{Name: DeleteDirectVolumeMigrationResources, Step: StepCleanupHelpers, all: DirectVolume},
		{Name: EnsureAnnotationsDeleted, Step: StepCleanupHelpers, all: HasStageBackup},
		{Name: UnQuiesceSrcVM, Step: StepCleanupUnquiesce},
		{Name: Canceled, Step: StepCleanup},
		{Name: Completed, Step: StepCleanup},
	},
}

var FailedItinerary = Itinerary{
	Name: FailedItineraryName,
	Phases: []Phase{
		{Name: MigrationFailed, Step: StepCleanupHelpers},
		{Name: EnsureAnnotationsDeleted, Step: StepCleanupHelpers, all: HasStageBackup},
		{Name: Completed, Step: StepCleanup},
	},
}
