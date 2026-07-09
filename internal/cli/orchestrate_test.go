package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withStubbedTmux swaps orchestrateTmuxRun for a recorder and restores it.
func withStubbedTmux(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	prev := orchestrateTmuxRun
	orchestrateTmuxRun = func(args ...string) error {
		calls = append(calls, append([]string{}, args...))
		return nil
	}
	t.Cleanup(func() { orchestrateTmuxRun = prev })
	return &calls
}

func TestGlobalStartPreviewDoesNotLaunch(t *testing.T) {
	calls := withStubbedTmux(t)
	out, _, err := captureOutput(t, func() error {
		return runGlobalStart([]string{"--root", t.TempDir()})
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("preview must not launch tmux, got calls: %v", *calls)
	}
	if !strings.Contains(out, "PREVIEW only") {
		t.Fatalf("preview output missing PREVIEW banner:\n%s", out)
	}
	if !strings.Contains(out, "poller mode") {
		t.Fatalf("preview output should describe poller mode:\n%s", out)
	}
}

func TestGlobalStartGoLaunchesTmuxWithAgentArgv(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	}
	calls := withStubbedTmux(t)
	root := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runGlobalStart([]string{"--root", root, "--agent", "codex", "--model", "gpt-5", "--codex-args", "--enable goals", "--go"})
	})
	// LookPath for "codex"/"tmux" may fail in CI; only assert argv shape when it launched.
	if err != nil {
		if strings.Contains(err.Error(), "not found on PATH") {
			t.Skipf("agent/tmux binary unavailable in this environment: %v", err)
		}
		t.Fatalf("go launch returned error: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected exactly one tmux call, got %v", *calls)
	}
	got := strings.Join((*calls)[0], " ")
	for _, want := range []string{"new-window", "-c " + root, "-n global-orch", "codex --model gpt-5 --enable goals"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tmux argv %q missing %q", got, want)
		}
	}
}

func TestGlobalStartRejectsBadAgent(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGlobalStart([]string{"--agent", "vim", "--root", t.TempDir()})
	})
	if err == nil || !strings.Contains(err.Error(), "--agent must be claude or codex") {
		t.Fatalf("expected agent validation error, got %v", err)
	}
}

func TestGlobalUnknownSubcommand(t *testing.T) {
	err := runGlobal([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown 'global' subcommand") {
		t.Fatalf("expected unknown-subcommand error, got %v", err)
	}
}

func TestGlobalDispatchHelpAndEmpty(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return runGlobal([]string{}) })
	if err == nil || !strings.Contains(err.Error(), "global requires a subcommand") {
		t.Fatalf("empty global should require a subcommand, got %v", err)
	}
	_, _, err = captureOutput(t, func() error { return runGlobal([]string{"-h"}) })
	if err != nil {
		t.Fatalf("global -h should not error, got %v", err)
	}
}

func TestRunCmdDispatch(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return runRunCmd([]string{}, "test") })
	if err == nil || !strings.Contains(err.Error(), "run requires a subcommand") {
		t.Fatalf("empty run should require a subcommand, got %v", err)
	}
	_, _, err = captureOutput(t, func() error { return runRunCmd([]string{"bogus"}, "test") })
	if err == nil || !strings.Contains(err.Error(), "unknown 'run' subcommand") {
		t.Fatalf("expected unknown-subcommand error, got %v", err)
	}
	_, _, err = captureOutput(t, func() error { return runRunCmd([]string{"-h"}, "test") })
	if err != nil {
		t.Fatalf("run -h should not error, got %v", err)
	}
}

func TestRunStartRequiresProjectAndSession(t *testing.T) {
	if err := runRunStart([]string{"-s", "x"}, "test"); err == nil || !strings.Contains(err.Error(), "requires --project") {
		t.Fatalf("expected --project error, got %v", err)
	}
	if err := runRunStart([]string{"-p", t.TempDir()}, "test"); err == nil || !strings.Contains(err.Error(), "requires --session") {
		t.Fatalf("expected --session error, got %v", err)
	}
}

func TestRunStartExternalLeadUnsupported(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--external-lead"}, "test")
	if err == nil || !strings.Contains(err.Error(), "external-lead mode is not yet supported") {
		t.Fatalf("expected external-lead unsupported error, got %v", err)
	}
}

func TestRunStartNoRolesNoTeamErrors(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "sess"}, "test")
	if err == nil || !strings.Contains(err.Error(), "no team profile") {
		t.Fatalf("expected no-team error, got %v", err)
	}
}

func TestRunStartRejectsBadSession(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "Bad Session!", "--roles", "cto"}, "test")
	if err == nil || !strings.Contains(err.Error(), "invalid --session") {
		t.Fatalf("expected session validation error, got %v", err)
	}
}

func TestRunStartDefaultsToDetachedInPreview(t *testing.T) {
	// A fresh project with --roles: preview should describe a detached (hidden)
	// spawn by default and note the deferred spawn validation.
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto"}, "test")
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if !strings.Contains(out, "--visibility detached") {
		t.Fatalf("default visibility should be detached:\n%s", out)
	}
	if !strings.Contains(out, "hidden") {
		t.Fatalf("preview should explain hidden spawn:\n%s", out)
	}
}

func TestRunStartProfileAliasAndExplicitLead(t *testing.T) {
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "-P", "release", "--roles", "cto,qa", "--lead", "qa"}, "test")
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if !strings.Contains(out, "profile: release") {
		t.Fatalf("-P alias should set profile release:\n%s", out)
	}
	if !strings.Contains(out, "lead:    qa") || !strings.Contains(out, "--lead qa") {
		t.Fatalf("explicit --lead qa should be honored, not defaulted to cto:\n%s", out)
	}
}

func TestRunStartPreviewSurfacesPlannerLeadModeForFreshRoster(t *testing.T) {
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto,fullstack", "--lead-mode", "planner"}, "test")
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	for _, want := range []string{
		"lead-mode: planner",
		"--lead-mode planner",
		"# lead-mode: planner",
		"# implementation-allowed: false",
		"# mutable-actor: fullstack",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("run start planner preview missing %q:\n%s", want, out)
		}
	}
}

func TestRunStartLeadModeExistingProfileRequiresExplicitProfileMutation(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--lead-mode", "planner"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "team lead set") {
		t.Fatalf("expected existing-profile lead-mode error, got %v", err)
	}
}

func TestRunStartExistingProfileWithRolesInfersLead(t *testing.T) {
	// Regression: --roles + an EXISTING profile whose lead is not cto must not
	// force cto. new team is skipped, so the run infers the profile's lead.
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "qa"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--roles", "cto,qa", "--goal", "do x"}, "test")
	})
	if err != nil {
		t.Fatalf("preview error: %v", err)
	}
	if strings.Contains(out, "lead:    cto") {
		t.Fatalf("existing qa-led team must not display cto lead:\n%s", out)
	}
	if !strings.Contains(out, "inferred from profile") {
		t.Fatalf("existing team should infer lead:\n%s", out)
	}
	if !strings.Contains(out, "already exists") {
		t.Fatalf("should note the existing profile / skipped roster:\n%s", out)
	}
}

func TestRunStartGoGoalWaitsForLeadReadiness(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}

	var upCalls [][]string
	var goalCalls [][]string
	var sleeps []time.Duration
	readyChecks := 0
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error {
			upCalls = append(upCalls, append([]string{}, args...))
			return nil
		},
		func(args []string, version string) error {
			goalCalls = append(goalCalls, append([]string{}, args...))
			return nil
		},
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			readyChecks++
			if readyChecks < 3 {
				return runStartLeadReadiness{Detail: "still starting"}, nil
			}
			return runStartLeadReadiness{Ready: true, Detail: "live"}, nil
		},
		func(d time.Duration) { sleeps = append(sleeps, d) },
		time.Now,
	)

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--goal", "ship it", "--go"}, "test")
	})
	if err != nil {
		t.Fatalf("run start --go returned error: %v", err)
	}
	if len(upCalls) != 1 {
		t.Fatalf("expected one up call, got %v", upCalls)
	}
	if len(goalCalls) != 1 {
		t.Fatalf("expected one goal call, got %v", goalCalls)
	}
	if readyChecks != 3 || len(sleeps) != 2 {
		t.Fatalf("expected two waits before readiness, checks=%d sleeps=%v", readyChecks, sleeps)
	}
	goal := strings.Join(goalCalls[0], " ")
	for _, want := range []string{"start", "--project " + dir, "--profile default", "--session sess", "--role cto", "--goal ship it", "--yes"} {
		if !strings.Contains(goal, want) {
			t.Fatalf("goal args %q missing %q", goal, want)
		}
	}
}

func TestRunStartGoGoalFailurePrintsQuotedRetryCommand(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}

	now := time.Unix(100, 0)
	goal := "ship 'quotes'\nand $stuff"
	goalCalled := false
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error { return nil },
		func(args []string, version string) error {
			goalCalled = true
			return nil
		},
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			return runStartLeadReadiness{Detail: "pane not live yet"}, nil
		},
		func(d time.Duration) { now = now.Add(d) },
		func() time.Time { return now },
	)
	prevTimeout := runStartLeadReadyTimeout
	runStartLeadReadyTimeout = time.Millisecond
	t.Cleanup(func() { runStartLeadReadyTimeout = prevTimeout })

	_, stderr, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--goal", goal, "--go"}, "test")
	})
	if err == nil {
		t.Fatal("expected readiness timeout error")
	}
	if goalCalled {
		t.Fatal("goal delivery must not run when readiness never succeeds")
	}
	wantCmd := "amq-squad goal start --project " + shellQuote(dir) +
		" --profile default --session sess --role cto --goal " + shellQuote(goal) + " --yes"
	if !strings.Contains(stderr, wantCmd) {
		t.Fatalf("stderr missing quoted retry command\nwant: %s\nstderr:\n%s", wantCmd, stderr)
	}
}

func TestRunStartGoGoalDeliveryFailureAfterReadyPrintsRetryCommand(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}

	goalErr := errors.New("pane rejected paste")
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error { return nil },
		func(args []string, version string) error { return goalErr },
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			return runStartLeadReadiness{Ready: true, Detail: "live"}, nil
		},
		func(time.Duration) {},
		time.Now,
	)

	_, stderr, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--goal", "ship it", "--go"}, "test")
	})
	if !errors.Is(err, goalErr) || !strings.Contains(err.Error(), "goal delivery failed after lead became ready") {
		t.Fatalf("expected wrapped delivery error, got %v", err)
	}
	wantCmd := "amq-squad goal start --project " + shellQuote(dir) +
		" --profile default --session sess --role cto --goal 'ship it' --yes"
	if !strings.Contains(stderr, wantCmd) {
		t.Fatalf("stderr missing retry command\nwant: %s\nstderr:\n%s", wantCmd, stderr)
	}
}

func TestRunStartLeadReadinessTransientErrorKeepsPolling(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(100, 0)
	calls := 0
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error { return nil },
		func(args []string, version string) error { return nil },
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			calls++
			if calls == 1 {
				return runStartLeadReadiness{}, errors.New("profile temporarily unreadable")
			}
			return runStartLeadReadiness{Ready: true, Detail: "live"}, nil
		},
		func(d time.Duration) { now = now.Add(d) },
		func() time.Time { return now },
	)

	err := waitForRunStartLeadReady(runStartGoalDeliveryOptions{
		Project: dir,
		Profile: "default",
		Session: "sess",
		Role:    "cto",
	})
	if err != nil {
		t.Fatalf("transient readiness error should be retried, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("readiness calls = %d, want 2", calls)
	}
}

func stubRunStartGoalDelivery(
	t *testing.T,
	up func([]string, string) error,
	goal func([]string, string) error,
	ready func(project, profile, session, role string) (runStartLeadReadiness, error),
	sleep func(time.Duration),
	now func() time.Time,
) {
	t.Helper()
	prevUp := runStartUpWithVersion
	prevGoal := runStartGoalWithVersion
	prevReady := runStartLeadReadyCheck
	prevSleep := runStartLeadReadySleep
	prevNow := runStartLeadReadyNow
	runStartUpWithVersion = up
	runStartGoalWithVersion = goal
	runStartLeadReadyCheck = ready
	runStartLeadReadySleep = sleep
	runStartLeadReadyNow = now
	t.Cleanup(func() {
		runStartUpWithVersion = prevUp
		runStartGoalWithVersion = prevGoal
		runStartLeadReadyCheck = prevReady
		runStartLeadReadySleep = prevSleep
		runStartLeadReadyNow = prevNow
	})
}

func TestStripFlagValue(t *testing.T) {
	got, had := stripFlagValue([]string{"sess", "--project", "p", "--seed-from", "issue:9", "--visibility", "detached"}, "--seed-from")
	if !had {
		t.Fatal("expected had=true when flag present")
	}
	if strings.Join(got, " ") != "sess --project p --visibility detached" {
		t.Fatalf("unexpected strip result: %q", strings.Join(got, " "))
	}
	if _, had := stripFlagValue([]string{"sess", "--project", "p"}, "--seed-from"); had {
		t.Fatal("expected had=false when flag absent")
	}
}

func TestRunStartPreviewSeedFromValidatesRealSpawn(t *testing.T) {
	// With --seed-from, the validation dry-run must strip it (else up --dry-run
	// returns brief-only and skips roster/session validation). Existing team is
	// pinned to sess, so the real validation passes and the seed note appears.
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}
	brief := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(brief, []byte("# brief\nwork on it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--seed-from", "file:" + brief}, "test")
	})
	if err != nil {
		t.Fatalf("preview error: %v", err)
	}
	if !strings.Contains(out, "Preview OK") {
		t.Fatalf("expected Preview OK for a valid pinned team:\n%s", out)
	}
	if !strings.Contains(out, "--seed-from brief is written at --go") {
		t.Fatalf("expected seed-from note:\n%s", out)
	}
}

func TestRunStartRejectsBadVisibility(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto", "--visibility", "bogus"}, "test")
	if err == nil || !strings.Contains(err.Error(), "unsupported visibility") {
		t.Fatalf("expected visibility validation error, got %v", err)
	}
	err = runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto", "--visibility", "plan"}, "test")
	if err == nil || !strings.Contains(err.Error(), "not valid for run start") {
		t.Fatalf("expected plan-rejection error, got %v", err)
	}
}

func TestAppendPassthroughArgs(t *testing.T) {
	got := appendPassthroughArgs([]string{"up"}, "cto=gpt-5", "--enable goals", "")
	want := "up --model cto=gpt-5 --codex-args --enable goals"
	if strings.Join(got, " ") != want {
		t.Fatalf("appendPassthroughArgs = %q, want %q", strings.Join(got, " "), want)
	}
	if joined := strings.Join(appendPassthroughArgs([]string{"up"}, "", "", ""), " "); joined != "up" {
		t.Fatalf("empty passthrough should be a no-op, got %q", joined)
	}
}

func TestCompletionCoversGlobalRunSubcommands(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out, _, err := captureOutput(t, func() error { return runCompletion([]string{shell}) })
		if err != nil {
			t.Fatalf("%s completion error: %v", shell, err)
		}
		for _, verb := range []string{"global", "run"} {
			if !strings.Contains(out, verb) {
				t.Errorf("%s completion missing top command %q", shell, verb)
			}
		}
		// Each verb's sole subcommand is "start"; assert the script wires it.
		if strings.Count(out, "start") == 0 {
			t.Errorf("%s completion does not surface the start subcommand:\n%s", shell, out)
		}
	}
}

func TestGlobalAndRunRegistered(t *testing.T) {
	for _, name := range []string{"global", "run"} {
		if _, ok := lookupCommand(name, "test"); !ok {
			t.Fatalf("command %q not registered", name)
		}
		if commandSummary(name) == "" {
			t.Fatalf("command %q missing catalog summary", name)
		}
	}
}
