package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunVersionCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"version"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run version: %v\nstderr:\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "amq-squad v-test" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRunHelpIncludesVersionCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"--help"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run help: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "version   Print the amq-squad version") {
		t.Fatalf("help missing version command:\n%s", stdout)
	}
}

// TestRunHelpVerbsStillPrintUsage proves the explicit help paths are
// unchanged after bare `amq-squad` was repurposed as the status board: help,
// -h, and --help must all print the usage block, not the board.
func TestRunHelpVerbsStillPrintUsage(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		stdout, stderr, err := captureOutput(t, func() error {
			return Run(args, "v-test")
		})
		if err != nil {
			t.Fatalf("Run %v: %v\nstderr:\n%s", args, err, stderr)
		}
		if !strings.Contains(stdout, "amq-squad - role-aware agent team launcher") {
			t.Errorf("Run %v should print usage, got:\n%s", args, stdout)
		}
		if strings.Contains(stdout, "AM_BASE_ROOT") {
			t.Errorf("Run %v should NOT render the board, got:\n%s", args, stdout)
		}
	}
}

// TestRunBareConfiguredRoutesToBoard proves bare `amq-squad` in a configured
// project runs the status board (not usage). PATH is stripped of `amq` so the
// board degrades to its guidance line rather than execing a real subprocess;
// the key assertion is that it took the board route, not printUsage.
func TestRunBareConfiguredRoutesToBoard(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	t.Setenv("PATH", "")
	stdout, stderr, err := captureOutput(t, func() error {
		return Run(nil, "v-test")
	})
	if err != nil {
		t.Fatalf("bare run in configured project must not error: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "amq-squad - role-aware agent team launcher") {
		t.Errorf("bare configured run should route to the board, not usage:\n%s", stdout)
	}
	// The board's graceful-degradation guidance is on stdout (amq missing).
	if !strings.Contains(stdout, "amq-squad:") {
		t.Errorf("expected board guidance on stdout, got:\n%s", stdout)
	}
}

// TestRunBareUnconfiguredShowsGuidance proves bare `amq-squad` in an
// unconfigured project shows a short setup guidance message — not the board,
// not usage, and not a crash.
func TestRunBareUnconfiguredShowsGuidance(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	stdout, stderr, err := captureOutput(t, func() error {
		return Run(nil, "v-test")
	})
	if err != nil {
		t.Fatalf("bare run in unconfigured project must not error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "no team is configured") {
		t.Errorf("expected setup guidance, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "amq-squad team init") {
		t.Errorf("guidance should point at team init, got:\n%s", stdout)
	}
}
