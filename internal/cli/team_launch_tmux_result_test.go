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
