package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// #540 acceptance criterion 4: agent bootstrap failure must fail the launch
// loudly, with the pane's error text attributed to the member.

// fakePanes wires both probe seams from one table so a test describes panes as
// {current command, screen content} and nothing else.
type fakePane struct {
	command string
	screen  string
}

func withFakePanes(t *testing.T, panes map[string]fakePane) {
	t.Helper()
	origProbe := paneBootstrapProbe
	origTail := paneTailForBootstrap
	origBudget := paneBootstrapProbeBudget
	origInterval := paneBootstrapProbeInterval
	origTmux := tmuxOutputCommand
	t.Cleanup(func() {
		paneBootstrapProbe = origProbe
		paneTailForBootstrap = origTail
		paneBootstrapProbeBudget = origBudget
		paneBootstrapProbeInterval = origInterval
		tmuxOutputCommand = origTmux
	})
	// #598 added a pane-ENUMERATION fallback to waitForPaneBootstrap. These
	// fixtures stub the per-pane probes but historically left tmuxOutputCommand
	// alone, so without this the enumeration would reach the REAL shared tmux
	// server (the one the squad itself runs in) from tests that were hermetic.
	// That is both a wrong answer and a shared-state violation, so the fake owns
	// the seam too.
	//
	// It reports tmux as UNREACHABLE, which preserves these fixtures' meaning
	// exactly: "cannot inspect the pane AND cannot enumerate" is the
	// inconclusive case that must stay fail-open. The enumerable-but-absent
	// case, where absence is positive proof, is covered separately in
	// bootstrap_failure_vanished_598_test.go.
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "list-panes" {
			return "", fmt.Errorf("tmux: no server running (fake)")
		}
		return "", fmt.Errorf("unexpected tmux call in fake pane fixture: %s %v", name, args)
	}
	paneBootstrapProbe = func(paneID string) (string, bool) {
		p, ok := panes[paneID]
		if !ok {
			return "", false
		}
		return p.command, true
	}
	paneTailForBootstrap = func(paneID string, n int) (string, error) {
		return panes[paneID].screen, nil
	}
	// Keep the inconclusive budget short so tests that intentionally never
	// converge do not stall the suite.
	paneBootstrapProbeBudget = 20 * time.Millisecond
	paneBootstrapProbeInterval = 5 * time.Millisecond
}

// The exact #540 field state: panes exist, pane_current_command is the shell, and
// each pane holds the contradictory bootstrap error.
func TestDeadPaneAtShellPromptIsReportedAsBootstrapFailure(t *testing.T) {
	const bootErr = "error: load accepted prepared launch identity: prepared launch record namespace drift: accepted=squad/v2-25-0 current=squad/v2-25-0"
	withFakePanes(t, map[string]fakePane{
		"%1": {command: "zsh", screen: "some earlier output\n" + bootErr + "\n"},
		"%2": {command: "zsh", screen: bootErr + "\n"},
	})
	probes := []bootstrapProbe{
		{Role: "cto", PaneID: "%1", Engine: "claude"},
		{Role: "amq-dev-1", PaneID: "%2", Engine: "claude"},
	}
	failures := waitForPaneBootstrap(probes)
	if len(failures) != 2 {
		t.Fatalf("expected both dead panes reported, got %d: %+v", len(failures), failures)
	}
	err := bootstrapFailureError(failures)
	if err == nil {
		t.Fatal("dead panes must produce a launch error")
	}
	msg := err.Error()
	// Member attribution and the pane's own error text are both required.
	for _, want := range []string{"cto", "%1", "amq-dev-1", "%2", "load accepted prepared launch identity"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("bootstrap failure %q is missing %q", msg, want)
		}
	}
}

// A healthy launch must not be reported as a failure, and must not pay the
// inconclusive budget: every engine is running, so the probe returns at once.
func TestRunningEnginesAreNotReportedAsBootstrapFailure(t *testing.T) {
	withFakePanes(t, map[string]fakePane{
		"%1": {command: "claude", screen: "welcome\n"},
		"%2": {command: "codex", screen: "welcome\n"},
	})
	probes := []bootstrapProbe{
		{Role: "cto", PaneID: "%1", Engine: "claude"},
		{Role: "amq-dev-2", PaneID: "%2", Engine: "codex"},
	}
	if failures := waitForPaneBootstrap(probes); len(failures) != 0 {
		t.Fatalf("healthy panes reported as bootstrap failures: %+v", failures)
	}
	if err := bootstrapFailureError(nil); err != nil {
		t.Fatalf("no failures must yield a nil verdict, got %v", err)
	}
}

// A running agent that merely PRINTS an error is not a bootstrap death. The
// engine is still the pane's current command, so the launch stands. Without the
// liveness half of the signal this would be a false positive that breaks working
// launches.
func TestRunningEngineWithErrorTextIsNotABootstrapFailure(t *testing.T) {
	withFakePanes(t, map[string]fakePane{
		"%1": {command: "claude", screen: "error: some recoverable tool error\n"},
	})
	probes := []bootstrapProbe{{Role: "cto", PaneID: "%1", Engine: "claude"}}
	if failures := waitForPaneBootstrap(probes); len(failures) != 0 {
		t.Fatalf("a live agent printing an error was misreported as dead: %+v", failures)
	}
}

// A pane at the shell with no error yet has simply not exec'd the binary. It must
// keep waiting and, if it never converges, report NO failure rather than invent
// one -- an inconclusive probe falls back to the pre-existing behavior.
func TestInconclusiveProbeReportsNoFailure(t *testing.T) {
	withFakePanes(t, map[string]fakePane{
		"%1": {command: "zsh", screen: "$ \n"},
	})
	probes := []bootstrapProbe{{Role: "cto", PaneID: "%1", Engine: "claude"}}
	if failures := waitForPaneBootstrap(probes); len(failures) != 0 {
		t.Fatalf("an inconclusive probe must not fabricate a failure, got %+v", failures)
	}
}

// A pane that cannot be inspected at all (gone, tmux unavailable, sandboxed)
// must not be reported as a bootstrap failure.
func TestUninspectablePaneReportsNoFailure(t *testing.T) {
	withFakePanes(t, map[string]fakePane{})
	probes := []bootstrapProbe{{Role: "cto", PaneID: "%missing", Engine: "claude"}}
	if failures := waitForPaneBootstrap(probes); len(failures) != 0 {
		t.Fatalf("an uninspectable pane must not be reported as dead, got %+v", failures)
	}
}

// The goal-delivery timeout must name the pane error instead of reporting only
// that the lead never became ready.
func TestGoalDeliveryTimeoutDetailCarriesPaneError(t *testing.T) {
	withFakePanes(t, map[string]fakePane{
		"%1": {command: "zsh", screen: "error: prepared launch record identity drift: project: accepted=\".\" current=\"/repo\"\n"},
	})
	probes := []bootstrapProbe{{Role: "cto", PaneID: "%1", Engine: "claude"}}
	detail := describePaneBootstrapFailures(waitForPaneBootstrap(probes))
	if detail == "" {
		t.Fatal("a dead pane must produce an attributable detail for the readiness timeout")
	}
	for _, want := range []string{"cto", "identity drift"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("timeout detail %q is missing %q", detail, want)
		}
	}
	// No failures must contribute nothing, so a healthy-but-slow lead keeps its
	// original timeout message unchanged.
	if got := describePaneBootstrapFailures(nil); got != "" {
		t.Fatalf("no failures must add no detail, got %q", got)
	}
}

// The error-line matcher must be tight enough that ordinary prose does not read
// as a dead pane, and must report the LAST error when a failure cascades.
func TestPaneErrorDetectionIsAnchoredAndReportsLastError(t *testing.T) {
	withFakePanes(t, map[string]fakePane{
		"%prose":   {command: "zsh", screen: "I will explain what an error: prefix means inline.\n"},
		"%cascade": {command: "zsh", screen: "error: first cause\nerror: final failure\n"},
		"%fatal":   {command: "zsh", screen: "fatal: repository not found\n"},
		"%blank":   {command: "zsh", screen: "error:   \n"},
	})
	if _, failed := paneBootstrapFailure("%prose"); failed {
		t.Fatal("mid-line 'error:' in prose must not read as a pane failure")
	}
	text, failed := paneBootstrapFailure("%cascade")
	if !failed || !strings.Contains(text, "final failure") {
		t.Fatalf("cascading errors must report the last one, got %q (failed=%t)", text, failed)
	}
	if text, failed := paneBootstrapFailure("%fatal"); !failed || !strings.Contains(text, "repository not found") {
		t.Fatalf("a fatal: line must be detected, got %q (failed=%t)", text, failed)
	}
	if _, failed := paneBootstrapFailure("%blank"); failed {
		t.Fatal("an error line with no message must not read as a pane failure")
	}
}

// #540 acceptance criterion 5: the failure must name a documented,
// non-destructive recovery path.
func TestBootstrapFailureNamesNonDestructiveRecovery(t *testing.T) {
	if !strings.Contains(bootstrapFailureRecoveryHint, "--prepare") {
		t.Fatalf("recovery hint %q must name the re-arm command", bootstrapFailureRecoveryHint)
	}
	if !strings.Contains(bootstrapFailureRecoveryHint, "no namespace reset is required") {
		t.Fatalf("recovery hint %q must state that no destructive reset is needed", bootstrapFailureRecoveryHint)
	}
	// No destructive verb may appear as something the operator is told to RUN.
	// Checked against the command portion only, so the hint can still promise
	// that a reset is unnecessary.
	command := bootstrapFailureRecoveryHint
	if i := strings.Index(command, "("); i >= 0 {
		command = command[:i]
	}
	for _, forbidden := range []string{"rm ", "reset", "delete", "--force", "kill"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("recovery command %q must be non-destructive but mentions %q", command, forbidden)
		}
	}
}

// Probe construction must pair panes with their planned role and engine, and
// must skip a pane whose engine is unknown rather than guess.
func TestTmuxBootstrapProbesPairPanesWithPlannedEngines(t *testing.T) {
	panes := []teamLaunchPane{
		{Role: "cto", Engine: "claude"},
		{Role: "amq-dev-2", Engine: "codex"},
		{Role: "mystery", Engine: ""},
	}
	probes := tmuxBootstrapProbes(panes, []string{"%1", "%2", "%3"})
	if len(probes) != 2 {
		t.Fatalf("expected the engineless pane to be skipped, got %+v", probes)
	}
	if probes[0].Role != "cto" || probes[0].PaneID != "%1" || probes[0].Engine != "claude" {
		t.Fatalf("first probe mispaired: %+v", probes[0])
	}
	if probes[1].Role != "amq-dev-2" || probes[1].PaneID != "%2" || probes[1].Engine != "codex" {
		t.Fatalf("second probe mispaired: %+v", probes[1])
	}
	// Fewer pane ids than planned panes must not panic or invent pairings.
	if got := tmuxBootstrapProbes(panes, []string{"%1"}); len(got) != 1 {
		t.Fatalf("truncated pane id list must yield one probe, got %+v", got)
	}
	if got := tmuxBootstrapProbes(nil, nil); len(got) != 0 {
		t.Fatalf("no panes must yield no probes, got %+v", got)
	}
}

// Regression guard for a defect I introduced and `make ci` caught: the probe
// paid its full inconclusive budget on every launch when no pane could be
// inspected. In a suite that launches many times behind a faked tmux that cost
// 6s per launch and timed the package out.
//
// When nothing is inspectable there is no liveness signal to wait for, so the
// probe must return immediately rather than sleep out the budget.
func TestUninspectableProbeReturnsWithoutPayingTheBudget(t *testing.T) {
	withFakePanes(t, map[string]fakePane{})
	// A budget long enough that paying it would be unmistakable.
	paneBootstrapProbeBudget = 30 * time.Second
	paneBootstrapProbeInterval = 5 * time.Second

	start := time.Now()
	failures := waitForPaneBootstrap([]bootstrapProbe{
		{Role: "cto", PaneID: "%missing", Engine: "claude"},
		{Role: "amq-dev-1", PaneID: "%also-missing", Engine: "claude"},
	})
	elapsed := time.Since(start)

	if len(failures) != 0 {
		t.Fatalf("uninspectable panes must not be reported as failures, got %+v", failures)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("probe slept %s waiting on a signal that cannot arrive; it must return immediately", elapsed)
	}
}

// The probe must not trust `tmux display-message` for a pane that is gone: tmux
// silently answers with the CLIENT's current pane instead of erroring. If that
// fallback row were accepted, a closed agent pane would report the launcher's own
// shell and be misread as a dead agent.
func TestProbeRejectsTmuxFallbackPaneRow(t *testing.T) {
	orig := tmuxOutputCommand
	t.Cleanup(func() { tmuxOutputCommand = orig })
	// Asked about %gone, tmux answers about %launcher, whose command is a shell.
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		return "%launcher\tzsh\n", nil
	}
	if _, ok := paneBootstrapProbe("%gone"); ok {
		t.Fatal("a fallback pane row for a different pane id must be rejected, not treated as the requested pane")
	}
	tmuxOutputCommand = func(name string, args ...string) (string, error) {
		return "%real\tclaude\n", nil
	}
	command, ok := paneBootstrapProbe("%real")
	if !ok || command != "claude" {
		t.Fatalf("a matching pane row must be accepted, got (%q, %t)", command, ok)
	}
}

// Second-review MUST-FIX 1: the death rule is "the SHELL is the current command",
// not "anything other than the expected engine".
//
// Between send-keys and the engine being exec'd, a pane's current command passes
// through intermediate executables (a wrapper, a shell function, amq-squad
// itself). If shell init prints an anchored error line in that window, treating
// any non-engine command as death kills a launch that is starting normally.
func TestIntermediateExecutableWithErrorTextIsNotABootstrapDeath(t *testing.T) {
	const initNoise = "error: plugin cache unavailable\n"
	for _, command := range []string{"amq-squad", "node", "env", "wrapper.sh", "codex-launcher"} {
		t.Run(command, func(t *testing.T) {
			withFakePanes(t, map[string]fakePane{
				"%1": {command: command, screen: initNoise},
			})
			probes := []bootstrapProbe{{Role: "cto", PaneID: "%1", Engine: "claude"}}
			if failures := waitForPaneBootstrap(probes); len(failures) != 0 {
				t.Fatalf("pane mid-startup at %q was killed as a bootstrap death: %+v", command, failures)
			}
		})
	}
}

// The complement: a real shell IS a death when the pane also shows an error,
// including a login shell, which tmux reports with a leading dash.
func TestKnownShellsWithErrorTextAreBootstrapDeaths(t *testing.T) {
	for _, command := range []string{"zsh", "bash", "-zsh", "/bin/sh", "fish"} {
		t.Run(command, func(t *testing.T) {
			withFakePanes(t, map[string]fakePane{
				"%1": {command: command, screen: "error: load accepted prepared launch identity\n"},
			})
			probes := []bootstrapProbe{{Role: "cto", PaneID: "%1", Engine: "claude"}}
			failures := waitForPaneBootstrap(probes)
			if len(failures) != 1 {
				t.Fatalf("pane at shell %q with an error was not reported dead: %+v", command, failures)
			}
		})
	}
}

// paneIsAtShell must not classify an agent engine as a shell, or a healthy pane
// would be eligible for the death check.
func TestPaneIsAtShellRejectsEnginesAndBlanks(t *testing.T) {
	for _, command := range []string{"claude", "codex", "", "   ", "amq-squad"} {
		if paneIsAtShell(command) {
			t.Fatalf("%q must not be classified as a shell", command)
		}
	}
	for _, command := range []string{"zsh", "-bash", "/usr/bin/fish", "BASH"} {
		if !paneIsAtShell(command) {
			t.Fatalf("%q must be classified as a shell", command)
		}
	}
}
