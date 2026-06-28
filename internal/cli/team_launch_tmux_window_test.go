package cli

import (
	"fmt"
	"strings"
	"testing"
)

func TestTmuxBackendAcceptsNewWindowTarget(t *testing.T) {
	b := tmuxTeamLaunchBackend{}
	for _, tgt := range []string{"current-window", "new-window", "new-session"} {
		if err := b.Validate(teamLaunchOptions{Target: tgt, Layout: "vertical"}); err != nil {
			t.Errorf("target %q should be valid: %v", tgt, err)
		}
	}
	if err := b.Validate(teamLaunchOptions{Target: "bogus", Layout: "vertical"}); err == nil {
		t.Error("an unknown target must be rejected")
	}
}

func TestTmuxDryRunNewWindowOneWindowPerAgent(t *testing.T) {
	plan := tmuxLaunchPlan{
		Session:    "amq-squad-proj",
		Workstream: "issue-96",
		Target:     "new-window",
		Layout:     "vertical",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"},
			{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role qa"},
		},
	}
	joined := strings.Join(tmuxDryRunLines(plan), "\n")

	// Window-per-agent: a new-window per role, and NO pane splitting/layout.
	if strings.Contains(joined, "split-window") {
		t.Errorf("new-window must not split panes:\n%s", joined)
	}
	if strings.Contains(joined, "select-layout") {
		t.Errorf("new-window has no pane-layout step:\n%s", joined)
	}
	if c := strings.Count(joined, "tmux new-window"); c != 2 {
		t.Errorf("expected 2 tmux new-window invocations (one per agent), got %d:\n%s", c, joined)
	}
	if c := strings.Count(joined, "tmux send-keys"); c != 2 {
		t.Errorf("expected one send-keys per agent, got %d:\n%s", c, joined)
	}
	for _, line := range strings.Split(joined, "\n") {
		if strings.Contains(line, "tmux send-keys") && strings.Contains(line, "TMUX_PANE") {
			t.Fatalf("spawn command must target the new agent pane, not the launching/lead pane:\n%s\nfull plan:\n%s", line, joined)
		}
	}
	for _, target := range []string{"$win_0", "$win_1"} {
		if !strings.Contains(joined, "tmux send-keys -t \""+target+"\"") {
			t.Fatalf("new-window plan should send spawn command to %s:\n%s", target, joined)
		}
	}
	// Each agent still gets its deterministic pane-title token (so focus/send
	// resolve identically to the pane backends) and a human window name.
	for _, role := range []string{"cto", "qa"} {
		// Title uses the WORKSTREAM (issue-96), not the terminal session name
		// (amq-squad-proj) — so it matches what the resolver expects.
		token := "amq:issue-96:" + role
		if !strings.Contains(joined, "-T '"+token+"'") && !strings.Contains(joined, "-T "+token) {
			t.Errorf("missing pane title token for %q:\n%s", role, joined)
		}
		if !strings.Contains(joined, "-n '"+role+"'") && !strings.Contains(joined, "-n "+role) {
			t.Errorf("missing window name for %q:\n%s", role, joined)
		}
	}
}

func TestTmuxDryRunCurrentWindowSplitsPaneForEveryAgent(t *testing.T) {
	plan := tmuxLaunchPlan{
		Session:    "amq-squad-proj",
		Workstream: "issue-96",
		Target:     "current-window",
		Layout:     "vertical",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"},
			{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role qa"},
		},
	}
	joined := strings.Join(tmuxDryRunLines(plan), "\n")
	if strings.Contains(joined, "first_pane") {
		t.Fatalf("current-window launch must not reuse the launching pane:\n%s", joined)
	}
	if c := strings.Count(joined, "tmux split-window"); c != 2 {
		t.Fatalf("current-window should split one pane per agent, got %d:\n%s", c, joined)
	}
	for _, target := range []string{"$pane_0", "$pane_1"} {
		if !strings.Contains(joined, target) {
			t.Fatalf("current-window plan missing %s:\n%s", target, joined)
		}
	}
}

func TestTmuxWindowsHostSessionReusesExistingDetachedSession(t *testing.T) {
	t.Setenv("TMUX", "")
	oldExists := tmuxSessionExists
	oldOutput := tmuxOutputCommand
	t.Cleanup(func() {
		tmuxSessionExists = oldExists
		tmuxOutputCommand = oldOutput
	})
	tmuxSessionExists = func(session string) bool {
		return session == "amq-squad-proj"
	}
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		t.Fatalf("existing detached session should not create a new session, got %s %s", name, strings.Join(args, " "))
		return "", nil
	}

	session, firstPaneID, created, err := tmuxWindowsHostSession(tmuxLaunchPlan{
		Session:              "amq-squad-proj",
		Workstream:           "issue-96",
		Target:               "new-window",
		AllowExistingSession: true,
		Panes: []teamLaunchPane{
			{Role: "qa", CWD: "/repo", Command: "true"},
		},
	})
	if err != nil {
		t.Fatalf("tmuxWindowsHostSession: %v", err)
	}
	if session != "amq-squad-proj" || firstPaneID != "" || created {
		t.Fatalf("host = session %q firstPaneID %q created %v, want existing detached session", session, firstPaneID, created)
	}
}

func TestTmuxWindowsHostSessionRejectsExistingDetachedSessionForFreshLaunch(t *testing.T) {
	t.Setenv("TMUX", "")
	oldExists := tmuxSessionExists
	oldOutput := tmuxOutputCommand
	t.Cleanup(func() {
		tmuxSessionExists = oldExists
		tmuxOutputCommand = oldOutput
	})
	tmuxSessionExists = func(session string) bool {
		return session == "amq-squad-proj"
	}
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		t.Fatalf("fresh launch should refuse before running tmux commands, got %s %s", name, strings.Join(args, " "))
		return "", nil
	}

	_, _, _, err := tmuxWindowsHostSession(tmuxLaunchPlan{
		Session:    "amq-squad-proj",
		Workstream: "issue-96",
		Target:     "new-window",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "true"},
			{Role: "qa", CWD: "/repo", Command: "true"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("fresh existing-session error = %v, want collision refusal", err)
	}
}

func TestRunTmuxWindowsPlanAddsWindowsToExistingDetachedSession(t *testing.T) {
	t.Setenv("TMUX", "")
	oldExists := tmuxSessionExists
	oldOutput := tmuxOutputCommand
	oldRun := tmuxRunCommand
	t.Cleanup(func() {
		tmuxSessionExists = oldExists
		tmuxOutputCommand = oldOutput
		tmuxRunCommand = oldRun
	})
	tmuxSessionExists = func(session string) bool {
		return session == "amq-squad-proj"
	}
	var outputCalls []string
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		call := name + " " + strings.Join(args, " ")
		outputCalls = append(outputCalls, call)
		if len(args) > 0 && args[0] == "new-session" {
			return "", fmt.Errorf("unexpected new-session against existing detached session")
		}
		if len(args) > 0 && args[0] == "new-window" {
			return fmt.Sprintf("%%%d\n", len(outputCalls)), nil
		}
		return "", fmt.Errorf("unexpected tmux output command: %s", call)
	}
	var runCalls []string
	tmuxRunCommand = func(name string, args ...string) error {
		runCalls = append(runCalls, name+" "+strings.Join(args, " "))
		return nil
	}

	_, stderr, err := captureOutput(t, func() error {
		return runTmuxWindowsPlan(tmuxLaunchPlan{
			Session:              "amq-squad-proj",
			Workstream:           "issue-96",
			Target:               "new-window",
			AllowExistingSession: true,
			Panes: []teamLaunchPane{
				{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role qa"},
				{Role: "reviewer", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role reviewer"},
			},
		})
	})
	if err != nil {
		t.Fatalf("runTmuxWindowsPlan: %v", err)
	}
	joinedOutput := strings.Join(outputCalls, "\n")
	if strings.Contains(joinedOutput, "new-session") {
		t.Fatalf("existing detached session must not create a new session:\n%s", joinedOutput)
	}
	if got := strings.Count(joinedOutput, "new-window"); got != 2 {
		t.Fatalf("expected one new-window per pane, got %d:\n%s", got, joinedOutput)
	}
	if !strings.Contains(joinedOutput, "-t amq-squad-proj:") {
		t.Fatalf("new windows should target existing detached session:\n%s", joinedOutput)
	}
	joinedRun := strings.Join(runCalls, "\n")
	for _, want := range []string{"select-pane -t %1 -T amq:issue-96:qa", "select-pane -t %2 -T amq:issue-96:reviewer", "send-keys -t %1", "send-keys -t %2"} {
		if !strings.Contains(joinedRun, want) {
			t.Fatalf("missing run call %q in:\n%s", want, joinedRun)
		}
	}
	if !strings.Contains(stderr, "existing tmux session amq-squad-proj") {
		t.Fatalf("operator notice should identify existing detached session, got:\n%s", stderr)
	}
}

func TestTmuxWindowName(t *testing.T) {
	if got := tmuxWindowName("cto"); got != "cto" {
		t.Errorf("tmuxWindowName(cto) = %q, want cto", got)
	}
	if got := tmuxWindowName(""); got != "agent" {
		t.Errorf("empty role should fall back to %q, got %q", "agent", got)
	}
}
