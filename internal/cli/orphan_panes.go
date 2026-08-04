package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

type orphanPane struct {
	PaneID  string
	Title   string
	Session string
	Role    string
}

func liveLaunchPaneTokens(projectDir, baseRoot string) (map[string]bool, error) {
	entries, err := launch.ScanEntriesInRoot(projectDir, baseRoot)
	if err != nil {
		return nil, fmt.Errorf("scan launch records: %w", err)
	}
	out := map[string]bool{}
	for _, e := range entries {
		if e.Record.Tmux == nil {
			continue
		}
		paneID := strings.TrimSpace(e.Record.Tmux.PaneID)
		session := strings.TrimSpace(e.Record.Session)
		role := strings.TrimSpace(e.Record.Role)
		if role == "" {
			role = strings.TrimSpace(e.Record.Handle)
		}
		if paneID == "" || session == "" || role == "" {
			continue
		}
		out[launchPaneKey(paneID, paneTitleToken(session, role))] = true
	}
	return out, nil
}

func findOrphanPanes(panes []tmuxpane.TmuxPane, liveRecords map[string]bool, sessionFilter string) []orphanPane {
	var out []orphanPane
	for _, p := range panes {
		session, role, ok := parsePaneTitleToken(p.Title)
		if !ok {
			continue
		}
		if sessionFilter != "" && session != sessionFilter {
			continue
		}
		paneID := strings.TrimSpace(p.PaneID)
		if paneID == "" {
			continue
		}
		if liveRecords[launchPaneKey(paneID, p.Title)] {
			continue
		}
		out = append(out, orphanPane{
			PaneID:  paneID,
			Title:   p.Title,
			Session: session,
			Role:    role,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Session != out[j].Session {
			return out[i].Session < out[j].Session
		}
		if out[i].Role != out[j].Role {
			return out[i].Role < out[j].Role
		}
		return out[i].PaneID < out[j].PaneID
	})
	return out
}

func launchPaneKey(paneID, title string) string {
	return strings.TrimSpace(paneID) + "\x00" + strings.TrimSpace(title)
}

func parsePaneTitleToken(title string) (session, role string, ok bool) {
	parts := strings.Split(strings.TrimSpace(title), ":")
	if len(parts) != 3 || parts[0] != "amq" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}
