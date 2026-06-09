package cli

import (
	"strings"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/procinfo"
	"github.com/omriariav/amq-squad/internal/state"
	"github.com/omriariav/amq-squad/internal/tmuxpane"
)

// verifiedAgentPID returns pid only when it is a LIVE process running the
// expected binary. PID-lineage pane resolution treats a match as definitive and
// bypasses the cwd/engine heuristics, so the pid MUST be verified first or a
// stale/reused pid from an old launch record could resolve onto an unrelated
// pane. Callers that read a record directly (focus/send) must pass the result
// of this; callers that already hold a verified agent-live verdict (status /
// resume) may pass the verified Signals.AgentPID directly.
func verifiedAgentPID(pid int, binary string) int {
	if pid <= 0 || !procinfo.Alive(pid) {
		return 0
	}
	b := strings.TrimSpace(binary)
	if b == "" || !procinfo.Match(pid, agentProcessMatcher(b)) {
		return 0
	}
	return pid
}

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
//
// agentPID MUST be a VERIFIED live agent pid (caller's responsibility: a verdict
// of agent-live, or verifiedAgentPID for record-only callers). Passing an
// unverified/stale pid risks resolving onto an unrelated pane via reuse.
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
