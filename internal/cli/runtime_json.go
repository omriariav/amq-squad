package cli

import (
	"io"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/runtimeaction"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
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
	Backend    string `json:"backend,omitempty"`
	Session    string `json:"session,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
	WindowName string `json:"window_name,omitempty"`
	TabID      string `json:"tab_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	PaneID     string `json:"pane_id,omitempty"`
	TTY        string `json:"tty,omitempty"`
	Target     string `json:"target,omitempty"`
	PaneAlive  bool   `json:"pane_alive"`
	PIDAlive   bool   `json:"pid_alive,omitempty"`
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
	fillPaneAlive(rt, live)
	if rt == nil || rt.PaneAlive || strings.TrimSpace(rt.PaneID) == "" || liveness == nil {
		return
	}
	if liveness.Signals.AgentAlive && liveness.Signals.BinaryMatch {
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
	if caps == nil {
		return policyAwareMemberActions(t, profile, session, row.Role, row.Tmux != nil && row.Tmux.PaneAlive)
	}
	return applyMemberActionPolicy(t, row.Role, runtimeaction.MemberForCapabilities(t.Project, profile, session, row.Role, *caps))
}

func runtimeCapabilitiesForStatusRow(row statusRecord) *runtimecontrol.Capabilities {
	if row.Terminal == nil || strings.TrimSpace(row.Terminal.Backend) == "" {
		return nil
	}
	backend := strings.TrimSpace(row.Terminal.Backend)
	ctrl, ok := runtimecontrol.DefaultRegistry().Lookup(backend)
	if !ok {
		reason := "runtime backend " + backend + " is unsupported"
		caps := runtimecontrol.NewCapabilities(map[runtimecontrol.Capability]runtimecontrol.CapabilityState{
			runtimecontrol.CapabilityFocus:       {Available: false, Reason: reason},
			runtimecontrol.CapabilitySendPrompt:  {Available: false, Reason: reason},
			runtimecontrol.CapabilityGoalDeliver: {Available: false, Reason: reason},
			runtimecontrol.CapabilityDispatch:    {Available: false, Reason: reason},
		})
		return &caps
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
	return &caps
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

// resumeMemberJSON is one member row in the resume_plan envelope. It mirrors the
// human plan (role/action/note/command) and adds the runtime identity so a
// client can decide whether to focus a live pane or re-open one.
type resumeMemberJSON struct {
	Role             string           `json:"role"`
	Handle           string           `json:"handle,omitempty"`
	Action           string           `json:"action"`
	LaunchState      string           `json:"launch_state"`
	RecordState      string           `json:"record_state"`
	HasRestoreRecord bool             `json:"has_restore_record"`
	Wake             string           `json:"wake,omitempty"`
	Note             string           `json:"note,omitempty"`
	Command          string           `json:"command,omitempty"`
	Tmux             *tmuxRuntimeJSON `json:"tmux,omitempty"`
	// Liveness is the shared liveness verdict (status + detail + signals), the
	// SAME classification `status --json` reports. A client compares
	// liveness.status to status's status instead of inferring liveness from the
	// planning `action`. Omitted only on the blocked paths where no verdict ran.
	Liveness *resumeLivenessJSON `json:"liveness,omitempty"`
}

// resumeLivenessJSON exposes the shared liveness verdict on a resume_plan member
// so `resume --json` and `status --json` carry identical liveness for the same
// agent. status is the same statusState string status emits.
type resumeLivenessJSON struct {
	Status  string        `json:"status"`
	Detail  string        `json:"detail,omitempty"`
	Signals statusSignals `json:"signals"`
}

// resumeEnvelopeData is the `resume_plan` envelope body: the same per-member
// classification `resume` prints, in a stable shape clients can render as
// actions. It is a read-only preview (never executes).
type resumeEnvelopeData struct {
	TeamHome                  string                     `json:"team_home"`
	Workstream                string                     `json:"workstream"`
	Profile                   string                     `json:"profile,omitempty"`
	Mode                      string                     `json:"mode"`
	NamespaceConflict         *namespaceConflictData     `json:"namespace_conflict,omitempty"`
	Members                   int                        `json:"members"`
	Plan                      []resumeMemberJSON         `json:"plan"`
	GoalPlan                  *runwizard.ResumeGoalPlan  `json:"goal_plan,omitempty"`
	NativeGoalBlockedRecovery []resumeNativeGoalRecovery `json:"native_goal_blocked_recovery,omitempty"`
}

// writeResumeJSON emits the resume_plan envelope. Pane liveness is resolved once
// across every member that carries a tmux identity.
func writeResumeJSON(out io.Writer, t team.Team, workstream string, mode resumeMode, profile string, conflict *namespaceConflictData, plans []resumePlan) error {
	return writeResumeJSONWithGoal(out, t, workstream, mode, profile, conflict, plans, runwizard.ResumeGoalPlan{})
}

func writeResumeJSONWithGoal(out io.Writer, t team.Team, workstream string, mode resumeMode, profile string, conflict *namespaceConflictData, plans []resumePlan, goalPlan runwizard.ResumeGoalPlan) error {
	rows := make([]resumeMemberJSON, 0, len(plans))
	var livePanes map[string]bool
	for _, p := range plans {
		rt := tmuxRuntimeFromInfo(p.Tmux)
		if rt != nil {
			if livePanes == nil {
				livePanes = livePaneIDSet(statusPaneLister)
			}
			fillPaneAliveFromLiveness(rt, livePanes, p.Liveness)
		}
		var liveness *resumeLivenessJSON
		if p.Liveness != nil {
			liveness = &resumeLivenessJSON{
				Status:  string(p.Liveness.Status),
				Detail:  p.Liveness.Detail,
				Signals: p.Liveness.Signals,
			}
		}
		rows = append(rows, resumeMemberJSON{
			Role:             p.Role,
			Handle:           p.Handle,
			Action:           string(p.Action),
			LaunchState:      resumeLaunchState(p),
			RecordState:      resumeRecordState(p),
			HasRestoreRecord: p.HasRestoreRecord,
			Wake:             wakeForJSON(p.Wake),
			Note:             p.Note,
			Command:          p.Command,
			Tmux:             rt,
			Liveness:         liveness,
		})
	}
	profile = strings.TrimSpace(profile)
	if profile == team.DefaultProfile {
		profile = ""
	}
	var goalPlanJSON *runwizard.ResumeGoalPlan
	if goalPlan.SchemaVersion != 0 {
		copy := goalPlan
		goalPlanJSON = &copy
	}
	return writeJSONEnvelope(out, "resume_plan", resumeEnvelopeData{
		TeamHome:                  t.Project,
		Workstream:                workstream,
		Profile:                   profile,
		Mode:                      string(mode),
		NamespaceConflict:         conflict,
		Members:                   len(rows),
		Plan:                      rows,
		GoalPlan:                  goalPlanJSON,
		NativeGoalBlockedRecovery: resumeNativeGoalBlockedRecoveries(plans),
	})
}

func resumeLaunchState(p resumePlan) string {
	switch {
	case p.Action == resumeBlocked:
		return "blocked"
	case strings.TrimSpace(p.Command) != "":
		return "will-launch"
	case p.Action == resumeLive:
		return "skipped"
	default:
		return "failed"
	}
}

func resumeRecordState(p resumePlan) string {
	if p.Liveness != nil {
		switch p.Liveness.Status {
		case statusStateLive, statusStateWakeLive:
			if !p.Liveness.LaunchFound {
				return "missing"
			}
			return "launched"
		case statusStateStale:
			if p.Liveness.LaunchFound {
				return "stale-record"
			}
			return "stale-signal"
		case statusStateMissing:
			return "missing"
		}
	}
	if p.HasRestoreRecord {
		return "restorable"
	}
	return "missing"
}

// wakeForJSON normalizes the human "-" placeholder to an empty (omitted) field.
func wakeForJSON(w string) string {
	w = strings.TrimSpace(w)
	if w == "-" {
		return ""
	}
	return w
}
