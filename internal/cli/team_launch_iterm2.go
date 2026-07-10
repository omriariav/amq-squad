package cli

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func init() {
	registerTeamLaunchBackend(iterm2TeamLaunchBackend{})
}

type iterm2TeamLaunchBackend struct{}

type iterm2LaunchPlan struct {
	Workstream string
	Target     string
	Panes      []teamLaunchPane
	StartDelay time.Duration
}

var iterm2RunCommand = runCommand
var iterm2OutputCommand = outputCommand

func (iterm2TeamLaunchBackend) Name() string {
	return "iterm2"
}

func (iterm2TeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("--terminal iterm2 requires macOS")
	}
	if opts.Target != "new-window" {
		return fmt.Errorf("--terminal iterm2 supports --target new-window; got %q", opts.Target)
	}
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	return nil
}

func (b iterm2TeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	printITerm2LaunchPlan(b.buildPlan(t, opts))
	return nil
}

func (b iterm2TeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	return runITerm2LaunchPlan(b.buildPlan(t, opts))
}

func (iterm2TeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) iterm2LaunchPlan {
	return iterm2LaunchPlan{
		Workstream: opts.Workstream,
		Target:     "new-window",
		Panes:      buildTeamLaunchPanes(t, opts),
		StartDelay: opts.Stagger,
	}
}

func printITerm2LaunchPlan(plan iterm2LaunchPlan) {
	fmt.Println("# amq-squad team launch - iTerm2")
	fmt.Printf("# target:  %s\n", plan.Target)
	if plan.Workstream != "" {
		fmt.Printf("# workstream: %s\n", plan.Workstream)
	}
	fmt.Printf("# windows: %d\n\n", len(plan.Panes))
	for _, line := range iterm2DryRunLines(plan) {
		fmt.Println(line)
	}
}

func iterm2DryRunLines(plan iterm2LaunchPlan) []string {
	lines := make([]string, 0, len(plan.Panes)*2)
	for i, pane := range plan.Panes {
		lines = append(lines, shellCommand("osascript", iterm2LaunchArgv(plan.Workstream, pane)...))
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	return lines
}

func runITerm2LaunchPlan(plan iterm2LaunchPlan) error {
	if len(plan.Panes) == 0 {
		return fmt.Errorf("iTerm2 plan has no panes")
	}
	for i, pane := range plan.Panes {
		if err := iterm2RunCommand("osascript", iterm2LaunchArgv(plan.Workstream, pane)...); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	quietNotice("Opened %d iTerm2 window(s) for %s. %s\n", len(plan.Panes), shellQuote(plan.Workstream), runtimecontrol.ITerm2InjectionDisabledReason)
	verbosePolicyEcho()
	return nil
}

func iterm2LaunchArgv(workstream string, pane teamLaunchPane) []string {
	script := `on run argv
set workstreamName to item 1 of argv
set roleName to item 2 of argv
set agentCommand to item 3 of argv
set windowName to "amq:" & workstreamName & ":" & roleName
tell application "iTerm2"
	activate
	set w to (create window with default profile)
	set winID to (id of w as string)
	set tabID to ""
	try
		set tabID to (id of current tab of w as string)
	end try
	set sess to current session of w
	set sessID to ""
	try
		set sessID to (id of sess as string)
	end try
	tell sess
		set name to windowName
		set fullCommand to "(export AMQ_SQUAD_TERMINAL_BACKEND=iterm2 AMQ_SQUAD_TERMINAL_SESSION=" & quoted form of workstreamName & " AMQ_SQUAD_TERMINAL_TARGET=new-window AMQ_SQUAD_TERMINAL_WINDOW_ID=" & quoted form of winID & " AMQ_SQUAD_TERMINAL_WINDOW_NAME=" & quoted form of windowName & " AMQ_SQUAD_TERMINAL_TAB_ID=" & quoted form of tabID & " AMQ_SQUAD_TERMINAL_SESSION_ID=" & quoted form of sessID & "; " & agentCommand & ")"
		write text fullCommand
	end tell
end tell
return winID
end run`
	return []string{"-e", script, workstream, pane.Role, pane.Command}
}

func focusITerm2Window(windowID string) error {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return fmt.Errorf("iTerm2 window id is unavailable")
	}
	out, err := iterm2OutputCommand("osascript", iterm2FocusArgv(windowID)...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "OK" {
		return fmt.Errorf("could not raise iTerm2 window %s; it may have been closed", windowID)
	}
	return nil
}

func iterm2FocusArgv(windowID string) []string {
	script := `on run argv
set targetID to item 1 of argv
tell application "iTerm2"
	activate
	repeat with w in windows
		if (id of w as string) is targetID then
			tell w to select
			return "OK"
		end if
	end repeat
end tell
return "MISS"
end run`
	return []string{"-e", script, windowID}
}
