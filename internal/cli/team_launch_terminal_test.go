package cli

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalAppLaunchArgvShape(t *testing.T) {
	pane := teamLaunchPane{Role: "cto", CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"}
	argv := terminalAppLaunchArgv("issue-332", pane)
	if len(argv) != 4 || argv[0] != "-e" || argv[2] != "amq:issue-332:cto" {
		t.Fatalf("argv = %#v", argv)
	}
	script := argv[1]
	for _, want := range []string{
		`tell application "Terminal"`,
		`set targetTab to do script ""`,
		`set custom title of targetTab to windowName`,
		`set tabIndex to (index of targetTab as string)`,
		`set ttyName to (tty of targetTab as string)`,
		`set targetWindow to missing value`,
		`repeat with candidateWindow in windows`,
		`repeat with candidateTab in tabs of candidateWindow`,
		`if (tty of candidateTab as string) is ttyName then`,
		`set targetWindow to candidateWindow`,
		`if targetWindow is missing value then`,
		`set targetWindow to front window`,
		`set winID to (id of targetWindow as string)`,
		`set payloadTemplate to item 2 of argv`,
		`set payload to my replaceText(payloadTemplate, "__AMQ_SQUAD_TERMINAL_WINDOW_ID__", my shellSingleQuote(winID))`,
		`set payload to my replaceText(payload, "__AMQ_SQUAD_TERMINAL_TAB_ID__", my shellSingleQuote(tabIndex))`,
		`set payload to my replaceText(payload, "__AMQ_SQUAD_TERMINAL_TTY__", my shellSingleQuote(ttyName))`,
		`set fullCommand to "/bin/sh -c " & quoted form of payload`,
		`on shellSingleQuote(valueText)`,
		`do script fullCommand in targetTab`,
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
	if strings.Contains(script, `window of targetTab`) {
		t.Fatalf("script must not use unsupported Terminal.app tab->window property:\n%s", script)
	}
	payload := argv[3]
	for _, want := range []string{
		`export AMQ_SQUAD_TERMINAL_BACKEND='terminal_app'`,
		`AMQ_SQUAD_TERMINAL_SESSION='issue-332'`,
		`AMQ_SQUAD_TERMINAL_TARGET='new-window'`,
		`AMQ_SQUAD_TERMINAL_WINDOW_ID=__AMQ_SQUAD_TERMINAL_WINDOW_ID__`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME='amq:issue-332:cto'`,
		`AMQ_SQUAD_TERMINAL_TAB_ID=__AMQ_SQUAD_TERMINAL_TAB_ID__`,
		`AMQ_SQUAD_TERMINAL_TTY=__AMQ_SQUAD_TERMINAL_TTY__`,
		`; ` + pane.Command,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
}

func TestTerminalAppLaunchPayloadQuotesHostileValues(t *testing.T) {
	workstream := `issue 332 'x $(touch /tmp/amq-pwn)`
	pane := teamLaunchPane{Role: `cto '$(nope)`, CWD: "/repo", Command: "cd /repo && amq-squad agent up codex --role cto"}
	argv := terminalAppLaunchArgv(workstream, pane)
	payload := argv[3]
	for _, want := range []string{
		`AMQ_SQUAD_TERMINAL_SESSION='issue 332 '\''x $(touch /tmp/amq-pwn)'`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME='amq:issue 332 '\''x $(touch /tmp/amq-pwn):cto '\''$(nope)'`,
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("hostile value was not inert in payload; missing %q:\n%s", want, payload)
		}
	}
	for _, unwanted := range []string{
		`AMQ_SQUAD_TERMINAL_SESSION=issue 332`,
		`AMQ_SQUAD_TERMINAL_WINDOW_NAME=amq:issue 332`,
		`" & workstreamName & "`,
		`" & windowName & "`,
	} {
		if strings.Contains(payload, unwanted) || strings.Contains(argv[1], unwanted) {
			t.Fatalf("hostile value has naked shell/script splice %q\nscript:\n%s\npayload:\n%s", unwanted, argv[1], payload)
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
