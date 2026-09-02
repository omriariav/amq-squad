package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/runtimeaction"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// tmuxRuntimeJSON is the stable tmux runtime-identity block that amq-noc (and
// other JSON clients) consume to make a launched agent actionable: target
// follow-up control by exact pane id, and know whether that pane is still
// valid. It mirrors launch.TmuxInfo plus a computed pane_alive. It is nil (and
// omitted) when the launch record carried no tmux identity, so clients detect
// runtime-control availability by presence.
type tmuxRuntimeJSON struct {
	Session    string `json:"session,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
	WindowName string `json:"window_name,omitempty"`
	PaneID     string `json:"pane_id,omitempty"`
	Target     string `json:"target,omitempty"`
	// PaneAlive reports whether the recorded pane_id is still present in the
	// live tmux server. Always serialized so clients can branch on it without
	// guessing. False when the pane is gone or tmux is unavailable.
	PaneAlive bool `json:"pane_alive"`
}

// terminalRuntimeJSON is the additive backend-neutral runtime identity block.
// For tmux-backed launches it mirrors tmuxRuntimeJSON so consumers can start
// selecting a controller by backend without losing the legacy tmux contract.
type terminalRuntimeJSON struct {
	Backend      string                                    `json:"backend,omitempty"`
	Session      string                                    `json:"session,omitempty"`
	WindowID     string                                    `json:"window_id,omitempty"`
	WindowName   string                                    `json:"window_name,omitempty"`
	TabID        string                                    `json:"tab_id,omitempty"`
	SessionID    string                                    `json:"session_id,omitempty"`
	PaneID       string                                    `json:"pane_id,omitempty"`
	TTY          string                                    `json:"tty,omitempty"`
	Target       string                                    `json:"target,omitempty"`
	PaneAlive    bool                                      `json:"pane_alive"`
	PIDAlive     bool                                      `json:"pid_alive,omitempty"`
	Tier         string                                    `json:"tier"`
	Capabilities map[string]runtimecontrol.CapabilityState `json:"capabilities"`
}

// tmuxRuntimeFromInfo converts a persisted launch.TmuxInfo into the JSON block,
// leaving PaneAlive false for the caller to fill from a live-pane set. Returns
// nil when there is no tmux identity.
func tmuxRuntimeFromInfo(info *launch.TmuxInfo) *tmuxRuntimeJSON {
	if info == nil {
		return nil
	}
	// Defensive: a record with an empty tmux object (malformed or externally
	// written) carries no identity, so omit the block rather than emitting
	// {"pane_alive": false} with no ids.
	if info.PaneID == "" && info.WindowID == "" && info.Session == "" && info.WindowName == "" && info.Target == "" {
		return nil
	}
	return &tmuxRuntimeJSON{
		Session:    info.Session,
		WindowID:   info.WindowID,
		WindowName: info.WindowName,
		PaneID:     info.PaneID,
		Target:     info.Target,
	}
}

func terminalRuntimeFromInfo(info *launch.TerminalInfo) *terminalRuntimeJSON {
	if info == nil {
		return nil
	}
	if info.Backend == "" && info.PaneID == "" && info.WindowID == "" && info.Session == "" && info.WindowName == "" && info.TabID == "" && info.SessionID == "" && info.TTY == "" && info.Target == "" {
		return nil
	}
	return &terminalRuntimeJSON{
		Backend:    info.Backend,
		Session:    info.Session,
		WindowID:   info.WindowID,
		WindowName: info.WindowName,
		TabID:      info.TabID,
		SessionID:  info.SessionID,
		PaneID:     info.PaneID,
		TTY:        info.TTY,
		Target:     info.Target,
	}
}

func terminalRuntimeFromTmuxInfo(info *launch.TmuxInfo) *terminalRuntimeJSON {
	return terminalRuntimeFromInfo(launch.TerminalInfoFromTmux(info))
}

func syncTerminalRuntimeFromTmux(row *statusRecord) {
	if row == nil || row.Tmux == nil {
		return
	}
	if row.Terminal == nil {
		row.Terminal = terminalRuntimeFromTmuxInfo(&launch.TmuxInfo{
			Session:    row.Tmux.Session,
			WindowID:   row.Tmux.WindowID,
			WindowName: row.Tmux.WindowName,
			PaneID:     row.Tmux.PaneID,
			Target:     row.Tmux.Target,
		})
	}
	if row.Terminal != nil && row.Terminal.Backend == "tmux" {
		row.Terminal.PaneAlive = row.Tmux.PaneAlive
	}
}

// memoizePaneLister wraps a pane lister so the underlying `tmux list-panes`
// runs at most once; the cached (panes, error) is returned on every call. A
// command installs this for its duration so independent readers (e.g. status's
// live-replacement detection and pane_alive resolution) share one snapshot and
// one tmux call instead of re-listing per member.
func memoizePaneLister(list tmuxpane.PaneLister) tmuxpane.PaneLister {
	var (
		done  bool
		panes []tmuxpane.TmuxPane
		err   error
	)
	return func() ([]tmuxpane.TmuxPane, error) {
		if !done {
			panes, err = list()
			done = true
		}
		return panes, err
	}
}

// livePaneIDSet returns the set of #{pane_id} for every live tmux pane via the
// injectable lister. It degrades to an empty set (never an error) when tmux is
// unavailable, so pane_alive resolves to false rather than failing the command.
func livePaneIDSet(list tmuxpane.PaneLister) map[string]bool {
	set := map[string]bool{}
	panes, err := list()
	if err != nil {
		return set
	}
	for _, p := range panes {
		if p.PaneID != "" {
			set[p.PaneID] = true
		}
	}
	return set
}

// fillPaneAlive sets PaneAlive on a runtime block from a live-pane set. A nil
// block (no tmux identity) is left untouched.
func fillPaneAlive(rt *tmuxRuntimeJSON, live map[string]bool) {
	if rt == nil {
		return
	}
	if rt.PaneID == "" {
		rt.PaneAlive = false
		return
	}
	if live[rt.PaneID] {
		rt.PaneAlive = true
		return
	}
	// The global `list-panes` scan can miss a live pane while the iTerm2 -CC
	// control client is paused (it comes back empty / exit 1). Before declaring
	// the pane dead, confirm the recorded id DIRECTLY — the same robust path
	// send/focus use — so resume/status liveness stops flapping under -CC and
	// agrees with the control plane. statusPaneInspector retries internally.
	if _, ok := statusPaneInspector(rt.PaneID); ok {
		rt.PaneAlive = true
		return
	}
	rt.PaneAlive = false
}

func fillPaneAliveFromLiveness(rt *tmuxRuntimeJSON, live map[string]bool, liveness *agentLiveness) {
	if rt == nil {
		return
	}
	// An external record registers one exact pane identity. Neither a recycled
	// pane id in list-panes nor a successful direct inspect is sufficient:
	// only the shared classifier's exact amq:<session>:<role> title check may
	// mark that registered pane live.
	if liveness != nil && liveness.LaunchFound && liveness.LaunchRecord.External {
		rt.PaneAlive = liveness.RuntimeIdentity.PaneLive
		return
	}
	fillPaneAlive(rt, live)
	if rt.PaneAlive || strings.TrimSpace(rt.PaneID) == "" || liveness == nil {
		return
	}
	if liveness.RuntimeIdentity.PIDLive {
		rt.PaneAlive = true
	}
}

// runtimeActionJSON is one stable, project-scoped operator action a client
// (amq-noc) can render, copy, or execute for a member. Emitting the exact
// command keeps the control contract in amq-squad: clients call/copy these
// instead of assembling tmux or amq-squad invocations themselves. The structured
// metadata (mutates / needs_confirmation / available / reason) lets a client
// gate an EXECUTABLE action deterministically without hard-coding policy.
type runtimeActionJSON = runtimeaction.Action

// memberActions builds the per-member action catalog. focus/send require a live
// pane (paneAlive); resume and status are always available. Each action carries
// the metadata a client needs to render a confirm-gated executable action. The
// project flag is included so the command is runnable from anywhere.
func memberActions(projectDir, profile, session, role string, paneAlive bool) []runtimeActionJSON {
	return runtimeaction.Member(projectDir, profile, session, role, paneAlive)
}

func policyAwareMemberActions(t team.Team, profile, session, role string, paneAlive bool) []runtimeActionJSON {
	return applyMemberActionPolicy(t, role, memberActions(t.Project, profile, session, role, paneAlive))
}

func policyAwareMemberActionsForRow(t team.Team, profile, session string, row statusRecord) []runtimeActionJSON {
	caps := runtimeCapabilitiesForStatusRow(row)
	actions := runtimeaction.MemberForCapabilities(t.Project, profile, session, row.Role, caps)
	actions = append(actions, tmuxControlContinueActionsForStatusRow(t.Project, profile, session, row)...)
	return applyMemberActionPolicy(t, row.Role, actions)
}

func tmuxControlContinueActionsForStatusRow(project, profile, session string, row statusRecord) []runtimeActionJSON {
	if row.LiveIdentityMode != "managed_verified" || row.Terminal == nil || row.Terminal.Backend != runtimecontrol.BackendTmux ||
		!row.Terminal.PaneAlive || strings.TrimSpace(row.Terminal.Session) == "" || strings.TrimSpace(row.Terminal.PaneID) == "" || strings.TrimSpace(row.Terminal.WindowID) == "" ||
		row.LiveIdentity == nil || row.LiveIdentity.Verified == nil {
		return nil
	}
	canonicalProject, err := liveidentity.CanonicalProject(project)
	if err != nil {
		return nil
	}
	terminal := liveidentity.Terminal{Backend: row.Terminal.Backend, Target: row.Terminal.Target, Session: row.Terminal.Session,
		WindowID: row.Terminal.WindowID, PaneID: row.Terminal.PaneID, TabID: row.Terminal.TabID, SessionID: row.Terminal.SessionID, TTY: row.Terminal.TTY}
	verified := row.LiveIdentity.Verified
	if verified.Key.Project != canonicalProject || verified.Key.Profile != profile || verified.Key.Session != session || verified.Key.Handle != row.Handle ||
		verified.Role != row.Role || verified.Terminal != terminal {
		return nil
	}
	clients, err := listExactTmuxControlClients(row.Terminal.Session, tmuxOutputCommand)
	if err != nil || len(clients) != 1 || clients[0].Session != row.Terminal.Session {
		return nil
	}
	client, err := validateTmuxControlClientName(clients[0].Name)
	if err != nil {
		return nil
	}
	command := shellCommand("amq-squad", "team", "member", "control-continue", row.Role,
		"--client", client, "--project", project, "--profile", profile, "--session", session, "--json")
	// R4 (#505 review, cto decision): this action is offered for any row that
	// passes the canonical/verified/unique-client/exact-pane checks below, not
	// only rows that are actually paused - tmux does not expose per-client
	// pane pause state to gate on. Label and describe it as a safe, idempotent
	// pause-recovery/resync action rather than implying a detected pause.
	return runtimeaction.ApplyCanonical([]runtimeActionJSON{{
		Kind: "control_continue", Label: "pause-recovery resync for tmux control client " + client, Scope: "agent",
		NamespaceID: squadnamespace.ID(profile, session), Command: command,
		Mutates: true, NeedsConfirmation: true, Available: true,
		Reason: "safe, idempotent pause-recovery/resync action, offered after canonical namespace, verified managed identity, unique client, and exact pane checks; no-ops if the client is not actually paused",
	}})
}

func rawRuntimeCapabilitiesForStatusRow(row statusRecord) runtimecontrol.Capabilities {
	if row.Terminal == nil || strings.TrimSpace(row.Terminal.Backend) == "" {
		return runtimecontrol.UnknownCapabilities("runtime backend is missing")
	}
	backend := strings.TrimSpace(row.Terminal.Backend)
	ctrl, ok := runtimecontrol.DefaultRegistry().Lookup(backend)
	if !ok {
		reason := "runtime backend " + backend + " is unsupported"
		return runtimecontrol.UnknownCapabilities(reason)
	}
	caps := ctrl.Capabilities(runtimecontrol.Identity{
		Backend:    backend,
		Session:    row.Terminal.Session,
		WindowID:   row.Terminal.WindowID,
		WindowName: row.Terminal.WindowName,
		TabID:      row.Terminal.TabID,
		SessionID:  row.Terminal.SessionID,
		PaneID:     row.Terminal.PaneID,
		TTY:        row.Terminal.TTY,
		Target:     row.Terminal.Target,
	}, runtimecontrol.Liveness{
		PaneAlive:   row.Terminal.PaneAlive,
		AgentAlive:  row.Signals.AgentAlive,
		BinaryMatch: row.Signals.BinaryMatch,
	})
	return caps
}

func runtimeCapabilitiesForStatusRow(row statusRecord) runtimecontrol.Capabilities {
	raw := rawRuntimeCapabilitiesForStatusRow(row)
	return runtimecontrol.ResolveEffectiveActions(raw, runtimecontrol.DeliveryEvidence{
		DurableAMQ: memberHasDurableAMQ(row),
	})
}

func memberHasDurableAMQ(row statusRecord) bool {
	handle := strings.TrimSpace(row.Handle)
	root := strings.TrimSpace(row.Root)
	agentDir := strings.TrimSpace(row.AgentDir)
	if handle == "" || root == "" || agentDir == "" || strings.TrimSpace(row.Session) == "" {
		return false
	}
	if row.Namespace.Session != row.Session || row.Namespace.ID != squadnamespace.ID(row.Namespace.Profile, row.Session) {
		return false
	}
	expectedAgentDir := filepath.Join(root, "agents", handle)
	if filepath.Clean(agentDir) != filepath.Clean(expectedAgentDir) {
		return false
	}
	for _, path := range []string{
		agentDir,
		filepath.Join(agentDir, "inbox"),
		filepath.Join(agentDir, "inbox", "cur"),
		filepath.Join(agentDir, "inbox", "new"),
		filepath.Join(agentDir, "inbox", "tmp"),
	} {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

func decorateTerminalRuntimeCapabilities(row *statusRecord) {
	if row == nil || row.Terminal == nil {
		return
	}
	row.Terminal.Tier = runtimecontrol.TierForBackend(row.Terminal.Backend)
	caps := rawRuntimeCapabilitiesForStatusRow(*row)
	row.Terminal.Capabilities = caps.RawSnapshot()
}

func applyMemberActionPolicy(t team.Team, role string, actions []runtimeActionJSON) []runtimeActionJSON {
	mode := effectiveTeamExecutionMode(t)
	if mode != executionModeProjectLead && mode != executionModeProjectTeam {
		return actions
	}
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 {
		lead = t.Members[0].Role
	}
	if strings.TrimSpace(role) == "" || role == lead {
		return actions
	}
	reason := "execution policy routes mutating child control through the visible lead"
	out := append([]runtimeActionJSON(nil), actions...)
	for i := range out {
		switch out[i].Kind {
		case "send", "goal_deliver", "dispatch":
			out[i].Available = false
			out[i].Reason = reason
			runtimeaction.SyncUnavailableReason(&out[i])
		}
	}
	return out
}

// sessionActions builds the SESSION-scope operator action catalog for a
// workstream: the lifecycle controls a client renders for a session row. They
// map to real amq-squad verbs (no synthetic "restart" — a client composes that
// from stop + a resume). resume_new_session lets amq-squad derive the tmux
// session name (omitting --terminal-session). All are runnable commands, so
// available is true; the mutating ones request confirmation.
//
// tmuxSession is the live tmux session the workstream's agents run in (derived
// from the status rows). When non-empty, an attach_control action is appended
// so a client can open/attach the session in iTerm2's tmux -CC control mode;
// when empty it is omitted (no attach target to point at).
func sessionActions(projectDir, profile, session, tmuxSession string) []runtimeActionJSON {
	return runtimeaction.Session(projectDir, profile, session, tmuxSession)
}

// firstLiveTmuxSession returns the tmux session name of the first status row
// that carries a live tmux pane (Tmux != nil && Tmux.PaneAlive), or "" when no
// row has a live pane. It is how the status write site derives the attach
// target for the session-scope attach_control action.
func firstLiveTmuxSession(rows []statusRecord) string {
	for _, r := range rows {
		if r.Tmux != nil && r.Tmux.PaneAlive {
			return r.Tmux.Session
		}
	}
	return ""
}

func statusTopologyForRows(rows []statusRecord, orchestrated bool) *statusTopology {
	sessionSet := map[string]bool{}
	windowSet := map[string]bool{}
	livePanes := 0
	unknownWindow := false
	for _, r := range rows {
		if r.Tmux == nil || !r.Tmux.PaneAlive {
			continue
		}
		livePanes++
		session := strings.TrimSpace(r.Tmux.Session)
		if session != "" {
			sessionSet[session] = true
		}
		window := strings.TrimSpace(r.Tmux.WindowID)
		if window == "" {
			window = strings.TrimSpace(r.Tmux.WindowName)
		}
		if session == "" || window == "" {
			unknownWindow = true
			continue
		}
		windowSet[session+"\x00"+window] = true
	}
	sessions := make([]string, 0, len(sessionSet))
	for s := range sessionSet {
		sessions = append(sessions, s)
	}
	sort.Strings(sessions)
	topology := &statusTopology{
		Mode:         "unknown",
		TmuxSessions: sessions,
		LivePanes:    livePanes,
		LiveWindows:  len(windowSet),
	}
	switch {
	case livePanes == 0:
		topology.Detail = "no live tmux panes with runtime identity"
	case len(sessionSet) > 1:
		topology.Mode = "split-session"
		topology.Detail = "live agents span multiple tmux sessions"
		if orchestrated {
			topology.VisibleProblem = true
			topology.ProblemFor = visibilitySiblingTabs
		}
	case len(sessionSet) == 1 && !unknownWindow && len(windowSet) == livePanes:
		topology.Mode = visibilitySiblingTabs
		topology.Detail = "live agents are sibling tmux windows in one session"
	case len(sessionSet) == 1 && !unknownWindow && len(windowSet) == 1 && livePanes > 1:
		topology.Mode = "current-window"
		topology.Detail = "live agents share one tmux window as split panes"
	case len(sessionSet) == 1:
		topology.Mode = "mixed"
		topology.Detail = "live agents share one tmux session but window topology is mixed or partially unknown"
	default:
		topology.Detail = "tmux session topology is unknown"
	}
	return topology
}
