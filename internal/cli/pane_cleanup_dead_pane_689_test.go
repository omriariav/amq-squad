package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// #689 regression: winding down a member whose process already exited must
// close its lingering dead pane under --close-panes instead of fail-closing it
// into "preserved" (operator review) and aborting the removal.

// deadPaneFixture returns the standard down fixture with the recorded pane
// reported dead by tmux (signal recorded), as after the agent process exits
// under remain-on-exit.
func deadPaneFixture(t *testing.T) (team.Team, team.Member, tmuxpane.TmuxPane, string) {
	t.Helper()
	configured, member, _, pane, project := completeDownPaneFixture(t)
	pane.Dead = true
	pane.DeadStatus = "0"
	pane.DeadSignal = "15"
	return configured, member, pane, project
}

func TestDownClosePanesClosesDeadPaneOfDeadAgent(t *testing.T) {
	configured, member, pane, project := deadPaneFixture(t)
	closed := []string{}
	deps := PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
		},
		Close: func(id string) error { closed = append(closed, id); return nil },
	}
	// Recorded pid 4242 is affirmatively not alive.
	probe := downFakeProbe(map[int]bool{}, map[int]bool{})
	report := terminateMember(configured, project, team.DefaultProfile, member, "issue-465", eventTerminator{events: &[]string{}}, probe, nil, true, deps, fakeMissingWakeCheck)
	if report.Pane.Outcome != PaneCleanupClosed {
		t.Fatalf("dead agent + dead pane must close under --close-panes, got %s (%s) mismatches=%v", report.Pane.Outcome, report.Pane.Detail, report.Pane.Mismatches)
	}
	if !strings.Contains(report.Pane.Detail, "pane_dead=1") || !strings.Contains(report.Pane.Detail, "signal=15") {
		t.Fatalf("closed detail must carry the dead-pane evidence, got %q", report.Pane.Detail)
	}
	if len(closed) != 1 || closed[0] != "%9" {
		t.Fatalf("expected exactly the recorded pane %%9 closed, got %v", closed)
	}
}

func TestDownClosePanesPreservesLivePaneOfDeadAgent(t *testing.T) {
	configured, member, pane, project := deadPaneFixture(t)
	pane.Dead = false // pane process still running; agent record dead
	deps := PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
		},
		Close: func(string) error {
			t.Fatal("live pane must never be closed under the gone-agent contract")
			return nil
		},
	}
	probe := downFakeProbe(map[int]bool{}, map[int]bool{})
	report := terminateMember(configured, project, team.DefaultProfile, member, "issue-465", eventTerminator{events: &[]string{}}, probe, nil, true, deps, fakeMissingWakeCheck)
	if report.Pane.Outcome != PaneCleanupPreservedIdentityUnconfirmed {
		t.Fatalf("live pane with gone agent must be preserved, got %s (%s)", report.Pane.Outcome, report.Pane.Detail)
	}
	found := false
	for _, m := range report.Pane.Mismatches {
		if m.Field == "pane_dead" {
			found = true
		}
	}
	if !found {
		t.Fatalf("preservation must carry the pane_dead mismatch, got %v", report.Pane.Mismatches)
	}
}

func TestDownClosePanesReportsAlreadyGoneForVanishedDeadAgentPane(t *testing.T) {
	configured, member, _, project := deadPaneFixture(t)
	deps := PaneCleanupDependencies{
		Inspect: func(id string) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "tmux returned fallback pane %0 for requested pane " + id}
		},
		Close: func(string) error { t.Fatal("gone pane must not be closed"); return nil },
	}
	probe := downFakeProbe(map[int]bool{}, map[int]bool{})
	report := terminateMember(configured, project, team.DefaultProfile, member, "issue-465", eventTerminator{events: &[]string{}}, probe, nil, true, deps, fakeMissingWakeCheck)
	if report.Pane.Outcome != PaneCleanupAlreadyGone {
		t.Fatalf("vanished recorded pane must report already_gone, got %s (%s)", report.Pane.Outcome, report.Pane.Detail)
	}
}

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

// #689 (b): a partial stop (pane cleanups incomplete) no longer aborts the
// roster removal; the command removes the entry and reports both outcomes.
func TestTeamMemberRmRemovesDespitePartialPaneCleanup(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	})
	prev := teamMemberStop
	teamMemberStop = func([]string) error {
		return &PartialError{Message: "down: 1 pane cleanup(s) were not completed"}
	}
	t.Cleanup(func() { teamMemberStop = prev })

	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"rm", "qa", "--stop", "--close-panes"})
	})
	if err == nil {
		t.Fatal("partial stop must surface as a partial error, not silent success")
	}
	if _, ok := err.(*PartialError); !ok {
		t.Fatalf("want *PartialError, got %T: %v", err, err)
	}
	for _, want := range []string{"removed qa from the team roster", "pane cleanup"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("partial error must state the roster outcome, missing %q in %v", want, err)
		}
	}
	if !strings.Contains(out, "removed qa from the team.") {
		t.Fatalf("stdout must state the roster outcome, got:\n%s", out)
	}
	if got := len(teamMembers(t, dir)); got != 1 {
		t.Fatalf("roster must be mutated despite partial stop, members=%d", got)
	}
}

// Hard stop refusals (not partial) still abort before any roster mutation.
func TestTeamMemberRmHardStopRefusalStillPreservesRoster(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	})
	prev := teamMemberStop
	teamMemberStop = func([]string) error {
		return errString("launch record failed exact named-profile identity validation")
	}
	t.Cleanup(func() { teamMemberStop = prev })

	_, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"rm", "qa", "--stop", "--close-panes"})
	})
	if err == nil || !strings.Contains(err.Error(), "stop before remove") {
		t.Fatalf("hard refusal must abort with 'stop before remove', got %v", err)
	}
	if got := len(teamMembers(t, dir)); got != 2 {
		t.Fatalf("roster must be preserved on hard refusal, members=%d", got)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
