package migmigration

// Phases
const (
	Created                                = ""
	Started                                = "Started"
	CleanStaleAnnotations                  = "CleanStaleAnnotations"
	StartRefresh                           = "StartRefresh"
	WaitForRefresh                         = "WaitForRefresh"
	AnnotateResources                      = "AnnotateResources"
	QuiesceSourceVM                        = "QuiesceSourceVM"
	EnsureSrcQuiesced                      = "EnsureSrcQuiesced"
	UnQuiesceSrcVM                         = "UnQuiesceSrcVM"
	SwapPVCReferences                      = "SwapPVCReferences"
	WaitForRegistriesReady                 = "WaitForRegistriesReady"
	EnsureStageBackup                      = "EnsureStageBackup"
	StageBackupCreated                     = "StageBackupCreated"
	StageBackupFailed                      = "StageBackupFailed"
	EnsureInitialBackupReplicated          = "EnsureInitialBackupReplicated"
	EnsureStageBackupReplicated            = "EnsureStageBackupReplicated"
	EnsureStageRestore                     = "EnsureStageRestore"
	CreateDirectVolumeMigration            = "CreateDirectVolumeMigrationFinal"
	WaitForDirectVolumeMigrationToComplete = "WaitForDirectVolumeMigrationToComplete"
	DirectVolumeMigrationFailed            = "DirectVolumeMigrationFailed"
	Verification                           = "Verification"
	EnsureAnnotationsDeleted               = "EnsureAnnotationsDeleted"
	EnsureMigratedDeleted                  = "EnsureMigratedDeleted"
	DeleteMigrated                         = "DeleteMigrated"
	DeleteDirectVolumeMigrationResources   = "DeleteDirectVolumeMigrationResources"
	MigrationFailed                        = "MigrationFailed"
	Canceling                              = "Canceling"
	Canceled                               = "Canceled"
	Completed                              = "Completed"
)

// Migration steps
const (
	StepPrepare               = "Prepare"
	StepDirectVolume          = "DirectVolume"
	StepCleanup               = "Cleanup"
	StepCleanupHelpers        = "CleanupHelpers"
	StepCleanupMigrated       = "CleanupMigrated"
	StepCleanupUnquiesce      = "CleanupUnquiesce"
	StepRollbackLiveMigration = "RollbackLiveMigration"
)

// Phase defines phase in the migration
type Phase struct {
	// A phase name.
	Name string
	// High level Step this phase belongs to
	Step string
}
