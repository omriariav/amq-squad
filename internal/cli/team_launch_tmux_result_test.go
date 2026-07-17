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
	if got := strings.Count(strings.Join(*runCalls, "\n"), "send-keys"); got != 2 {
		t.Fatalf("send calls = %v", *runCalls)
	}
}

func TestRunTmuxCurrentWindowResultFailureSendsNoAgentCommands(t *testing.T) {
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
	if got := strings.Count(strings.Join(*runCalls, "\n"), "send-keys"); got != 2 {
		t.Fatalf("send calls = %v", *runCalls)
	}
}

func TestRunTmuxNewSessionWithResultCapturesExactFirstPaneBeforeSend(t *testing.T) {
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
	if got := strings.Count(strings.Join(*runCalls, "\n"), "send-keys"); got != 1 {
		t.Fatalf("send calls = %v", *runCalls)
	}
}

func TestRunTmuxNewSessionResumeStageReusesExistingLeadSession(t *testing.T) {
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
	if !strings.Contains(joined, "select-pane -t %10 -T amq:issue-473:qa") || !strings.Contains(joined, "send-keys -t %10") {
		t.Fatalf("dependent stage calls = %s", joined)
	}
}

func TestTmuxLaunchResultRejectsNameLikePaneAndWindowTargets(t *testing.T) {
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

func TestPreparedRunGuardRollsBackCurrentWindowBeforeSecondPane(t *testing.T) {
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
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})
	guards := 0
	_, err := runTmuxLaunchPlanWithResult(tmuxLaunchPlan{
		Session: "unused", Workstream: "pinned", Target: "current-window", Layout: "tiled",
		Panes: []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "cto"}, {Role: "qa", CWD: "/repo", Command: "qa"}},
		PreparedRunGuard: func(stage, role string) error {
			guards++
			if stage == "pane creation" && role == "qa" {
				return fmt.Errorf("prepared generation changed")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "generation changed") {
		t.Fatalf("guard error=%v", err)
	}
	joined := strings.Join(*runCalls, "\n")
	if guards != 2 || !strings.Contains(joined, "kill-pane -t %2") || strings.Contains(joined, "send-keys") || nextPane != 2 {
		t.Fatalf("guards=%d next=%d calls=%s", guards, nextPane, joined)
	}
}

func TestPreparedRunGuardRollsBackNewWindowsBeforeSecondMember(t *testing.T) {
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
		default:
			return "", fmt.Errorf("unexpected output command: %s %s", name, call)
		}
	})
	_, err := runTmuxWindowsPlanWithResult(tmuxLaunchPlan{
		Session: "unused", Workstream: "pinned", Target: "new-window", Layout: "tiled",
		Panes: []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "cto"}, {Role: "qa", CWD: "/repo", Command: "qa"}},
		PreparedRunGuard: func(stage, role string) error {
			if stage == "window creation" && role == "qa" {
				return fmt.Errorf("prepared digest changed")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "digest changed") {
		t.Fatalf("guard error=%v", err)
	}
	joined := strings.Join(*runCalls, "\n")
	if !strings.Contains(joined, "kill-window -t %2") || strings.Contains(joined, "send-keys") || nextPane != 2 {
		t.Fatalf("next=%d calls=%s", nextPane, joined)
	}
}

func TestPreparedRunGuardSurfacesPrimaryAndCleanupFailure(t *testing.T) {
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%1")
	oldOutput, oldRun := tmuxOutputCommand, tmuxRunCommand
	t.Cleanup(func() { tmuxOutputCommand, tmuxRunCommand = oldOutput, oldRun })
	tmuxOutputCommand = func(_ string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		if strings.Contains(call, "#{session_name}:#{window_index}") {
			return "operator:0\n", nil
		}
		if len(args) > 0 && args[0] == "split-window" {
			return "%2\n", nil
		}
		return "", fmt.Errorf("unexpected output command %s", call)
	}
	tmuxRunCommand = func(_ string, args ...string) error {
		if len(args) > 0 && args[0] == "kill-pane" {
			return fmt.Errorf("cleanup kill-pane failed")
		}
		return nil
	}
	_, err := runTmuxLaunchPlanWithResult(tmuxLaunchPlan{
		Session: "unused", Workstream: "pinned", Target: "current-window", Layout: "tiled",
		Panes: []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "cto"}, {Role: "qa", CWD: "/repo", Command: "qa"}},
		PreparedRunGuard: func(stage, role string) error {
			if stage == "pane creation" && role == "qa" {
				return fmt.Errorf("primary prepared token failure")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "primary prepared token failure") || !strings.Contains(err.Error(), "cleanup kill-pane failed") {
		t.Fatalf("joined rollback error=%v", err)
	}
}

func TestPreparedRunGuardBarrierRunsForAllRolesBeforeAnySend(t *testing.T) {
	for _, tc := range []struct {
		name       string
		target     string
		windowMode bool
	}{
		{name: "current-window", target: "current-window"},
		{name: "new-window", target: "new-window", windowMode: true},
		{name: "external-remaining-workers", target: "current-window"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
			t.Setenv("TMUX_PANE", "%1")
			nextPane := 1
			calls := stubTmuxResultCommands(t, func(name string, args ...string) (string, error) {
				call := strings.Join(args, " ")
				switch {
				case strings.Contains(call, "#{session_name}:#{window_index}"):
					return "operator:0\n", nil
				case strings.Contains(call, "#{session_name}"):
					return "operator\n", nil
				case len(args) > 0 && (args[0] == "split-window" || args[0] == "new-window"):
					nextPane++
					return fmt.Sprintf("%%%d\n", nextPane), nil
				case strings.Contains(call, "#{window_id}"):
					if tc.windowMode {
						if strings.Contains(call, "-t %2") {
							return "@2\n", nil
						}
						return "@3\n", nil
					}
					return "@1\n", nil
				default:
					return "", fmt.Errorf("unexpected output command: %s %s", name, call)
				}
			})
			roles := []teamLaunchPane{{Role: "cto", CWD: "/repo", Command: "cto"}, {Role: "qa", CWD: "/repo", Command: "qa"}}
			if tc.name == "external-remaining-workers" {
				roles = []teamLaunchPane{{Role: "qa", CWD: "/repo", Command: "qa"}, {Role: "reviewer", CWD: "/repo", Command: "reviewer"}}
			}
			plan := tmuxLaunchPlan{Session: "unused", Workstream: "pinned", Target: tc.target, Layout: "tiled", Panes: roles}
			barriers := 0
			plan.PreparedRunGuard = func(stage, role string) error {
				if stage == "command barrier" {
					barriers++
					if role == roles[1].Role {
						return fmt.Errorf("forced barrier drift")
					}
				}
				return nil
			}
			var err error
			if tc.windowMode {
				_, err = runTmuxWindowsPlanWithResult(plan)
			} else {
				_, err = runTmuxLaunchPlanWithResult(plan)
			}
			if err == nil || !strings.Contains(err.Error(), "forced barrier drift") {
				t.Fatalf("barrier error=%v", err)
			}
			joined := strings.Join(*calls, "\n")
			if barriers != 2 || strings.Contains(joined, "send-keys") {
				t.Fatalf("barriers=%d calls=%s", barriers, joined)
			}
		})
	}
}

func TestCompleteTeamLaunchResultFailsClosed(t *testing.T) {
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
