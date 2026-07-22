package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func init() {
	registerTeamLaunchBackend(tmuxTeamLaunchBackend{})
}

type tmuxTeamLaunchBackend struct{}

type tmuxLaunchPlan struct {
	Session               string
	Workstream            string
	Target                string
	Layout                string
	Panes                 []teamLaunchPane
	StartDelay            time.Duration
	AllowExistingSession  bool
	PreparedRunGuard      func(stage, role string) error
	PreserveLauncherFocus bool
}

type tmuxClient struct {
	Name        string
	TTY         string
	ControlMode bool
	Flags       string
	Session     string
}

func (tmuxTeamLaunchBackend) Name() string {
	return "tmux"
}

func (tmuxTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if opts.Target != "new-session" && opts.Target != "current-window" && opts.Target != "new-window" {
		return fmt.Errorf("unsupported tmux target %q: use current-window, new-window, or new-session", opts.Target)
	}
	if opts.Layout != "vertical" && opts.Layout != "horizontal" && opts.Layout != "tiled" {
		return fmt.Errorf("unsupported tmux layout %q: use vertical, horizontal, or tiled", opts.Layout)
	}
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	return nil
}

func (b tmuxTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	printTmuxLaunchPlan(b.buildPlan(t, opts))
	return nil
}

func (b tmuxTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	plan := b.buildPlan(t, opts)
	controlClients := tmuxControlModeClients()
	if len(controlClients) > 0 {
		warnTmuxControlModeClients(controlClients)
	}
	_, err := runTmuxLaunchPlanInternal(plan, false)
	return err
}

func (b tmuxTeamLaunchBackend) LaunchWithResult(t team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	plan := b.buildPlan(t, opts)
	controlClients := tmuxControlModeClients()
	if len(controlClients) > 0 {
		warnTmuxControlModeClients(controlClients)
	}
	return runTmuxLaunchPlanWithResult(plan)
}

func (tmuxTeamLaunchBackend) preparedResultBeforeDispatch() {}

func (tmuxTeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) tmuxLaunchPlan {
	if opts.TerminalSession == "" {
		opts.TerminalSession = defaultTmuxSessionName(t.Project)
	}
	return buildTmuxLaunchPlan(t, opts)
}

func buildTmuxLaunchPlan(t team.Team, opts teamLaunchOptions) tmuxLaunchPlan {
	return tmuxLaunchPlan{
		Session:               opts.TerminalSession,
		Workstream:            opts.Workstream,
		Target:                opts.Target,
		Layout:                opts.Layout,
		Panes:                 buildTeamLaunchPanes(t, opts),
		StartDelay:            opts.Stagger,
		PreparedRunGuard:      opts.PreparedRunGuard,
		PreserveLauncherFocus: opts.PreserveLauncherFocus,
	}
}

func guardTmuxPreparedRun(plan tmuxLaunchPlan, stage, role string) error {
	if plan.PreparedRunGuard == nil {
		return nil
	}
	return plan.PreparedRunGuard(stage, role)
}

func setTmuxPreparedPaneMetadata(plan tmuxLaunchPlan, paneID, role string) error {
	if plan.PreserveLauncherFocus {
		return tmuxRunCommand("tmux", "set-option", "-p", "-t", paneID, "@amq_squad_title", paneTitleToken(plan.Workstream, role))
	}
	return tmuxRunCommand("tmux", "select-pane", "-t", paneID, "-T", paneTitleToken(plan.Workstream, role))
}

func rollbackTmuxPreparedPanes(createdSession string, paneIDs []string) error {
	if createdSession != "" {
		return tmuxRunCommand("tmux", "kill-session", "-t", createdSession)
	}
	var cleanup []error
	for i := len(paneIDs) - 1; i >= 0; i-- {
		if err := tmuxRunCommand("tmux", "kill-pane", "-t", paneIDs[i]); err != nil {
			cleanup = append(cleanup, err)
		}
	}
	return errors.Join(cleanup...)
}

func rollbackTmuxPreparedWindows(createdSession string, paneIDs []string) error {
	if createdSession != "" {
		return tmuxRunCommand("tmux", "kill-session", "-t", createdSession)
	}
	var cleanup []error
	for i := len(paneIDs) - 1; i >= 0; i-- {
		if err := tmuxRunCommand("tmux", "kill-window", "-t", paneIDs[i]); err != nil {
			cleanup = append(cleanup, err)
		}
	}
	return errors.Join(cleanup...)
}

func defaultTmuxSessionName(projectDir string) string {
	base := filepath.Base(projectDir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "project"
	}
	return "amq-squad-" + sanitizeTmuxSessionName(base)
}

func sanitizeTmuxSessionName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "project"
	}
	return out
}

func printTmuxLaunchPlan(plan tmuxLaunchPlan) {
	fmt.Println("# amq-squad team launch - tmux")
	fmt.Printf("# target:  %s\n", plan.Target)
	fmt.Printf("# layout:  %s\n", plan.Layout)
	if plan.Workstream != "" {
		fmt.Printf("# workstream: %s\n", plan.Workstream)
	}
	fmt.Printf("# session: %s\n", plan.Session)
	fmt.Printf("# panes:   %d\n\n", len(plan.Panes))
	for _, line := range tmuxDryRunLines(plan) {
		fmt.Println(line)
	}
}

func tmuxDryRunLines(plan tmuxLaunchPlan) []string {
	if len(plan.Panes) == 0 {
		return nil
	}
	if plan.Target == "new-window" {
		return tmuxWindowsDryRunLines(plan)
	}
	windowTarget := plan.Session + ":0"
	firstTarget := plan.Session + ":0.0"
	lines := []string{}
	if plan.Target == "current-window" {
		windowTarget = "$window"
		firstTarget = "$first_pane"
		lines = append(lines,
			`window=$(tmux display-message -p -t "${TMUX_PANE:?run amq-squad up from inside a tmux pane}" '#{session_name}:#{window_index}')`,
		)
	} else {
		lines = append(lines, shellCommand("tmux", "new-session", "-d", "-s", plan.Session, "-n", "squad", "-c", plan.Panes[0].CWD))
	}
	targets := []string{firstTarget}
	lines = append(lines, tmuxSelectPaneDryRunLine(firstTarget, paneTitleToken(plan.Workstream, plan.Panes[0].Role)))
	start := 1
	if plan.Target == "current-window" {
		targets = nil
		lines = lines[:len(lines)-1]
		start = 0
	}
	for i, pane := range plan.Panes[start:] {
		paneVar := fmt.Sprintf("pane_%d", i+start)
		targets = append(targets, "$"+paneVar)
		lines = append(lines,
			tmuxSplitDryRunLine(paneVar, windowTarget, pane.CWD, plan.Layout),
			tmuxSelectPaneDryRunLine("$"+paneVar, paneTitleToken(plan.Workstream, pane.Role)),
		)
	}
	if len(targets) > 1 {
		lines = append(lines, tmuxSelectLayoutDryRunLine(windowTarget, plan.Layout))
	}
	for i, pane := range plan.Panes {
		lines = append(lines, tmuxSendKeysDryRunLine(targets[i], pane.Command))
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	if plan.Target == "current-window" {
		lines = append(lines, "# using current tmux window; no attach needed")
	} else {
		lines = append(lines, "# attach later with: "+shellCommand("tmux", "attach-session", "-t", plan.Session))
	}
	return lines
}

// tmuxWindowsDryRunLines previews the window-per-agent launch: each agent goes
// into its own tmux window (in the current session when run from inside tmux,
// otherwise a new session). Control still targets exact pane ids.
func tmuxWindowsDryRunLines(plan tmuxLaunchPlan) []string {
	// This preview shows the inside-tmux path (add a window per agent to the
	// current session) — the common way to use --target new-window. Launched
	// LIVE outside tmux, the backend instead creates a new detached
	// '<session>' session whose first window hosts the first agent; that path
	// isn't copy-pasteable as a one-liner, so the `:?` guard fails loudly here
	// rather than emitting a command against a session that doesn't exist.
	lines := []string{
		"# one tmux window per agent, added to your current tmux session",
		"# (launched live outside tmux, a new detached '" + plan.Session + "' session is created instead)",
		`session=$(tmux display-message -p -t "${TMUX_PANE:?run from inside tmux, or launch live with: amq-squad up --target new-window}" '#{session_name}')`,
	}
	targets := make([]string, 0, len(plan.Panes))
	for i, pane := range plan.Panes {
		v := fmt.Sprintf("win_%d", i)
		targets = append(targets, "$"+v)
		lines = append(lines,
			v+`=$(tmux new-window -d -P -F '#{pane_id}' -t "$session:" -n `+shellQuote(tmuxWindowName(pane.Role))+" -c "+shellQuote(pane.CWD)+")",
			tmuxSelectPaneDryRunLine("$"+v, paneTitleToken(plan.Workstream, pane.Role)),
		)
	}
	for i, pane := range plan.Panes {
		lines = append(lines, tmuxSendKeysDryRunLine(targets[i], pane.Command))
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	if plan.Workstream != "" {
		// Switch to an agent by pane id, not window name (names can collide):
		// amq-squad focus resolves the exact pane.
		lines = append(lines, "# focus an agent with: "+shellCommand("amq-squad", "focus", "--session", plan.Workstream, "--role", "<role>"))
	}
	return lines
}

func tmuxSplitDryRunLine(varName, target, cwd, layout string) string {
	return varName + "=$(tmux split-window -P -F '#{pane_id}' -t " + shellTarget(target) + " " + tmuxSplitDirection(layout) + " -c " + shellQuote(cwd) + ")"
}

func tmuxSelectLayoutDryRunLine(target, layout string) string {
	return "tmux select-layout -t " + shellTarget(target) + " " + tmuxSelectLayout(layout)
}

func shellTarget(target string) string {
	if strings.HasPrefix(target, "$") {
		return `"` + target + `"`
	}
	return shellQuote(target)
}

func sleepDryRunLine(d time.Duration) string {
	seconds := fmt.Sprintf("%.3f", d.Seconds())
	seconds = strings.TrimRight(strings.TrimRight(seconds, "0"), ".")
	if seconds == "" {
		seconds = "0"
	}
	return "sleep " + seconds
}

func tmuxSelectPaneDryRunLine(target, title string) string {
	return "tmux select-pane -t " + shellTarget(target) + " -T " + shellQuote(title)
}

// paneTitleToken builds the deterministic, machine-parseable pane title that the
// name-first tmux pane resolution matches: "amq:<workstream>:<role>". The
// session component is the AMQ WORKSTREAM (not the tmux/terminal session name),
// because that is the agent identity every resolver caller (status, focus, send)
// keys off — stamping the terminal-session name here would make the title never
// match the resolver's expectedPaneToken and break name-first/exclusion. The
// role is unique per agent within a workstream, so two agents that share a repo
// AND an engine (cpo·codex + cto·codex in one dir) still get distinct titles.
// MUST stay in lockstep with expectedPaneToken in internal/tmuxpane/tmux.go,
// which is called with the workstream.
func paneTitleToken(workstream, role string) string {
	return "amq:" + workstream + ":" + role
}

func tmuxSendKeysDryRunLine(target, command string) string {
	return "tmux send-keys -t " + shellTarget(target) + " " + shellQuote(command) + " C-m"
}

func runTmuxLaunchPlan(plan tmuxLaunchPlan) error {
	_, err := runTmuxLaunchPlanInternal(plan, false)
	return err
}

func runTmuxLaunchPlanWithResult(plan tmuxLaunchPlan) (teamLaunchResult, error) {
	return runTmuxLaunchPlanInternal(plan, true)
}

func runTmuxLaunchPlanInternal(plan tmuxLaunchPlan, collectResult bool) (teamLaunchResult, error) {
	if len(plan.Panes) == 0 {
		return teamLaunchResult{}, fmt.Errorf("tmux plan has no panes")
	}
	if plan.PreparedRunGuard != nil && !collectResult {
		return teamLaunchResult{}, fmt.Errorf("pinned prepared tmux launch requires exact result collection")
	}
	if plan.Target == "new-window" {
		return runTmuxWindowsPlanInternal(plan, collectResult)
	}
	windowTarget := plan.Session + ":0"
	firstTarget := plan.Session + ":0.0"
	reuseExistingSession := false
	createdSession := ""
	createdTargets := []string{}
	failCreated := func(cause error) (teamLaunchResult, error) {
		return teamLaunchResult{}, errors.Join(cause, rollbackTmuxPreparedPanes(createdSession, createdTargets))
	}
	switch plan.Target {
	case "current-window":
		paneID := strings.TrimSpace(os.Getenv("TMUX_PANE"))
		if os.Getenv("TMUX") == "" || paneID == "" {
			return teamLaunchResult{}, fmt.Errorf("--target current-window requires running inside tmux (TMUX_PANE unset); attach a tmux session and launch from one of its panes, or use --target new-session")
		}
		// Anchor on the launching shell's own pane ($TMUX_PANE), never tmux's
		// focused pane. `tmux display-message -p` without -t reports whichever
		// window is active *now*, so under iTerm2 -CC a focus change mid-launch
		// rehomes the panes onto an unrelated tab (#40). Resolving the window
		// from TMUX_PANE with -t makes targeting deterministic regardless of focus.
		var err error
		windowTarget, err = tmuxOutputCommand("tmux", "display-message", "-p", "-t", paneID, "#{session_name}:#{window_index}")
		if err != nil {
			return teamLaunchResult{}, err
		}
		windowTarget = strings.TrimSpace(windowTarget)
		firstTarget = paneID
	case "new-session":
		if tmuxSessionExists(plan.Session) && plan.AllowExistingSession {
			windowTarget = plan.Session + ":0"
			reuseExistingSession = true
			break
		}
		if err := tmuxEnsureSessionAbsent(plan.Session); err != nil {
			return teamLaunchResult{}, err
		}
		if err := guardTmuxPreparedRun(plan, "pane creation", plan.Panes[0].Role); err != nil {
			return teamLaunchResult{}, err
		}
		if collectResult {
			paneID, err := tmuxOutputCommand("tmux", "new-session", "-d", "-P", "-F", "#{pane_id}", "-s", plan.Session, "-n", "squad", "-c", plan.Panes[0].CWD)
			if err != nil {
				return teamLaunchResult{}, err
			}
			createdSession = plan.Session
			firstTarget = strings.TrimSpace(paneID)
			if _, err := exactTmuxPaneID(firstTarget); err != nil {
				return failCreated(err)
			}
		} else if err := tmuxRunCommand("tmux", "new-session", "-d", "-s", plan.Session, "-n", "squad", "-c", plan.Panes[0].CWD); err != nil {
			return teamLaunchResult{}, err
		}
		createdSession = plan.Session
	default:
		return teamLaunchResult{}, fmt.Errorf("unsupported tmux target %q", plan.Target)
	}
	targets := []string{}
	panesToSplit := plan.Panes
	if plan.Target != "current-window" && !reuseExistingSession {
		if err := tmuxRunCommand("tmux", "select-pane", "-t", firstTarget, "-T", paneTitleToken(plan.Workstream, plan.Panes[0].Role)); err != nil {
			return failCreated(err)
		}
		targets = append(targets, firstTarget)
		panesToSplit = plan.Panes[1:]
	}
	for _, pane := range panesToSplit {
		if err := guardTmuxPreparedRun(plan, "pane creation", pane.Role); err != nil {
			return failCreated(err)
		}
		splitArgs := []string{"split-window"}
		if plan.PreserveLauncherFocus {
			splitArgs = append(splitArgs, "-d")
		}
		splitArgs = append(splitArgs, "-P", "-F", "#{pane_id}", "-t", windowTarget, tmuxSplitDirection(plan.Layout), "-c", pane.CWD)
		paneID, err := tmuxOutputCommand("tmux", splitArgs...)
		if err != nil {
			return failCreated(err)
		}
		paneID = strings.TrimSpace(paneID)
		if paneID == "" {
			return failCreated(fmt.Errorf("tmux split-window returned empty pane id"))
		}
		targets = append(targets, paneID)
		if createdSession == "" {
			createdTargets = append(createdTargets, paneID)
		}
		if err := guardTmuxPreparedRun(plan, "pane creation postcondition", pane.Role); err != nil {
			return failCreated(err)
		}
		if err := guardTmuxPreparedRun(plan, "pane metadata", pane.Role); err != nil {
			return failCreated(err)
		}
		if err := setTmuxPreparedPaneMetadata(plan, paneID, pane.Role); err != nil {
			return failCreated(err)
		}
	}
	if len(targets) != len(plan.Panes) {
		return failCreated(fmt.Errorf("tmux launch created %d pane target(s), want %d", len(targets), len(plan.Panes)))
	}
	if len(targets) > 1 {
		if err := tmuxRunCommand("tmux", "select-layout", "-t", windowTarget, tmuxSelectLayout(plan.Layout)); err != nil {
			return failCreated(err)
		}
	}
	var launchResult teamLaunchResult
	if collectResult {
		for _, pane := range plan.Panes {
			if err := guardTmuxPreparedRun(plan, "result collection", pane.Role); err != nil {
				return failCreated(err)
			}
		}
		var resultErr error
		launchResult, resultErr = tmuxLaunchResult(plan.Panes, targets)
		if resultErr != nil {
			return failCreated(resultErr)
		}
		if resultErr := validateCompleteTeamLaunchResult(plan.Panes, plan.Target, launchResult); resultErr != nil {
			return failCreated(resultErr)
		}
	}
	for _, pane := range plan.Panes {
		if err := guardTmuxPreparedRun(plan, "command barrier", pane.Role); err != nil {
			return failCreated(err)
		}
	}
	for i, pane := range plan.Panes {
		if err := tmuxRunCommand("tmux", "send-keys", "-t", targets[i], withTmuxTargetEnv(plan.Target, pane.Command), "C-m"); err != nil {
			return failCreated(err)
		}
		if err := guardTmuxPreparedRun(plan, "command dispatch postcondition", pane.Role); err != nil {
			return failCreated(err)
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	if plan.Target == "current-window" {
		quietNotice("Added %d team pane(s) to current tmux window.\n", len(targets))
		verbosePolicyEcho()
		if collectResult {
			return launchResult, nil
		}
		return teamLaunchResult{}, nil
	}
	if reuseExistingSession {
		quietNotice("Added %d team pane(s) to existing tmux session %s. Attach with: tmux attach -t %s\n", len(targets), plan.Session, shellQuote(plan.Session))
	} else {
		quietNotice("Created tmux session %s. Attach with: tmux attach -t %s\n", plan.Session, shellQuote(plan.Session))
	}
	verbosePolicyEcho()
	if collectResult {
		return launchResult, nil
	}
	return teamLaunchResult{}, nil
}

// runTmuxWindowsPlan launches one tmux WINDOW per agent (Sagi-style window-per-
// agent), so each agent gets a full-size terminal instead of a cramped split
// pane. The host session is the current tmux session when launched from inside
// tmux, otherwise a new detached session whose first window hosts the first
// agent. Each agent's pane carries the same amq pane-title token and is driven
// by send-keys exactly like the pane backends, so the runtime metadata/control
// layer (pane-id capture, status, focus, send) works unchanged.
func runTmuxWindowsPlan(plan tmuxLaunchPlan) error {
	_, err := runTmuxWindowsPlanInternal(plan, false)
	return err
}

func runTmuxWindowsPlanWithResult(plan tmuxLaunchPlan) (teamLaunchResult, error) {
	return runTmuxWindowsPlanInternal(plan, true)
}

func runTmuxWindowsPlanInternal(plan tmuxLaunchPlan, collectResult bool) (teamLaunchResult, error) {
	if plan.PreparedRunGuard != nil && !collectResult {
		return teamLaunchResult{}, fmt.Errorf("pinned prepared tmux window launch requires exact result collection")
	}
	if err := guardTmuxPreparedRun(plan, "window creation", plan.Panes[0].Role); err != nil {
		return teamLaunchResult{}, err
	}
	session, firstPaneID, createdSession, err := tmuxWindowsHostSession(plan)
	if err != nil {
		return teamLaunchResult{}, err
	}
	targets := make([]string, 0, len(plan.Panes))
	ownedSession := ""
	if createdSession {
		ownedSession = session
	}
	failCreated := func(cause error) (teamLaunchResult, error) {
		return teamLaunchResult{}, errors.Join(cause, rollbackTmuxPreparedWindows(ownedSession, targets))
	}
	for i, pane := range plan.Panes {
		if i > 0 {
			if err := guardTmuxPreparedRun(plan, "window creation", pane.Role); err != nil {
				return failCreated(err)
			}
		}
		paneID := ""
		if i == 0 && firstPaneID != "" {
			// First agent reuses the window the new session was created with.
			paneID = firstPaneID
		} else {
			out, werr := tmuxOutputCommand("tmux", "new-window", "-d", "-P", "-F", "#{pane_id}",
				"-t", session+":", "-n", tmuxWindowName(pane.Role), "-c", pane.CWD)
			if werr != nil {
				return failCreated(werr)
			}
			paneID = strings.TrimSpace(out)
		}
		if paneID == "" {
			return failCreated(fmt.Errorf("tmux returned an empty pane id for window %q", pane.Role))
		}
		targets = append(targets, paneID)
		if err := guardTmuxPreparedRun(plan, "window creation postcondition", pane.Role); err != nil {
			return failCreated(err)
		}
		if err := guardTmuxPreparedRun(plan, "pane metadata", pane.Role); err != nil {
			return failCreated(err)
		}
		if err := setTmuxPreparedPaneMetadata(plan, paneID, pane.Role); err != nil {
			return failCreated(err)
		}
	}
	var launchResult teamLaunchResult
	if collectResult {
		for _, pane := range plan.Panes {
			if err := guardTmuxPreparedRun(plan, "result collection", pane.Role); err != nil {
				return failCreated(err)
			}
		}
		launchResult, err = tmuxLaunchResult(plan.Panes, targets)
		if err != nil {
			return failCreated(err)
		}
		if err := validateCompleteTeamLaunchResult(plan.Panes, plan.Target, launchResult); err != nil {
			return failCreated(err)
		}
	}
	for _, pane := range plan.Panes {
		if err := guardTmuxPreparedRun(plan, "command barrier", pane.Role); err != nil {
			return failCreated(err)
		}
	}
	for i, pane := range plan.Panes {
		if err := tmuxRunCommand("tmux", "send-keys", "-t", targets[i], withTmuxTargetEnv("new-window", pane.Command), "C-m"); err != nil {
			return failCreated(err)
		}
		if err := guardTmuxPreparedRun(plan, "command dispatch postcondition", pane.Role); err != nil {
			return failCreated(err)
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	if createdSession {
		quietNotice("Created tmux session %s with one window per agent. Attach with: tmux attach -t %s\n", plan.Session, shellQuote(plan.Session))
	} else if os.Getenv("TMUX") == "" {
		quietNotice("Added %d agent window(s) to existing tmux session %s. Attach with: tmux attach -t %s\n", len(plan.Panes), session, shellQuote(session))
	} else {
		quietNotice("Added %d agent window(s) to the current tmux session.\n", len(plan.Panes))
	}
	verbosePolicyEcho()
	if collectResult {
		return launchResult, nil
	}
	return teamLaunchResult{}, nil
}

func tmuxLaunchResult(panes []teamLaunchPane, paneIDs []string) (teamLaunchResult, error) {
	if len(panes) != len(paneIDs) {
		return teamLaunchResult{}, fmt.Errorf("tmux result has %d pane id(s), want %d", len(paneIDs), len(panes))
	}
	result := teamLaunchResult{Panes: make([]teamLaunchResultPane, 0, len(panes))}
	for i, pane := range panes {
		paneID, err := exactTmuxPaneID(paneIDs[i])
		if err != nil {
			return teamLaunchResult{}, fmt.Errorf("tmux launch result for role %s: %w", pane.Role, err)
		}
		windowID, err := tmuxOutputCommand("tmux", "display-message", "-p", "-t", paneID, "#{window_id}")
		if err != nil {
			return teamLaunchResult{}, err
		}
		windowID, err = exactTmuxWindowID(windowID)
		if err != nil {
			return teamLaunchResult{}, fmt.Errorf("tmux launch result for role %s: %w", pane.Role, err)
		}
		result.Panes = append(result.Panes, teamLaunchResultPane{Role: pane.Role, PaneID: paneID, WindowID: windowID})
	}
	return result, nil
}

// tmuxWindowsHostSession resolves the session to add agent windows to. Inside
// tmux it is the current session (firstPaneID empty: every agent gets a fresh
// window). Outside tmux it creates a new detached session whose initial window
// hosts the first agent. Existing detached sessions are reused only for
// explicit resume/repair plans; fresh up/launch keeps the session-collision
// guard so it cannot inject a full roster into another project's session.
func tmuxWindowsHostSession(plan tmuxLaunchPlan) (session, firstPaneID string, createdSession bool, err error) {
	if len(plan.Panes) == 0 {
		return "", "", false, fmt.Errorf("tmux new-window plan has no panes")
	}
	if os.Getenv("TMUX") != "" {
		pane := strings.TrimSpace(os.Getenv("TMUX_PANE"))
		if pane == "" {
			return "", "", false, fmt.Errorf("--target new-window inside tmux requires TMUX_PANE; launch from a tmux pane, or use --target new-session")
		}
		s, derr := tmuxOutputCommand("tmux", "display-message", "-p", "-t", pane, "#{session_name}")
		if derr != nil {
			return "", "", false, derr
		}
		return strings.TrimSpace(s), "", false, nil
	}
	if tmuxSessionExists(plan.Session) && plan.AllowExistingSession {
		return plan.Session, "", false, nil
	}
	if err := tmuxEnsureSessionAbsent(plan.Session); err != nil {
		return "", "", false, err
	}
	out, err := tmuxOutputCommand("tmux", "new-session", "-d", "-P", "-F", "#{pane_id}",
		"-s", plan.Session, "-n", tmuxWindowName(plan.Panes[0].Role), "-c", plan.Panes[0].CWD)
	if err != nil {
		return "", "", false, err
	}
	return plan.Session, strings.TrimSpace(out), true, nil
}

// tmuxWindowName is the human-facing window label for an agent (the role).
// Window names are labels only — control always targets the exact pane id — so
// duplicates across the user's session are harmless.
func tmuxWindowName(role string) string {
	if strings.TrimSpace(role) == "" {
		return "agent"
	}
	return sanitizeTmuxSessionName(role)
}

func tmuxSplitDirection(layout string) string {
	if layout == "horizontal" {
		return "-v"
	}
	return "-h"
}

func tmuxSelectLayout(layout string) string {
	switch layout {
	case "horizontal":
		return "even-vertical"
	case "vertical":
		return "even-horizontal"
	default:
		return "tiled"
	}
}

func tmuxControlModeClients() []tmuxClient {
	out, err := tmuxOutputCommand("tmux", "list-clients", "-F", "#{client_name}\t#{client_tty}\t#{client_control_mode}\t#{client_flags}\t#{client_session}")
	if err != nil {
		return nil
	}
	return parseTmuxClients(out)
}

func parseTmuxClients(out string) []tmuxClient {
	var clients []tmuxClient
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		name, tty, controlField, flags, session := "", "", "", "", ""
		if len(parts) == 5 {
			name, tty, controlField, flags, session = parts[0], parts[1], parts[2], parts[3], parts[4]
		} else if len(parts) == 3 {
			// Compatibility with the original three-column warning parser.
			tty, controlField = parts[0], parts[1]
			name = tty
			flags = parts[2]
		} else {
			continue
		}
		name, tty, controlField, flags, session = strings.TrimSpace(name), strings.TrimSpace(tty), strings.TrimSpace(controlField), strings.TrimSpace(flags), strings.TrimSpace(session)
		control := controlField == "1" || strings.Contains(flags, "control-mode")
		if !control {
			continue
		}
		clients = append(clients, tmuxClient{
			Name:        name,
			TTY:         tty,
			ControlMode: true,
			Flags:       flags,
			Session:     session,
		})
	}
	return clients
}

func warnTmuxControlModeClients(clients []tmuxClient) {
	fmt.Fprintf(os.Stderr, "warning: detected %d tmux control-mode client(s); iTerm2 tmux -CC large output bursts can trigger pause-after without blocking launch. If input stalls, use the confirmation-gated control_continue status action; iTerm2 'Unpause Automatically' can prevent persistent pauses (use --verbose for details).\n", len(clients))
	if !outputPolicyCurrent().Verbose {
		return
	}
	for _, c := range clients {
		name := c.Name
		if name == "" {
			name = c.TTY
		}
		fmt.Fprintf(os.Stderr, "verbose: control client %s flags: %s session=%s\n", name, c.Flags, c.Session)
	}
	fmt.Fprintln(os.Stderr, "verbose: starting panes with a stagger to reduce the initial output burst.")
	fmt.Fprintln(os.Stderr, "verbose: bounded retries cover transient read failures only; they do not clear a persistent control-client pause.")
	fmt.Fprintln(os.Stderr, "verbose: status exposes control_continue only when one exact control client and one verified managed pane are bound to the same session.")
}

func tmuxEnsureSessionAbsent(session string) error {
	if tmuxSessionExists(session) {
		return fmt.Errorf("tmux session %q already exists. Attach with 'tmux attach -t %s' or choose --terminal-session", session, session)
	}
	return nil
}

var tmuxSessionExists = func(session string) bool {
	return exec.Command("tmux", "has-session", "-t", session).Run() == nil
}

var tmuxRunCommand = runCommand
var tmuxOutputCommand = outputCommand

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("%s: %w: %s", shellCommand(name, args...), err, msg)
		}
		return fmt.Errorf("%s: %w", shellCommand(name, args...), err)
	}
	return nil
}

func outputCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%s: %w: %s", shellCommand(name, args...), err, msg)
		}
		return "", fmt.Errorf("%s: %w", shellCommand(name, args...), err)
	}
	return string(out), nil
}
