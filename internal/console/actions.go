package console

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/runtimeaction"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

type paletteAction = runtimeaction.Action

func (m Model) actionPalette() []paletteAction {
	sel, ok := m.selectedRow()
	if !ok {
		return nil
	}
	switch {
	case m.route == routeBoard && sel.kind == rowSession:
		s, ok := sessionByKey(m.snapshot, sel.ID)
		return runtimeaction.Session(m.rebuild.ProjectDir, sessionProfile(s, ok), sel.ID, firstSessionTmux(m.snapshot, sel.ID))
	case sel.kind == rowAgent:
		s, sok := m.currentSession()
		handle := strings.TrimPrefix(sel.ID, "agent:")
		a := agentByHandle(s, sok, handle)
		role := handle
		if strings.TrimSpace(a.Role) != "" {
			role = a.Role
		}
		return runtimeaction.Member(m.rebuild.ProjectDir, agentProfile(s, sok, a), m.session, role, agentPaneAlive(a))
	case sel.kind == rowThread:
		s, ok := m.currentSession()
		return runtimeaction.Thread(m.rebuild.ProjectDir, sessionProfile(s, ok), m.session, strings.TrimPrefix(sel.ID, "thread:"))
	default:
		s, ok := m.currentSession()
		return runtimeaction.Session(m.rebuild.ProjectDir, sessionProfile(s, ok), m.session, firstSessionTmux(m.snapshot, m.session))
	}
}

func agentPaneAlive(a state.Agent) bool {
	return a.Tmux != nil && a.Tmux.PaneAlive
}

func agentProfile(s state.Session, ok bool, a state.Agent) string {
	if strings.TrimSpace(a.TeamProfile) != "" {
		return a.TeamProfile
	}
	return sessionProfile(s, ok)
}

func sessionProfile(s state.Session, ok bool) string {
	if !ok {
		return ""
	}
	for _, a := range s.Agents {
		if strings.TrimSpace(a.TeamProfile) != "" {
			return a.TeamProfile
		}
	}
	return ""
}

func sessionByKey(snap state.Snapshot, key string) (state.Session, bool) {
	for _, s := range snap.Sessions {
		if sessionKey(s) == key {
			return s, true
		}
	}
	return state.Session{}, false
}

func firstSessionTmux(snap state.Snapshot, session string) string {
	for _, s := range snap.Sessions {
		if sessionKey(s) != session {
			continue
		}
		for _, a := range s.Agents {
			if a.Tmux != nil && a.Tmux.PaneAlive && strings.TrimSpace(a.Tmux.Session) != "" {
				return a.Tmux.Session
			}
		}
	}
	return ""
}

func renderActionLine(a paletteAction, width int) string {
	status := "available"
	if !a.Available {
		status = "unavailable: " + a.Reason
	}
	line := fmt.Sprintf("  %-11s %-18s %s", a.Kind, status, a.Command)
	if width > 0 && len([]rune(line)) > width {
		return wrapLine(line, width)
	}
	return line
}

func wrapLine(line string, width int) string {
	if width < 32 {
		width = 32
	}
	rs := []rune(line)
	if len(rs) <= width {
		return line
	}
	var b strings.Builder
	for len(rs) > width {
		b.WriteString(string(rs[:width]))
		b.WriteRune('\n')
		b.WriteString("      ")
		rs = rs[width:]
	}
	b.WriteString(string(rs))
	return b.String()
}
