package cli

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

// swapPaneInspectorMatching makes the identity-checked close see each pane id as
// a LIVE pane carrying the matching amq title token (amq:<session>:<role>), so a
// safe close validates and proceeds. Ids not in idRole resolve to "gone".
func swapPaneInspectorMatching(t *testing.T, session string, idRole map[string]string) {
	t.Helper()
	prev := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		role, ok := idRole[id]
		if !ok {
			return tmuxpane.TmuxPane{}, false
		}
		return tmuxpane.TmuxPane{PaneID: id, Title: paneTitleToken(session, role)}, true
	}
	t.Cleanup(func() { statusPaneInspector = prev })
}

// TestCloseDownedPanesOnlyClosesConfirmedDown pins the stop-side guardrail: a
// pane is closed only for a member that is CONFIRMED down (stopped / cleaned /
// not-live) and carries a recorded pane id. maybe-live and failed members are
// never touched (amq-squad never closes a pane it is unsure is dead).
func TestCloseDownedPanesOnlyClosesConfirmedDown(t *testing.T) {
	closed := swapPaneCloser(t)
	swapPaneInspectorMatching(t, "s", map[string]string{"%1": "a", "%2": "b", "%3": "c"})
	reports := []downReport{
		{Role: "a", PaneID: "%1", Status: downStatusStopped},
		{Role: "b", PaneID: "%2", Status: downStatusCleaned},
		{Role: "c", PaneID: "%3", Status: downStatusNotLive},
		{Role: "d", PaneID: "%4", Status: downStatusMaybeLive}, // unsure -> keep
		{Role: "e", PaneID: "%5", Status: downStatusFailed},    // failed -> keep
		{Role: "f", PaneID: "", Status: downStatusStopped},     // no pane id -> skip
	}
	closeDownedPanes(reports, "s")
	if want := []string{"%1", "%2", "%3"}; !reflect.DeepEqual(*closed, want) {
		t.Fatalf("closed = %v, want %v", *closed, want)
	}
	if !strings.Contains(reports[0].Detail, "closed tmux pane %1") {
		t.Errorf("a closed pane should be noted in the report detail, got %q", reports[0].Detail)
	}
}

// TestCloseRecordedPaneSafelyGuardsReuse pins the destructive-twin-of-#156
// guard: a recorded pane id is closed only when the live pane still proves it is
// the same agent; a reused id (different agent's title, or a non-amq pane in a
// different cwd) is LEFT OPEN with a reason; a gone pane is a silent no-op.
func TestCloseRecordedPaneSafelyGuardsReuse(t *testing.T) {
	closed := swapPaneCloser(t)
	prev := statusPaneInspector
	t.Cleanup(func() { statusPaneInspector = prev })
	inspect := func(p tmuxpane.TmuxPane, ok bool) {
		statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
			p.PaneID = id
			return p, ok
		}
	}

	// Reused id: the live pane now carries a DIFFERENT agent's amq token -> skip.
	inspect(tmuxpane.TmuxPane{Title: paneTitleToken("other", "dev")}, true)
	if ok, skip := closeRecordedPaneSafely("%9", "issue-96", "cto", ""); ok || skip == "" {
		t.Fatalf("reused (different token) pane must be left open with a reason; closed=%v skip=%q", ok, skip)
	}

	// Untitled pane in a DIFFERENT cwd -> skip (can't confirm identity).
	inspect(tmuxpane.TmuxPane{Title: "", CWD: "/somewhere/else"}, true)
	if ok, skip := closeRecordedPaneSafely("%9", "issue-96", "cto", "/repo"); ok || skip == "" {
		t.Fatalf("untitled pane in a different cwd must be left open; closed=%v skip=%q", ok, skip)
	}

	// Gone pane -> silent no-op.
	inspect(tmuxpane.TmuxPane{}, false)
	if ok, skip := closeRecordedPaneSafely("%9", "issue-96", "cto", ""); ok || skip != "" {
		t.Fatalf("a gone pane must be a silent no-op; closed=%v skip=%q", ok, skip)
	}

	// Matching amq token -> close.
	inspect(tmuxpane.TmuxPane{Title: paneTitleToken("issue-96", "cto")}, true)
	if ok, _ := closeRecordedPaneSafely("%9", "issue-96", "cto", ""); !ok {
		t.Fatal("matching amq title should close")
	}
	if want := []string{"%9"}; !reflect.DeepEqual(*closed, want) {
		t.Fatalf("only the matching pane should have been closed; closed = %v", *closed)
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
			swapPaneInspectorMatching(t, "issue-96", map[string]string{"%88": "cto"})
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
	seedAgentRecord(t, base, "s", "cto", launch.Record{Binary: "codex", Handle: "cto", Role: "cto", Tmux: &launch.TmuxInfo{PaneID: "%10"}})
	seedAgentRecord(t, base, "s", "qa", launch.Record{Binary: "codex", Handle: "qa", Role: "qa", Tmux: &launch.TmuxInfo{PaneID: "%11"}})
	seedAgentRecord(t, base, "s", "dev", launch.Record{Binary: "codex", Handle: "dev", Role: "dev"}) // no tmux record

	panes := collectSessionPaneIDs(filepath.Join(base, "s"), map[string]bool{"qa": true})
	if want := []recordedPane{{PaneID: "%10", Role: "cto"}}; !reflect.DeepEqual(panes, want) {
		t.Fatalf("collected %v, want %v (qa is live -> excluded; dev has no pane)", panes, want)
	}
}

// TestRmClosesPanesByDefault / TestRmKeepPanes pin rm's default-close behavior
// and the --keep-panes opt-out, end to end through executeRm.
func TestRmClosesPanesByDefault(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{Binary: "codex", Handle: "cto", Role: "cto", Tmux: &launch.TmuxInfo{PaneID: "%77"}})
	closed := swapPaneCloser(t)
	swapPaneInspectorMatching(t, "issue-96", map[string]string{"%77": "cto"})

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
