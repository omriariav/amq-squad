package cli

import (
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func fullyManagedLaunchRecord(project, baseRoot, root, profile, session string, member team.Member, pid int, tmux *launch.TmuxInfo) launch.Record {
	record := launch.Record{
		CWD: member.EffectiveCWD(project), Binary: member.Binary, Session: session, Handle: member.Handle, Role: member.Role,
		Root: root, BaseRoot: baseRoot, TeamProfile: profile, TeamHome: project, AgentPID: pid,
	}
	if tmux != nil {
		record.AdoptionMode = "managed_window"
		record.Tmux = tmux
		record.Terminal = launch.TerminalInfoFromTmux(tmux)
	}
	return record
}

// swapPaneCloser replaces the paneCloser seam with a recorder and restores it.
func swapPaneCloser(t *testing.T) *[]string {
	t.Helper()
	var closed []string
	prev := paneCloser
	paneCloser = func(id string) error {
		closed = append(closed, id)
		return nil
	}
	t.Cleanup(func() { paneCloser = prev })
	return &closed
}

// swapPaneInspectorMatching makes the identity-checked close see each pane id as
// a LIVE pane carrying the matching amq title token (amq:<session>:<role>), so a
// safe close validates and proceeds. Ids not in idRole resolve to "gone".
func swapPaneInspectorMatching(t *testing.T, session string, idRole map[string]string) {
	t.Helper()
	prev := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		role, ok := idRole[id]
		if !ok {
			return tmuxpane.TmuxPane{}, false
		}
		return tmuxpane.TmuxPane{PaneID: id, Title: paneTitleToken(session, role)}, true
	}
	t.Cleanup(func() { statusPaneInspector = prev })
}
