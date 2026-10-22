package cli

// v3_holding.go is the temporary holding file for phase 2a-1 extractions
// (plan 4.1 step 3). Every declaration here was moved verbatim from a file
// deleted in 2a-1 because a surviving non-test caller still needs it. 2a-2
// moves each one to its 3.0 home or deletes it; this file's target at the end
// of 2a is zero lines. Each block names its source file and line range on the
// v3 branch at the time of the cut.

// From internal/cli/goal.go:314-317 (C5 cut). Not listed in the manifest
// extract field; needed by global_noc.go, team_launch.go and the C3/C9/C18
// files that are deleted later in 2a-1.
const (
	goalOrchestratorRole          = "orchestrator"
	defaultGoalOrchestratorHandle = "orchestrator"
)
