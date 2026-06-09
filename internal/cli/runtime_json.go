package cli

import (
	"io"
	"strings"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
	"github.com/omriariav/amq-squad/internal/tmuxpane"
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
	rt.PaneAlive = rt.PaneID != "" && live[rt.PaneID]
}

// runtimeActionJSON is one stable, project-scoped operator action a client
// (amq-noc) can render, copy, or execute for a member. Emitting the exact
// command keeps the control contract in amq-squad: clients call/copy these
// instead of assembling tmux or amq-squad invocations themselves. The structured
// metadata (mutates / needs_confirmation / available / reason) lets a client
// gate an EXECUTABLE action deterministically without hard-coding policy.
type runtimeActionJSON struct {
	// Kind is the stable id of the action (focus | send | resume | status).
	Kind string `json:"kind"`
	// Label is a short human-facing name for the action.
	Label string `json:"label"`
	// Scope is the action's target granularity (currently always "agent").
	Scope             string `json:"scope"`
	Command           string `json:"command"`
	Mutates           bool   `json:"mutates"`            // changes squad/agent state
	NeedsConfirmation bool   `json:"needs_confirmation"` // a client should confirm first
	Available         bool   `json:"available"`
	// Reason explains why an action is unavailable in the current context;
	// empty when available.
	Reason string `json:"reason,omitempty"`
}

// memberActions builds the per-member action catalog. focus/send require a live
// pane (paneAlive); resume and status are always available. Each action carries
// the metadata a client needs to render a confirm-gated executable action. The
// project flag is included so the command is runnable from anywhere.
func memberActions(projectDir, profile, session, role string, paneAlive bool) []runtimeActionJSON {
	base := "amq-squad"
	scope := " --project " + shellQuote(projectDir)
	if profile != "" && profile != team.DefaultProfile {
		scope += " --profile " + shellQuote(profile)
	}
	scope += " --session " + shellQuote(session)
	roleArg := " --role " + shellQuote(role)
	deadReason := ""
	if !paneAlive {
		deadReason = "agent pane is not live"
	}
	// focus/send carry --role (agent scope); resume/status as commanded here act
	// on the whole session (no --role), so their scope is "session". A per-agent
	// dedicated catalog with agent-scoped resume/restart is a follow-up.
	return []runtimeActionJSON{
		{Kind: "focus", Label: "focus pane", Scope: "agent", Mutates: false, NeedsConfirmation: false, Available: paneAlive, Reason: deadReason, Command: base + " focus" + scope + roleArg},
		{Kind: "send", Label: "send a prompt", Scope: "agent", Mutates: true, NeedsConfirmation: true, Available: paneAlive, Reason: deadReason, Command: base + " send" + scope + roleArg + " --body-file -"},
		{Kind: "resume", Label: "resume session", Scope: "session", Mutates: true, NeedsConfirmation: true, Available: true, Command: base + " resume" + scope + " --exec"},
		{Kind: "status", Label: "show session status", Scope: "session", Mutates: false, NeedsConfirmation: false, Available: true, Command: base + " status" + scope + " --json"},
	}
}

// resumeMemberJSON is one member row in the resume_plan envelope. It mirrors the
// human plan (role/action/note/command) and adds the runtime identity so a
// client can decide whether to focus a live pane or re-open one.
type resumeMemberJSON struct {
	Role             string           `json:"role"`
	Handle           string           `json:"handle,omitempty"`
	Action           string           `json:"action"`
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
	TeamHome   string             `json:"team_home"`
	Workstream string             `json:"workstream"`
	Profile    string             `json:"profile,omitempty"`
	Mode       string             `json:"mode"`
	Members    int                `json:"members"`
	Plan       []resumeMemberJSON `json:"plan"`
}

// writeResumeJSON emits the resume_plan envelope. Pane liveness is resolved once
// across every member that carries a tmux identity.
func writeResumeJSON(out io.Writer, t team.Team, workstream string, mode resumeMode, profile string, plans []resumePlan) error {
	rows := make([]resumeMemberJSON, 0, len(plans))
	var livePanes map[string]bool
	for _, p := range plans {
		rt := tmuxRuntimeFromInfo(p.Tmux)
		if rt != nil {
			if livePanes == nil {
				livePanes = livePaneIDSet(statusPaneLister)
			}
			fillPaneAlive(rt, livePanes)
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
	return writeJSONEnvelope(out, "resume_plan", resumeEnvelopeData{
		TeamHome:   t.Project,
		Workstream: workstream,
		Profile:    profile,
		Mode:       string(mode),
		Members:    len(rows),
		Plan:       rows,
	})
}

// wakeForJSON normalizes the human "-" placeholder to an empty (omitted) field.
func wakeForJSON(w string) string {
	w = strings.TrimSpace(w)
	if w == "-" {
		return ""
	}
	return w
}
