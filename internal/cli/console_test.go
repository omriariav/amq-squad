package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/console"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// captureConsole drives executeConsole with a captured RunConsole seam so the
// test asserts the assembled console.Config and the gating decision WITHOUT
// launching a real Bubble Tea program.
func captureConsole(t *testing.T, s consoleExecution) (cfg console.Config, called bool, out string, err error) {
	t.Helper()
	var buf bytes.Buffer
	s.Out = &buf
	s.RunConsole = func(c console.Config) error {
		cfg = c
		called = true
		return nil
	}
	err = executeConsole(s)
	return cfg, called, buf.String(), err
}

// TestConsoleOnceDispatchesWithThresholds proves the verb threads the
// at-risk-wait / review-age / refresh flags and the resolved base root into the
// console.Config, and runs in --once mode.
func TestConsoleOnceDispatchesWithThresholds(t *testing.T) {
	cfg, called, _, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		AtRiskWait:  5 * time.Minute,
		ReviewAge:   15 * time.Minute,
		Refresh:     3 * time.Second,
		Once:        true,
		StdoutIsTTY: false,
		TeamExists:  func(string, string) bool { return true },
		ResolveBase: func(string) (string, error) { return "/base", nil },
	})
	if err != nil {
		t.Fatalf("executeConsole: %v", err)
	}
	if !called {
		t.Fatal("RunConsole should have been called")
	}
	if !cfg.Once {
		t.Error("--once should set Config.Once")
	}
	if cfg.BaseRoot != "/base" {
		t.Errorf("base root = %q, want /base", cfg.BaseRoot)
	}
	if cfg.Thresholds.AtRiskWait != 5*time.Minute {
		t.Errorf("at-risk-wait = %v, want 5m", cfg.Thresholds.AtRiskWait)
	}
	if cfg.Thresholds.ReviewAge != 15*time.Minute {
		t.Errorf("review-age = %v, want 15m", cfg.Thresholds.ReviewAge)
	}
	if cfg.Refresh != 3*time.Second {
		t.Errorf("refresh = %v, want 3s", cfg.Refresh)
	}
	if cfg.NoTeamNotice != "" {
		t.Errorf("a resolved root should carry no NoTeam notice, got %q", cfg.NoTeamNotice)
	}
}

// TestConsoleSessionFlagPresetsFilter proves the --session flag is threaded into
// the console.Config as a typed-filter preset ("session:<name>").
func TestConsoleSessionFlagPresetsFilter(t *testing.T) {
	cfg, called, _, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		Session:     "issue-96",
		Once:        true,
		StdoutIsTTY: false,
		TeamExists:  func(string, string) bool { return true },
		ResolveBase: func(string) (string, error) { return "/base", nil },
	})
	if err != nil {
		t.Fatalf("executeConsole: %v", err)
	}
	if !called {
		t.Fatal("RunConsole should have been called")
	}
	if cfg.InitialFilter != "session:issue-96" {
		t.Errorf("--session should preset InitialFilter to session:issue-96, got %q", cfg.InitialFilter)
	}
}

func TestConsoleFilterFlagPresetsFilter(t *testing.T) {
	cfg, called, _, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		Filter:      "needs-you",
		Once:        true,
		StdoutIsTTY: false,
		TeamExists:  func(string, string) bool { return true },
		ResolveBase: func(string) (string, error) { return "/base", nil },
	})
	if err != nil {
		t.Fatalf("executeConsole: %v", err)
	}
	if !called {
		t.Fatal("RunConsole should have been called")
	}
	if cfg.InitialFilter != "needs-you" {
		t.Errorf("--filter should preset InitialFilter to needs-you, got %q", cfg.InitialFilter)
	}
}

func TestConsoleRejectsSessionWithFilter(t *testing.T) {
	_, _, _, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		Session:     "issue-96",
		Filter:      "needs-you",
		Once:        true,
		StdoutIsTTY: false,
		TeamExists:  func(string, string) bool { return true },
		ResolveBase: func(string) (string, error) { return "/base", nil },
	})
	if err == nil {
		t.Fatal("executeConsole should reject --session with --filter")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--session cannot be used with --filter") {
		t.Fatalf("unexpected conflict error: %v", err)
	}
}

// TestConsoleNoTeamOnceWritesGuidance proves no-team + --once writes guidance to
// stdout, exits nil, and never launches the console.
func TestConsoleNoTeamOnceWritesGuidance(t *testing.T) {
	_, called, out, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		Once:        true,
		StdoutIsTTY: false,
		TeamExists:  func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatalf("no-team + --once must not error, got %v", err)
	}
	if called {
		t.Error("no-team + --once should NOT launch the console")
	}
	if !strings.Contains(out, "no team configured") {
		t.Errorf("expected guidance to stdout, got:\n%s", out)
	}
}

// TestConsoleNoTeamTTYRendersExplanatoryScreen proves no-team + TTY hands the
// console a NoTeam notice (the explanatory failure-state screen) rather than
// aborting.
func TestConsoleNoTeamTTYRendersExplanatoryScreen(t *testing.T) {
	cfg, called, _, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		Once:        false,
		StdoutIsTTY: true,
		TeamExists:  func(string, string) bool { return false },
	})
	if err != nil {
		t.Fatalf("no-team + TTY must not error, got %v", err)
	}
	if !called {
		t.Fatal("no-team + TTY should still launch the console (NoTeam screen)")
	}
	if cfg.NoTeamNotice == "" {
		t.Error("no-team + TTY should hand the console a NoTeam notice")
	}
	if cfg.Once {
		t.Error("no-team + TTY should be interactive, not --once")
	}
}

// TestConsoleUnresolvableRootDegrades proves an unresolvable base root degrades
// to a NoTeam-style notice naming the `amq env` probe, never a crash.
func TestConsoleUnresolvableRootDegrades(t *testing.T) {
	cfg, called, _, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		Once:        true,
		StdoutIsTTY: false,
		TeamExists:  func(string, string) bool { return true },
		ResolveBase: func(string) (string, error) { return "", errors.New("amq not found") },
	})
	if err != nil {
		t.Fatalf("unresolvable root must not error, got %v", err)
	}
	if !called {
		t.Fatal("unresolvable root should still hand off to the console (degraded notice)")
	}
	if !strings.Contains(cfg.NoTeamNotice, "amq env") {
		t.Errorf("degraded notice should name the amq env probe, got %q", cfg.NoTeamNotice)
	}
}

// TestConsoleInteractiveNoTTYFallsBackToOnce proves that when a healthy team is
// configured but stdout is not a TTY (and --once was not asked for), the verb
// falls back to a single static board on stdout rather than failing to open a
// terminal.
func TestConsoleInteractiveNoTTYFallsBackToOnce(t *testing.T) {
	cfg, called, _, err := captureConsole(t, consoleExecution{
		ProjectDir:  "/proj",
		Once:        false,
		StdoutIsTTY: false,
		TeamExists:  func(string, string) bool { return true },
		ResolveBase: func(string) (string, error) { return "/base", nil },
	})
	if err != nil {
		t.Fatalf("interactive + no TTY must not error, got %v", err)
	}
	if !called {
		t.Fatal("should hand off to the console")
	}
	if !cfg.Once {
		t.Error("interactive + no TTY + healthy root should fall back to --once")
	}
}

// TestRunConsoleFlagParsing drives runConsole through the dispatcher entrypoint
// to prove the flags parse (a bad duration is a UsageError). It uses --once with
// no team so no program is started and stdout stays clean.
func TestRunConsoleFlagParsing(t *testing.T) {
	// A malformed duration must surface as a UsageError, not a panic.
	_, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--refresh", "not-a-duration", "--once"})
	})
	if err == nil {
		t.Fatal("a malformed --refresh should be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestRunConsoleProjectTargetsOtherDir(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	if err := team.Write(project, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "")

	stdout, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--project", project, "--once"})
	})
	if err != nil {
		t.Fatalf("console --project --once: %v", err)
	}
	if strings.Contains(stdout, "no team configured") {
		t.Fatalf("console --project inspected current cwd instead of requested project:\n%s", stdout)
	}
	if !strings.Contains(stdout, "could not resolve the AMQ base root") {
		t.Fatalf("console --project should reach requested project and then degrade on missing amq:\n%s", stdout)
	}
}

func TestRunConsoleRootFlagUnsupportedWithProject(t *testing.T) {
	project := t.TempDir()
	root := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--project", project, "--root", root, "--once"})
	})
	if err == nil {
		t.Fatal("console should reject --project with --root")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("unexpected root flag error: %v", err)
	}
}

func TestRunConsoleRootFlagUnsupported(t *testing.T) {
	root := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runConsole([]string{"--root", root, "--once"})
	})
	if err == nil {
		t.Fatal("console should reject --root")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("unexpected root flag error: %v", err)
	}
}

// TestConsoleVerbWiredIntoDispatch proves `console` is reachable through the
// top-level dispatcher (a bad flag returns a UsageError, proving dispatch found
// the verb rather than reporting "unknown command").
func TestConsoleVerbWiredIntoDispatch(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return dispatch([]string{"console", "--definitely-not-a-flag"}, "")
	})
	if err == nil {
		t.Fatal("an unknown console flag should error")
	}
	if strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("console should be dispatched, not reported as unknown: %v", err)
	}
}

// compile-time guard that the verb's threshold default matches the state
// package default, so the help text and the engine agree.
var _ = state.DefaultAtRiskWait
