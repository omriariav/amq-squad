package cli

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// Bootstrap-failure surfacing (#540).
//
// The launch that produced #540 spawned three panes, printed
//
//	Added 3 team pane(s) to current tmux window. started v2-25-0 using profile squad
//
// and exited as if it had succeeded. All three agents had in fact died on their
// first command, each leaving an `error: ...` line and a shell prompt in its
// pane. The only signal the operator got was a later, unrelated-looking
// "lead role \"cto\" did not become ready within 45s".
//
// The launcher can read those panes, so it must: an agent that died at bootstrap
// is a launch failure, and the pane's own error text is the diagnostic. This
// file turns pane content into a member-attributed failure.

// paneErrorLineRE matches the error line an amq-squad or agent bootstrap failure
// leaves on screen. The set is deliberately tight -- an anchored `error:` /
// `fatal:` / `panic:` at the start of a line -- so ordinary agent output that
// merely discusses an error does not read as a dead pane.
var paneErrorLineRE = regexp.MustCompile(`(?im)^\s*(error|fatal|panic):\s*(.+)$`)

// bootstrapFailureTailLines is how much of each pane to inspect. A bootstrap
// error is the last thing printed before the shell prompt, so the tail is where
// it is.
//
// PRECONDITION: reading a fixed-size tail assumes the pane contains ONLY output
// from this launch. That holds today because every agent pane is created within
// the launch operation itself, and the session-reuse branches do NOT weaken it:
// AllowExistingSession reuses the SESSION, not a pane, so reuseExistingSession
// skips the first-target block and every agent still gets a fresh split/window.
// The only pane ever reused for an agent is a session-initial pane created
// microseconds earlier in the same operation.
//
// If an agent is ever placed into a PRE-EXISTING pane, this probe needs a
// scrollback fence: mark the pane before dispatch and read only past the mark.
// Without one it could attribute a stale anchored error line from unrelated
// earlier output to this launch and fail a healthy spawn.
const bootstrapFailureTailLines = 12

// Probe pacing. The budget bounds only the INCONCLUSIVE case: a healthy launch
// stops as soon as every engine is seen running, and a dead one stops as soon as
// the first pane is confirmed dead, so neither normally pays the full budget.
//
// Known cost, accepted: a MIXED roster, where some panes report their engine and
// others cannot be inspected at all, never reaches either early-return condition
// and so pays the whole budget before failing open. It is bounded, it only
// delays a launch rather than failing it, and the alternative (failing open as
// soon as any pane is uninspectable) would give up the signal for the panes that
// are readable.
var (
	paneBootstrapProbeBudget   = 6 * time.Second
	paneBootstrapProbeInterval = 250 * time.Millisecond
)

// paneBootstrapFailure returns the error text a pane is showing, and whether one
// was found.
//
// It is deliberately conservative in the "no failure" direction: a capture error
// (pane already gone, tmux unavailable) reports no failure, because inventing a
// launch failure from a failed capture would break launches that are fine. The
// cost of a miss is the pre-existing behavior; the cost of a false positive is a
// working launch reported as broken.
func paneBootstrapFailure(paneID string) (string, bool) {
	out, err := paneTailForBootstrap(paneID, bootstrapFailureTailLines)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	matches := paneErrorLineRE.FindAllStringSubmatch(out, -1)
	if len(matches) == 0 {
		return "", false
	}
	// Report the LAST error line: when a failure cascades, the final line is the
	// one that actually stopped the process.
	last := matches[len(matches)-1]
	text := strings.TrimSpace(last[2])
	if text == "" {
		return "", false
	}
	return strings.TrimSpace(last[1]) + ": " + text, true
}

// paneTailForBootstrap is a seam so tests can supply pane content without tmux.
// Like paneBootstrapProbe it uses tmuxOutputCommand, so faked launches never
// shell out to the real tmux.
var paneTailForBootstrap = func(paneID string, n int) (string, error) {
	if n <= 0 {
		n = bootstrapFailureTailLines
	}
	return tmuxOutputCommand("tmux", "capture-pane", "-p", "-t", paneID, "-S", "-"+strconv.Itoa(n))
}

// memberPaneBootstrapFailure names one member's pane failure.
type memberPaneBootstrapFailure struct {
	Role   string
	PaneID string
	Error  string
}

// bootstrapFailureError renders member-attributed pane failures as one launch
// error. It returns nil when no pane failed, so callers can use it directly as
// the launch verdict.
func bootstrapFailureError(failures []memberPaneBootstrapFailure) error {
	if len(failures) == 0 {
		return nil
	}
	parts := make([]string, 0, len(failures))
	for _, f := range failures {
		parts = append(parts, fmt.Sprintf("%s (pane %s): %s", f.Role, f.PaneID, f.Error))
	}
	noun := "agent"
	if len(failures) > 1 {
		noun = "agents"
	}
	return fmt.Errorf("%d spawned %s died at bootstrap: %s", len(failures), noun, strings.Join(parts, "; "))
}

// bootstrapFailureRecoveryHint is the documented non-destructive recovery path
// after a bootstrap failure (#540 acceptance criterion 5). Run-owned panes are
// already cleaned up by launch rollback, and simple start reconciles whatever
// durable state remains. Keep this printed command paired with the end-to-end
// rerun coverage in v228_contract_crash_injection_test.go (AC2): if the command
// changes, that recovery test must exercise the new text's behavior.
const bootstrapFailureRecoveryHint = "recover non-destructively by rerunning: amq-squad start <session> --project <project> --profile <profile>   (no namespace reset is required)"

// printBootstrapFailureRecovery tells the operator how to get out of the state.
// It goes to stderr unconditionally rather than through quietNotice: a launch
// that failed must state its recovery path even under --quiet.
func printBootstrapFailureRecovery() {
	fmt.Fprintf(os.Stderr, "%s\n", bootstrapFailureRecoveryHint)
}

// describePaneBootstrapFailures renders pane failures for attribution inside
// another error, or "" when there are none. Used to make the goal-delivery
// timeout name the pane's error instead of only reporting a timeout.
func describePaneBootstrapFailures(failures []memberPaneBootstrapFailure) string {
	if err := bootstrapFailureError(failures); err != nil {
		return err.Error()
	}
	return ""
}

// Liveness-confirmed bootstrap detection.
//
// An `error:` line alone is not proof the agent is dead: an agent can print a
// recoverable error and keep running. The decisive signal is the one #540
// recorded from the field -- the pane exists, is correctly titled, and
// pane_current_command is the SHELL, so the engine binary is not running. That
// plus the pane's error text is a bootstrap death.

// paneBootstrapProbe is the seam for reading a pane's current command. It
// returns the command, whether the pane could be inspected at all, and is
// replaced in tests.
//
// It goes through tmuxOutputCommand, the same seam the launch path uses, so a
// test that fakes tmux for the launch also controls this probe. Reading tmux
// directly here would bypass those fakes and make every faked launch pay a real
// tmux round trip.
var paneBootstrapProbe = func(paneID string) (string, bool) {
	paneID = strings.TrimSpace(paneID)
	if paneID == "" {
		return "", false
	}
	// Ask for the pane id alongside the command and VERIFY it. `tmux
	// display-message -t <id>` does not error on a missing pane: it silently
	// resolves to the client's current pane and prints that one's fields (the
	// #156 false positive). Unverified, a closed agent pane would report the
	// LAUNCHER's shell as its command and read as a dead agent.
	out, err := tmuxOutputCommand("tmux", "display-message", "-p", "-t", paneID, "#{pane_id}\t#{pane_current_command}")
	if err != nil {
		return "", false
	}
	fields := strings.SplitN(strings.TrimSpace(out), "\t", 2)
	if len(fields) != 2 || strings.TrimSpace(fields[0]) != paneID {
		return "", false
	}
	return strings.TrimSpace(fields[1]), true
}

// paneShellCommands are the interactive shells a pane falls back to when its
// agent has exited. A bootstrap death is "the SHELL is the current command",
// not merely "the engine is not the current command".
//
// The distinction is the difference between a correct probe and one that kills
// healthy launches. Between `send-keys` and the engine being exec'd, the pane's
// current command passes through intermediate executables -- the wrapper, a
// shell function, amq-squad itself. If shell init happens to print an anchored
// error line in that window (say "error: plugin cache unavailable"), treating
// any non-engine command as death would fail a launch that is starting normally.
// An unrecognized command is therefore INCONCLUSIVE: keep waiting.
var paneShellCommands = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "fish": true,
	"dash": true, "ksh": true, "csh": true, "tcsh": true,
	"ash": true, "busybox": true, "login": true,
}

// paneIsAtShell reports whether a pane's current command is a known interactive
// shell, i.e. the agent that was launched there is no longer running.
func paneIsAtShell(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	if i := strings.LastIndexByte(command, '/'); i >= 0 {
		command = command[i+1:]
	}
	// tmux reports a login shell as "-zsh".
	command = strings.TrimPrefix(command, "-")
	return paneShellCommands[command]
}

// bootstrapProbe describes one pane to check.
type bootstrapProbe struct {
	Role   string
	PaneID string
	Engine string
}

// paneBootstrapProbesForResult builds probes from a completed launch result,
// taking each role's expected engine from the team profile. Used by the
// goal-delivery wait, which knows the launch result and the namespace but not
// the original pane plan.
//
// A profile that cannot be read yields no probes: attribution is a diagnostic
// improvement and must never itself become a failure path.
func paneBootstrapProbesForResult(project, profile string, result teamLaunchResult) []bootstrapProbe {
	if len(result.Panes) == 0 {
		return nil
	}
	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		return nil
	}
	engines := make(map[string]string, len(tm.Members))
	for _, m := range tm.Members {
		engines[m.Role] = normalizedAgentBinary(m.Binary)
	}
	probes := make([]bootstrapProbe, 0, len(result.Panes))
	for _, pane := range result.Panes {
		engine := strings.TrimSpace(engines[pane.Role])
		if engine == "" || strings.TrimSpace(pane.PaneID) == "" {
			continue
		}
		probes = append(probes, bootstrapProbe{Role: pane.Role, PaneID: pane.PaneID, Engine: engine})
	}
	return probes
}

// tmuxBootstrapProbes pairs each planned pane with the tmux pane id it was
// created as. A pane with no known engine is skipped rather than guessed at,
// since without an expected command there is no liveness signal to compare.
func tmuxBootstrapProbes(panes []teamLaunchPane, paneIDs []string) []bootstrapProbe {
	probes := make([]bootstrapProbe, 0, len(panes))
	for i, pane := range panes {
		if i >= len(paneIDs) {
			break
		}
		engine := strings.TrimSpace(pane.Engine)
		if engine == "" {
			continue
		}
		probes = append(probes, bootstrapProbe{Role: pane.Role, PaneID: paneIDs[i], Engine: engine})
	}
	return probes
}

// livePaneIDs reports the set of pane ids tmux currently knows about, and
// whether tmux could be asked at all.
//
// This exists because #540's per-pane probes cannot tell "this pane is gone"
// from "I could not look" (#598). `display-message -t <missing>` silently
// resolves to the client's current pane, so the per-pane probe deliberately
// treats every inspection error as inconclusive. Asking tmux to enumerate every
// pane it has answers the question the per-pane probe structurally cannot: a
// successful enumeration that omits a pane is positive proof of absence, while
// a failed enumeration stays inconclusive exactly as before.
var livePaneIDs = func() (map[string]bool, bool) {
	out, err := tmuxOutputCommand("tmux", "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		return nil, false
	}
	ids := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids[id] = true
		}
	}
	return ids, true
}

var (
	runStartLeadReadyNow   = time.Now
	runStartLeadReadySleep = time.Sleep
)

// vanishedPaneBootstrapError is the failure text for an agent whose pane no
// longer exists. There is no pane content to quote, so the message says what is
// known and why nothing more specific is available, instead of implying the
// diagnostic was retrievable and merely omitted.
const vanishedPaneBootstrapError = "pane no longer exists: the agent exited and its pane closed before bootstrap completed, taking its error output with it (run with a pane that stays on exit to capture it, e.g. tmux set-option -g remain-on-exit on)"

// waitForPaneBootstrap polls the spawned panes until every agent is confirmed
// running or one is confirmed dead, and returns the members that died.
//
// It exits EARLY in both directions, so the happy path costs one or two polls
// rather than the whole budget: as soon as every pane reports its engine as the
// current command, the launch is healthy and polling stops. A pane whose current
// command is the shell AND which shows an error line is reported dead
// immediately.
//
// Exhausting the budget without a verdict returns no failures. That is
// deliberate: an inconclusive probe must fall back to the pre-existing behavior
// rather than fail a launch that may be fine.
func waitForPaneBootstrap(probes []bootstrapProbe) []memberPaneBootstrapFailure {
	if len(probes) == 0 {
		return nil
	}
	deadline := runStartLeadReadyNow().Add(paneBootstrapProbeBudget)
	for {
		var failures []memberPaneBootstrapFailure
		running := 0
		inspectable := 0
		// Resolved lazily and at most once per poll, and only when some pane
		// fails to inspect: a healthy launch never asks tmux for the pane list
		// at all.
		var present map[string]bool
		var presentKnown, presentAsked bool
		for _, p := range probes {
			command, ok := paneBootstrapProbe(p.PaneID)
			if !ok {
				// #598: every caller of this function has already proved this
				// pane existed, via verifyPaneProcessLaunched at dispatch. So
				// an uninspectable pane is not automatically inconclusive; if
				// tmux can still enumerate its panes and this one is absent,
				// the agent died and took its pane with it. That is the case
				// #540's detector structurally could not see, and it is the one
				// that let a bricked launch print "Added N team pane(s)" and
				// exit 0.
				if !presentAsked {
					present, presentKnown = livePaneIDs()
					presentAsked = true
				}
				if presentKnown && !present[p.PaneID] {
					inspectable++
					failures = append(failures, memberPaneBootstrapFailure{Role: p.Role, PaneID: p.PaneID, Error: vanishedPaneBootstrapError})
				}
				continue
			}
			inspectable++
			if tmuxpane.CommandMatchesEngine(command, p.Engine) {
				running++
				continue
			}
			// Not the engine. Only a KNOWN SHELL means the agent is gone; any
			// other command is an intermediate executable in the startup window
			// and is inconclusive, so it keeps waiting rather than being killed
			// on the strength of an error line printed during init.
			if !paneIsAtShell(command) {
				continue
			}
			// At the shell. Require the pane to also show an error before calling
			// it a death, so a shell that simply has not exec'd yet keeps waiting.
			if text, failed := paneBootstrapFailure(p.PaneID); failed {
				failures = append(failures, memberPaneBootstrapFailure{Role: p.Role, PaneID: p.PaneID, Error: text})
			}
		}
		if len(failures) > 0 {
			return failures
		}
		if running == len(probes) {
			return nil
		}
		// No pane could be inspected at all: tmux is unavailable, sandboxed, or
		// stubbed out. There is no liveness signal to wait for, so waiting would
		// buy nothing and cost the whole budget on every launch. Return at once.
		if inspectable == 0 {
			return nil
		}
		now := runStartLeadReadyNow()
		if !now.Before(deadline) {
			return nil
		}
		sleepFor := paneBootstrapProbeInterval
		if remaining := deadline.Sub(now); sleepFor > remaining {
			sleepFor = remaining
		}
		runStartLeadReadySleep(sleepFor)
	}
}
