package cli

import (
	"fmt"
	"runtime"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func init() {
	registerTeamLaunchBackend(terminalAppTeamLaunchBackend{})
}

type terminalAppTeamLaunchBackend struct{}

type terminalAppLaunchPlan struct {
	Workstream string
	Target     string
	Panes      []teamLaunchPane
	StartDelay time.Duration
}

func (terminalAppTeamLaunchBackend) Name() string {
	return "terminal"
}

func (terminalAppTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("--terminal terminal requires macOS")
	}
	if opts.Target != "new-window" {
		return fmt.Errorf("--terminal terminal supports --target new-window; got %q", opts.Target)
	}
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	return nil
}

func (b terminalAppTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	printTerminalAppLaunchPlan(b.buildPlan(t, opts))
	return nil
}

func (b terminalAppTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	return runTerminalAppLaunchPlan(b.buildPlan(t, opts))
}

func (terminalAppTeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) terminalAppLaunchPlan {
	return terminalAppLaunchPlan{
		Workstream: opts.Workstream,
		Target:     "new-window",
		Panes:      buildTeamLaunchPanes(t, opts),
		StartDelay: opts.Stagger,
	}
}

func printTerminalAppLaunchPlan(plan terminalAppLaunchPlan) {
	fmt.Println("# amq-squad team launch - Terminal.app")
	fmt.Printf("# target:  %s\n", plan.Target)
	if plan.Workstream != "" {
		fmt.Printf("# workstream: %s\n", plan.Workstream)
	}
	fmt.Printf("# windows: %d\n\n", len(plan.Panes))
	for _, line := range terminalAppDryRunLines(plan) {
		fmt.Println(line)
	}
}

func terminalAppDryRunLines(plan terminalAppLaunchPlan) []string {
	lines := make([]string, 0, len(plan.Panes)*2)
	for i, pane := range plan.Panes {
		lines = append(lines, shellCommand("osascript", terminalAppLaunchArgv(plan.Workstream, pane)...))
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	return lines
}

func runTerminalAppLaunchPlan(plan terminalAppLaunchPlan) error {
	if len(plan.Panes) == 0 {
		return fmt.Errorf("Terminal.app plan has no panes")
	}
	for i, pane := range plan.Panes {
		if err := runCommand("osascript", terminalAppLaunchArgv(plan.Workstream, pane)...); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	quietNotice("Opened %d Terminal.app window(s) for %s. %s\n", len(plan.Panes), shellQuote(plan.Workstream), runtimecontrol.TerminalAppInjectionDisabledReason)
	verbosePolicyEcho()
	return nil
}

func terminalAppLaunchArgv(workstream string, pane teamLaunchPane) []string {
	windowName := nativeTerminalWindowName(workstream, pane.Role)
	payload := nativeTerminalLaunchPayload(pane.Command, []nativeTerminalEnv{
		{Key: envTerminalBackend, Value: "terminal_app"},
		{Key: envTerminalSession, Value: workstream},
		{Key: envTerminalTarget, Value: "new-window"},
		{Key: envTerminalWindowID, Value: nativeTerminalWindowIDPlaceholder, Raw: true},
		{Key: envTerminalWindowName, Value: windowName},
		{Key: envTerminalTabID, Value: nativeTerminalTabIDPlaceholder, Raw: true},
		{Key: envTerminalTTY, Value: nativeTerminalTTYPlaceholder, Raw: true},
	})
	script := `on run argv
set windowName to item 1 of argv
set payloadTemplate to item 2 of argv
tell application "Terminal"
	activate
	set targetTab to do script ""
	set custom title of targetTab to windowName
	set tabIndex to ""
	try
		set tabIndex to (index of targetTab as string)
	end try
	set ttyName to ""
	try
		set ttyName to (tty of targetTab as string)
	end try
	set targetWindow to missing value
	if ttyName is not "" then
		repeat with candidateWindow in windows
			repeat with candidateTab in tabs of candidateWindow
				try
					if (tty of candidateTab as string) is ttyName then
						set targetWindow to candidateWindow
						exit repeat
					end if
				end try
			end repeat
			if targetWindow is not missing value then
				exit repeat
			end if
		end repeat
	end if
	if targetWindow is missing value then
		set targetWindow to front window
	end if
	set winID to ""
	try
		set winID to (id of targetWindow as string)
	end try
	set payload to my replaceText(payloadTemplate, "__AMQ_SQUAD_TERMINAL_WINDOW_ID__", my shellSingleQuote(winID))
	set payload to my replaceText(payload, "__AMQ_SQUAD_TERMINAL_TAB_ID__", my shellSingleQuote(tabIndex))
	set payload to my replaceText(payload, "__AMQ_SQUAD_TERMINAL_TTY__", my shellSingleQuote(ttyName))
	set fullCommand to "/bin/sh -c " & quoted form of payload
	do script fullCommand in targetTab
end tell
return winID
end run

on replaceText(sourceText, searchText, replacementText)
	set oldDelimiters to AppleScript's text item delimiters
	set AppleScript's text item delimiters to searchText
	set textItems to text items of sourceText
	set AppleScript's text item delimiters to replacementText
	set replacedText to textItems as text
	set AppleScript's text item delimiters to oldDelimiters
	return replacedText
end replaceText

on shellSingleQuote(valueText)
	set valueText to valueText as string
	set singleQuote to ASCII character 39
	set spliceQuote to singleQuote & (ASCII character 92) & singleQuote & singleQuote
	return singleQuote & my replaceText(valueText, singleQuote, spliceQuote) & singleQuote
end shellSingleQuote`
	return []string{"-e", script, windowName, payload}
}
