package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// swapPaneCloser replaces the paneCloser seam with a recorder and restores it.
func swapPaneCloser(t *testing.T) *[]string {
	t.Helper()
	var closed []string
	prev := paneCloser
	paneCloser = func(id string) error {
		closed = append(closed, id)
		return nil
	}
	t.Cleanup(func() { paneCloser = prev })
	return &closed
}

// TestCloseDownedPanesOnlyClosesConfirmedDown pins the stop-side guardrail: a
// pane is closed only for a member that is CONFIRMED down (stopped / cleaned /
// not-live) and carries a recorded pane id. maybe-live and failed members are
// never touched (amq-squad never closes a pane it is unsure is dead).
func TestCloseDownedPanesOnlyClosesConfirmedDown(t *testing.T) {
	closed := swapPaneCloser(t)
	reports := []downReport{
		{Role: "a", PaneID: "%1", Status: downStatusStopped},
		{Role: "b", PaneID: "%2", Status: downStatusCleaned},
		{Role: "c", PaneID: "%3", Status: downStatusNotLive},
		{Role: "d", PaneID: "%4", Status: downStatusMaybeLive}, // unsure -> keep
		{Role: "e", PaneID: "%5", Status: downStatusFailed},    // failed -> keep
		{Role: "f", PaneID: "", Status: downStatusStopped},     // no pane id -> skip
	}
	closeDownedPanes(reports)
	if want := []string{"%1", "%2", "%3"}; !reflect.DeepEqual(*closed, want) {
		t.Fatalf("closed = %v, want %v", *closed, want)
	}
	if !strings.Contains(reports[0].Detail, "closed tmux pane %1") {
		t.Errorf("a closed pane should be noted in the report detail, got %q", reports[0].Detail)
	}
}

// TestExecuteDownClosePanesOptIn proves the wiring: ClosePanes=false (stop's
// default) closes nothing; ClosePanes=true closes the stopped agent's pane.
func TestExecuteDownClosePanesOptIn(t *testing.T) {
	for _, tc := range []struct {
		name       string
		closePanes bool
		wantClosed int
	}{
		{"default keeps panes", false, 0},
		{"--close-panes closes", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := setupFakeAMQSessionRoots(t)
			dir := seedTeam(t, team.Team{
				Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
			})
			seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
				Binary: "codex", Handle: "cto", AgentPID: 0,
				Tmux: &launch.TmuxInfo{PaneID: "%88"},
			})
			closed := swapPaneCloser(t)
			_, err := runDownExec(t, downExecution{
				Verb: "stop", ProjectDir: dir, RequestedSession: "issue-96",
				ExplicitSession: true, Role: "cto",
				Terminator: &recordingTerminator{}, Probe: downFakeProbe(nil, nil),
				ClosePanes: tc.closePanes,
			})
			if err != nil {
				t.Fatalf("stop: %v", err)
			}
			if len(*closed) != tc.wantClosed {
				t.Fatalf("closed = %v, want %d closed", *closed, tc.wantClosed)
			}
		})
	}
}

// TestCollectSessionPaneIDsSkipsLiveAndMissing pins the rm-side guardrail: only
// recorded panes of NON-live agents are collected (rm --force must never close a
// still-running agent's pane), and agents without a tmux record are skipped.
func TestCollectSessionPaneIDsSkipsLiveAndMissing(t *testing.T) {
	base := t.TempDir()
	seedAgentRecord(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Tmux: &launch.TmuxInfo{PaneID: "%10"}})
	seedAgentRecord(t, base, "s", "qa", launch.Record{Binary: "codex", Handle: "qa", Tmux: &launch.TmuxInfo{PaneID: "%11"}})
	seedAgentRecord(t, base, "s", "dev", launch.Record{Binary: "codex", Handle: "dev"}) // no tmux record

	ids := collectSessionPaneIDs(filepath.Join(base, "s"), map[string]bool{"qa": true})
	if want := []string{"%10"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("collected %v, want %v (qa is live -> excluded; dev has no pane)", ids, want)
	}
}

// TestRmClosesPanesByDefault / TestRmKeepPanes pin rm's default-close behavior
// and the --keep-panes opt-out, end to end through executeRm.
func TestRmClosesPanesByDefault(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{Binary: "codex", Handle: "cto", Tmux: &launch.TmuxInfo{PaneID: "%77"}})
	closed := swapPaneCloser(t)

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir, Session: "issue-96", Mode: rmModeDelete,
		Yes: true, ClosePanes: true, BaseRoot: base,
	})
	if err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if want := []string{"%77"}; !reflect.DeepEqual(*closed, want) {
		t.Fatalf("closed = %v, want %v", *closed, want)
	}
	if !strings.Contains(out, "closed tmux pane %77") {
		t.Errorf("rm should note the closed pane:\n%s", out)
	}
}

func TestRmKeepPanes(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{Binary: "codex", Handle: "cto", Tmux: &launch.TmuxInfo{PaneID: "%77"}})
	closed := swapPaneCloser(t)

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir, Session: "issue-96", Mode: rmModeDelete,
		Yes: true, ClosePanes: false, BaseRoot: base,
	})
	if err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if len(*closed) != 0 {
		t.Fatalf("--keep-panes must close nothing; closed = %v", *closed)
	}
}
