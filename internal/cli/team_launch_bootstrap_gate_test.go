package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// #540: a dead agent must fail the launch on EVERY tmux target, not just the
// one the issue was reported from.
//
// The gate lives in two places -- runTmuxLaunchPlanInternal for split panes and
// runTmuxWindowsPlanInternal for window-per-agent -- because each prints its own
// success notice. I originally wired only the panes path and caught the gap by
// reading my own diff, not from a failing test, so this pins both. That failure
// mode (two lookalike paths that must enforce one invariant, only one of them
// checked) is the same shape as #539's readiness-vs-spawn split.

// fakeTmuxLaunch answers the minimum tmux surface a launch plan needs, and
// records the pane ids it created. Because paneBootstrapProbe and
// paneTailForBootstrap also go through tmuxOutputCommand, this single fake drives
// the bootstrap probe too -- no extra stubbing.
type fakeTmuxLaunch struct {
	// engine is what pane_current_command reports for every created pane.
	// A shell here means the agent is not running.
	engine string
	// screen is the pane content the probe will read.
	screen string

	created  []string
	killed   []string
	nextID   int
	dispatch int
}

func (f *fakeTmuxLaunch) install(t *testing.T) {
	t.Helper()
	oldOutput := tmuxOutputCommand
	oldRun := tmuxRunCommand
	oldBudget := paneBootstrapProbeBudget
	oldInterval := paneBootstrapProbeInterval
	t.Cleanup(func() {
		tmuxOutputCommand = oldOutput
		tmuxRunCommand = oldRun
		paneBootstrapProbeBudget = oldBudget
		paneBootstrapProbeInterval = oldInterval
	})
	// Bound the inconclusive case tightly; a dead pane is decided on the first
	// poll, so this never gates the assertions.
	paneBootstrapProbeBudget = 50 * time.Millisecond
	paneBootstrapProbeInterval = 5 * time.Millisecond

	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		call := strings.Join(args, " ")
		switch {
		// The bootstrap probe: echo the requested pane id back with the engine,
		// so the probe's pane-id verification passes and it reads a real command.
		case strings.Contains(call, "#{pane_id}\t#{pane_current_command}"):
			requested := args[len(args)-2]
			return requested + "\t" + f.engine + "\n", nil
		case args[0] == "capture-pane":
			return f.screen, nil
		case args[0] == "split-window", args[0] == "new-window", args[0] == "new-session":
			f.nextID++
			id := fmt.Sprintf("%%%d", 100+f.nextID)
			f.created = append(f.created, id)
			return id + "\n", nil
		case strings.Contains(call, "#{pane_pid}"), strings.Contains(call, "#{pane_dead}"):
			// #571 reads the pane ROOT pid; #577 also checks identity and pane_dead.
			return fakePaneIdentityReply(args), nil
		case strings.Contains(call, "#{session_name}:#{window_index}"):
			return "harness:0\n", nil
		case strings.Contains(call, "#{session_name}"):
			return "harness\n", nil
		case strings.Contains(call, "#{window_id}"):
			return "@200\n", nil
		default:
			return "", fmt.Errorf("unexpected tmux output command: %s", call)
		}
	}
	tmuxRunCommand = func(name string, args ...string) error {
		switch args[0] {
		case "kill-pane", "kill-window":
			f.killed = append(f.killed, args[len(args)-1])
		case "respawn-pane":
			// #571: delivery is respawn-pane now, not send-keys, because typing into a
			// pane loses anything over MAX_CANON. Counting the old verb would have made
			// this assertion vacuous rather than failing.
			f.dispatch++
		}
		return nil
	}
}

func bootstrapGatePlan(target string) tmuxLaunchPlan {
	return tmuxLaunchPlan{
		Session:    "harness",
		Workstream: "v2-25-0",
		Target:     target,
		Layout:     "vertical",
		Panes: []teamLaunchPane{
			{Role: "cto", CWD: "/tmp", Command: "true", Engine: "claude"},
			{Role: "amq-dev-1", CWD: "/tmp", Command: "true", Engine: "claude"},
		},
	}
}

// A dead agent must fail the launch loudly and name the member, on both targets.
func TestBootstrapDeathFailsLaunchOnEveryTmuxTarget(t *testing.T) {
	stubExactPaneInspection(t)
	const bootErr = "error: load accepted prepared launch identity: prepared launch record identity drift: project: accepted=\".\" current=\"/repo\""
	for _, target := range []string{"current-window", "new-window"} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("TMUX", "/tmp/harness,1,0")
			t.Setenv("TMUX_PANE", "%1")
			// The #540 field state: panes exist, the shell is the current
			// command, and the pane holds the bootstrap error.
			fake := &fakeTmuxLaunch{engine: "zsh", screen: bootErr + "\n"}
			fake.install(t)

			_, err := runTmuxLaunchPlanInternal(bootstrapGatePlan(target), false)
			if err == nil {
				t.Fatalf("target %s reported a successful launch while its agents were dead at bootstrap", target)
			}
			msg := err.Error()
			if !strings.Contains(msg, "died at bootstrap") {
				t.Fatalf("target %s failed for the wrong reason: %v", target, err)
			}
			// Member attribution and the pane's own error text.
			for _, want := range []string{"cto", "load accepted prepared launch identity"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("target %s failure %q is missing %q", target, msg, want)
				}
			}
			// The agent commands were dispatched before the gate ran, so this is
			// the gate rejecting a real launch attempt rather than a plan that
			// never started.
			if fake.dispatch != len(bootstrapGatePlan(target).Panes) {
				t.Fatalf("target %s dispatched %d agent command(s), want %d", target, fake.dispatch, len(bootstrapGatePlan(target).Panes))
			}
			// Failing must roll back the panes this launch created.
			if len(fake.killed) == 0 {
				t.Fatalf("target %s failed without rolling back its created topology", target)
			}
		})
	}
}

// The mirror case: a healthy launch must still succeed on both targets. Without
// this, a gate that rejected everything would pass the test above.
func TestHealthyLaunchStillSucceedsOnEveryTmuxTarget(t *testing.T) {
	stubExactPaneInspection(t)
	for _, target := range []string{"current-window", "new-window"} {
		t.Run(target, func(t *testing.T) {
			t.Setenv("TMUX", "/tmp/harness,1,0")
			t.Setenv("TMUX_PANE", "%1")
			// Engine running, and a pane that even mentions an error: a live
			// agent printing an error is not a bootstrap death.
			fake := &fakeTmuxLaunch{engine: "claude", screen: "error: a recoverable tool error\n"}
			fake.install(t)

			if _, err := runTmuxLaunchPlanInternal(bootstrapGatePlan(target), false); err != nil {
				t.Fatalf("target %s failed a healthy launch: %v", target, err)
			}
			if len(fake.killed) != 0 {
				t.Fatalf("target %s rolled back a healthy launch: killed %v", target, fake.killed)
			}
		})
	}
}
