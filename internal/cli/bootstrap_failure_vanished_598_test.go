package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// stubVanishedPaneProbes wires the seams waitForPaneBootstrap depends on: the
// per-pane command probe, the underlying tmux command runner, and the clock.
// Nothing here touches a real tmux server.
//
// It deliberately stubs tmuxOutputCommand rather than the pane-list helper
// directly, so these tests compile and run unchanged against the pre-fix code.
// A regression test that only fails to COMPILE without the fix proves nothing
// about behavior.
func stubVanishedPaneProbes(t *testing.T, commands map[string]string, livePanes []string, tmuxReachable bool) *int {
	t.Helper()
	origProbe, origTmux := paneBootstrapProbe, tmuxOutputCommand
	origNow, origSleep := runStartLeadReadyNow, runStartLeadReadySleep
	t.Cleanup(func() {
		paneBootstrapProbe, tmuxOutputCommand = origProbe, origTmux
		runStartLeadReadyNow, runStartLeadReadySleep = origNow, origSleep
	})
	paneBootstrapProbe = func(paneID string) (string, bool) {
		cmd, ok := commands[paneID]
		return cmd, ok
	}
	listCalls := 0
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-panes" {
			listCalls++
			if !tmuxReachable {
				return "", fmt.Errorf("tmux: no server running")
			}
			return strings.Join(livePanes, "\n") + "\n", nil
		}
		return "", fmt.Errorf("unexpected tmux call: %s %v", name, args)
	}
	now := time.Unix(0, 0).UTC()
	runStartLeadReadyNow = func() time.Time { return now }
	runStartLeadReadySleep = func(d time.Duration) { now = now.Add(d) }
	return &listCalls
}

// TestVanishedPaneIsALaunchFailureNotSilentSuccess is the #598 root cause 3
// regression for the launcher half.
//
// #540 taught the launcher to read a dead agent's pane. It cannot read a pane
// that no longer exists, and its per-pane probe treats every inspection failure
// as inconclusive, so an agent that exited and took its pane with it produced
// NO signal at all: the launcher printed "Added N team pane(s)", exited 0, and
// the only symptom was a later 45s goal-delivery timeout blamed on lead
// readiness. That is exactly how the fresh-namespace brick stayed invisible.
func TestVanishedPaneIsALaunchFailureNotSilentSuccess(t *testing.T) {
	// tmux answers, and it does not list %2: that pane is gone.
	stubVanishedPaneProbes(t, map[string]string{"%1": "claude"}, []string{"%1"}, true)

	failures := waitForPaneBootstrap([]bootstrapProbe{
		{Role: "cto", PaneID: "%1", Engine: "claude"},
		{Role: "amq-dev-1", PaneID: "%2", Engine: "claude"},
	})
	if len(failures) != 1 {
		t.Fatalf("a vanished pane must be reported as a launch failure; got %d failures: %+v", len(failures), failures)
	}
	if failures[0].Role != "amq-dev-1" || failures[0].PaneID != "%2" {
		t.Errorf("failure must name the member and pane; got role=%q pane=%q", failures[0].Role, failures[0].PaneID)
	}
	if !strings.Contains(failures[0].Error, "no longer exists") {
		t.Errorf("failure text must say the pane is gone, not imply a readable error was omitted; got %q", failures[0].Error)
	}
	// The launch verdict the caller actually uses must be non-nil and must name
	// the member, since "which agent died" was the missing information.
	err := bootstrapFailureError(failures)
	if err == nil {
		t.Fatal("bootstrapFailureError must turn a vanished pane into a launch error")
	}
	if !strings.Contains(err.Error(), "amq-dev-1") {
		t.Errorf("launch error must name the member; got %q", err.Error())
	}
}

// TestUninspectablePanesStayInconclusiveWhenTmuxCannotBeAsked is the guard on
// the fix above. Absence of a pane is only evidence when tmux successfully
// enumerated its panes. If tmux itself cannot be reached, every pane is
// uninspectable for reasons that say nothing about the agent, and inventing a
// launch failure there would break healthy launches in sandboxed or stubbed
// environments. This is the false-positive direction the #540 comment is
// explicit about protecting, and this fix must not erode it.
func TestUninspectablePanesStayInconclusiveWhenTmuxCannotBeAsked(t *testing.T) {
	stubVanishedPaneProbes(t, map[string]string{}, nil, false)

	failures := waitForPaneBootstrap([]bootstrapProbe{
		{Role: "cto", PaneID: "%1", Engine: "claude"},
		{Role: "amq-dev-1", PaneID: "%2", Engine: "claude"},
	})
	if len(failures) != 0 {
		t.Fatalf("an unreachable tmux must stay inconclusive, not fail the launch; got %+v", failures)
	}
}

// TestHealthyLaunchNeverConsultsThePaneList keeps the fix off the happy path.
// Enumerating panes is an extra tmux round trip, and a launch where every agent
// reports its engine has nothing to diagnose.
func TestHealthyLaunchNeverConsultsThePaneList(t *testing.T) {
	listCalls := stubVanishedPaneProbes(t,
		map[string]string{"%1": "claude", "%2": "codex"},
		[]string{"%1", "%2"}, true)

	failures := waitForPaneBootstrap([]bootstrapProbe{
		{Role: "cto", PaneID: "%1", Engine: "claude"},
		{Role: "amq-dev-2", PaneID: "%2", Engine: "codex"},
	})
	if len(failures) != 0 {
		t.Fatalf("healthy launch reported failures: %+v", failures)
	}
	if *listCalls != 0 {
		t.Errorf("healthy launch enumerated panes %d times; it must not be asked at all", *listCalls)
	}
}
