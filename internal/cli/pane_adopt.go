package cli

import (
	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/procinfo"
	"github.com/omriariav/amq-squad/internal/state"
	"github.com/omriariav/amq-squad/internal/tmuxpane"
)

// childrenPidTree returns a best-effort, fork-free pid->children function for
// the tmux resolver's PID-lineage matching (#95: adopt externally-launched
// panes). Returns nil when the process table cannot be read, in which case the
// resolver degrades to title/cwd/engine matching rather than failing.
func childrenPidTree() func(int) []int {
	tree, err := procinfo.ChildrenIndex()
	if err != nil {
		return nil
	}
	return tree
}

// adoptLivePane resolves a LIVE tmux pane for an agent that has no usable
// recorded pane — e.g. one launched OUTSIDE amq-squad's tmux backend
// (Sagi-style raw `tmux new-window`), whose launch record carries no tmux
// block. It resolves by title token, then cwd+engine, anchored by PID lineage
// (agentPID must live in the pane's process subtree) so an externally-launched
// pane is attributed to the right agent even when peers share cwd+engine.
// Returns a synthesized tmux identity (no Target — it was not amq-launched) or
// nil when no pane resolves. This is what lets focus/send/attach_control work
// for adopted agents.
func adoptLivePane(role, handle, binary, cwd, workstream string, agentPID int, panes []tmuxpane.TmuxPane, pidTree func(int) []int) *launch.TmuxInfo {
	if len(panes) == 0 {
		return nil
	}
	ag := state.Agent{Handle: handle, Role: role, Engine: binary, AgentPID: agentPID}
	tgt, ok := tmuxpane.ResolveTmuxTargetForSession(ag, workstream, cwd, panes, pidTree)
	if !ok {
		return nil
	}
	paneID := paneIDForTarget(tgt, panes)
	if paneID == "" {
		return nil
	}
	p, found := tmuxpane.FindPaneByID(paneID, panes)
	if !found {
		return nil
	}
	return &launch.TmuxInfo{
		Session:    p.Session,
		WindowID:   p.WindowID,
		WindowName: p.WindowName,
		PaneID:     p.PaneID,
	}
}
