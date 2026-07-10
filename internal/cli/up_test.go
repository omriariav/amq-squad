package cli

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
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

// fakeBackend is a teamLaunchBackend stub used by live-up tests so the test
// suite does not need a real tmux. Each Launch / DryRun call captures the
// effective teamLaunchOptions for assertion.
type fakeBackend struct {
	mu       sync.Mutex
	launches []teamLaunchOptions
	dryRuns  []teamLaunchOptions
	teams    []team.Team
}

func (f *fakeBackend) Name() string                          { return "fake" }
func (f *fakeBackend) Validate(opts teamLaunchOptions) error { return nil }
func (f *fakeBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dryRuns = append(f.dryRuns, opts)
	f.teams = append(f.teams, t)
	return nil
}
func (f *fakeBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launches = append(f.launches, opts)
	f.teams = append(f.teams, t)
	return nil
}

// useFakeBackend registers a fresh fake backend under the name "fake" for
// the duration of t and restores the prior teamLaunchBackends entry on
// cleanup. The registry is package-global, so leaking the fake would change
// registeredTeamLaunchTerminals() in unrelated tests.
func useFakeBackend(t *testing.T) *fakeBackend {
	t.Helper()
	backend := &fakeBackend{}
	prev, hadPrev := teamLaunchBackends[backend.Name()]
	teamLaunchBackends[backend.Name()] = backend
	t.Cleanup(func() {
		if hadPrev {
			teamLaunchBackends[backend.Name()] = prev
			return
		}
		delete(teamLaunchBackends, backend.Name())
	})
	return backend
}

// TestUpDryRunMatchesTeamShowCoreWithExplicitTarget proves the low-level
// escape hatch: when an operator explicitly chooses a target, `up --dry-run`
// keeps the legacy launch-command plan shape used by `team show`.
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
		return runUp([]string{"--dry-run", "--no-bootstrap", "--target", "current-window"})
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v", err)
	}
	if showOut != upOut {
		t.Fatalf("up --dry-run output differs from team show.\nteam show:\n%s\nup --dry-run:\n%s", showOut, upOut)
	}
}

func TestUpDryRunDefaultsToSiblingTabsVisibility(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-238"}},
	})

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"issue-238", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("up --dry-run default visibility: %v", err)
	}
	env := decodeJSONEnvelope[teamPlan](t, stdout)
	if env.Data.Visibility != "sibling-tabs" {
		t.Fatalf("visibility = %q, want sibling-tabs", env.Data.Visibility)
	}
	if env.Data.LaunchCommand != "amq-squad up issue-238 --visibility sibling-tabs" {
		t.Fatalf("launch_command = %q", env.Data.LaunchCommand)
	}
}

func TestUpDryRunJSONIncludesClaudeWorkerPreauthAndBootstrapStatus(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "v2-14-0"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "v2-14-0"},
		},
		Orchestrated: true,
		Lead:         "cto",
	})

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"v2-14-0", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --json: %v", err)
	}
	env := decodeJSONEnvelope[teamPlan](t, stdout)
	var worker *teamPlanMember
	for i := range env.Data.Plan {
		if env.Data.Plan[i].Role == "fullstack" {
			worker = &env.Data.Plan[i]
			break
		}
	}
	if worker == nil {
		t.Fatalf("fullstack worker missing from plan: %+v", env.Data.Plan)
	}
	if worker.Bootstrap != "appended" {
		t.Fatalf("worker bootstrap = %q, want appended", worker.Bootstrap)
	}
	joinedArgs := strings.Join(worker.ChildArgs, " ")
	for _, want := range []string{"--permission-mode auto", "--allowedTools", "Bash(gh pr create:*)"} {
		if !strings.Contains(joinedArgs, want) {
			t.Fatalf("worker child_args missing %q: %v", want, worker.ChildArgs)
		}
		if !strings.Contains(worker.Command, want) {
			t.Fatalf("worker command missing %q: %s", want, worker.Command)
		}
	}
	if len(worker.PreauthorizedActions) != 1 || !strings.Contains(worker.PreauthorizedActions[0], "gh pr create") {
		t.Fatalf("worker preauthorized_actions = %v, want gh pr create", worker.PreauthorizedActions)
	}
}

func TestUpDryRunCrossCWDClaudeWorkerPreauthMatchesLiveLaunch(t *testing.T) {
	teamHome := t.TempDir()
	workerDir := t.TempDir()
	chdir(t, teamHome)
	if err := team.Write(teamHome, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "v2-14-0"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "v2-14-0", CWD: workerDir},
		},
		Orchestrated: true,
		Lead:         "cto",
	}); err != nil {
		t.Fatal(err)
	}

	jsonOut, _, err := captureOutput(t, func() error {
		return runUp([]string{"v2-14-0", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --json: %v", err)
	}
	env := decodeJSONEnvelope[teamPlan](t, jsonOut)
	var worker *teamPlanMember
	for i := range env.Data.Plan {
		if env.Data.Plan[i].Role == "fullstack" {
			worker = &env.Data.Plan[i]
			break
		}
	}
	if worker == nil {
		t.Fatalf("fullstack worker missing from plan: %+v", env.Data.Plan)
	}
	if worker.CWD != workerDir {
		t.Fatalf("worker cwd = %q, want %q", worker.CWD, workerDir)
	}
	planTeamHome := env.Data.TeamHome
	if worker.Bootstrap != "appended" || !strings.Contains(worker.Command, "--allowedTools") || !strings.Contains(worker.Command, "--team-home "+planTeamHome) {
		t.Fatalf("preview did not show cross-cwd preauth+bootstrap correctly: %+v", *worker)
	}

	textOut, _, err := captureOutput(t, func() error {
		return runUp([]string{"v2-14-0", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("up --dry-run text: %v", err)
	}
	for _, want := range []string{"#    bootstrap: appended", "#    launcher-added args: --allowedTools 'Bash(gh pr create:*)'"} {
		if !strings.Contains(textOut, want) {
			t.Fatalf("text dry-run missing %q in:\n%s", want, textOut)
		}
	}

	setupFakeAMQ(t)
	chdir(t, workerDir)
	liveOut, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{
			"--dry-run",
			"--role", "fullstack",
			"--session", "v2-14-0",
			"--team-home", planTeamHome,
			"claude",
			"--",
			"--permission-mode", "auto",
			"--allowedTools", "Bash(gh pr create:*)",
		})
	})
	if err != nil {
		t.Fatalf("runLaunch cross-cwd dry-run: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"--allowedTools", "Bash(gh pr create:*)", "You are a fresh amq-squad agent."} {
		if !strings.Contains(liveOut, want) {
			t.Fatalf("live cross-cwd dry-run missing %q in:\n%s", want, liveOut)
		}
	}
}

func TestRunUpProjectDryRunTargetsOtherDir(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	if err := team.Write(project, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--project", project, "--dry-run", "--no-bootstrap", "issue-102"})
	})
	if err != nil {
		t.Fatalf("up --project --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	wantProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# team-home: " + wantProject, "# workstream: issue-102", "agent up codex"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("up --project output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunUpProjectSeedFromFileResolvesInsideProject(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	if err := team.Write(project, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "brief-source.md"), []byte("# Project Brief\n\nseeded from project cwd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--project", project, "--dry-run", "--seed-from", "file:brief-source.md", "issue-103"})
	})
	if err != nil {
		t.Fatalf("up --project --seed-from file: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "seeded from project cwd") {
		t.Fatalf("up --project should resolve file: seed paths inside the target project:\n%s", stdout)
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
			{Role: "cto", Binary: "codex", Handle: "cto"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack"},
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
		"--no-gitignore",
		"--wake-inject-via", "/opt/amq-inject",
		"--wake-inject-arg=--pane",
	}
	upFlagSet := append([]string{"--target", "current-window"}, flagSet...)

	showOut, _, err := captureOutput(t, func() error {
		return runTeamShow(flagSet)
	})
	if err != nil {
		t.Fatalf("team show: %v", err)
	}
	upOut, _, err := captureOutput(t, func() error {
		return runUp(append([]string{"--dry-run"}, upFlagSet...))
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
	if !strings.Contains(upOut, "--no-gitignore") {
		t.Errorf("--no-gitignore not applied: %s", upOut)
	}
	if !strings.Contains(upOut, "--codex-args='--enable goals --profile fast'") {
		t.Errorf("--codex-args not merged: %s", upOut)
	}
	if !strings.Contains(upOut, "--claude-args=--chrome") {
		t.Errorf("--claude-args not applied: %s", upOut)
	}
	if !strings.Contains(upOut, "--wake-inject-via /opt/amq-inject") || !strings.Contains(upOut, "--wake-inject-arg=--pane") {
		t.Errorf("wake injector flags not applied: %s", upOut)
	}
}

func TestUpDryRunVisibilityChangesPreviewTopology(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-238"}},
	})

	siblingOut, _, err := captureOutput(t, func() error {
		return runUp([]string{"issue-238", "--dry-run", "--visibility", "sibling-tabs"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --visibility sibling-tabs: %v", err)
	}
	detachedOut, _, err := captureOutput(t, func() error {
		return runUp([]string{"issue-238", "--dry-run", "--visibility", "detached"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --visibility detached: %v", err)
	}
	if siblingOut == detachedOut {
		t.Fatalf("sibling-tabs and detached dry-runs must differ:\n%s", siblingOut)
	}
	for _, want := range []string{
		"# visibility: sibling-tabs",
		"# launch:    amq-squad up issue-238 --visibility sibling-tabs",
	} {
		if !strings.Contains(siblingOut, want) {
			t.Fatalf("sibling-tabs preview missing %q:\n%s", want, siblingOut)
		}
	}
	for _, want := range []string{
		"# visibility: detached",
		"# launch:    amq-squad up issue-238 --visibility detached",
	} {
		if !strings.Contains(detachedOut, want) {
			t.Fatalf("detached preview missing %q:\n%s", want, detachedOut)
		}
	}
}

func TestUpDryRunVisibilityPlanIsPreviewOnly(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-238"}},
	})

	out, _, err := captureOutput(t, func() error {
		return runUp([]string{"issue-238", "--dry-run", "--visibility", "plan"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --visibility plan: %v", err)
	}
	for _, want := range []string{
		"# visibility: plan",
		"# launch:    amq-squad up issue-238 --visibility sibling-tabs --dry-run",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan preview missing %q:\n%s", want, out)
		}
	}
}

func TestUpDryRunJSONCarriesVisibilityTopology(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-238"}},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"issue-238", "--dry-run", "--json", "--visibility", "current"})
	})
	if err != nil {
		t.Fatalf("up --dry-run --json --visibility current: %v", err)
	}
	env := decodeJSONEnvelope[teamPlan](t, stdout)
	if env.Data.Visibility != "current" {
		t.Fatalf("visibility = %q, want current", env.Data.Visibility)
	}
	if env.Data.LaunchCommand != "amq-squad up issue-238 --visibility current" {
		t.Fatalf("launch_command = %q", env.Data.LaunchCommand)
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

// TestRunUpLiveDelegatesToBackendLaunch proves bare `up` reaches the same
// backend.Launch path as `team launch`, with the preview path untouched.
func TestRunUpLiveDelegatesToBackendLaunch(t *testing.T) {
	backend := useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	})

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if got := len(backend.launches); got != 1 {
		t.Fatalf("backend.Launch calls = %d, want 1", got)
	}
	if got := len(backend.dryRuns); got != 0 {
		t.Fatalf("backend.DryRun calls = %d, want 0 (live up must not invoke DryRun)", got)
	}
	opts := backend.launches[0]
	if opts.DryRun {
		t.Error("live up must not set opts.DryRun")
	}
	if opts.Terminal != "fake" {
		t.Errorf("opts.Terminal = %q, want fake", opts.Terminal)
	}
	if !opts.NoBootstrap {
		t.Error("--no-bootstrap not propagated to opts.NoBootstrap")
	}
}

// TestRunUpLiveHonorsBackendFlags asserts every backend-specific live flag
// lands in teamLaunchOptions before Launch is called.
func TestRunUpLiveHonorsBackendFlags(t *testing.T) {
	backend := useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-97"},
		},
	})

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{
			"--terminal", "fake",
			"--target", "new-session",
			"--layout", "tiled",
			"--terminal-session", "squad",
			"--stagger", "200ms",
			"--session", "issue-97",
			"--fresh",
			"--no-bootstrap",
			"--trust", "trusted",
			"--model", "cto=gpt-5",
			"--codex-args=--profile fast",
			"--claude-args=--chrome",
			"--force-duplicate",
			"--wake-inject-via", "/opt/amq-inject",
			"--wake-inject-arg=--pane",
			"--no-attach",
		})
	})
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("backend.Launch calls = %d, want 1", len(backend.launches))
	}
	opts := backend.launches[0]
	if opts.Target != "new-session" {
		t.Errorf("Target = %q, want new-session", opts.Target)
	}
	if opts.Layout != "tiled" {
		t.Errorf("Layout = %q, want tiled", opts.Layout)
	}
	if opts.TerminalSession != "squad" {
		t.Errorf("TerminalSession = %q, want squad", opts.TerminalSession)
	}
	if opts.Stagger.String() != "200ms" {
		t.Errorf("Stagger = %s, want 200ms", opts.Stagger)
	}
	if opts.Workstream != "issue-97" {
		t.Errorf("Workstream = %q, want issue-97", opts.Workstream)
	}
	// --fresh is reconciled to a no-op on `up`: refuse-existing is now the
	// default gate, so opts.Fresh must never be set from the up path even when
	// --fresh is passed.
	if opts.Fresh {
		t.Error("--fresh must be a no-op on up; opts.Fresh should stay false")
	}
	if !opts.NoBootstrap {
		t.Error("--no-bootstrap not propagated")
	}
	if opts.Trust != trustModeTrusted {
		t.Errorf("Trust = %q, want trusted", opts.Trust)
	}
	if opts.ModelOverrides["cto"] != "gpt-5" {
		t.Errorf("ModelOverrides[cto] = %q, want gpt-5", opts.ModelOverrides["cto"])
	}
	if got := opts.BinaryArgs["codex"]; len(got) == 0 || got[len(got)-1] != "fast" {
		t.Errorf("codex BinaryArgs = %v, want trailing 'fast'", got)
	}
	if got := opts.BinaryArgs["claude"]; len(got) != 1 || got[0] != "--chrome" {
		t.Errorf("claude BinaryArgs = %v, want [--chrome]", got)
	}
	if !opts.ForceDuplicate {
		t.Error("--force-duplicate not propagated")
	}
	if opts.WakeInjectVia != "/opt/amq-inject" {
		t.Errorf("WakeInjectVia = %q, want /opt/amq-inject", opts.WakeInjectVia)
	}
	if got := opts.WakeInjectArgs; len(got) != 1 || got[0] != "--pane" {
		t.Errorf("WakeInjectArgs = %v, want [--pane]", got)
	}
}

// TestRunUpDryRunDoesNotCallBackend guards the contract that --dry-run on
// `up` is the launch-command preview, never the tmux backend dry-run.
func TestRunUpDryRunDoesNotCallBackend(t *testing.T) {
	backend := useFakeBackend(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--terminal", "fake", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v", err)
	}
	if got := len(backend.launches) + len(backend.dryRuns); got != 0 {
		t.Fatalf("up --dry-run must not call backend; got %d Launch + %d DryRun", len(backend.launches), len(backend.dryRuns))
	}
}

// TestRunUpLiveRefusesExistingSessionByDefault proves the new default: plain
// `up` against a session whose AMQ root already exists is REFUSED (no --fresh
// needed) with the state-aware resume/--reset next-step hint, and the backend
// is never invoked.
func TestRunUpLiveRefusesExistingSessionByDefault(t *testing.T) {
	backend := useFakeBackend(t)
	base := setupFakeAMQSessionRoots(t)
	if err := os.MkdirAll(base+"/issue-97", 0o755); err != nil {
		t.Fatal(err)
	}
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-97", "--no-bootstrap"})
	})
	if err == nil {
		t.Fatalf("plain up against an existing session must be refused")
	}
	for _, want := range []string{
		`session "issue-97" already exists`,
		"amq-squad resume",
		"amq-squad up --reset",
		"pick a new name",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal missing %q in: %v", want, err)
		}
	}
	if len(backend.launches) != 0 {
		t.Errorf("refused up must not launch; got %d", len(backend.launches))
	}
}

// TestRunUpLiveFreshIsNoOp proves --fresh is reconciled to an accepted no-op:
// it prints a one-line hint and does not change the refuse-existing behavior.
func TestRunUpLiveFreshIsNoOp(t *testing.T) {
	backend := useFakeBackend(t)
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: ""}},
	})

	_, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake", "--session", "issue-101", "--fresh", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --fresh on a new session: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("up --fresh on a new session should launch once; got %d", len(backend.launches))
	}
	if !strings.Contains(stderr, "--fresh is now the default") {
		t.Errorf("expected --fresh no-op hint on stderr:\n%s", stderr)
	}
}

func TestRunUpLiveRequiresTeam(t *testing.T) {
	useFakeBackend(t)
	dir := t.TempDir()
	chdir(t, dir)
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--terminal", "fake"})
	})
	if err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("bare up without team config: got %v, want 'no team configured' error", err)
	}
}

// TestRunUpDryRunMixedSessionFiltersOutCrossSessionMembers verifies that
// up --dry-run applies the session filter (matching team launch --dry-run),
// closing the gap filed in #177.
func TestRunUpDryRunMixedSessionFiltersOutCrossSessionMembers(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "go-dev", Binary: "claude", Handle: "go-dev", Session: "v2-4-0"},
			{Role: "architect", Binary: "codex", Handle: "architect", Session: "v2-4-0"},
			{Role: "pm-copilot", Binary: "claude", Handle: "pm-copilot", Session: "pm-copilot"},
		},
	})
	stdout, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap", "v2-4-0"})
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "pm-copilot") {
		t.Errorf("up --dry-run should not include cross-session member pm-copilot:\n%s", stdout)
	}
	for _, want := range []string{"--me go-dev", "--me architect"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("up --dry-run missing session member %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "skipping pm-copilot") {
		t.Errorf("stderr missing skip notice for pm-copilot:\n%s", stderr)
	}
}

// TestRunUpDryRunAllCrossSessionErrors verifies that up --dry-run returns an
// error when no members match the target session (mirrors the live-launch
// behavior added in #170).
func TestRunUpDryRunAllCrossSessionErrors(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "go-dev", Binary: "claude", Handle: "go-dev", Session: "alpha"},
			{Role: "architect", Binary: "codex", Handle: "architect", Session: "alpha"},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap", "beta"})
	})
	if err == nil || !strings.Contains(err.Error(), `no team members are pinned to session "beta"`) {
		t.Fatalf("up --dry-run all-cross-session: got %v, want 'no team members' error", err)
	}
}
