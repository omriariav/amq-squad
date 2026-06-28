package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// statusPaneLister lists live tmux panes so status can detect a live agent that
// was relaunched OUTSIDE amq-squad (its recorded PID is dead, but a replacement
// process is running). Injected as a package var so tests supply a fake and the
// classifier never shells real tmux. Defaults to the same read-only lister the
// tmux pane resolver uses, keeping detection consistent across surfaces.
var statusPaneLister = tmuxpane.DefaultPaneLister

// statusPaneInspector resolves a single pane directly by its recorded tmux id,
// bypassing the global `list-panes -a` scan. It is the authoritative-address
// path used when the scan misses or fails wholesale (e.g. under iTerm2 tmux -CC
// control mode). Injected as a package var so tests supply a fake.
var statusPaneInspector = tmuxpane.InspectPaneByID

// paneCloser closes an agent's tmux pane on teardown (kill-pane). Injected as a
// package var so tests record the call instead of killing a real pane. It
// MUTATES tmux, so callers gate it on the agent being down.
var paneCloser = tmuxpane.ClosePane

// statusState is the precise state vocabulary emitted by `amq-squad status`.
// Definitions:
//   - live:      launch-record PID alive AND binary matches; the agent is running.
//   - wake-live: wake helper is verified live for this handle/root, but the
//     agent PID itself is not verified.
//   - stale:     live signals exist on disk (launch record, wake lock, or
//     presence) but none verify as usable for this handle.
//   - missing:   no launch record, no wake lock, no presence file. The member
//     is configured but has never run in the resolved session.
type statusState string

const (
	statusStateLive     statusState = "live"
	statusStateWakeLive statusState = "wake-live"
	statusStateStale    statusState = "stale"
	statusStateMissing  statusState = "missing"
)

type statusSignals struct {
	AgentPID    int       `json:"agent_pid,omitempty"`
	AgentAlive  bool      `json:"agent_alive,omitempty"`
	BinaryMatch bool      `json:"binary_match,omitempty"`
	WakePID     int       `json:"wake_pid,omitempty"`
	WakeAlive   bool      `json:"wake_alive,omitempty"`
	Presence    string    `json:"presence,omitempty"`
	LastSeen    time.Time `json:"last_seen,omitempty"`
}

// statusEnvelopeData is the kind="status" payload: resolved team-home,
// workstream, profile, and the per-member records.
type statusEnvelopeData struct {
	TeamHome     string                `json:"team_home"`
	Workstream   string                `json:"workstream"`
	Profile      string                `json:"profile,omitempty"`
	Namespace    squadnamespace.Ref    `json:"namespace"`
	Operator     team.OperatorView     `json:"operator"`
	Capabilities team.Capabilities     `json:"capabilities"`
	Orchestrated bool                  `json:"orchestrated,omitempty"`
	Lead         string                `json:"lead,omitempty"`
	LeadHandle   string                `json:"lead_handle,omitempty"`
	GoalBinding  goalBindingData       `json:"goal_binding"`
	Autonomous   team.AutonomousStatus `json:"autonomous"`
	Topology     *statusTopology       `json:"topology,omitempty"`
	Records      []statusRecord        `json:"records"`
	// Actions are the SESSION-scope operator actions (status / resume preview /
	// resume in current window / resume in new tmux session / stop), the catalog
	// counterpart to each record's agent-scope actions. A client renders these
	// for the session row instead of constructing the commands itself.
	Actions []runtimeActionJSON `json:"actions"`
}

type statusRecord struct {
	Role        string             `json:"role"`
	Handle      string             `json:"handle"`
	Binary      string             `json:"binary"`
	Session     string             `json:"session"`
	Namespace   squadnamespace.Ref `json:"namespace"`
	CWD         string             `json:"cwd"`
	SpawnOrigin string             `json:"spawn_origin,omitempty"`
	SpawnDepth  int                `json:"spawn_depth,omitempty"`
	Root        string             `json:"root,omitempty"`
	AgentDir    string             `json:"agent_dir,omitempty"`
	Status      statusState        `json:"status"`
	RecordState string             `json:"record_state"`
	Detail      string             `json:"detail,omitempty"`
	Signals     statusSignals      `json:"signals"`
	goalBinding *launch.GoalBinding
	// Tmux is the persisted tmux runtime identity (exact pane/window ids) plus
	// a computed pane_alive, so clients can target follow-up control. Omitted
	// when the agent's launch record carried no tmux identity.
	Tmux *tmuxRuntimeJSON `json:"tmux,omitempty"`
	// Actions are the stable, project-scoped commands a client can render/copy
	// for this member (focus/send/resume/status). Populated for --json only.
	Actions []runtimeActionJSON `json:"actions,omitempty"`
}

type statusTopology struct {
	Mode           string   `json:"mode"`
	TmuxSessions   []string `json:"tmux_sessions,omitempty"`
	LivePanes      int      `json:"live_panes"`
	LiveWindows    int      `json:"live_windows,omitempty"`
	VisibleProblem bool     `json:"visible_problem,omitempty"`
	ProblemFor     string   `json:"problem_for,omitempty"`
	Detail         string   `json:"detail,omitempty"`
}

type sessionStatusContext struct {
	Team         team.Team
	Profile      string
	Workstream   string
	Orchestrated bool
	Lead         string
	LeadHandle   string
	Actions      []runtimeActionJSON
}

func newSessionStatusContext(t team.Team, profile, workstream, tmuxSession string) sessionStatusContext {
	orchestrated, lead, leadHandle := orchestrationStatusFields(t)
	return sessionStatusContext{
		Team:         t,
		Profile:      profile,
		Workstream:   workstream,
		Orchestrated: orchestrated,
		Lead:         lead,
		LeadHandle:   leadHandle,
		Actions:      sessionActions(t.Project, profile, workstream, tmuxSession),
	}
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	sessionName := fs.String("session", "", "AMQ workstream session name (default: a board over all discovered sessions)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned status envelope instead of the human table")
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad status - live state of this project's sessions and team

Usage:
  amq-squad status [--project DIR] [--json]
  amq-squad status --session NAME [--project DIR] [--profile NAME] [--json]

With no --session, prints a multi-session BOARD over every discovered
session (docker-ps / git branch -v style): session name, rolled-up state
(running/stopped/degraded), agent health (N/M alive + at-risk), a one-line
brief, and last-activity. This is also the bare 'amq-squad' default.

With --session NAME, prints the single-session detail table: each
configured team member's live state in that session, using launch-record
PID + binary match, wake-lock PID + handle/root match, and fresh presence.

Examples:
  amq-squad status
  amq-squad status --project ~/Code/app
  amq-squad status --json
  amq-squad status --session issue-96 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	// No --session: the multi-session board over ALL discovered sessions.
	// This is the front-door default, so it degrades gracefully rather than
	// hard-erroring when `amq` is missing or there are no sessions.
	if !flagWasSet(fs, "session") {
		return runStatusBoard(projectDir, *jsonOut)
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return executeStatus(statusExecution{
		ProjectDir:       projectDir,
		RequestedSession: *sessionName,
		ExplicitSession:  flagWasSet(fs, "session"),
		Profile:          profile,
		Probe:            defaultDuplicateLaunchProbe,
		Out:              os.Stdout,
		JSON:             *jsonOut,
	})
}

type statusExecution struct {
	ProjectDir       string
	RequestedSession string
	ExplicitSession  bool
	Profile          string
	Probe            duplicateLaunchProbe
	Out              io.Writer
	JSON             bool
}

func executeStatus(s statusExecution) error {
	t, err := team.ReadProfile(s.ProjectDir, s.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	workstream, err := resolveTeamWorkstreamName(t, s.RequestedSession, s.ExplicitSession)
	if err != nil {
		return err
	}

	rows := buildStatusRows(t, s.Profile, workstream, s.Probe)
	if s.JSON {
		ns := squadnamespace.Resolve(t.Project, s.Profile, workstream)
		// Attach the stable action commands a client can render/copy per member.
		for i := range rows {
			rows[i].Namespace = ns
			alive := rows[i].Tmux != nil && rows[i].Tmux.PaneAlive
			rows[i].Actions = memberActions(t.Project, s.Profile, workstream, rows[i].Role, alive)
		}
		ctx := newSessionStatusContext(t, s.Profile, workstream, firstLiveTmuxSession(rows))
		return writeJSONEnvelope(s.Out, "status", statusEnvelopeData{
			TeamHome:     t.Project,
			Workstream:   workstream,
			Profile:      s.Profile,
			Namespace:    ns,
			Operator:     team.EffectiveOperator(t),
			Capabilities: team.EffectiveCapabilities(t),
			Orchestrated: ctx.Orchestrated,
			Lead:         ctx.Lead,
			LeadHandle:   ctx.LeadHandle,
			GoalBinding:  goalBindingForStatus(ns, ctx, rows),
			Autonomous:   team.EffectiveAutonomousStatus(t),
			Topology:     statusTopologyForRows(rows, ctx.Orchestrated),
			Records:      rows,
			Actions:      ctx.Actions,
		})
	}
	policy := outputPolicyCurrent()
	fmt.Fprintf(s.Out, "# workstream: %s\n", workstream)
	if root := firstStatusRoot(rows); root != "" {
		fmt.Fprintf(s.Out, "# AM_ROOT:    %s\n", root)
	}
	fmt.Fprintln(s.Out)
	w := tabwriter.NewWriter(s.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tSTATUS\tDETAIL")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Role, r.Handle, r.Binary, r.Session, colorStatus(policy, string(r.Status)), r.Detail)
	}
	return w.Flush()
}

func goalBindingForNamespace(ns squadnamespace.Ref) goalBindingData {
	binding := goalBindingData{
		Mode:       "amq_task_brief",
		NativeGoal: false,
		Verified:   false,
		Source:     "amq-task-brief",
		Detail:     "This runtime does not set a native /goal value; the visible lead is bound by the durable AMQ task, active brief, and task store for the namespace.",
	}
	if ns.Paths.Brief != "" {
		binding.BriefPath = ns.Paths.Brief
	}
	if ns.Paths.Tasks != "" {
		binding.TasksPath = ns.Paths.Tasks
	}
	return binding
}

func goalBindingForStatus(ns squadnamespace.Ref, ctx sessionStatusContext, rows []statusRecord) goalBindingData {
	binding := goalBindingForNamespace(ns)
	if !ctx.Orchestrated || strings.TrimSpace(ctx.Lead) == "" {
		return binding
	}
	for _, row := range rows {
		if row.Role != ctx.Lead || row.goalBinding == nil || !row.goalBinding.NativeGoal {
			continue
		}
		if row.Status != statusStateLive && row.Status != statusStateWakeLive {
			continue
		}
		binding.Mode = "native_goal"
		binding.NativeGoal = true
		binding.Verified = true
		binding.Source = "launch-record"
		binding.NativeSource = row.goalBinding.Source
		binding.Command = row.goalBinding.Command
		if detail := strings.TrimSpace(row.goalBinding.Detail); detail != "" {
			binding.Detail = detail
		} else {
			binding.Detail = "configured visible lead launch record carries native /goal binding evidence"
		}
		return binding
	}
	return binding
}

func buildStatusRows(t team.Team, profile, workstream string, probe duplicateLaunchProbe) []statusRecord {
	// Share one tmux pane snapshot across this whole command: live-replacement
	// detection inside classifyMemberStatus and pane_alive resolution below
	// both read statusPaneLister, so memoize it for the command's duration.
	restoreLister := statusPaneLister
	statusPaneLister = memoizePaneLister(restoreLister)
	defer func() { statusPaneLister = restoreLister }()

	members := orderedTeamMembers(t.Members)
	rows := make([]statusRecord, 0, len(members))
	for _, m := range members {
		rows = append(rows, classifyMemberStatus(t, profile, m, workstream, probe))
	}
	// #95: adopt a live tmux pane for live agents with no recorded tmux identity
	// (launched outside amq-squad's tmux backend, e.g. a raw `tmux new-window`),
	// so focus/send/attach_control and pane_alive work for them too.
	pidTree := childrenPidTree()
	for i := range rows {
		if rows[i].Tmux == nil && rows[i].Signals.AgentAlive && rows[i].Signals.BinaryMatch {
			if panes, perr := statusPaneLister(); perr == nil {
				if adopted := adoptLivePane(rows[i].Role, rows[i].Handle, rows[i].Binary, rows[i].CWD, workstream, rows[i].Signals.AgentPID, panes, pidTree); adopted != nil {
					rows[i].Tmux = tmuxRuntimeFromInfo(adopted)
				}
			}
		}
	}
	var livePanes map[string]bool
	for i := range rows {
		if rows[i].Tmux != nil {
			if livePanes == nil {
				livePanes = livePaneIDSet(statusPaneLister)
			}
			fillPaneAliveFromLiveness(rows[i].Tmux, livePanes, &agentLiveness{Signals: rows[i].Signals})
		}
	}
	return rows
}

func orchestrationStatusFields(t team.Team) (bool, string, string) {
	if !t.Orchestrated {
		return false, "", ""
	}
	lead := strings.TrimSpace(t.Lead)
	leadHandle := lead
	for _, m := range t.Members {
		if m.Role == lead {
			leadHandle = memberHandle(m)
			break
		}
	}
	return true, lead, leadHandle
}

func firstStatusRoot(rows []statusRecord) string {
	for _, r := range rows {
		if r.Root != "" {
			return r.Root
		}
	}
	return ""
}

func classifyMemberStatus(t team.Team, profile string, m team.Member, workstream string, probe duplicateLaunchProbe) statusRecord {
	rec := statusRecord{
		Role:        m.Role,
		Handle:      m.Handle,
		Binary:      m.Binary,
		Session:     workstream,
		CWD:         m.EffectiveCWD(t.Project),
		SpawnOrigin: m.SpawnOrigin,
		SpawnDepth:  m.SpawnDepth,
	}
	env, err := resolveAMQEnvForTeamProfile(rec.CWD, profile, workstream, m.Handle)
	if err != nil {
		rec.Status = statusStateMissing
		rec.RecordState = "missing"
		rec.Detail = "amq env unresolved: " + err.Error()
		return rec
	}
	if env.Me != "" {
		rec.Handle = env.Me
	}
	root := absoluteAMQRoot(rec.CWD, env.Root)
	rec.Root = root
	rec.AgentDir = filepath.Join(root, "agents", rec.Handle)

	// Consume the single shared liveness classifier so status and resume can
	// never disagree. classifyAgentLiveness does the one disk read + probe
	// checks and returns the verdict, signals, detail, status state, and the
	// persisted tmux identity. classifyMemberStatus then just adopts them; the
	// verdict->statusState mapping lives in the classifier (Status field).
	live := classifyAgentLiveness(rec.AgentDir, root, profile, rec.Handle, m.Role, m.Binary, workstream, rec.CWD, probe)
	rec.Tmux = tmuxRuntimeFromInfo(live.Tmux)
	if live.LaunchFound {
		rec.goalBinding = live.LaunchRecord.GoalBinding
		if origin := strings.TrimSpace(live.LaunchRecord.SpawnOrigin); origin != "" {
			rec.SpawnOrigin = origin
		}
		if live.LaunchRecord.SpawnDepth > 0 {
			rec.SpawnDepth = live.LaunchRecord.SpawnDepth
		}
	}
	rec.Signals = live.Signals
	rec.Status = live.Status
	rec.RecordState = statusRecordState(live)
	rec.Detail = live.Detail
	return rec
}

func statusRecordState(live agentLiveness) string {
	switch live.Status {
	case statusStateLive, statusStateWakeLive:
		if !live.LaunchFound {
			return "missing"
		}
		return "launched"
	case statusStateStale:
		if live.LaunchFound {
			return "stale-record"
		}
		return "stale-signal"
	case statusStateMissing:
		return "missing"
	default:
		return "unknown"
	}
}

// liveReplacementPane reports a live tmux pane that resolves to this member when
// its recorded PID is dead — the case where the agent was relaunched outside
// amq-squad. It reuses the neutral tmux pane resolver (title-first amq:<session>:<role>,
// then engine+cwd) so detection is consistent and conservative: only a
// SAME-ENGINE match is attributed, never a bare differently-engined pane. The
// pane lister is injectable (statusPaneLister) so tests never shell real tmux;
// any tmux/lister error degrades to "not found" (the caller stays stale).
func liveReplacementPane(m team.Member, rec statusRecord, workstream string) (string, bool) {
	panes, err := statusPaneLister()
	if err != nil || len(panes) == 0 {
		return "", false
	}
	ag := state.Agent{
		Handle: rec.Handle,
		Role:   m.Role,
		Engine: m.Binary,
	}
	target, ok := tmuxpane.ResolveTmuxTargetForSession(ag, workstream, rec.CWD, panes, nil)
	if !ok {
		return "", false
	}
	return tmuxpane.SuggestJump(target), true
}

func readWakeLock(agentDir string) (wakeLockFile, error) {
	path := wakeLockPath(agentDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return wakeLockFile{}, err
	}
	var lock wakeLockFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return wakeLockFile{}, err
	}
	return lock, nil
}

func staleDetail(s statusSignals, presenceMismatched bool) string {
	var parts []string
	if s.AgentPID > 0 {
		switch {
		case !s.AgentAlive:
			parts = append(parts, fmt.Sprintf("agent pid %d not alive", s.AgentPID))
		case !s.BinaryMatch:
			parts = append(parts, fmt.Sprintf("agent pid %d binary mismatch", s.AgentPID))
		}
	}
	if s.WakePID > 0 && !s.WakeAlive {
		parts = append(parts, fmt.Sprintf("wake pid %d not alive or unrelated", s.WakePID))
	}
	if s.WakeAlive {
		parts = append(parts, fmt.Sprintf("wake pid %d alive without verified agent", s.WakePID))
	}
	if presenceMismatched {
		parts = append(parts, "fresh presence for unrelated handle")
	}
	if len(parts) == 0 {
		return "stale signals on disk"
	}
	return strings.Join(parts, "; ")
}

func wakeLiveDetail(s statusSignals) string {
	parts := []string{fmt.Sprintf("wake pid %d alive", s.WakePID)}
	if s.AgentPID > 0 {
		switch {
		case !s.AgentAlive:
			parts = append(parts, fmt.Sprintf("agent pid %d not alive", s.AgentPID))
		case !s.BinaryMatch:
			parts = append(parts, fmt.Sprintf("agent pid %d binary mismatch", s.AgentPID))
		}
	} else {
		parts = append(parts, "no verified agent pid")
	}
	return strings.Join(parts, "; ")
}
