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
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
