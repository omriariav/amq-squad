package cli

import (
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/tmuxpane"
)

// #689 regression: winding down a member whose process already exited must
// close its lingering dead pane under --close-panes instead of fail-closing it
// into "preserved" (operator review) and aborting the removal.

// The v2.29.3 double-report: after a manual kill-pane, a follow-up cleanup
// re-reported "preserved" for the nonexistent pane id because the durable
// record failed identity validation before live tmux was ever consulted. A
// gone pane must now win over unconfirmed identity even on the live path.
func TestPreparePaneCleanupUnconfirmedIdentityStillReportsGonePane(t *testing.T) {
	_, _, record, _, project := completeDownPaneFixture(t)
	record.Role = "someone-else" // force an identity mismatch
	req := PaneCleanupRequest{
		Requested: true,
		Record:    record,
		Scope: PaneCleanupScope{
			ProjectDir: project, TeamHome: project, Profile: team.DefaultProfile,
			Root: record.Root, BaseRoot: record.BaseRoot, Session: "issue-465",
			Role: "cto", Handle: "cto", Binary: "codex", CWD: project,
		},
	}
	deps := PaneCleanupDependencies{
		Inspect: func(id string) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "no such pane " + id}
		},
	}
	prepared := PreparePaneCleanup(req, deps)
	if prepared.Result.Outcome != PaneCleanupAlreadyGone {
		t.Fatalf("gone pane must report already_gone despite unconfirmed identity, got %s (%s) mismatches=%v",
			prepared.Result.Outcome, prepared.Result.Detail, prepared.Result.Mismatches)
	}
}

func TestClosePreparedDeadPanePreservesWhenPaneComesAlive(t *testing.T) {
	_, _, record, pane, project := completeDownPaneFixture(t)
	pane.Dead = true
	pane.DeadSignal = "15"
	inspections := 0
	alive := pane
	alive.Dead = false
	deps := PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			inspections++
			if inspections <= 2 {
				// preparation inspections see the dead pane
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
			}
			// close-time revalidation sees it alive again
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: alive}
		},
		Close: func(string) error { t.Fatal("revived pane must not be closed"); return nil },
	}
	req := PaneCleanupRequest{
		Requested:   true,
		Record:      record,
		Attestation: PaneCleanupAgentAttestation{AgentGone: true},
		Scope: PaneCleanupScope{
			ProjectDir: project, TeamHome: project, Profile: team.DefaultProfile,
			Root: record.Root, BaseRoot: record.BaseRoot, Session: "issue-465",
			Role: "cto", Handle: "cto", Binary: "codex", CWD: project,
		},
	}
	prepared := PreparePaneCleanup(req, deps)
	if !prepared.Ready || !prepared.DeadPane {
		t.Fatalf("dead pane preparation must be Ready+DeadPane, got %+v", prepared.Result)
	}
	result := ClosePreparedPane(prepared, deps)
	if result.Outcome != PaneCleanupPreservedIdentityUnconfirmed {
		t.Fatalf("revived pane must be preserved at close time, got %s (%s)", result.Outcome, result.Detail)
	}
}

