package cli

import (
	"os"
	"path/filepath"
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
	if !strings.Contains(stdout, "roles     List built-in role IDs") {
		t.Fatalf("help missing roles command:\n%s", stdout)
	}
}

func TestRunRolesListsMarketNumbers(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"roles"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run roles: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"NUM",
		"ROLE",
		"DEFAULT CLI",
		"1",
		"cpo",
		"2",
		"cto",
		"9",
		"qa",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("roles output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunRolesRejectsPositionalArgs(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return Run([]string{"roles", "cto"}, "v-test")
	})
	if err == nil {
		t.Fatal("roles with a positional argument should fail")
	}
	if !strings.Contains(err.Error(), "no positional arguments") {
		t.Fatalf("roles positional error = %v", err)
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
	// Strip PATH so the footprint check's session probe can never resolve a real
	// base root from a stray `amq` — a truly empty project must show guidance.
	t.Setenv("PATH", "")
	stdout, stderr, err := captureOutput(t, func() error {
		return Run(nil, "v-test")
	})
	if err != nil {
		t.Fatalf("bare run in unconfigured project must not error: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "no team is configured") {
		t.Errorf("expected setup guidance, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "amq-squad new team") {
		t.Errorf("guidance should point at new team, got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "amq-squad roles") {
		t.Errorf("guidance should point at roles, got:\n%s", stdout)
	}
}

// TestRunBareNamedProfilesOnlyRoutesToBoard is the end-to-end PR12 regression:
// bare `amq-squad` in a project that has ONLY named profiles (no default
// team.json) must route to the board, NOT print the "no team configured"
// guidance. PATH is stripped so the board degrades to its guidance line instead
// of execing a real `amq`; the key assertion is the ROUTE taken.
func TestRunBareNamedProfilesOnlyRoutesToBoard(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.WriteProfile(dir, "code-truth", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	if team.Exists(dir) {
		t.Fatal("guard: no default team.json should exist for this case")
	}
	t.Setenv("PATH", "")
	stdout, stderr, err := captureOutput(t, func() error {
		return Run(nil, "v-test")
	})
	if err != nil {
		t.Fatalf("bare run in named-profile-only project must not error: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "no team is configured") {
		t.Errorf("named-profile-only project must route to the board, not setup guidance:\n%s", stdout)
	}
	if !strings.Contains(stdout, "amq-squad:") {
		t.Errorf("expected board guidance on stdout, got:\n%s", stdout)
	}
}

// TestProjectHasFootprintDefaultProfile proves a project with a default-profile
// team.json is recognized as having a footprint without consulting the session
// probe at all.
func TestProjectHasFootprintDefaultProfile(t *testing.T) {
	dir := t.TempDir()
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	resolverCalled := false
	resolve := func(string) (string, error) {
		resolverCalled = true
		return "", os.ErrNotExist
	}
	if !projectHasFootprint(dir, resolve) {
		t.Errorf("default-profile team.json should count as a footprint")
	}
	if resolverCalled {
		t.Errorf("default-profile shortcut must not consult the session probe")
	}
}

// TestProjectHasFootprintNamedProfilesOnly is the core PR12 regression: a
// project with ONLY named profiles (no default team.json) — like
// taboola-sales-skills (.amq-squad/teams/code-truth.json + my-voice.json) — must
// be treated as having a footprint and run the board, NOT the "no team
// configured / run team init" guidance.
func TestProjectHasFootprintNamedProfilesOnly(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, "code-truth", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(dir, "my-voice", team.Team{
		Members: []team.Member{{Role: "fs", Binary: "claude", Handle: "fs", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	if team.Exists(dir) {
		t.Fatal("guard: no default team.json should exist for this case")
	}
	// Resolver fails -> proves named profiles ALONE flip the footprint decision.
	resolve := func(string) (string, error) { return "", os.ErrNotExist }
	if !projectHasFootprint(dir, resolve) {
		t.Errorf("named-profile-only project should count as a footprint")
	}
}

// TestProjectHasFootprintSessionsOnly proves a project with no team config at
// all but with discoverable sessions under the resolved base root still runs the
// board (the footprint is the live work on disk).
func TestProjectHasFootprintSessionsOnly(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".agent-mail")
	if err := os.MkdirAll(filepath.Join(base, "issue-96", "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	if team.Exists(dir) {
		t.Fatal("guard: no default team.json should exist for this case")
	}
	if profiles, _ := team.ListProfiles(dir); len(profiles) != 0 {
		t.Fatal("guard: no named profiles should exist for this case")
	}
	resolve := func(string) (string, error) { return base, nil }
	if !projectHasFootprint(dir, resolve) {
		t.Errorf("project with discoverable sessions should count as a footprint")
	}
}

// TestProjectHasFootprintEmptyProject proves a truly empty project — no team
// config, no named profiles, no discoverable sessions — has NO footprint, so the
// bare command shows the setup guidance rather than the board.
func TestProjectHasFootprintEmptyProject(t *testing.T) {
	dir := t.TempDir()
	// Resolver succeeds but the base root has no session/agents layout.
	emptyBase := filepath.Join(dir, ".agent-mail")
	if err := os.MkdirAll(emptyBase, 0o755); err != nil {
		t.Fatal(err)
	}
	resolve := func(string) (string, error) { return emptyBase, nil }
	if projectHasFootprint(dir, resolve) {
		t.Errorf("empty project should have no footprint")
	}
	// Also: an unresolvable base root must not crash and must report no footprint.
	failResolve := func(string) (string, error) { return "", os.ErrNotExist }
	if projectHasFootprint(dir, failResolve) {
		t.Errorf("empty project with unresolvable base root should have no footprint")
	}
}
