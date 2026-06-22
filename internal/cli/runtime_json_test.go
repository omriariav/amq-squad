package cli

import (
	"bytes"
	"errors"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// swapStatusPaneLister installs a fake pane lister for the duration of a test.
// It also stubs the direct pane inspector to not-found so the pane_alive
// recorded-id fallback (which fires for a pane missing from the scan) never
// shells real tmux; a test that wants the inspector to find a pane overrides it
// afterward.
func swapStatusPaneLister(t *testing.T, panes []tmuxpane.TmuxPane, err error) {
	t.Helper()
	prev := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return panes, err }
	prevInspect := statusPaneInspector
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { return tmuxpane.TmuxPane{}, false }
	t.Cleanup(func() { statusPaneLister = prev; statusPaneInspector = prevInspect })
}

func TestTmuxRuntimeFromInfo(t *testing.T) {
	if rt := tmuxRuntimeFromInfo(nil); rt != nil {
		t.Fatalf("nil info should map to nil runtime, got %+v", rt)
	}
	rt := tmuxRuntimeFromInfo(&launch.TmuxInfo{Session: "main", WindowID: "@1", PaneID: "%5", Target: "current-window"})
	if rt == nil || rt.PaneID != "%5" || rt.Target != "current-window" || rt.PaneAlive {
		t.Fatalf("unexpected runtime block: %+v", rt)
	}
}

func TestTmuxRuntimeFromEmptyInfoIsNil(t *testing.T) {
	// A record with an empty tmux object carries no identity -> omit the block.
	if rt := tmuxRuntimeFromInfo(&launch.TmuxInfo{}); rt != nil {
		t.Fatalf("empty tmux info should map to nil, got %+v", rt)
	}
}

func TestMemoizePaneListerCallsUnderlyingOnce(t *testing.T) {
	calls := 0
	memo := memoizePaneLister(func() ([]tmuxpane.TmuxPane, error) {
		calls++
		return []tmuxpane.TmuxPane{{PaneID: "%1"}}, nil
	})
	for i := 0; i < 3; i++ {
		panes, _ := memo()
		if len(panes) != 1 || panes[0].PaneID != "%1" {
			t.Fatalf("memoized lister returned wrong snapshot: %+v", panes)
		}
	}
	if calls != 1 {
		t.Fatalf("underlying lister called %d times, want exactly 1", calls)
	}
}

func TestFillPaneAlive(t *testing.T) {
	live := map[string]bool{"%5": true}

	// Stub the direct inspector: it "finds" %7 only (the recorded-id fallback
	// for a pane the global scan missed under -CC), and records whether it was
	// consulted so we can assert the scan-hit fast path skips it.
	inspected := []string{}
	prev := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		inspected = append(inspected, id)
		return tmuxpane.TmuxPane{PaneID: id}, id == "%7"
	}
	t.Cleanup(func() { statusPaneInspector = prev })

	// In the scan set -> alive, and the inspector is NOT consulted.
	rt := &tmuxRuntimeJSON{PaneID: "%5"}
	fillPaneAlive(rt, live)
	if !rt.PaneAlive {
		t.Error("pane %5 (in scan) should be alive")
	}
	if len(inspected) != 0 {
		t.Errorf("a scan hit must not consult the inspector; got %v", inspected)
	}

	// Missing from the scan but the direct inspect finds it (the -CC fallback).
	revived := &tmuxRuntimeJSON{PaneID: "%7"}
	fillPaneAlive(revived, live)
	if !revived.PaneAlive {
		t.Error("pane %7 missing from scan but found by direct inspect should be alive")
	}

	// Missing from the scan AND the inspector misses -> genuinely dead.
	dead := &tmuxRuntimeJSON{PaneID: "%9"}
	fillPaneAlive(dead, live)
	if dead.PaneAlive {
		t.Error("pane %9 (scan miss + inspect miss) should be dead")
	}

	// No pane id -> dead, and never inspected.
	inspected = nil
	noID := &tmuxRuntimeJSON{}
	fillPaneAlive(noID, live)
	if noID.PaneAlive {
		t.Error("a block with no pane id should be dead")
	}
	if len(inspected) != 0 {
		t.Errorf("no pane id must not consult the inspector; got %v", inspected)
	}

	fillPaneAlive(nil, live) // must not panic
}

func TestLivePaneIDSetDegradesOnError(t *testing.T) {
	set := livePaneIDSet(func() ([]tmuxpane.TmuxPane, error) { return nil, errors.New("no tmux server") })
	if len(set) != 0 {
		t.Fatalf("tmux error should yield empty set, got %v", set)
	}
	set = livePaneIDSet(func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{PaneID: "%1"}, {PaneID: ""}, {PaneID: "%2"}}, nil
	})
	if !set["%1"] || !set["%2"] || set[""] {
		t.Fatalf("unexpected live set: %v", set)
	}
}

func TestWriteResumeJSONShapeAndPaneAlive(t *testing.T) {
	// One restore member whose recorded pane is live, one fresh member with no
	// tmux identity.
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%265"}}, nil)
	plans := []resumePlan{
		{
			Role: "cto", Handle: "cto", Action: resumeRestore, HasRestoreRecord: true,
			Wake: "-", Command: "cd /r && amq-squad agent up codex --role cto",
			Tmux: &launch.TmuxInfo{Session: "main", WindowID: "@42", PaneID: "%265", Target: "current-window"},
		},
		{Role: "qa", Handle: "qa", Action: resumeFresh, Wake: "-", Command: "cd /r && amq-squad agent up claude --role qa"},
	}
	var buf bytes.Buffer
	if err := writeResumeJSON(&buf, team.Team{Project: "/r"}, "issue-96", resumeModeDefault, team.DefaultProfile, plans); err != nil {
		t.Fatalf("writeResumeJSON: %v", err)
	}
	env := decodeJSONEnvelope[resumeEnvelopeData](t, buf.String())
	if env.Kind != "resume_plan" {
		t.Fatalf("kind = %q, want resume_plan", env.Kind)
	}
	if env.Data.Profile != "" {
		t.Errorf("default profile should be omitted, got %q", env.Data.Profile)
	}
	if env.Data.Members != 2 || len(env.Data.Plan) != 2 {
		t.Fatalf("members/plan = %d/%d, want 2/2", env.Data.Members, len(env.Data.Plan))
	}
	cto := env.Data.Plan[0]
	if cto.Action != "restore" || !cto.HasRestoreRecord {
		t.Errorf("cto plan wrong: %+v", cto)
	}
	if cto.LaunchState != "will-launch" || cto.RecordState != "restorable" {
		t.Errorf("cto state wrong: launch=%q record=%q", cto.LaunchState, cto.RecordState)
	}
	if cto.Wake != "" {
		t.Errorf("wake '-' should normalize to empty, got %q", cto.Wake)
	}
	if cto.Tmux == nil || cto.Tmux.PaneID != "%265" || !cto.Tmux.PaneAlive {
		t.Errorf("cto tmux/pane_alive wrong: %+v", cto.Tmux)
	}
	qa := env.Data.Plan[1]
	if qa.Action != "launch fresh" || qa.Tmux != nil {
		t.Errorf("qa plan should be fresh with no tmux: %+v", qa)
	}
	if qa.LaunchState != "will-launch" || qa.RecordState != "missing" {
		t.Errorf("qa state wrong: launch=%q record=%q", qa.LaunchState, qa.RecordState)
	}
}

func TestHistoryRecordsCarryTmuxAndPaneAlive(t *testing.T) {
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%7"}}, nil)
	entries := []launch.Entry{
		{Source: "x", Record: launch.Record{
			Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-96", CWD: "/r",
			Tmux: &launch.TmuxInfo{PaneID: "%7", Session: "main"},
		}},
		{Source: "x", Record: launch.Record{Role: "qa", Handle: "qa", Binary: "claude", Session: "issue-96", CWD: "/r"}},
	}
	rows := historyRecordsFromEntries(entries)
	if rows[0].Tmux == nil || !rows[0].Tmux.PaneAlive {
		t.Errorf("history cto should carry live tmux: %+v", rows[0].Tmux)
	}
	if rows[1].Tmux != nil {
		t.Errorf("history qa should have no tmux: %+v", rows[1].Tmux)
	}
}

func TestResumeJSONRejectsExec(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runResume([]string{"--json", "--exec"})
	})
	if err == nil {
		t.Fatal("--json --exec should be rejected")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}
