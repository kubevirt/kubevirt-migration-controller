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
		Started,
		BeginLiveMigration,
		WaitForLiveMigrationToComplete,
		Completed,
	},
}

var CancelItinerary = Itinerary{
	Name: CancelItineraryName,
	Phases: []Phase{
		Canceling,
		Canceled,
	},
}
