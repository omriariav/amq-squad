package cli

import (
	"strings"
	"testing"
	"time"
)

func TestITerm2LaunchArgvShape(t *testing.T) {
	pane := teamLaunchPane{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"}
	argv := iterm2LaunchArgv("issue-331", pane)
	if len(argv) != 5 || argv[0] != "-e" || argv[2] != "issue-331" || argv[3] != "cto" || argv[4] != pane.Command {
		t.Fatalf("argv = %#v", argv)
	}
	script := argv[1]
	for _, want := range []string{
		`tell application "iTerm2"`,
		`create window with default profile`,
		`set winID to (id of w as string)`,
		`set fullCommand to "env AMQ_SQUAD_TERMINAL_BACKEND=iterm2`,
		` /bin/sh -c " & quoted form of agentCommand`,
		`AMQ_SQUAD_TERMINAL_BACKEND=iterm2`,
		`AMQ_SQUAD_TERMINAL_TARGET=new-window`,
		`AMQ_SQUAD_TERMINAL_WINDOW_ID=`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME=`,
		`AMQ_SQUAD_TERMINAL_TAB_ID=`,
		`AMQ_SQUAD_TERMINAL_SESSION_ID=`,
		`write text fullCommand`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	for _, unwanted := range []string{`(export AMQ_SQUAD_TERMINAL_BACKEND`, `; " & agentCommand`} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("script contains shell-specific launch fragment %q:\n%s", unwanted, script)
		}
	}
}

func TestITerm2DryRunLines(t *testing.T) {
	plan := iterm2LaunchPlan{
		Workstream: "issue-331",
		Target:     "new-window",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"},
			{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up claude --role qa"},
		},
		StartDelay: time.Second,
	}
	lines := iterm2DryRunLines(plan)
	joined := strings.Join(lines, "\n")
	if got := strings.Count(joined, "osascript -e"); got != 2 {
		t.Fatalf("dry run should emit one osascript per agent, got %d:\n%s", got, joined)
	}
	if !strings.Contains(joined, "sleep 1") {
		t.Fatalf("dry run missing stagger sleep:\n%s", joined)
	}
	if strings.Contains(joined, "tmux send-keys") {
		t.Fatalf("iTerm2 dry run must not use tmux send-keys:\n%s", joined)
	}
}

func TestITerm2FocusArgvShape(t *testing.T) {
	argv := iterm2FocusArgv("101")
	if len(argv) != 3 || argv[0] != "-e" || argv[2] != "101" {
		t.Fatalf("focus argv = %#v", argv)
	}
	for _, want := range []string{`tell application "iTerm2"`, `targetID`, `tell w to select`, `return "OK"`, `return "MISS"`} {
		if !strings.Contains(argv[1], want) {
			t.Fatalf("focus script missing %q:\n%s", want, argv[1])
		}
	}
}

func TestITerm2BackendRegistered(t *testing.T) {
	if _, ok := teamLaunchBackends["iterm2"]; !ok {
		t.Fatal("iterm2 backend not registered")
	}
	got := strings.Join(registeredTeamLaunchTerminals(), ",")
	if !strings.Contains(got, "iterm2") {
		t.Fatalf("registeredTeamLaunchTerminals = %q, want iterm2 included", got)
	}
}
