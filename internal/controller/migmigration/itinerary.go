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
		{Name: QuiesceSourceVM, Step: StepDirectVolume},
		{Name: EnsureSrcQuiesced, Step: StepDirectVolume},
		{Name: AnnotateResources, Step: StepDirectVolume},
		{Name: CreateDirectVolumeMigration, Step: StepDirectVolume},
		{Name: WaitForDirectVolumeMigrationToComplete, Step: StepDirectVolume},
		{Name: SwapPVCReferences, Step: StepCleanup},
		{Name: Verification, Step: StepCleanup},
		{Name: Completed, Step: StepCleanup},
	},
}

var CancelItinerary = Itinerary{
	Name: CancelItineraryName,
	Phases: []Phase{
		{Name: Canceling, Step: StepCleanupHelpers},
		{Name: DeleteDirectVolumeMigrationResources, Step: StepCleanupHelpers},
		{Name: EnsureAnnotationsDeleted, Step: StepCleanupHelpers},
		{Name: UnQuiesceSrcVM, Step: StepCleanupUnquiesce},
		{Name: Canceled, Step: StepCleanup},
		{Name: Completed, Step: StepCleanup},
	},
}

var FailedItinerary = Itinerary{
	Name: FailedItineraryName,
	Phases: []Phase{
		{Name: MigrationFailed, Step: StepCleanupHelpers},
		{Name: EnsureAnnotationsDeleted, Step: StepCleanupHelpers},
		{Name: Completed, Step: StepCleanup},
	},
}
