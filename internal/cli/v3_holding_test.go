package cli

// v3_holding_test.go: test-side companion of v3_holding.go. Shared test
// fixtures moved verbatim from test files deleted in 2a-1 because surviving
// tests still use them. 2a-2 relocates or deletes them with their callers.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// From internal/cli/up_test.go:15-127 (C11 cut): chdir, seedTeam,
// seedTeamWithoutSharedCwdException, fakeBackend and useFakeBackend.
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
//
// Fixtures across this package routinely seed 2+ mutation-capable members at
// the same (default, unset) cwd because they are testing something else
// entirely (goal delivery, external-lead binding, layout, ...), not #497
// worktree isolation. Auto-recording the exception here keeps that huge
// existing surface green; a test that specifically wants to exercise the
// fail-closed collision path uses seedTeamWithoutSharedCwdException instead.
func seedTeam(t *testing.T, cfg team.Team) string {
	t.Helper()
	if cfg.SharedCwdException == "" {
		cfg.SharedCwdException = "test fixture: not exercising #497 worktree isolation"
	}
	return seedTeamWithoutSharedCwdException(t, cfg)
}

// seedTeamWithoutSharedCwdException is seedTeam without the automatic #497
// shared-cwd exception, for tests that specifically exercise the worktree
// isolation readiness check.
func seedTeamWithoutSharedCwdException(t *testing.T, cfg team.Team) string {
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

func (f *fakeBackend) LaunchWithResult(t team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	if err := f.Launch(t, opts); err != nil {
		return teamLaunchResult{}, err
	}
	panes := resolvedTeamLaunchPanes(t, opts)
	result := teamLaunchResult{Panes: make([]teamLaunchResultPane, 0, len(panes))}
	for i, pane := range panes {
		window := "@1"
		if opts.Target == "new-window" {
			window = fmt.Sprintf("@%d", i+1)
		}
		result.Panes = append(result.Panes, teamLaunchResultPane{
			Role: pane.Role, PaneID: fmt.Sprintf("%%%d", i+1), WindowID: window, ChildCommand: pane.Command,
		})
	}
	return result, nil
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

// From internal/cli/down_test.go (C11 cut): recordingTerminator, selfCleaningTerminator,
// fakeMissingWakeCheck, downFakeProbe, seedAgentRecord; per-block source lines below.

// From down_test.go:21-26
type recordingTerminator struct {
	mu     sync.Mutex
	name   string // signal label this fake reports via SignalName; defaults to SIGTERM
	calls  []int
	failOn map[int]error
}

// From down_test.go:53-61
func (r *recordingTerminator) Terminate(pid int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, pid)
	if err, ok := r.failOn[pid]; ok {
		return err
	}
	return nil
}

// From down_test.go:63-68
func (r *recordingTerminator) SignalName() string {
	if r.name == "" {
		return "SIGTERM"
	}
	return r.name
}

// From down_test.go:28-36
// selfCleaningTerminator models AMQ's signal handler: the process exits and
// removes its own lock. Production raw-wake teardown succeeds only after both
// effects are observable; tests that exercise that success path must model
// them explicitly.
type selfCleaningTerminator struct {
	recordingTerminator
	alive     map[int]bool
	wakeLocks map[int]string
}

// From down_test.go:38-51
func (r *selfCleaningTerminator) Terminate(pid int) error {
	if err := r.recordingTerminator.Terminate(pid); err != nil {
		return err
	}
	if r.alive != nil {
		r.alive[pid] = false
	}
	if path := r.wakeLocks[pid]; path != "" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// From down_test.go:765-775
// downFakeProbe implements duplicateLaunchProbe with explicit per-PID liveness and
// binary-match decisions so tests never shell out to ps or send real signals.
func downFakeProbe(alive map[int]bool, match map[int]bool) duplicateLaunchProbe {
	return duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool {
			return match[pid]
		},
		Now: time.Now,
	}
}

// From down_test.go:796-809
// seedAgentRecord writes a launch.json under the AMQ fake-root layout for a
// given workstream and handle. Returns the agent dir so tests can assert on
// resolution side effects.
func seedAgentRecord(t *testing.T, base, workstream, handle string, rec launch.Record) string {
	t.Helper()
	agentDir := filepath.Join(base, workstream, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

// From internal/cli/resume_shared_test.go (C11 cut): writeMemberLaunchRecord, resumeChdir.

// From resume_shared_test.go:16-30
// writeMemberLaunchRecord drops a v0.6 launch.json under the fake AMQ base
// for the given session/handle so a test can seed a member as already
// launched.
func writeMemberLaunchRecord(t *testing.T, base, session, handle string, rec launch.Record) {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Handle = handle
	rec.Session = session
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatalf("write launch record: %v", err)
	}
}

// From resume_shared_test.go:32-46
func resumeChdir(t *testing.T, dir string) {
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

// From internal/cli/simple_start_test.go (C4 cut): validSimpleStartBriefDraft, used by
// brief_test.go (brief --goal, kept).

// From simple_start_test.go:1483-1495
func validSimpleStartBriefDraft(session, goal string, members ...team.Member) string {
	var roster strings.Builder
	for _, member := range members {
		fmt.Fprintf(&roster, "- `%s` (`%s`, `%s`): Own the scoped implementation.\n", member.Role, memberHandle(member), member.Binary)
	}
	return "# " + session + " brief\n\n" +
		"## Goal\n" + goal + "\n\n" +
		"## Source\nGenerated from the operator goal through the configured drafter.\n\n" +
		"## Scope\n- Implement the reviewed change.\n\n" +
		"## Out of scope\n- Do not release or send externally.\n\n" +
		"## Team shape\n" + roster.String() + "\n" +
		"## Acceptance\n- Focused and full validation pass.\n"
}

// From internal/cli/team_lead.go:62-76 (C18 cut). Non-test code with no surviving
// non-test caller; status_test.go (two wake auto-drain tests of kept status code)
// builds its fixture WakeInjectCmd with it, so it is held on the test side.
// wakeDrainInject is the standard instruction amq-squad asks the wake sidecar to
// inject on each durable-message arrival (amq wake --inject-cmd). It re-engages a
// lead or orchestrator even after its active goal reaches a terminal "achieved"
// state: the inbound directive drives an inbox drain through AMQ's sanctioned
// injector instead of a raw tmux send-keys. Shared by lead register --wake (#283)
// and the goal orchestrator registration (#288) so both use one mechanism.
//
// The injected agent shell already owns the correct AM_ME. Only the exact root
// is asserted: unlike --me, --root remains usable while recovering a root whose
// config is absent or malformed.
func wakeDrainInject(root string) string {
	return "amq-squad: a durable AMQ message arrived. Run `amq drain --include-body --root " +
		shellQuote(strings.TrimSpace(root)) +
		"` now and act on the newest item, even if your current goal looks complete. Do not wait to be polled."
}

// From internal/cli/namespace_migration_adapters.go:79-84 (C7 cut). No surviving
// non-test caller; newNamespaceMigrationFixtureProfiles (namespace_migration_test.go,
// fixture of a kept writer-admission test) records it as the fixture's base root.
func namespaceMigrationBaseRoot(ref squadnamespace.Ref) string {
	if squadnamespace.NormalizeProfile(ref.Profile) == team.DefaultProfile {
		return filepath.Dir(ref.AMQRoot)
	}
	return ref.AMQRoot
}
