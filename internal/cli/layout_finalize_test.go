package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestPrepareLayoutFinalizationFailsClosedBeforeSpawn(t *testing.T) {
	selection := runStartLayoutSelection{Preset: layoutPresetLeadLeft, LauncherPane: launcherPaneCloseAfterStart}
	t.Setenv("TMUX_PANE", "")
	if _, err := prepareLayoutFinalization(selection); err == nil || !strings.Contains(err.Error(), "TMUX_PANE") {
		t.Fatalf("missing launcher pane error = %v", err)
	}
	t.Setenv("TMUX_PANE", "%lead")
	if _, err := prepareLayoutFinalization(selection); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("name-like launcher pane error = %v", err)
	}

	t.Setenv("TMUX_PANE", "%1")
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{PaneID: "%2", WindowID: "@1"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	if _, err := prepareLayoutFinalization(selection); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("launcher mismatch error = %v", err)
	}
}

func TestBuildLayoutFinalizationPlanUsesConfiguredLeadResult(t *testing.T) {
	selection, err := resolveRunStartLayout(runStartLayoutInput{Preset: layoutPresetLeadLeft, PresetSet: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildLayoutFinalizationPlan(t.TempDir(), "review", "issue-393", "cto", selection,
		layoutFinalizationContext{LauncherPaneID: "%1", LauncherWindowID: "@1"},
		teamLaunchResult{Panes: []teamLaunchResultPane{
			{Role: "qa", PaneID: "%2", WindowID: "@1"},
			{Role: "cto", PaneID: "%3", WindowID: "@1"},
		}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.LeadPaneID != "%3" || plan.LeadWindowID != "@1" || plan.LauncherPaneID != "%1" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestBuildLayoutFinalizationPlanRejectsEveryNonExactRuntimeID(t *testing.T) {
	selection := runStartLayoutSelection{Preset: layoutPresetLeadLeft, LauncherPane: launcherPaneKeep}
	validContext := layoutFinalizationContext{LauncherPaneID: "%1", LauncherWindowID: "@1"}
	validResult := teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "%2", WindowID: "@1"}}}
	for _, tc := range []struct {
		name   string
		ctx    layoutFinalizationContext
		result teamLaunchResult
	}{
		{name: "launcher pane name", ctx: layoutFinalizationContext{LauncherPaneID: "launcher", LauncherWindowID: "@1"}, result: validResult},
		{name: "launcher window name", ctx: layoutFinalizationContext{LauncherPaneID: "%1", LauncherWindowID: "lead"}, result: validResult},
		{name: "result pane name", ctx: validContext, result: teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "cto", WindowID: "@1"}}}},
		{name: "result window name", ctx: validContext, result: teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "%2", WindowID: "main"}}}},
		{name: "pane prefix with non-digits", ctx: validContext, result: teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "%cto", WindowID: "@1"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := buildLayoutFinalizationPlan(t.TempDir(), "", "issue-393", "cto", selection, tc.ctx, tc.result, false)
			if err == nil || !strings.Contains(err.Error(), "exact") {
				t.Fatalf("error = %v, want exact-ID rejection", err)
			}
		})
	}
}

func TestLayoutFinalizationScriptExactIDsBoundedAndRenameAgnostic(t *testing.T) {
	selection := runStartLayoutSelection{
		Preset: layoutPresetLeadLeft, LauncherPane: launcherPaneCloseAfterStart,
		FinalLayout: "main-vertical", MainPaneOption: "main-pane-width", MainPaneValue: "60%", LeadMain: true,
	}
	plan := layoutFinalizationPlan{
		Selection: selection, ParentPID: 4242, LauncherPaneID: "%1", LeadPaneID: "%3", LeadWindowID: "@7",
		WarningPath: "/tmp/amq-layout/default/issue-393.warning",
	}
	script := layoutFinalizationScript(plan)
	for _, want := range []string{
		"kill -0 4242", "-ge 200", "tmux kill-pane -t '%1'", "tmux list-panes -t '@7'", "tmux swap-pane -d -s '%3'",
		"tmux set-option -w -t '@7' main-pane-width", "total*60/100", "tmux select-layout -t '@7' main-vertical", "tmux select-pane -t '%3'",
		"lead_pane_probe=$(tmux display-message -p -t '%3' '##{pane_id}'", "[ \"$lead_pane_probe\" = '%3' ] || fail 'layout finalization skipped: lead pane is missing'",
		"lead_window_probe=$(tmux display-message -p -t '@7' '##{window_id}'", "[ \"$lead_window_probe\" = '@7' ] || fail 'layout finalization skipped: lead window is missing'",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if strings.Index(script, "timed out waiting") > strings.Index(script, "tmux kill-pane") {
		t.Fatalf("timeout guard must precede launcher close:\n%s", script)
	}
	setOption := strings.Index(script, "tmux set-option")
	selectLayout := strings.Index(script, "tmux select-layout")
	resolveMain := strings.Index(script, "main=$(tmux list-panes")
	swapLead := strings.Index(script, "tmux swap-pane")
	verifyLead := strings.Index(script, "actual=$(tmux display-message")
	if setOption < 0 || selectLayout < setOption || resolveMain < selectLayout || swapLead < resolveMain || verifyLead < swapLead {
		t.Fatalf("finalizer must size and apply the layout before moving and verifying the exact lead pane:\n%s", script)
	}
	if !strings.Contains(script, "'##{pane_width}'") || !strings.Contains(script, "lead pane dimension mismatch: expected $main_size, got $actual") {
		t.Fatalf("finalizer must verify the lead received the requested main width:\n%s", script)
	}
	if !strings.Contains(script, "'##{pane_id} ##{pane_left} ##{pane_width}'") || !strings.Contains(script, "awk -v size=\"$main_size\" '$2 == 0 && $3 == size") || strings.Contains(script, "head -n 1") {
		t.Fatalf("finalizer must resolve the post-layout main pane by geometry, not enumeration order:\n%s", script)
	}
	if strings.Contains(script, "'##{pane_id}' >/dev/null") || strings.Contains(script, "'##{window_id}' >/dev/null") {
		t.Fatalf("liveness probes must reject empty or wrong successful output, not trust tmux exit status:\n%s", script)
	}
	if strings.LastIndex(script, "rm -f") < strings.LastIndex(script, "select-pane") {
		t.Fatalf("successful helper must clear its warning only after layout and focus:\n%s", script)
	}
	for _, forbidden := range []string{"window_name", "pane_title", "amq:issue-393", "cto", "qa"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("script targets rename-sensitive token %q:\n%s", forbidden, script)
		}
	}
}

func TestLayoutFinalizationScriptVerifiesLeadTopGeometryAfterReflow(t *testing.T) {
	selection := runStartLayoutSelection{
		Preset: layoutPresetLeadTop, LauncherPane: launcherPaneCloseAfterStart,
		FinalLayout: "main-horizontal", MainPaneOption: "main-pane-height", MainPaneValue: "60%", LeadMain: true,
	}
	plan := layoutFinalizationPlan{
		Selection: selection, ParentPID: 4242, LauncherPaneID: "%1", LeadPaneID: "%3", LeadWindowID: "@7",
		WarningPath: "/tmp/amq-layout/default/issue-393.warning",
	}
	script := layoutFinalizationScript(plan)
	selectLayout := strings.Index(script, "tmux select-layout -t '@7' main-horizontal")
	swapLead := strings.Index(script, "tmux swap-pane -d -s '%3'")
	verifyLead := strings.Index(script, "'##{pane_height}'")
	if selectLayout < 0 || swapLead < selectLayout || verifyLead < swapLead {
		t.Fatalf("lead-top finalizer must reflow before moving and verifying the lead:\n%s", script)
	}
	if !strings.Contains(script, "'##{pane_id} ##{pane_top} ##{pane_height}'") {
		t.Fatalf("lead-top finalizer must resolve the main pane by top/height geometry:\n%s", script)
	}
}

func TestLayoutFinalizationLauncherSafetyAndWindowPreset(t *testing.T) {
	selection := runStartLayoutSelection{Preset: layoutPresetOneWindowPerAgent, LauncherPane: launcherPaneCloseAfterStart}
	base := layoutFinalizationPlan{Selection: selection, ParentPID: 1, LauncherPaneID: "%1", LeadPaneID: "%2", LeadWindowID: "@2", WarningPath: "/tmp/w.warning"}
	script := layoutFinalizationScript(base)
	if !strings.Contains(script, "launcher_probe=$(tmux display-message") || !strings.Contains(script, "[ \"$launcher_probe\" = '%1' ]") || !strings.Contains(script, "tmux kill-pane -t '%1'") {
		t.Fatalf("missing idempotent launcher close:\n%s", script)
	}
	if strings.Contains(script, "select-layout") || !strings.Contains(script, "select-window -t '@2'") {
		t.Fatalf("one-window finalizer must only focus exact lead window/pane:\n%s", script)
	}
	base.LauncherPaneID = base.LeadPaneID
	if got := layoutFinalizationScript(base); strings.Contains(got, "kill-pane") {
		t.Fatalf("launcher==lead must never be killed:\n%s", got)
	}
}

func TestExternalLeadFinalizationUsesCapturedPaneAndForcesKeep(t *testing.T) {
	selection, err := resolveRunStartLayout(runStartLayoutInput{Preset: layoutPresetEvenGrid, PresetSet: true, ExternalLead: true})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildLayoutFinalizationPlan(t.TempDir(), "", "issue-393", "cto", selection,
		layoutFinalizationContext{LauncherPaneID: "%42", LauncherWindowID: "@7"}, teamLaunchResult{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.LeadPaneID != "%42" || plan.LeadWindowID != "@7" || plan.Selection.LauncherPane != launcherPaneKeep {
		t.Fatalf("external plan = %+v", plan)
	}
	if strings.Contains(layoutFinalizationScript(plan), "kill-pane") {
		t.Fatal("external lead finalizer attempted to close the lead/launcher")
	}
}

func TestScheduleLayoutFinalizationPersistsWarningWithoutTeardown(t *testing.T) {
	prev := layoutFinalizationScheduler
	layoutFinalizationScheduler = func(string, string) error { return errors.New("tmux unavailable") }
	t.Cleanup(func() { layoutFinalizationScheduler = prev })
	dir := t.TempDir()
	plan := layoutFinalizationPlan{
		Selection: runStartLayoutSelection{Preset: layoutPresetEvenGrid, LauncherPane: launcherPaneKeep, FinalLayout: "tiled"},
		ParentPID: 1, LauncherPaneID: "%1", LeadPaneID: "%2", LeadWindowID: "@1",
		WarningPath: layoutFinalizationWarningPath(dir, team.DefaultProfile, "issue-393"),
	}
	err := scheduleLayoutFinalization(plan)
	if err == nil {
		t.Fatal("expected scheduling error")
	}
	body, readErr := os.ReadFile(plan.WarningPath)
	if readErr != nil || !strings.Contains(string(body), "tmux unavailable") {
		t.Fatalf("warning = %q err=%v", body, readErr)
	}
	warnings, statusErr := statusWarnings(dir, team.DefaultProfile, "issue-393", time.Now())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	found := false
	for _, warning := range warnings {
		if warning.Kind == "layout_finalization" && strings.Contains(warning.Detail, "tmux unavailable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("status warnings = %+v", warnings)
	}
}

func TestScheduleLayoutFinalizationUsesBackgroundRunShell(t *testing.T) {
	prevRun := layoutFinalizationRunCommand
	t.Cleanup(func() { layoutFinalizationRunCommand = prevRun })
	var gotName string
	var gotArgs []string
	layoutFinalizationRunCommand = func(name string, args ...string) error {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return nil
	}
	controlRoot := t.TempDir()
	plan := layoutFinalizationPlan{
		Selection: runStartLayoutSelection{
			Preset: layoutPresetLeadLeft, LauncherPane: launcherPaneCloseAfterStart,
			FinalLayout: "main-vertical", MainPaneOption: "main-pane-width", MainPaneValue: "60%", LeadMain: true,
		},
		ParentPID: 1, LauncherPaneID: "%1", LeadPaneID: "%2", LeadWindowID: "@1",
		WarningPath: layoutFinalizationWarningPath(controlRoot, team.DefaultProfile, "issue-393"),
		ControlRoot: controlRoot,
	}
	if err := scheduleLayoutFinalization(plan); err != nil {
		t.Fatal(err)
	}
	if gotName != "tmux" || len(gotArgs) != 3 || gotArgs[0] != "run-shell" || gotArgs[1] != "-b" {
		t.Fatalf("scheduler command = %s %v", gotName, gotArgs)
	}
	if !strings.Contains(gotArgs[2], "while kill -0 1") || !strings.Contains(gotArgs[2], "-ge 200") {
		t.Fatalf("scheduled helper lacks bounded parent wait: %q", gotArgs[2])
	}
	for _, escaped := range []string{
		"'##{pane_id}'", "'##{window_id}'", "'##{window_width}'",
		"'##{pane_id} ##{pane_left} ##{pane_width}'", "'##{pane_width}'",
	} {
		if !strings.Contains(gotArgs[2], escaped) {
			t.Fatalf("scheduled run-shell helper lacks escaped nested format %q: %q", escaped, gotArgs[2])
		}
	}
	if strings.Contains(strings.ReplaceAll(gotArgs[2], "##{", ""), "#{") {
		t.Fatalf("scheduled run-shell helper contains an unescaped nested tmux format: %q", gotArgs[2])
	}
	// #525: the payload actually handed to `tmux run-shell -b` must be the
	// silenced wrapper, not the bare script.
	if !strings.Contains(gotArgs[2], "( ") || !strings.HasSuffix(gotArgs[2], " || true") {
		t.Fatalf("scheduled run-shell payload is not wrapped for silence: %q", gotArgs[2])
	}
	if !strings.Contains(gotArgs[2], ">>"+shellQuote(runShellLogPath(controlRoot))+" 2>&1") {
		t.Fatalf("scheduled run-shell payload does not redirect to the run-shell log: %q", gotArgs[2])
	}
	if !strings.Contains(gotArgs[2], "mkdir -p "+shellQuote(filepath.Dir(runShellLogPath(controlRoot)))) {
		t.Fatalf("scheduled run-shell payload does not ensure its log directory exists: %q", gotArgs[2])
	}
}

func TestPrintLayoutFinalizationDryRunShowsBoundedBackgroundExactIDFlow(t *testing.T) {
	selection := runStartLayoutSelection{
		Preset: layoutPresetLeadLeft, LauncherPane: launcherPaneCloseAfterStart,
		FinalLayout: "main-vertical", MainPaneOption: "main-pane-width", MainPaneValue: "60%", LeadMain: true,
	}
	out, _, err := captureOutput(t, func() error {
		printLayoutFinalizationDryRun(selection, "%1")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"tmux run-shell -b", "wait up to 200 ticks", "$parent_cli_pid", "$lead_pane_id", "$lead_window_id",
		"exact synchronous backend result", "idempotently skipped when missing or equal to lead",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "tmux run-shell -b") > strings.Index(out, "tmux kill-pane") {
		t.Fatalf("dry-run must present close as part of the bounded background finalizer:\n%s", out)
	}
}
