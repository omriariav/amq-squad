package cli

import (
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/tmuxpane"
)

type eventTerminator struct {
	events *[]string
	err    error
}

func (t eventTerminator) Terminate(int) error {
	*t.events = append(*t.events, "signal")
	return t.err
}

func (eventTerminator) SignalName() string { return "SIGTERM" }

func completeDownPaneFixture(t *testing.T) (team.Team, team.Member, launch.Record, tmuxpane.TmuxPane, string) {
	t.Helper()
	base := setupFakeAMQSessionRoots(t)
	project := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-465"}}})
	member := team.Member{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-465", CWD: project}
	configured := team.Team{Project: project, Members: []team.Member{member}}
	tmux := &launch.TmuxInfo{Session: "mux-465", WindowID: "@7", WindowName: "cto", PaneID: "%9", Target: "new-window"}
	record := launch.Record{
		CWD: project, Binary: "codex", Session: "issue-465", Handle: "cto", Role: "cto",
		Root: filepath.Join(base, "issue-465"), BaseRoot: base, TeamProfile: team.DefaultProfile, TeamHome: project,
		AdoptionMode: "managed_window", AgentPID: 4242, Tmux: tmux, Terminal: launch.TerminalInfoFromTmux(tmux),
	}
	seedAgentRecord(t, base, "issue-465", "cto", record)
	pane := tmuxpane.TmuxPane{Session: tmux.Session, WindowID: tmux.WindowID, PaneID: tmux.PaneID, PID: 100, CWD: project}
	return configured, member, record, pane, project
}
