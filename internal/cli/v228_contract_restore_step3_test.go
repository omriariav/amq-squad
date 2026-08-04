// AC14: a restored agent resumes its RECORDED conversation, so it continues with
// its accumulated context. A blank respawn is a failed restore.
//
// Bound to the step-3 seams:
//   - teamLaunchResultPane.ChildCommand — every backend copies it from the exact
//     per-role teamLaunchPane.Command it dispatched;
//   - teamLaunchOptions.RestoreConversations, which simple start populates from
//     each stopped role's recorded Conversation and buildTeamLaunchPanes threads
//     into the composed command;
//   - simple start's own validation of the returned command against the
//     recorded-conversation / no-bootstrap contract, which must fail the role
//     BEFORE any "started" report.
//
// The fake backend below composes its result from buildTeamLaunchPanes, so the
// command under assertion is the PRODUCTION-composed one, not a test fixture's
// idea of it.
//
// Gated behind the v228step3 build tag rather than a placeholder: ChildCommand
// lands on senior-dev's branch, and a tag keeps the assertions bound to the real
// field instead of to a local stand-in that would need rewriting at merge.
// P3 INTEGRATION STEP: delete the //go:build line above. No skips to remove.
package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func v228ChildCommandForRole(result teamLaunchResult, role string) (string, bool) {
	for _, pane := range result.Panes {
		if pane.Role == role {
			return pane.ChildCommand, true
		}
	}
	return "", false
}

// v228RestoreBackend is a fake tmux backend that reports the production-composed
// per-role command. mangle lets a test corrupt exactly one role's command to
// exercise start's own validation.
type v228RestoreBackend struct {
	Fixture      v228StartFixture
	PIDs         map[string]int
	Alive        map[int]bool
	Conversation map[string]string
	Mangle       func(role, command string) string
	Captured     teamLaunchResult
}

func (b *v228RestoreBackend) launch(spawn team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
	composed := buildTeamLaunchPanes(spawn, opts)
	commandFor := map[string]string{}
	for _, pane := range composed {
		commandFor[pane.Role] = pane.Command
	}
	result := teamLaunchResult{}
	for i, member := range spawn.Members {
		pid := b.PIDs[member.Role]
		info := &launch.TmuxInfo{
			Session: b.Fixture.Session, WindowID: fmt.Sprintf("@%d", 900+i),
			PaneID: fmt.Sprintf("%%%d", 910+i), Target: opts.Target,
		}
		agentDir := filepath.Join(b.Fixture.Root, "agents", member.Handle)
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			return teamLaunchResult{}, err
		}
		if err := launch.Write(agentDir, launch.Record{
			Schema: launch.SchemaVersion, Binary: member.Binary,
			Role: member.Role, Handle: member.Handle, Session: b.Fixture.Session,
			TeamProfile: b.Fixture.Profile, TeamHome: b.Fixture.Project, CWD: b.Fixture.Project,
			Root: b.Fixture.Root, BaseRoot: filepath.Dir(b.Fixture.Root),
			Conversation: b.Conversation[member.Role],
			Trust:        opts.Trust, ToolProfile: team.ToolProfileFull,
			AgentPID: pid, StartedAt: v228Now,
			Tmux: info, Terminal: launch.TerminalInfoFromTmux(info),
		}); err != nil {
			return teamLaunchResult{}, err
		}
		b.Alive[pid] = true
		command := commandFor[member.Role]
		if b.Mangle != nil {
			command = b.Mangle(member.Role, command)
		}
		result.Panes = append(result.Panes, teamLaunchResultPane{
			Role: member.Role, PaneID: info.PaneID, WindowID: info.WindowID, ChildCommand: command,
		})
	}
	b.Captured = result
	return result, nil
}

func (b *v228RestoreBackend) deps() simpleStartDependencies {
	probeAlive := func(pid int) bool { return b.Alive[pid] }
	return simpleStartDependencies{
		LookPath: func(name string) (string, error) { return filepath.Join("/usr/bin", name), nil },
		ResolveAMQEnv: func(string, string, string, string) (amqEnv, error) {
			return amqEnv{Root: b.Fixture.Root, BaseRoot: filepath.Dir(b.Fixture.Root), AMQVersion: doctorMinAMQVersion}, nil
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
		Launch:       b.launch,
		StartWatcher: func(team.Team, string, string, string) error { return nil },
		Sleep:        func(time.Duration) {},
	}
}

// v228SeedExitedAgent records an agent that has exited: a recorded conversation,
// a dead pid, a dead pane. That is "stopped" to the reconciler, so start respawns.
func v228SeedExitedAgent(t *testing.T, fixture v228StartFixture, role, conversation string, deadPID int) {
	t.Helper()
	pane := &launch.TmuxInfo{Session: fixture.Session, WindowID: "@8", PaneID: "%81", Target: "new-session"}
	v228SeedLiveRecord(t, fixture.Root, launch.Record{
		Schema: launch.SchemaVersion, Binary: "codex",
		Role: role, Handle: role, Session: fixture.Session,
		TeamProfile: fixture.Profile, TeamHome: fixture.Project, CWD: fixture.Project,
		Root: fixture.Root, BaseRoot: filepath.Dir(fixture.Root),
		Conversation: conversation,
		AgentPID:     deadPID, StartedAt: v228Now.Add(-time.Hour),
		Tmux: pane, Terminal: launch.TerminalInfoFromTmux(pane),
	})
}

func TestV228ContractRestoredAgentRetainsPreExitContext(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	const (
		session      = "ac14"
		role         = "dev"
		conversation = "conv-ac14-recorded"
	)
	fixture := v228NewStartFixture(t, session, v228StartMembers(session, "cto", role))
	v228SeedExitedAgent(t, fixture, role, conversation, 6201)

	backend := &v228RestoreBackend{
		Fixture:      fixture,
		PIDs:         map[string]int{"cto": 6203, role: 6202},
		Alive:        map[int]bool{},
		Conversation: map[string]string{role: conversation},
	}
	var out bytes.Buffer
	if err := runStartWithDependencies([]string{
		"--project", fixture.Project, "--profile", fixture.Profile, "--session", session,
		"--terminal", "tmux", "--yes",
	}, backend.deps(), strings.NewReader(""), &out); err != nil {
		t.Fatalf("start restoring an exited agent: %v\n%s", err, out.String())
	}

	command, ok := v228ChildCommandForRole(backend.Captured, role)
	if !ok {
		t.Fatalf("no result pane for %s", role)
	}
	if strings.TrimSpace(command) == "" {
		t.Fatal("result pane carries no ChildCommand; the dispatched command must be reported")
	}
	// The respawn reuses the RECORDED conversation rather than minting a session.
	if !strings.Contains(command, conversation) {
		t.Errorf("restored child command lost the recorded conversation %q:\n%s", conversation, command)
	}
	if !strings.Contains(command, "--conversation") {
		t.Errorf("restored child command carries no --conversation flag:\n%s", command)
	}
	// Bootstrap must NOT be re-appended for a resumed conversation: the agent
	// already holds the brief, and re-running bootstrap clobbers that context.
	for _, bootstrapMarker := range []string{"team-rules.md", "your brief at"} {
		if strings.Contains(command, bootstrapMarker) {
			t.Errorf("resumed restore re-appended the startup instruction (%q):\n%s", bootstrapMarker, command)
		}
	}
	// The fresh sibling is the control: no conversation, and it DOES get bootstrapped.
	if sibling, ok := v228ChildCommandForRole(backend.Captured, "cto"); ok {
		if strings.Contains(sibling, "--conversation") {
			t.Errorf("fresh role cto was handed a conversation:\n%s", sibling)
		}
		if !strings.Contains(sibling, "team-rules.md") {
			t.Errorf("fresh role cto did not get the startup instruction:\n%s", sibling)
		}
	}
	// The persisted record keeps the id, so the NEXT restore works too.
	rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Conversation != conversation {
		t.Errorf("restored record conversation = %q, want %q", rec.Conversation, conversation)
	}
}

// A respawn whose dispatched command drops the recorded conversation is a FAILED
// restore: start must refuse the role before reporting success. A blank agent
// looks healthy from the outside, so silence here is the whole danger.
func TestV228ContractRestoreWithoutConversationFailsBeforeStarted(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	const (
		session      = "ac14"
		role         = "dev"
		conversation = "conv-ac14-dropped"
	)
	fixture := v228NewStartFixture(t, session, v228StartMembers(session, "cto", role))
	v228SeedExitedAgent(t, fixture, role, conversation, 6211)

	backend := &v228RestoreBackend{
		Fixture:      fixture,
		PIDs:         map[string]int{"cto": 6213, role: 6212},
		Alive:        map[int]bool{},
		Conversation: map[string]string{role: conversation},
		// The backend dispatched a command that lost the conversation binding.
		Mangle: func(mangledRole, command string) string {
			if mangledRole != role {
				return command
			}
			return strings.ReplaceAll(command, conversation, "")
		},
	}
	var out bytes.Buffer
	err := runStartWithDependencies([]string{
		"--project", fixture.Project, "--profile", fixture.Profile, "--session", session,
		"--terminal", "tmux", "--yes",
	}, backend.deps(), strings.NewReader(""), &out)
	if err == nil {
		t.Fatalf("start accepted a blank respawn of a recorded conversation:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), role) {
		t.Errorf("failure does not name the failed role: %v", err)
	}
	if strings.Contains(out.String(), "started "+session) {
		t.Errorf("start reported success before failing the restore:\n%s", out.String())
	}
}
