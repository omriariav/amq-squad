package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

// chdir switches into dir for the duration of the test, restoring the
// previous cwd on cleanup. Used by the up <-> team show parity tests so
// runTeamShow / runUp see a configured team.
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
}

// seedTeam writes cfg into a fresh temp dir and chdirs into it. Used by the
// parity tests so both `team show` and `up --dry-run` see the same project
// path and emit byte-identical output.
func seedTeam(t *testing.T, cfg team.Team) string {
	t.Helper()
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestUpDryRunMatchesTeamShowCore proves the core path: with no extra flags,
// `up --dry-run` emits the same launch-command plan as `team show`.
func TestUpDryRunMatchesTeamShowCore(t *testing.T) {
	cfg := team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	}
	seedTeam(t, cfg)

	showOut, _, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("team show: %v", err)
	}
	upOut, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v", err)
	}
	if showOut != upOut {
		t.Fatalf("up --dry-run output differs from team show.\nteam show:\n%s\nup --dry-run:\n%s", showOut, upOut)
	}
}

// TestUpDryRunMatchesTeamShowWithFlags exercises every preview flag and
// confirms parity. If a flag is added to one entry point but not the other,
// the diff lands here.
func TestUpDryRunMatchesTeamShowWithFlags(t *testing.T) {
	cfg := team.Team{
		Trust:      trustModeTrusted,
		BinaryArgs: map[string][]string{"codex": {"--enable", "goals"}},
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	}
	setupFakeAMQSessionRoots(t)
	seedTeam(t, cfg)

	flagSet := []string{
		"--no-bootstrap",
		"--session", "issue-97",
		"--fresh",
		"--trust", "trusted",
		"--model", "cto=gpt-5,fullstack=sonnet",
		"--codex-args=--profile fast",
		"--claude-args=--chrome",
		"--force-duplicate",
	}

	showOut, _, err := captureOutput(t, func() error {
		return runTeamShow(flagSet)
	})
	if err != nil {
		t.Fatalf("team show: %v", err)
	}
	upOut, _, err := captureOutput(t, func() error {
		return runUp(append([]string{"--dry-run"}, flagSet...))
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v", err)
	}
	if showOut != upOut {
		t.Fatalf("up --dry-run output differs from team show with flags.\nteam show:\n%s\nup --dry-run:\n%s", showOut, upOut)
	}
	if !strings.Contains(upOut, "--session issue-97 --team-workstream") {
		t.Errorf("--session not applied: %s", upOut)
	}
	if !strings.Contains(upOut, "--force-duplicate") {
		t.Errorf("--force-duplicate not applied: %s", upOut)
	}
	if !strings.Contains(upOut, "--codex-args='--enable goals --profile fast'") {
		t.Errorf("--codex-args not merged: %s", upOut)
	}
	if !strings.Contains(upOut, "--claude-args=--chrome") {
		t.Errorf("--claude-args not applied: %s", upOut)
	}
}

func TestUpRequiresDryRun(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runUp(nil)
	})
	if err == nil {
		t.Fatal("bare 'up' should fail until live launch lands")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("bare 'up' should return UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("bare 'up' error should point at --dry-run: %v", err)
	}
}

func TestUpDryRunRequiresTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run"})
	})
	if err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("up --dry-run without team config: got %v, want 'no team configured' error", err)
	}
}
