package migmigration

// Phases
const (
	Created                        Phase = ""
	Started                        Phase = "Started"
	BeginLiveMigration             Phase = "BeginLiveMigration"
	WaitForLiveMigrationToComplete Phase = "WaitForLiveMigrationToComplete"
	LiveMigrationFailed            Phase = "LiveMigrationFailed"
	Canceling                      Phase = "Canceling"
	Canceled                       Phase = "Canceled"
	Completed                      Phase = "Completed"
)

// Phase defines phase in the migration
type Phase string
