package cli

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalAppLaunchArgvShape(t *testing.T) {
	pane := teamLaunchPane{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"}
	argv := terminalAppLaunchArgv("issue-332", pane)
	if len(argv) != 5 || argv[0] != "-e" || argv[2] != "issue-332" || argv[3] != "cto" || argv[4] != pane.Command {
		t.Fatalf("argv = %#v", argv)
	}
	script := argv[1]
	for _, want := range []string{
		`tell application "Terminal"`,
		`set targetTab to do script ""`,
		`set custom title of targetTab to windowName`,
		`set targetWindow to window of targetTab`,
		`set targetWindow to front window`,
		`set winID to (id of targetWindow as string)`,
		`set tabIndex to (index of targetTab as string)`,
		`set ttyName to (tty of targetTab as string)`,
		`set fullCommand to "env AMQ_SQUAD_TERMINAL_BACKEND=terminal_app`,
		` /bin/sh -c " & quoted form of agentCommand`,
		`AMQ_SQUAD_TERMINAL_BACKEND=terminal_app`,
		`AMQ_SQUAD_TERMINAL_TARGET=new-window`,
		`AMQ_SQUAD_TERMINAL_WINDOW_ID=`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME=`,
		`AMQ_SQUAD_TERMINAL_TAB_ID=`,
		`AMQ_SQUAD_TERMINAL_TTY=`,
		`do script fullCommand in targetTab`,
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

func TestTerminalAppDryRunLines(t *testing.T) {
	plan := terminalAppLaunchPlan{
		Workstream: "issue-332",
		Target:     "new-window",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"},
			{Role: "qa", CWD: "/repo", Command: "cd /repo && amq-squad agent up claude --role qa"},
		},
		StartDelay: time.Second,
	}
	lines := terminalAppDryRunLines(plan)
	joined := strings.Join(lines, "\n")
	if got := strings.Count(joined, "osascript -e"); got != 2 {
		t.Fatalf("dry run should emit one osascript per agent, got %d:\n%s", got, joined)
	}
	if !strings.Contains(joined, "sleep 1") {
		t.Fatalf("dry run missing stagger sleep:\n%s", joined)
	}
	if strings.Contains(joined, "tmux send-keys") {
		t.Fatalf("Terminal.app dry run must not use tmux send-keys:\n%s", joined)
	}
}

func TestTerminalAppBackendRegistered(t *testing.T) {
	if _, ok := teamLaunchBackends["terminal"]; !ok {
		t.Fatal("terminal backend not registered")
	}
	got := strings.Join(registeredTeamLaunchTerminals(), ",")
	if !strings.Contains(got, "terminal") {
		t.Fatalf("registeredTeamLaunchTerminals = %q, want terminal included", got)
	}
}
