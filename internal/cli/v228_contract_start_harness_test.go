package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// Shared hermetic harness for the tranche-2 criteria that drive `start`
// (AC11 legacy-namespace recovery, AC12 roster changes).
//
// Names are deliberately distinct from the tranche-1 crash-injection file's
// v228Crash* helpers: that file is behind the v228seam build tag until step 3
// removes it, and both files must coexist in one package afterward.

// v228StartFixture is a project with a profile, team rules, and a canonical root.
type v228StartFixture struct {
	Project string
	Profile string
	Session string
	Root    string
}

func v228NewStartFixture(t *testing.T, session string, members []team.Member) v228StartFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const profile = "v228"
	v228SeedProfile(t, project, profile, session, members)
	if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(project), []byte("# team rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return v228StartFixture{
		Project: project, Profile: profile, Session: session,
		Root: v228CanonicalRoot(project, profile, session),
	}
}

func v228StartMembers(session string, roles ...string) []team.Member {
	members := make([]team.Member, 0, len(roles))
	for _, role := range roles {
		members = append(members, team.Member{Role: role, Binary: "codex", Handle: role, Session: session})
	}
	return members
}

// v228StartRun is one `start` invocation and what the fake backend observed.
type v228StartRun struct {
	Err         error
	Output      string
	SpawnedRole []string
}

// v228RunStart drives the real start command with a fake tmux backend. Roles
// already recorded alive stay alive; the backend spawns only what start asks
// for, recording pane ownership the way a real launch does.
//
// alive maps pid -> live. It is mutated as the fake backend "spawns", and the
// caller keeps the map so a second start sees the first one's agents as live.
func v228RunStart(t *testing.T, fixture v228StartFixture, alive map[int]bool, pidFor func(role string) int) v228StartRun {
	t.Helper()
	run := v228StartRun{}
	var mu sync.Mutex
	probeAlive := func(pid int) bool {
		mu.Lock()
		defer mu.Unlock()
		return alive[pid]
	}

	var out bytes.Buffer
	deps := simpleStartDependencies{
		LookPath: func(name string) (string, error) { return filepath.Join("/usr/bin", name), nil },
		ResolveAMQEnv: func(string, string, string, string) (amqEnv, error) {
			return amqEnv{Root: fixture.Root, BaseRoot: filepath.Dir(fixture.Root), AMQVersion: doctorMinAMQVersion}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{
			PIDAlive:     probeAlive,
			ProcessMatch: func(pid int, _ func(string) bool) bool { return probeAlive(pid) },
			ProcessTTY:   func(int) (string, bool) { return "", false },
			Now:          func() time.Time { return v228Now },
		},
		RuntimeProbe: launchRuntimeProbe{
			PIDAlive:     probeAlive,
			ProcessMatch: func(int, func(string) bool) bool { return true },
			ProcessTTY:   func(int) (string, bool) { return "", false },
		},
		Launch: func(spawn team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
			result := teamLaunchResult{}
			for i, member := range spawn.Members {
				pid := pidFor(member.Role)
				tmuxInfo := &launch.TmuxInfo{
					Session:  fixture.Session,
					WindowID: fmt.Sprintf("@%d", 300+i),
					PaneID:   fmt.Sprintf("%%%d", 400+pid%100),
					Target:   opts.Target,
				}
				agentDir := filepath.Join(fixture.Root, "agents", member.Handle)
				if err := os.MkdirAll(agentDir, 0o755); err != nil {
					return teamLaunchResult{}, err
				}
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
				mu.Lock()
				alive[pid] = true
				mu.Unlock()
				run.SpawnedRole = append(run.SpawnedRole, member.Role)
				result.Panes = append(result.Panes, teamLaunchResultPane{
					Role: member.Role, PaneID: tmuxInfo.PaneID, WindowID: tmuxInfo.WindowID,
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
