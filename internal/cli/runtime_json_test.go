package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
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

func TestTerminalRuntimeMirrorsTmuxIdentity(t *testing.T) {
	term := terminalRuntimeFromTmuxInfo(&launch.TmuxInfo{
		Session:    "main",
		WindowID:   "@1",
		WindowName: "lead",
		PaneID:     "%5",
		Target:     "external",
	})
	if term == nil {
		t.Fatalf("terminal runtime should be present")
	}
	if term.Backend != "tmux" || term.Session != "main" || term.WindowID != "@1" || term.WindowName != "lead" || term.PaneID != "%5" || term.Target != "external" {
		t.Fatalf("terminal runtime = %+v", term)
	}
}

func TestTerminalRuntimeFromITerm2Info(t *testing.T) {
	term := terminalRuntimeFromInfo(&launch.TerminalInfo{
		Backend:    "iterm2",
		Session:    "issue-331",
		WindowID:   "101",
		WindowName: "amq:issue-331:cto",
		TabID:      "tab-1",
		SessionID:  "session-1",
		TTY:        "/dev/ttys001",
		Target:     "new-window",
	})
	if term == nil {
		t.Fatalf("terminal runtime should be present")
	}
	if term.Backend != "iterm2" || term.WindowID != "101" || term.TabID != "tab-1" || term.SessionID != "session-1" || term.TTY != "/dev/ttys001" || term.PaneAlive {
		t.Fatalf("terminal runtime = %+v", term)
	}
}

func TestPolicyAwareMemberActionsForTerminalAppRow(t *testing.T) {
	tm := team.Team{Project: "/repo", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}}}
	row := statusRecord{
		Role:     "cto",
		Handle:   "cto",
		AgentDir: t.TempDir(), // Stale/unscoped directory is not a verified AMQ route.
		Signals:  statusSignals{AgentPID: 1234, AgentAlive: true, BinaryMatch: true},
		Terminal: &terminalRuntimeJSON{
			Backend:  "terminal_app",
			Session:  "issue-332",
			WindowID: "401",
			TabID:    "1",
			TTY:      "/dev/ttys001",
			Target:   "new-window",
			PIDAlive: true,
		},
	}
	actions := policyAwareMemberActionsForRow(tm, "default", "issue-332", row)
	byKind := map[string]runtimeActionJSON{}
	for _, action := range actions {
		byKind[action.Kind] = action
	}
	if action := byKind["focus"]; action.Available || action.Reason != runtimecontrol.TerminalAppFocusDisabledReason {
		t.Fatalf("Terminal.app focus action = %+v", action)
	}
	if action := byKind["send"]; action.Available || action.Reason != runtimecontrol.TerminalAppInjectionDisabledReason {
		t.Fatalf("send action = %+v", action)
	}
	if action := byKind["goal_deliver"]; action.Available || action.Reason == "" {
		t.Fatalf("unevidenced goal delivery must fail closed: %+v", action)
	}
	if action := byKind["dispatch"]; action.Available || action.Reason == "" {
		t.Fatalf("unevidenced dispatch must fail closed: %+v", action)
	}

	root := t.TempDir()
	row.Handle = "cto"
	row.Root = root
	row.Session = "issue-332"
	row.Namespace.Profile = "default"
	row.Namespace.Session = row.Session
	row.Namespace.ID = "default/issue-332"
	row.AgentDir = filepath.Join(root, "agents", "cto")
	if err := os.MkdirAll(filepath.Join(row.AgentDir, "inbox", "cur"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"new", "tmp"} {
		if err := os.MkdirAll(filepath.Join(row.AgentDir, "inbox", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, action := range policyAwareMemberActionsForRow(tm, "default", "issue-332", row) {
		if action.Kind == "goal_deliver" && action.Available {
			t.Fatalf("durable member evidence must not claim native goal delivery: %+v", action)
		}
		if action.Kind == "dispatch" && !action.Available {
			t.Fatalf("verified mailbox route should enable dispatch: %+v", action)
		}
	}
}

func TestPolicyAwareMemberActionsForITerm2Row(t *testing.T) {
	tm := team.Team{Project: "/repo", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}}}
	row := statusRecord{
		Role:     "cto",
		Handle:   "cto",
		AgentDir: t.TempDir(), // No namespace root: fail closed despite directory existence.
		Signals:  statusSignals{AgentPID: 1234, AgentAlive: true, BinaryMatch: true},
		Terminal: &terminalRuntimeJSON{
			Backend:  "iterm2",
			Session:  "issue-331",
			WindowID: "101",
			Target:   "new-window",
			PIDAlive: true,
		},
	}
	actions := policyAwareMemberActionsForRow(tm, "default", "issue-331", row)
	byKind := map[string]runtimeActionJSON{}
	for _, action := range actions {
		byKind[action.Kind] = action
	}
	if !byKind["focus"].Available {
		t.Fatalf("iTerm2 focus should be available with window id: %+v", byKind["focus"])
	}
	if action := byKind["send"]; action.Available || action.Reason != runtimecontrol.ITerm2InjectionDisabledReason {
		t.Fatalf("send action = %+v", action)
	}
	if action := byKind["goal_deliver"]; action.Available || action.Reason == "" {
		t.Fatalf("unevidenced goal delivery must fail closed: %+v", action)
	}
	if action := byKind["dispatch"]; action.Available || action.Reason == "" {
		t.Fatalf("unevidenced dispatch must fail closed: %+v", action)
	}

}

func TestSyncTerminalRuntimeFromTmuxUsesSamePaneAlive(t *testing.T) {
	row := statusRecord{Tmux: &tmuxRuntimeJSON{Session: "main", PaneID: "%5", PaneAlive: true}}
	syncTerminalRuntimeFromTmux(&row)
	if row.Terminal == nil {
		t.Fatalf("terminal should be derived from tmux")
	}
	if row.Terminal.Backend != "tmux" || row.Terminal.PaneID != "%5" || !row.Terminal.PaneAlive {
		t.Fatalf("terminal runtime = %+v", row.Terminal)
	}

	row.Tmux.PaneAlive = false
	syncTerminalRuntimeFromTmux(&row)
	if row.Terminal.PaneAlive {
		t.Fatalf("terminal pane_alive must follow tmux pane_alive: %+v", row.Terminal)
	}
}

func TestDecorateTerminalRuntimeCapabilitiesIsExplicitForLocalInput(t *testing.T) {
	tests := []struct {
		name      string
		row       statusRecord
		wantTier  string
		wantState string
	}{
		{name: "tmux", row: statusRecord{Terminal: &terminalRuntimeJSON{Backend: runtimecontrol.BackendTmux, PaneAlive: true}}, wantTier: runtimecontrol.TierA, wantState: runtimecontrol.SupportSupported},
		{name: "iterm2", row: statusRecord{Signals: statusSignals{AgentAlive: true, BinaryMatch: true}, Terminal: &terminalRuntimeJSON{Backend: runtimecontrol.BackendITerm2, WindowID: "101", PIDAlive: true}}, wantTier: runtimecontrol.TierB, wantState: runtimecontrol.SupportUnsupported},
		{name: "terminal", row: statusRecord{Signals: statusSignals{AgentAlive: true, BinaryMatch: true}, Terminal: &terminalRuntimeJSON{Backend: runtimecontrol.BackendTerminalApp, WindowID: "401", PIDAlive: true}}, wantTier: runtimecontrol.TierC, wantState: runtimecontrol.SupportUnsupported},
		{name: "unknown", row: statusRecord{Terminal: &terminalRuntimeJSON{Backend: "future"}}, wantTier: runtimecontrol.TierUnsupported, wantState: runtimecontrol.SupportUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decorateTerminalRuntimeCapabilities(&tc.row)
			if tc.row.Terminal.Tier != tc.wantTier {
				t.Fatalf("tier = %q", tc.row.Terminal.Tier)
			}
			local := tc.row.Terminal.Capabilities[string(runtimecontrol.CapabilityLocalInput)]
			if local.State != tc.wantState || (local.State != runtimecontrol.SupportSupported && (local.ReasonCode == "" || local.Reason == "")) {
				t.Fatalf("local-input state = %+v", local)
			}
			if _, ok := tc.row.Terminal.Capabilities[string(runtimecontrol.CapabilityGoalDeliver)]; ok {
				t.Fatalf("effective goal action leaked into raw terminal capabilities")
			}
			if _, ok := tc.row.Terminal.Capabilities[string(runtimecontrol.CapabilityDispatch)]; ok {
				t.Fatalf("effective dispatch action leaked into raw terminal capabilities")
			}
			goal := runtimeCapabilitiesForStatusRow(tc.row).State(runtimecontrol.CapabilityGoalDeliver)
			if tc.name == "iterm2" || tc.name == "terminal" {
				if goal.Available || goal.State != runtimecontrol.SupportUnsupported || goal.ReasonCode == "" {
					t.Fatalf("unevidenced effective goal action = %+v", goal)
				}
			}
		})
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
	if err := writeResumeJSON(&buf, team.Team{Project: "/r"}, "issue-96", resumeModeDefault, team.DefaultProfile, nil, plans); err != nil {
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
	if cto.Terminal == nil || cto.Terminal.Backend != runtimecontrol.BackendTmux || cto.Terminal.Tier != runtimecontrol.TierA || cto.Terminal.Capabilities[string(runtimecontrol.CapabilityLocalInput)].State != runtimecontrol.SupportSupported {
		t.Errorf("cto authoritative terminal contract wrong: %+v", cto.Terminal)
	}
	qa := env.Data.Plan[1]
	if qa.Action != "launch fresh" || qa.Tmux != nil {
		t.Errorf("qa plan should be fresh with no tmux: %+v", qa)
	}
	if qa.LaunchState != "will-launch" || qa.RecordState != "missing" {
		t.Errorf("qa state wrong: launch=%q record=%q", qa.LaunchState, qa.RecordState)
	}
}

func TestWriteResumeJSONGoalPlanIsAdditiveAndPreservesSelection(t *testing.T) {
	base := team.Team{Project: "/r"}
	plans := []resumePlan{{Role: "cto", Handle: "cto", Action: resumeRestore}}
	var legacy bytes.Buffer
	if err := writeResumeJSON(&legacy, base, "s", resumeModeDefault, team.DefaultProfile, nil, plans); err != nil {
		t.Fatal(err)
	}
	if env := decodeJSONEnvelope[resumeEnvelopeData](t, legacy.String()); env.Data.GoalPlan != nil || strings.Contains(legacy.String(), "goal_plan") {
		t.Fatalf("legacy resume JSON changed: %s", legacy.String())
	}
	for _, selected := range []bool{false, true} {
		var buf bytes.Buffer
		plan := runwizard.ResumeGoalPlan{SchemaVersion: 1, Action: "redeliver", Eligible: true, Selected: selected, BindingDigest: "sha256:binding", EvidenceDigest: "sha256:evidence"}
		if err := writeResumeJSONWithGoal(&buf, base, "s", resumeModeDefault, team.DefaultProfile, nil, plans, plan); err != nil {
			t.Fatal(err)
		}
		env := decodeJSONEnvelope[resumeEnvelopeData](t, buf.String())
		if env.SchemaVersion != 1 || env.Data.GoalPlan == nil || env.Data.GoalPlan.Selected != selected || env.Data.GoalPlan.BindingDigest != plan.BindingDigest {
			t.Fatalf("selected=%t JSON=%s", selected, buf.String())
		}
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
