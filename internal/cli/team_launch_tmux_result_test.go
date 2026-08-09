package cli

import (
	"fmt"
	"strings"
	"testing"
)

func stubTmuxResultCommands(t *testing.T, output func(string, ...string) (string, error)) *[]string {
	t.Helper()
	oldOutput := tmuxOutputCommand
	oldRun := tmuxRunCommand
	var runCalls []string
	tmuxOutputCommand = output
	tmuxRunCommand = func(name string, args ...string) error {
		runCalls = append(runCalls, name+" "+strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() {
		tmuxOutputCommand = oldOutput
		tmuxRunCommand = oldRun
	})
	return &runCalls
}

func TestRunTmuxCurrentWindowMapsExactResultToConfiguredNonFirstLead(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	nextPane := 1
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.Contains(call, "#{session_name}:#{window_index}"):
			return "operator:0\n", nil
		case len(args) > 0 && args[0] == "split-window":
			nextPane++
			return fmt.Sprintf("%%%d\n", nextPane), nil
		case strings.Contains(call, "#{window_id}"):
			return "@7\n", nil
		// #571 delivers the command as the pane ROOT PROCESS and then reads
		// #{pane_pid} to prove it started. These fakes answer with a fixed pid so
		// the launch counts as verified; the empty-pid refusal has its own test.
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})

	result, err := runTmuxLaunchPlanWithResult(tmuxLaunchPlan{
		Session: "unused", Workstream: "issue-393", Target: "current-window", Layout: "vertical",
		Panes: []teamLaunchPane{{Role: "qa", CWD: "/repo", Command: "qa-command"}, {Role: "cto", CWD: "/repo", Command: "cto-command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Panes) != 2 || result.Panes[0].PaneID != "%2" || result.Panes[1].PaneID != "%3" || result.Panes[1].WindowID != "@7" {
		t.Fatalf("result = %+v", result)
	}
	selection := runStartLayoutSelection{Preset: layoutPresetLeadLeft, LauncherPane: launcherPaneKeep}
	plan, err := buildLayoutFinalizationPlan(t.TempDir(), "", "issue-393", "cto", selection,
		layoutFinalizationContext{LauncherPaneID: "%1", LauncherWindowID: "@7"}, result, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.LeadPaneID != "%3" || plan.LeadWindowID != "@7" {
		t.Fatalf("configured lead plan = %+v", plan)
	}
	if got := strings.Count(strings.Join(*runCalls, "\n"), "respawn-pane"); got != 2 {
		t.Fatalf("send calls = %v", *runCalls)
	}
}

func TestRunTmuxCurrentWindowResultFailureSendsNoAgentCommands(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.Contains(call, "#{session_name}:#{window_index}"):
			return "operator:0\n", nil
		case len(args) > 0 && args[0] == "split-window":
			return "%2\n", nil
		case strings.Contains(call, "#{window_id}"):
			return "", fmt.Errorf("window id unavailable")
		// #571 delivers the command as the pane ROOT PROCESS and then reads
		// #{pane_pid} to prove it started. These fakes answer with a fixed pid so
		// the launch counts as verified; the empty-pid refusal has its own test.
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})

	_, err := runTmuxLaunchPlanWithResult(tmuxLaunchPlan{
		Session: "unused", Workstream: "issue-393", Target: "current-window", Layout: "tiled",
		Panes: []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "agent-command"}},
	})
	if err == nil || !strings.Contains(err.Error(), "window id unavailable") {
		t.Fatalf("result error = %v", err)
	}
	for _, call := range *runCalls {
		if strings.Contains(call, "send-keys") {
			t.Fatalf("ID failure must precede agent commands: %v", *runCalls)
		}
	}
}

func TestRunTmuxOneWindowMapsExactResultToConfiguredNonFirstLead(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	nextPane := 1
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.Contains(call, "#{session_name}"):
			return "operator\n", nil
		case len(args) > 0 && args[0] == "new-window":
			nextPane++
			return fmt.Sprintf("%%%d\n", nextPane), nil
		case strings.Contains(call, "#{window_id}") && strings.Contains(call, "%2"):
			return "@2\n", nil
		case strings.Contains(call, "#{window_id}") && strings.Contains(call, "%3"):
			return "@3\n", nil
		// #571 delivers the command as the pane ROOT PROCESS and then reads
		// #{pane_pid} to prove it started. These fakes answer with a fixed pid so
		// the launch counts as verified; the empty-pid refusal has its own test.
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})

	result, err := runTmuxWindowsPlanWithResult(tmuxLaunchPlan{
		Session: "unused", Workstream: "issue-393", Target: "new-window",
		Panes: []teamLaunchPane{{Role: "qa", CWD: "/repo", Command: "qa-command"}, {Role: "cto", CWD: "/repo", Command: "cto-command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	selection := runStartLayoutSelection{Preset: layoutPresetOneWindowPerAgent, LauncherPane: launcherPaneCloseAfterStart}
	plan, err := buildLayoutFinalizationPlan(t.TempDir(), "", "issue-393", "cto", selection,
		layoutFinalizationContext{LauncherPaneID: "%1", LauncherWindowID: "@1"}, result, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.LeadPaneID != "%3" || plan.LeadWindowID != "@3" {
		t.Fatalf("configured lead plan = %+v result=%+v", plan, result)
	}
	if got := strings.Count(strings.Join(*runCalls, "\n"), "respawn-pane"); got != 2 {
		t.Fatalf("send calls = %v", *runCalls)
	}
}

func TestRunTmuxNewSessionWithResultCapturesExactFirstPaneBeforeSend(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "")
	oldExists := tmuxSessionExists
	tmuxSessionExists = func(string) bool { return false }
	t.Cleanup(func() { tmuxSessionExists = oldExists })
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case len(args) > 0 && args[0] == "new-session":
			return "%9\n", nil
		case strings.Contains(call, "#{window_id}"):
			return "@4\n", nil
		// #571 delivers the command as the pane ROOT PROCESS and then reads
		// #{pane_pid} to prove it started. These fakes answer with a fixed pid so
		// the launch counts as verified; the empty-pid refusal has its own test.
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})
	result, err := runTmuxLaunchPlanWithResult(tmuxLaunchPlan{
		Session: "fresh", Workstream: "issue-393", Target: "new-session", Layout: "vertical",
		Panes: []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "cto-command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Panes) != 1 || result.Panes[0].PaneID != "%9" || result.Panes[0].WindowID != "@4" {
		t.Fatalf("result = %+v", result)
	}
	if got := strings.Count(strings.Join(*runCalls, "\n"), "respawn-pane"); got != 1 {
		t.Fatalf("send calls = %v", *runCalls)
	}
}

func TestRunTmuxNewSessionResumeStageReusesExistingLeadSession(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "")
	oldExists := tmuxSessionExists
	tmuxSessionExists = func(session string) bool { return session == "squad" }
	t.Cleanup(func() { tmuxSessionExists = oldExists })
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "new-session" {
			t.Fatalf("dependent stage must not recreate the lead session: %s %s", name, strings.Join(args, " "))
		}
		if len(args) > 0 && args[0] == "split-window" {
			return "%10\n", nil
		}
		// #571 reads #{pane_pid} after delivery to prove the command started.
		if strings.Contains(strings.Join(args, " "), "#{pane_pid}") || strings.Contains(strings.Join(args, " "), "#{pane_dead}") {
			return fakePaneIdentityReply(args), nil
		}
		return "", fmt.Errorf("unexpected output command: %s %s", name, strings.Join(args, " "))
	})

	err := runTmuxLaunchPlan(tmuxLaunchPlan{
		Session: "squad", Workstream: "issue-473", Target: "new-session", Layout: "vertical", AllowExistingSession: true,
		Panes: []teamLaunchPane{{Role: "qa", CWD: "/repo", Command: "worker-command"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*runCalls, "\n")
	if !strings.Contains(joined, "select-pane -t %10 -T amq:issue-473:qa") || !strings.Contains(joined, "respawn-pane -k -t %10") {
		t.Fatalf("dependent stage calls = %s", joined)
	}
}

func TestTmuxLaunchResultRejectsNameLikePaneAndWindowTargets(t *testing.T) {
	stubExactPaneInspection(t)
	oldOutput := tmuxOutputCommand
	t.Cleanup(func() { tmuxOutputCommand = oldOutput })
	tmuxOutputCommand = func(string, ...string) (string, error) { return "@1", nil }
	if _, err := tmuxLaunchResult([]teamLaunchPane{{Role: "cto"}}, []string{"cto"}); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("name-like pane error = %v", err)
	}
	tmuxOutputCommand = func(string, ...string) (string, error) { return "main", nil }
	if _, err := tmuxLaunchResult([]teamLaunchPane{{Role: "cto"}}, []string{"%2"}); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("name-like window error = %v", err)
	}
}

func TestCompleteTeamLaunchResultFailsClosed(t *testing.T) {
	stubExactPaneInspection(t)
	panes := []teamLaunchPane{{Role: "cto"}, {Role: "qa"}}
	for _, tc := range []struct {
		name   string
		target string
		result teamLaunchResult
	}{
		{name: "missing", result: teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "%1", WindowID: "@1"}}}},
		{name: "duplicate-role", result: teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "%1", WindowID: "@1"}, {Role: "cto", PaneID: "%2", WindowID: "@1"}}}},
		{name: "duplicate-pane", result: teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "%1", WindowID: "@1"}, {Role: "qa", PaneID: "%1", WindowID: "@1"}}}},
		{name: "duplicate-new-window", target: "new-window", result: teamLaunchResult{Panes: []teamLaunchResultPane{{Role: "cto", PaneID: "%1", WindowID: "@1"}, {Role: "qa", PaneID: "%2", WindowID: "@1"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateCompleteTeamLaunchResult(panes, tc.target, tc.result); err == nil {
				t.Fatal("incomplete or duplicate result unexpectedly accepted")
			}
		})
	}
}

// The mid-run member-add default: a current-window plan flagged
// LeadMainCurrentWindow arranges the window as main-vertical (launcher/lead
// keeps a full-height left column, added workers stack in rows to its right)
// with a best-effort 60% main-pane-width, and does NOT run the generic
// even-layout pass. A single added worker still gets the arrangement — the
// window already holds the lead — where the legacy path applied no layout at
// all.
func TestRunTmuxCurrentWindowLeadMainAppliesMainVertical(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	nextPane := 1
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.Contains(call, "#{session_name}:#{window_index}"):
			return "loco:1\n", nil
		case len(args) > 0 && args[0] == "show-options":
			return "", nil
		case len(args) > 0 && args[0] == "split-window":
			nextPane++
			return fmt.Sprintf("%%%d\n", nextPane), nil
		case strings.Contains(call, "#{window_width}"):
			return "200\n", nil
		case strings.Contains(call, "#{window_id}"):
			return "@7\n", nil
		// select-layout put the LAST pane in the main column, not the lead:
		// the arrangement must detect that and swap the lead in.
		case len(args) > 0 && args[0] == "list-panes":
			return "%2\t0\n%1\t121\n", nil
		case strings.Contains(call, "#{pane_left}"):
			// Post-swap verification probe: the lead now sits in column 0.
			return "%1\t0\n", nil
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})

	if err := runTmuxLaunchPlan(tmuxLaunchPlan{
		Session: "unused", Workstream: "omri-mem", Target: "current-window", Layout: "vertical",
		LeadMainCurrentWindow: true,
		Panes:                 []teamLaunchPane{{Role: "researcher", CWD: "/repo", Command: "worker-command"}},
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(*runCalls, "\n")
	if !strings.Contains(joined, "set-option -w -t loco:1 main-pane-width 120") {
		t.Fatalf("main-pane-width not set to 60%% of window width:\n%s", joined)
	}
	if !strings.Contains(joined, "select-layout -t loco:1 main-vertical") {
		t.Fatalf("main-vertical not applied:\n%s", joined)
	}
	// The whole point of the arrangement: the LEAD becomes the main pane, not
	// whichever pane the layout pass happened to leave in column 0.
	if !strings.Contains(joined, "swap-pane -d -s %1 -t %2") {
		t.Fatalf("lead not swapped into the main pane:\n%s", joined)
	}
	if !strings.Contains(joined, "select-pane -t %1\n") && !strings.HasSuffix(joined, "select-pane -t %1") {
		t.Fatalf("focus not restored to the lead pane:\n%s", joined)
	}
	if strings.Contains(joined, "even-horizontal") || strings.Contains(joined, "even-vertical") || strings.Contains(joined, "tiled") {
		t.Fatalf("generic layout pass must not run alongside lead-main:\n%s", joined)
	}
}

// When the layout pass already left the lead in the main column, no swap runs
// -- swapping unconditionally would move the lead OUT of the main pane.
func TestRunTmuxCurrentWindowLeadMainSkipsSwapWhenLeadAlreadyMain(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.Contains(call, "#{session_name}:#{window_index}"):
			return "loco:1\n", nil
		case len(args) > 0 && args[0] == "show-options":
			return "", nil
		case len(args) > 0 && args[0] == "split-window":
			return "%2\n", nil
		case strings.Contains(call, "#{window_width}"):
			return "200\n", nil
		case len(args) > 0 && args[0] == "list-panes":
			return "%1\t0\n%2\t121\n", nil
		case strings.Contains(call, "#{pane_left}"):
			return "%1\t0\n", nil
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})

	if err := runTmuxLaunchPlan(tmuxLaunchPlan{
		Session: "unused", Workstream: "omri-mem", Target: "current-window", Layout: "vertical",
		LeadMainCurrentWindow: true,
		Panes:                 []teamLaunchPane{{Role: "researcher", CWD: "/repo", Command: "worker-command"}},
	}); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(*runCalls, "\n"); strings.Contains(joined, "swap-pane") {
		t.Fatalf("swap-pane must not run when the lead already holds the main pane:\n%s", joined)
	}
}

// A failed arrangement rolls back the created panes AND the window option it
// mutated: the operator's own window must not keep a stray main-pane-width
// after a launch that reported failure.
func TestRunTmuxCurrentWindowLeadMainFailureRestoresWindowOptions(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.Contains(call, "#{session_name}:#{window_index}"):
			return "loco:1\n", nil
		case len(args) > 0 && args[0] == "show-options":
			return "", nil
		case len(args) > 0 && args[0] == "split-window":
			return "%2\n", nil
		case strings.Contains(call, "#{window_width}"):
			return "200\n", nil
		case len(args) > 0 && args[0] == "list-panes":
			return "%2\t0\n%1\t121\n", nil
		case strings.Contains(call, "#{pane_left}"):
			// Verification probe: the lead is NOT in the main column even
			// after the swap -- the arrangement must fail, not shrug.
			return "%1\t121\n", nil
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})

	err := runTmuxLaunchPlan(tmuxLaunchPlan{
		Session: "unused", Workstream: "omri-mem", Target: "current-window", Layout: "vertical",
		LeadMainCurrentWindow: true,
		Panes:                 []teamLaunchPane{{Role: "researcher", CWD: "/repo", Command: "worker-command"}},
	})
	if err == nil || !strings.Contains(err.Error(), "did not land in the main column") {
		t.Fatalf("unverified lead-main arrangement unexpectedly accepted: %v", err)
	}
	joined := strings.Join(*runCalls, "\n")
	if !strings.Contains(joined, "set-option -w -u -t loco:1 main-pane-width") {
		t.Fatalf("mutated main-pane-width not restored on failure:\n%s", joined)
	}
	if !strings.Contains(joined, "kill-pane -t %2") {
		t.Fatalf("created worker pane not rolled back on failure:\n%s", joined)
	}
}

// Without the flag (explicit --layout, non-orchestrated, or a lead launch),
// current-window keeps its legacy behavior: no layout pass for a single pane.
func TestRunTmuxCurrentWindowWithoutLeadMainKeepsLegacyLayout(t *testing.T) {
	stubExactPaneInspection(t)
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	runCalls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		case strings.Contains(call, "#{session_name}:#{window_index}"):
			return "loco:1\n", nil
		case len(args) > 0 && args[0] == "split-window":
			return "%2\n", nil
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			return fakePaneIdentityReply(args), nil
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})

	if err := runTmuxLaunchPlan(tmuxLaunchPlan{
		Session: "unused", Workstream: "omri-mem", Target: "current-window", Layout: "vertical",
		Panes: []teamLaunchPane{{Role: "researcher", CWD: "/repo", Command: "worker-command"}},
	}); err != nil {
		t.Fatal(err)
	}
	if joined := strings.Join(*runCalls, "\n"); strings.Contains(joined, "select-layout") || strings.Contains(joined, "main-pane-width") {
		t.Fatalf("single-pane legacy path must not apply a layout:\n%s", joined)
	}
}
