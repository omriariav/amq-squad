package cli

import (
	"strings"
	"testing"
	"time"
)

func TestITerm2LaunchArgvShape(t *testing.T) {
	pane := teamLaunchPane{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"}
	argv := iterm2LaunchArgv("issue-331", pane)
	if len(argv) != 4 || argv[0] != "-e" || argv[2] != "amq:issue-331:cto" {
		t.Fatalf("argv = %#v", argv)
	}
	script := argv[1]
	for _, want := range []string{
		`tell application "iTerm2"`,
		`create window with default profile`,
		`set winID to (id of w as string)`,
		`set payloadTemplate to item 2 of argv`,
		`set payload to my replaceText(payloadTemplate, "__AMQ_SQUAD_TERMINAL_WINDOW_ID__", my shellSingleQuote(winID))`,
		`set payload to my replaceText(payload, "__AMQ_SQUAD_TERMINAL_TAB_ID__", my shellSingleQuote(tabID))`,
		`set payload to my replaceText(payload, "__AMQ_SQUAD_TERMINAL_SESSION_ID__", my shellSingleQuote(sessID))`,
		`set ttyName to (tty of sess as string)`,
		`set payload to my replaceText(payload, "__AMQ_SQUAD_TERMINAL_TTY__", my shellSingleQuote(ttyName))`,
		`set fullCommand to "/bin/sh -c " & quoted form of payload`,
		`on shellSingleQuote(valueText)`,
		`write text fullCommand`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
	if got := strings.Count(script, "quoted form of"); got != 1 {
		t.Fatalf("script should apply AppleScript quoted form only at the receiving-shell boundary, got %d:\n%s", got, script)
	}
	for _, unwanted := range []string{`(export AMQ_SQUAD_TERMINAL_BACKEND`, `agentCommand`, `workstreamName`, `roleName`, `AMQ_SQUAD_TERMINAL_SESSION=" &`} {
		if strings.Contains(script, unwanted) {
			t.Fatalf("script contains shell-specific launch fragment %q:\n%s", unwanted, script)
		}
	}
	payload := argv[3]
	for _, want := range []string{
		`export AMQ_SQUAD_TERMINAL_BACKEND='iterm2'`,
		`AMQ_SQUAD_TERMINAL_SESSION='issue-331'`,
		`AMQ_SQUAD_TERMINAL_TARGET='new-window'`,
		`AMQ_SQUAD_TERMINAL_WINDOW_ID=__AMQ_SQUAD_TERMINAL_WINDOW_ID__`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME='amq:issue-331:cto'`,
		`AMQ_SQUAD_TERMINAL_TAB_ID=__AMQ_SQUAD_TERMINAL_TAB_ID__`,
		`AMQ_SQUAD_TERMINAL_SESSION_ID=__AMQ_SQUAD_TERMINAL_SESSION_ID__`,
		`AMQ_SQUAD_TERMINAL_TTY=__AMQ_SQUAD_TERMINAL_TTY__`,
		`; ` + pane.Command,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
}

func TestITerm2LaunchPayloadQuotesHostileValues(t *testing.T) {
	workstream := `issue 331 'x $(touch /tmp/amq-pwn)`
	pane := teamLaunchPane{Role: `cto '$(nope)`, CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"}
	argv := iterm2LaunchArgv(workstream, pane)
	payload := argv[3]
	for _, want := range []string{
		`AMQ_SQUAD_TERMINAL_SESSION='issue 331 '\''x $(touch /tmp/amq-pwn)'`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME='amq:issue 331 '\''x $(touch /tmp/amq-pwn):cto '\''$(nope)'`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("hostile value was not inert in payload; missing %q:\n%s", want, payload)
		}
	}
	for _, unwanted := range []string{
		`AMQ_SQUAD_TERMINAL_SESSION=issue 331`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME=amq:issue 331`,
		`" & workstreamName & "`,
		`" & windowName & "`,
	} {
		if strings.Contains(payload, unwanted) || strings.Contains(argv[1], unwanted) {
			t.Fatalf("hostile value has naked shell/script splice %q\nscript:\n%s\npayload:\n%s", unwanted, argv[1], payload)
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
