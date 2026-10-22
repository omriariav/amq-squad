package cli

import (
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
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
