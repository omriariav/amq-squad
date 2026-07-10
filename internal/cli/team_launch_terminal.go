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
	script := `on run argv
set workstreamName to item 1 of argv
set roleName to item 2 of argv
set agentCommand to item 3 of argv
set windowName to "amq:" & workstreamName & ":" & roleName
tell application "Terminal"
	activate
	set targetTab to do script ""
	set custom title of targetTab to windowName
	try
		set targetWindow to window of targetTab
	on error
		set targetWindow to front window
	end try
	set winID to ""
	try
		set winID to (id of targetWindow as string)
	end try
	set tabIndex to ""
	try
		set tabIndex to (index of targetTab as string)
	end try
	set ttyName to ""
	try
		set ttyName to (tty of targetTab as string)
	end try
	set fullCommand to "env AMQ_SQUAD_TERMINAL_BACKEND=terminal_app AMQ_SQUAD_TERMINAL_SESSION=" & workstreamName & " AMQ_SQUAD_TERMINAL_TARGET=new-window AMQ_SQUAD_TERMINAL_WINDOW_ID=" & winID & " AMQ_SQUAD_TERMINAL_WINDOW_NAME=" & windowName & " AMQ_SQUAD_TERMINAL_TAB_ID=" & tabIndex & " AMQ_SQUAD_TERMINAL_TTY=" & ttyName & " /bin/sh -c " & quoted form of agentCommand
	do script fullCommand in targetTab
end tell
return winID
end run`
	return []string{"-e", script, workstream, pane.Role, pane.Command}
}
