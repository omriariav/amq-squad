// AC2 / AC6 (#644 class) crash-injection contract tests.
//
// This file is written against the REAL Phase 2 seam in simple_start.go:
// runStartWithDependencies, simpleStartDependencies.AfterCheckpoint, the four
// simpleStartCheckpoint* constants, and *simpleStartCheckpointError. Those
// symbols now ship on the simple-start path. The env guard keeps the crash
// suite opt-in until the release contract flips after this deletion step.
//
// Only the announced contract is used. Internal helpers of the simple launcher
// (plan building, record verification) are deliberately not touched: they were
// still being renamed while this was written, and depending on them would make
// the file break on refactors that do not change the seam.
package cli

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var errV228InjectedCrash = errors.New("v2.28 contract: injected launcher crash")

// v228CrashFixture is one project/profile/session with two roles.
type v228CrashFixture struct {
	Project string
	Profile string
	Session string
	Root    string
	Roles   []string
	PIDs    map[string]int
}

func v228NewCrashFixture(t *testing.T) v228CrashFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const (
		profile = "v228"
		session = "ac2"
	)
	roles := []string{"cto", "dev"}
	members := make([]team.Member, 0, len(roles))
	for _, role := range roles {
		members = append(members, team.Member{Role: role, Binary: "codex", Handle: role, Session: session})
	}
	v228SeedProfile(t, project, profile, session, members)
	// start reads the team rules to compose the one startup instruction.
	if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(project), []byte("# team rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return v228CrashFixture{
		Project: project, Profile: profile, Session: session,
		Root:  v228CanonicalRoot(project, profile, session),
		Roles: roles,
		PIDs:  map[string]int{"cto": 4601, "dev": 4602},
	}
}

// v228CrashRun is one `start` invocation plus what the launcher did on the way.
type v228CrashRun struct {
	Err      error
	Output   string
	Launches int
	Reached  []simpleStartCheckpoint
}

// v228RunCrashStart drives the real start command with an abort injected at
// abortAt (empty = no abort). Everything external is stubbed: no tmux server, no
// child processes, no real amq.
//
// The fake Launch stands in for the managed tmux backend, including its two
// checkpoints (pane creation, child dispatch) in the backend's own order, and
// writes the launch records a real backend produces via `agent up`.
func v228RunCrashStart(t *testing.T, fixture v228CrashFixture, abortAt simpleStartCheckpoint) v228CrashRun {
	t.Helper()
	run := v228CrashRun{}
	alive := map[int]bool{}
	for _, role := range fixture.Roles {
		if rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role)); err == nil && rec.AgentPID > 0 {
			// Records surviving an earlier aborted attempt describe live agents.
			alive[rec.AgentPID] = true
		}
	}

	fired := false
	hook := func(checkpoint simpleStartCheckpoint) error {
		run.Reached = append(run.Reached, checkpoint)
		if abortAt != "" && checkpoint == abortAt && !fired {
			fired = true
			return errV228InjectedCrash
		}
		return nil
	}

	var out bytes.Buffer
	deps := simpleStartDependencies{
		AfterCheckpoint: hook,
		LookPath: func(name string) (string, error) {
			return filepath.Join("/usr/bin", name), nil
		},
		ResolveAMQEnv: func(string, string, string, string) (amqEnv, error) {
			return amqEnv{Root: fixture.Root, BaseRoot: filepath.Dir(fixture.Root), AMQVersion: doctorMinAMQVersion}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{
			PIDAlive:     func(pid int) bool { return alive[pid] },
			ProcessMatch: func(pid int, _ func(string) bool) bool { return alive[pid] },
			ProcessTTY:   func(int) (string, bool) { return "", false },
			Now:          func() time.Time { return v228Now },
		},
		RuntimeProbe: launchRuntimeProbe{
			PIDAlive:     func(pid int) bool { return alive[pid] },
			ProcessMatch: func(int, func(string) bool) bool { return true },
			ProcessTTY:   func(int) (string, bool) { return "", false },
		},
		Launch: func(spawn team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
			run.Launches++
			// callSimpleStartCheckpoint is how the real tmux backend reaches the
			// hook, so the abort arrives wrapped exactly as production wraps it.
			if err := callSimpleStartCheckpoint(hook, simpleStartCheckpointPaneCreation); err != nil {
				// The backend preserves created panes/windows: no rollback here
				// either, so a rerun rolls forward over them.
				return teamLaunchResult{}, err
			}
			if err := callSimpleStartCheckpoint(hook, simpleStartCheckpointChildDispatch); err != nil {
				return teamLaunchResult{}, err
			}
			result := teamLaunchResult{}
			for i, member := range spawn.Members {
				pid := fixture.PIDs[member.Role]
				if pid == 0 {
					pid = 4700 + i
				}
				tmuxInfo := &launch.TmuxInfo{
					Session:  fixture.Session,
					WindowID: fmt.Sprintf("@%d", 200+i),
					PaneID:   fmt.Sprintf("%%%d", 100+i),
					Target:   opts.Target,
				}
				agentDir := filepath.Join(fixture.Root, "agents", member.Handle)
				if err := os.MkdirAll(agentDir, 0o755); err != nil {
					return teamLaunchResult{}, err
				}
				// A real launch records pane ownership; start verifies the record
				// owns the pane it was told about, so the fake must too.
				if err := launch.Write(agentDir, launch.Record{
					Schema: launch.SchemaVersion, Binary: member.Binary,
					Role: member.Role, Handle: member.Handle, Session: fixture.Session,
					TeamProfile: fixture.Profile, TeamHome: fixture.Project, CWD: fixture.Project,
					Root: fixture.Root, BaseRoot: filepath.Dir(fixture.Root),
					Trust: opts.Trust, ToolProfile: team.ToolProfileFull,
					AgentPID: pid, StartedAt: v228Now,
					Tmux: tmuxInfo, Terminal: launch.TerminalInfoFromTmux(tmuxInfo),
				}); err != nil {
					return teamLaunchResult{}, err
				}
				alive[pid] = true
				result.Panes = append(result.Panes, teamLaunchResultPane{
					Role:     member.Role,
					PaneID:   tmuxInfo.PaneID,
					WindowID: tmuxInfo.WindowID,
				})
			}
			return result, nil
		},
		StartWatcher: func(team.Team, string, string, string) error { return nil },
	}

	run.Err = runStartWithDependencies([]string{
		"--project", fixture.Project,
		"--profile", fixture.Profile,
		"--session", fixture.Session,
		"--terminal", "tmux",
		"--yes",
	}, deps, strings.NewReader(""), &out)
	run.Output = out.String()
	return run
}

// v228AssertCrashConverges is the shared AC2 body: abort at one checkpoint, rerun
// `start`, converge to all-live, delete nothing.
func v228AssertCrashConverges(t *testing.T, checkpoint simpleStartCheckpoint) {
	t.Helper()
	fixture := v228NewCrashFixture(t)

	crashed := v228RunCrashStart(t, fixture, checkpoint)
	var checkpointErr *simpleStartCheckpointError
	if !errors.As(crashed.Err, &checkpointErr) {
		t.Fatalf("abort at %s returned %v (%T), want *simpleStartCheckpointError", checkpoint, crashed.Err, crashed.Err)
	}
	if checkpointErr.Checkpoint != checkpoint {
		t.Fatalf("abort reported checkpoint %q, want %q", checkpointErr.Checkpoint, checkpoint)
	}
	if !errors.Is(crashed.Err, errV228InjectedCrash) {
		t.Errorf("checkpoint error dropped the injected cause: %v", crashed.Err)
	}

	// What the partial launch left behind. Nothing here may be removed: the #644
	// dead-end was recovery that required deleting state, which re-armed the
	// failure it was recovering from.
	partialRoot := v228InventoryPaths(t, fixture.Root)
	partialSquad := v228InventoryPaths(t, filepath.Join(fixture.Project, team.DirName))
	if len(partialRoot) == 0 {
		t.Fatalf("abort at %s left no namespace state to roll forward over", checkpoint)
	}

	rerun := v228RunCrashStart(t, fixture, "")
	if rerun.Err != nil {
		t.Fatalf("rerun after abort at %s: %v\n%s", checkpoint, rerun.Err, rerun.Output)
	}
	if !strings.Contains(rerun.Output, "AM_ROOT: "+fixture.Root) {
		t.Errorf("rerun did not report the canonical root:\n%s", rerun.Output)
	}

	for path := range partialRoot {
		if !v228InventoryPaths(t, fixture.Root)[path] {
			t.Errorf("rerun after abort at %s deleted %s from the namespace", checkpoint, path)
		}
	}
	afterSquad := v228InventoryPaths(t, filepath.Join(fixture.Project, team.DirName))
	for path := range partialSquad {
		if !afterSquad[path] {
			t.Errorf("rerun after abort at %s deleted %s from .amq-squad", checkpoint, path)
		}
	}

	// Converged: every role has exactly one live record at the canonical root.
	entries, err := launch.ScanEntries(fixture.Project)
	if err != nil {
		t.Fatal(err)
	}
	byRole := map[string]int{}
	for _, entry := range entries {
		if entry.Record.Root != fixture.Root {
			t.Errorf("record for %s sits at %q, want the canonical root %q", entry.Record.Role, entry.Record.Root, fixture.Root)
		}
		byRole[entry.Record.Role]++
	}
	for _, role := range fixture.Roles {
		if byRole[role] != 1 {
			t.Errorf("%s has %d launch records after recovery, want 1", role, byRole[role])
		}
	}

	// A third `start` has nothing left to do: that is "all live" stated by the
	// launcher itself rather than inferred from the records it just wrote.
	settled := v228RunCrashStart(t, fixture, "")
	if settled.Err != nil {
		t.Fatalf("settled rerun after abort at %s: %v\n%s", checkpoint, settled.Err, settled.Output)
	}
	if !strings.Contains(settled.Output, "already started") {
		t.Errorf("third start did not report an already-live squad:\n%s", settled.Output)
	}
	if settled.Launches != 0 {
		t.Errorf("third start spawned %d time(s), want 0 (live roles are kept)", settled.Launches)
	}
}

func TestV228ContractAbortAfterNamespaceCreationConverges(t *testing.T) {
	requireV228Contract(t)
	v228AssertCrashConverges(t, simpleStartCheckpointNamespaceCreation)
}

func TestV228ContractAbortAfterPaneCreationConverges(t *testing.T) {
	requireV228Contract(t)
	v228AssertCrashConverges(t, simpleStartCheckpointPaneCreation)
}

func TestV228ContractAbortAfterChildDispatchConverges(t *testing.T) {
	requireV228Contract(t)
	v228AssertCrashConverges(t, simpleStartCheckpointChildDispatch)
}

func TestV228ContractAbortAfterLaunchRecordWriteConverges(t *testing.T) {
	requireV228Contract(t)
	v228AssertCrashConverges(t, simpleStartCheckpointLaunchRecordWrite)
}

// AC6: a role whose child dies on dispatch must produce a precise per-role
// failure, and a rerun must converge. The launch-record write is what proves the
// child came up, so a backend that dispatches without producing a record is the
// immediate-death case.
func TestV228ContractChildDeathIsPerRoleAndRerunConverges(t *testing.T) {
	requireV228Contract(t)
	fixture := v228NewCrashFixture(t)

	crashed := v228RunCrashStart(t, fixture, simpleStartCheckpointChildDispatch)
	if crashed.Err == nil {
		t.Fatal("a child that never records a launch must fail the start")
	}
	if !strings.Contains(crashed.Err.Error(), string(simpleStartCheckpointChildDispatch)) {
		t.Errorf("failure does not name the checkpoint it happened at: %v", crashed.Err)
	}

	rerun := v228RunCrashStart(t, fixture, "")
	if rerun.Err != nil {
		t.Fatalf("rerun after child death: %v\n%s", rerun.Err, rerun.Output)
	}
	for _, role := range fixture.Roles {
		rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role))
		if err != nil {
			t.Fatalf("no launch record for %s after recovery: %v", role, err)
		}
		if rec.AgentPID != fixture.PIDs[role] {
			t.Errorf("%s recovered with pid %d, want %d", role, rec.AgentPID, fixture.PIDs[role])
		}
	}
}

// The seam must be inert in production: nothing shipped may abort a launch.
func TestV228ContractProductionStartHasNoCheckpointHook(t *testing.T) {
	requireV228Contract(t)
	if defaultSimpleStartDependencies().AfterCheckpoint != nil {
		t.Fatal("production start dependencies carry an AfterCheckpoint hook; it must be nil outside tests")
	}
}
