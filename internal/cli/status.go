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
	TeamHome          string                 `json:"team_home"`
	Workstream        string                 `json:"workstream"`
	Profile           string                 `json:"profile,omitempty"`
	Namespace         squadnamespace.Ref     `json:"namespace"`
	Operator          statusOperatorView     `json:"operator"`
	OperatorDelivery  operatorDeliveryData   `json:"operator_delivery"`
	Capabilities      team.Capabilities      `json:"capabilities"`
	Orchestrated      bool                   `json:"orchestrated,omitempty"`
	Lead              string                 `json:"lead,omitempty"`
	LeadHandle        string                 `json:"lead_handle,omitempty"`
	GoalBinding       goalBindingData        `json:"goal_binding"`
	Autonomous        team.AutonomousStatus  `json:"autonomous"`
	Execution         executionModeData      `json:"execution"`
	NamespaceConflict *namespaceConflictData `json:"namespace_conflict,omitempty"`
	Warnings          []statusWarning        `json:"warnings,omitempty"`
	Topology          *statusTopology        `json:"topology,omitempty"`
	Records           []statusRecord         `json:"records"`
	// Actions are the SESSION-scope operator actions (status / resume preview /
	// resume in current window / resume in new tmux session / stop), the catalog
	// counterpart to each record's agent-scope actions. A client renders these
	// for the session row instead of constructing the commands itself.
	Actions []runtimeActionJSON `json:"actions"`
}

type statusWarning struct {
	Kind             string                       `json:"kind"`
	Session          string                       `json:"session"`
	Detail           string                       `json:"detail"`
	SuggestedCommand string                       `json:"suggested_command,omitempty"`
	Conflicts        []namespaceConflictCandidate `json:"conflicts,omitempty"`
}

type statusOperatorView struct {
	team.OperatorView
	CanonicalInbox *statusOperatorInbox `json:"canonical_inbox,omitempty"`
	Poll           *statusOperatorPoll  `json:"poll,omitempty"`
}

type statusOperatorInbox struct {
	Root    string `json:"root,omitempty"`
	Handle  string `json:"handle"`
	Session string `json:"session,omitempty"`
}

type statusOperatorPoll struct {
	Required     bool   `json:"required"`
	Owner        string `json:"owner,omitempty"`
	Cursor       string `json:"cursor,omitempty"`
	Unread       int    `json:"unread"`
	OpenGates    int    `json:"open_gates"`
	OpenBlockers int    `json:"open_blockers"`
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
	// Visibility fields distinguish "agent process is live" from "this member
	// is the operator-visible project lead". operator_visible is fail-closed:
	// it is true only when persisted launch-origin evidence proves visibility.
	OperatorVisible         bool                `json:"operator_visible"`
	AdoptionMode            string              `json:"adoption_mode,omitempty"`
	RoleBoundary            string              `json:"role_boundary,omitempty"`
	LauncherPaneID          string              `json:"launcher_pane_id,omitempty"`
	AgentPaneID             string              `json:"agent_pane_id,omitempty"`
	ManagedTarget           string              `json:"managed_target,omitempty"`
	CurrentPaneConflict     bool                `json:"current_pane_conflict"`
	VisibilityProblem       string              `json:"visibility_problem,omitempty"`
	VisibilityRepairActions []runtimeActionJSON `json:"visibility_repair_actions,omitempty"`
	// External reports that this member's launch record is an external pane (for
	// example a registered global orchestrator). It is surfaced in --json so a
	// client can positively identify the wakeable orchestrator identity rather
	// than inferring it from lead role alone. Set from launch.Record.External.
	External bool `json:"external,omitempty"`
	// WakeAutoDrain reports that this member's wake sidecar is configured to
	// inject a drain instruction on each durable-message arrival (the launch
	// record carries WakeInjectCmd). It means inbound messages are processed
	// reactively on wake, with no periodic `amq drain` polling loop. It is a
	// CONFIGURATION signal, not liveness: a client must still read signals
	// (wake_alive) and status to know whether the sidecar is actually running,
	// so a dead sidecar surfaces as degraded rather than silently lost.
	WakeAutoDrain bool `json:"wake_auto_drain,omitempty"`
	// PreauthorizedActions surfaces the in-scope worker actions amq-squad
	// pre-authorized at launch (#296) so the active allowlist is auditable from
	// status --json. Empty/omitted for legacy records and launches with no
	// pre-authorization. Mirrors launch.Record.PreauthorizedActions.
	PreauthorizedActions []string `json:"preauthorized_actions,omitempty"`
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
	return runStatusWithVersion(args, "dev")
}

func runStatusWithVersion(args []string, version string) error {
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
		return runStatusBoardWithVersion(projectDir, *jsonOut, version)
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
		RuntimeVersion:   version,
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
	RuntimeVersion   string
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
	warnings, err := statusNamespaceWarnings(t.Project, s.Profile, workstream)
	if err != nil {
		return fmt.Errorf("scan namespace warnings: %w", err)
	}

	rows := buildStatusRows(t, s.Profile, workstream, s.Probe)
	if s.JSON {
		ns := squadnamespace.Resolve(t.Project, s.Profile, workstream)
		conflict := namespaceConflictForProfileSession(t.Project, s.Profile, workstream)
		// Attach the stable action commands a client can render/copy per member.
		for i := range rows {
			rows[i].Namespace = ns
			alive := rows[i].Tmux != nil && rows[i].Tmux.PaneAlive
			rows[i].Actions = disableNamespaceConflictActions(policyAwareMemberActions(t, s.Profile, workstream, rows[i].Role, alive), conflict)
		}
		ctx := newSessionStatusContext(t, s.Profile, workstream, firstLiveTmuxSession(rows))
		ctx.Actions = disableNamespaceConflictActions(ctx.Actions, conflict)
		binding := goalBindingForStatus(ns, ctx, rows)
		operatorView := statusOperatorForTeam(t, ns)
		applyGoalBindingOpenBlockers(&operatorView, binding)
		topology := statusTopologyForRows(rows, ctx.Orchestrated)
		version := strings.TrimSpace(s.RuntimeVersion)
		if version == "" {
			version = "dev"
		}
		invariantErrors := annotateVisibilityInvariants(rows, ctx)
		execution := executionContractForTeam(t, s.Profile, workstream, binding.Mode, topologyMode(topology), version)
		execution.InvariantsEvaluated = true
		execution.InvariantOK = len(invariantErrors) == 0
		execution.InvariantErrors = invariantErrors
		applyLeadExecutionContract(&execution, t.LeadExecution)
		return writeJSONEnvelope(s.Out, "status", statusEnvelopeData{
			TeamHome:          t.Project,
			Workstream:        workstream,
			Profile:           s.Profile,
			Namespace:         ns,
			Operator:          operatorView,
			OperatorDelivery:  operatorDeliveryForTeam(t),
			Capabilities:      team.EffectiveCapabilities(t),
			Orchestrated:      ctx.Orchestrated,
			Lead:              ctx.Lead,
			LeadHandle:        ctx.LeadHandle,
			GoalBinding:       binding,
			Autonomous:        team.EffectiveAutonomousStatus(t),
			Execution:         execution,
			NamespaceConflict: conflict,
			Warnings:          warnings,
			Topology:          topology,
			Records:           rows,
			Actions:           ctx.Actions,
		})
	}
	policy := outputPolicyCurrent()
	fmt.Fprintf(s.Out, "# workstream: %s\n", workstream)
	if root := firstStatusRoot(rows); root != "" {
		fmt.Fprintf(s.Out, "# AM_ROOT:    %s\n", root)
	}
	for _, warning := range warnings {
		fmt.Fprintf(s.Out, "warning: %s\n", warning.Detail)
	}
	delivery := operatorDeliveryForTeam(t)
	if delivery.Enabled {
		fmt.Fprintf(s.Out, "# operator_delivery: %s\n", operatorDeliverySummary(delivery))
	}
	fmt.Fprintln(s.Out)
	w := tabwriter.NewWriter(s.Out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tSTATUS\tDETAIL")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Role, r.Handle, r.Binary, r.Session, colorStatus(policy, string(r.Status)), r.Detail)
	}
	return w.Flush()
}

func statusNamespaceWarnings(projectDir, profile, session string) ([]statusWarning, error) {
	if squadnamespace.NormalizeProfile(profile) != team.DefaultProfile || strings.TrimSpace(session) == "" {
		return nil, nil
	}
	profiles, err := team.ListProfiles(projectDir)
	if err != nil {
		return nil, err
	}
	conflicts := namedProfileSessionConflicts(projectDir, session, profiles, false)
	if len(conflicts) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(conflicts))
	for _, c := range conflicts {
		names = append(names, c.Profile)
	}
	suggested := ""
	if len(names) == 1 {
		suggested = "amq-squad status --project " + shellQuote(projectDir) + " --profile " + shellQuote(names[0]) + " --session " + shellQuote(session)
	}
	detail := fmt.Sprintf("showing default-profile data; session %s is also live under profile %s - run %s",
		shellQuote(session), pluralProfileList(names), "amq-squad status --profile <profile> --session "+shellQuote(session))
	if suggested != "" {
		detail = fmt.Sprintf("showing default-profile data; session %s is also live under profile %s - run %s",
			shellQuote(session), pluralProfileList(names), suggested)
	}
	return []statusWarning{{
		Kind:             "default_profile_shadowed",
		Session:          session,
		Detail:           detail,
		SuggestedCommand: suggested,
		Conflicts:        conflicts,
	}}, nil
}

func statusOperatorForTeam(t team.Team, ns squadnamespace.Ref) statusOperatorView {
	op := team.EffectiveOperator(t)
	out := statusOperatorView{OperatorView: op}
	if !op.Enabled {
		return out
	}
	handle := strings.TrimSpace(op.Handle)
	if handle == "" {
		handle = team.DefaultOperatorHandle
	}
	root := strings.TrimSpace(ns.AMQRoot)
	if root == "" {
		root = strings.TrimSpace(ns.Paths.AMQRoot)
	}
	out.CanonicalInbox = &statusOperatorInbox{
		Root:    root,
		Handle:  handle,
		Session: ns.Session,
	}
	out.Poll = &statusOperatorPoll{
		Required:     op.PollRequired,
		Owner:        "none",
		Unread:       0,
		OpenGates:    0,
		OpenBlockers: 0,
	}
	return out
}

func annotateVisibilityInvariants(rows []statusRecord, ctx sessionStatusContext) []executionInvariantError {
	mode := effectiveTeamExecutionMode(ctx.Team)
	requiresVisibleLead := mode == executionModeProjectLead || mode == executionModeProjectTeam
	lead := strings.TrimSpace(ctx.Lead)
	if lead == "" && len(ctx.Team.Members) == 1 {
		lead = ctx.Team.Members[0].Role
	}
	leadSeen := false
	leadVisible := false
	var leadError executionInvariantError

	for i := range rows {
		rows[i].RoleBoundary = roleBoundaryForStatus(ctx, rows[i], lead)
		if strings.TrimSpace(rows[i].AdoptionMode) == "" {
			rows[i].AdoptionMode = adoptionModeForStatus(rows[i])
		}
		if rows[i].RoleBoundary != "lead" {
			rows[i].OperatorVisible = false
			continue
		}
		leadSeen = true
		visible, code := operatorVisibilityForLead(&rows[i], mode)
		rows[i].OperatorVisible = visible
		rows[i].VisibilityProblem = code
		if !visible {
			rows[i].VisibilityRepairActions = visibilityRepairActions(ctx, rows[i])
		}
		if visible {
			leadVisible = true
		} else if leadError.Code == "" {
			leadError = invariantErrorForVisibilityProblem(rows[i], code, faultRepairScopeForStatus(ctx))
		}
	}

	if !requiresVisibleLead {
		return nil
	}
	switch {
	case lead == "":
		return []executionInvariantError{{Code: "no_visible_lead", Message: "project execution mode requires a configured visible lead"}}
	case !leadSeen:
		return []executionInvariantError{{Code: "no_visible_lead", Role: lead, Message: fmt.Sprintf("configured visible lead %q is not a team member", lead)}}
	case !leadVisible:
		if leadError.Code == "" {
			leadError = executionInvariantError{Code: "no_visible_lead", Role: lead, Message: "configured visible lead is not operator-visible"}
		}
		return []executionInvariantError{leadError}
	default:
		return nil
	}
}

func roleBoundaryForStatus(ctx sessionStatusContext, row statusRecord, lead string) string {
	mode := effectiveTeamExecutionMode(ctx.Team)
	if mode == executionModeGlobalOrchestrator {
		return "orchestrator"
	}
	if mode != executionModeProjectLead && mode != executionModeProjectTeam {
		// direct_lead_session: classify a DECLARED direct lead (registered as an
		// external lead, or a configured orchestration lead running in direct
		// mode) as role_boundary=lead so operatorVisibilityForLead can judge it.
		// Visibility stays fail-closed (external/managed-visible -> visible;
		// bare/collapsed/detached/unprovable -> not). A bare flat team that merely
		// defaults to direct_lead_session has no declared lead and stays
		// member/child, so flat output is unchanged. invariant_required stays
		// limited to project_lead/project_team (no no_visible_lead for direct leads).
		if mode == executionModeDirectLeadSession {
			configuredLead := strings.TrimSpace(ctx.Lead)
			if row.External || (configuredLead != "" && row.Role == configuredLead) {
				return "lead"
			}
		}
		if row.SpawnDepth > 0 || strings.TrimSpace(row.SpawnOrigin) != "" {
			return "child"
		}
		return "member"
	}
	if strings.TrimSpace(lead) != "" {
		if row.Role == lead {
			return "lead"
		}
		return "child"
	}
	if row.SpawnDepth > 0 || strings.TrimSpace(row.SpawnOrigin) != "" {
		return "child"
	}
	return "member"
}

func adoptionModeForStatus(row statusRecord) string {
	if row.External {
		return "external"
	}
	if row.Tmux == nil {
		if row.RecordState == "missing" {
			return "missing"
		}
		return "unmanaged"
	}
	switch strings.TrimSpace(row.Tmux.Target) {
	case "current-window":
		return "managed_current_window"
	case "new-window":
		return "managed_window"
	case "new-session":
		return "managed_session"
	case "":
		if row.LauncherPaneID != "" && row.LauncherPaneID == row.AgentPaneID {
			return "bare_agent_up"
		}
		return "unmanaged"
	default:
		return "tmux_" + strings.TrimSpace(row.Tmux.Target)
	}
}

func operatorVisibilityForLead(row *statusRecord, mode string) (bool, string) {
	if row != nil && (row.External || row.AdoptionMode == "external") {
		if projectExecutionMode(mode) &&
			strings.TrimSpace(row.AdoptionMode) != adoptionModeExternalProjectLead &&
			!launchRecordHasNativeGoal(launch.Record{GoalBinding: row.goalBinding}) {
			return false, "role_boundary_violation"
		}
		if row.Tmux == nil {
			return false, "no_pane"
		}
		if !row.Tmux.PaneAlive && strings.TrimSpace(row.Tmux.PaneID) != "" {
			if _, ok := statusPaneInspector(row.Tmux.PaneID); ok {
				row.Tmux.PaneAlive = true
			}
		}
		if row.Tmux.PaneAlive {
			row.Status = statusStateLive
			if strings.TrimSpace(row.Detail) == "" || strings.Contains(row.Detail, "no live signals") {
				row.Detail = fmt.Sprintf("external pane %s live (registered lead)", row.Tmux.PaneID)
			}
			return true, ""
		}
		return false, "pane_dead"
	}
	if row.Status != statusStateLive {
		return false, "lead_pane_dead"
	}
	if row.Tmux == nil {
		return false, "no_pane"
	}
	if !row.Tmux.PaneAlive {
		return false, "pane_dead"
	}
	if strings.TrimSpace(row.LauncherPaneID) == "" {
		return false, "pane_origin_unprovable"
	}
	row.CurrentPaneConflict = row.LauncherPaneID == row.AgentPaneID
	if row.CurrentPaneConflict {
		return false, "current_pane_collapse"
	}
	switch strings.TrimSpace(row.AdoptionMode) {
	case "managed_window", "managed_current_window":
		return true, ""
	case "managed_session":
		return false, "detached_session"
	case "bare_agent_up":
		return false, "unmanaged_agent_up"
	case "":
		return false, "pane_origin_unprovable"
	default:
		return false, "unmanaged_agent_up"
	}
}

type faultRepairScope struct {
	Project string
	Profile string
	Session string
}

func faultRepairScopeForStatus(ctx sessionStatusContext) faultRepairScope {
	return faultRepairScope{
		Project: ctx.Team.Project,
		Profile: ctx.Profile,
		Session: ctx.Workstream,
	}
}

func invariantErrorForVisibilityProblem(row statusRecord, code string, scope faultRepairScope) executionInvariantError {
	const docRef = "docs/v2.12.0-plan.md#repair-first-ux-for-topology-and-launch-failures"
	switch code {
	case "current_pane_collapse":
		return executionInvariantError{
			Code:    "lead_pane_collapsed",
			Role:    row.Role,
			Message: "visible lead is running in the launcher pane; relaunch in a managed visible pane or register an explicit external lead",
			DocRef:  docRef,
			Remedy:  faultRemedyRelaunch(scope),
		}
	case "lead_pane_dead", "pane_dead", "no_pane":
		return executionInvariantError{
			Code:    "lead_pane_dead",
			Role:    row.Role,
			Message: "visible lead has no live operator-addressable pane",
			DocRef:  docRef,
			Remedy:  faultRemedyResume(row.Role, scope),
		}
	case "detached_session":
		return executionInvariantError{
			Code:    "no_visible_lead",
			Role:    row.Role,
			Message: "visible lead is live in a detached tmux session, not an operator-visible pane",
			DocRef:  docRef,
			Remedy:  faultRemedyResume(row.Role, scope),
		}
	case "pane_origin_unprovable":
		return executionInvariantError{
			Code:    "no_visible_lead",
			Role:    row.Role,
			Message: "visible lead launch record does not prove launcher pane origin",
			DocRef:  docRef,
			Remedy:  faultRemedyRelaunch(scope),
		}
	case "role_boundary_violation":
		return executionInvariantError{
			Code:    "lead_role_boundary_violation",
			Role:    row.Role,
			Message: fmt.Sprintf("current pane is registered as a control-plane/external identity, not verified project lead %q", row.Role),
			DocRef:  docRef,
			Remedy:  faultRemedyResume(row.Role, scope),
		}
	default:
		return executionInvariantError{
			Code:    "no_visible_lead",
			Role:    row.Role,
			Message: "configured visible lead is not operator-visible",
			DocRef:  docRef,
			Remedy:  faultRemedyRelaunch(scope),
		}
	}
}

// faultRemedyRelaunch returns a repair action that relaunches the team in a
// managed visible pane. Available when the session is known.
func faultRemedyRelaunch(scope faultRepairScope) *faultRemedy {
	r := &faultRemedy{
		Kind:       "up",
		ID:         "up",
		Label:      "relaunch lead in a managed visible pane",
		ActionKind: "repair",
	}
	if strings.TrimSpace(scope.Session) == "" {
		r.Available = false
		r.Reason = "session name unknown; supply --session to build the repair command"
		r.UnavailableReason = r.Reason
		return r
	}
	r.Command = "amq-squad up" + faultRemedyScopeArgs(scope)
	r.Available = true
	return r
}

// faultRemedyResume returns a repair action that resumes a stale or detached
// lead. Available when both role and session are known.
func faultRemedyResume(role string, scope faultRepairScope) *faultRemedy {
	r := &faultRemedy{
		Kind:       "resume",
		ID:         "resume",
		Label:      "resume the lead session in a visible pane",
		ActionKind: "repair",
	}
	if strings.TrimSpace(role) == "" || strings.TrimSpace(scope.Session) == "" {
		r.Available = false
		r.Reason = "role or session unknown; supply --role and --session to build the repair command"
		r.UnavailableReason = r.Reason
		return r
	}
	r.Command = "amq-squad resume --role " + shellQuote(role) + faultRemedyScopeArgs(scope)
	r.Available = true
	return r
}

func faultRemedyScopeArgs(scope faultRepairScope) string {
	var args []string
	if strings.TrimSpace(scope.Project) != "" {
		args = append(args, "--project", shellQuote(scope.Project))
	}
	if strings.TrimSpace(scope.Profile) != "" && squadnamespace.NormalizeProfile(scope.Profile) != team.DefaultProfile {
		args = append(args, "--profile", shellQuote(scope.Profile))
	}
	if strings.TrimSpace(scope.Session) != "" {
		args = append(args, "--session", shellQuote(scope.Session))
	}
	if len(args) == 0 {
		return ""
	}
	return " " + strings.Join(args, " ")
}

func visibilityRepairActions(ctx sessionStatusContext, row statusRecord) []runtimeActionJSON {
	out := []runtimeActionJSON{}
	for _, action := range row.Actions {
		switch action.Kind {
		case "focus", "status":
			out = append(out, action)
		}
	}
	for _, action := range ctx.Actions {
		switch action.Kind {
		case "resume_preview", "resume_current_window":
			out = append(out, action)
		}
	}
	if row.Tmux != nil && strings.TrimSpace(row.Tmux.Session) != "" {
		out = append(out, runtimeActionJSON{
			Kind:              "attach_control",
			Label:             "open in iTerm2 (tmux -CC)",
			Scope:             "session",
			NamespaceID:       row.Namespace.ID,
			Command:           "tmux -CC attach -t " + shellQuote(row.Tmux.Session),
			Mutates:           false,
			NeedsConfirmation: false,
			Available:         true,
		})
	}
	return out
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
		if row.Role != ctx.Lead {
			continue
		}
		if row.Status != statusStateLive && row.Status != statusStateWakeLive {
			continue
		}
		if nativeGoalBindingBlocked(row.goalBinding) {
			binding.Mode = "native_goal_blocked"
			binding.NativeGoal = true
			binding.Verified = true
			binding.Source = "launch-record"
			binding.NativeSource = row.goalBinding.Source
			binding.Command = row.goalBinding.Command
			if detail := strings.TrimSpace(row.goalBinding.Detail); detail != "" {
				binding.Detail = detail
			} else {
				binding.Detail = "visible lead native /goal is blocked; operator or orchestrator should inspect and resume with /goal resume"
			}
			return binding
		}
		if row.goalBinding != nil && row.goalBinding.NativeGoal {
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
		if projectExecutionModeRequiresNativeGoal(ctx.Team) {
			return nativeGoalMissingBinding(binding, row)
		}
	}
	return binding
}

func nativeGoalBindingBlocked(binding *launch.GoalBinding) bool {
	return binding != nil && binding.NativeGoal && strings.TrimSpace(binding.Mode) == "native_goal_blocked"
}

func applyGoalBindingOpenBlockers(operatorView *statusOperatorView, binding goalBindingData) {
	if operatorView == nil || operatorView.Poll == nil {
		return
	}
	if binding.Mode == "native_goal_blocked" {
		operatorView.Poll.OpenBlockers++
	}
}

func blockedNativeGoalsInSnapshot(t team.Team, profile, workstream string, snap state.Snapshot) int {
	leadRole := strings.TrimSpace(t.Lead)
	if leadRole == "" {
		return 0
	}
	profile = squadnamespace.NormalizeProfile(profile)
	count := 0
	for _, session := range snap.Sessions {
		if session.Name != workstream || squadnamespace.NormalizeProfile(session.TeamProfile) != profile {
			continue
		}
		for _, agent := range session.Agents {
			if agent.Role == leadRole && nativeGoalBindingBlocked(agent.GoalBinding) {
				count++
			}
		}
	}
	return count
}

func projectExecutionModeRequiresNativeGoal(t team.Team) bool {
	switch effectiveTeamExecutionMode(t) {
	case executionModeProjectLead, executionModeProjectTeam:
		return true
	default:
		return false
	}
}

func nativeGoalMissingBinding(binding goalBindingData, row statusRecord) goalBindingData {
	binding.Mode = "native_goal_missing"
	binding.NativeGoal = false
	binding.Verified = false
	binding.NativeSource = "missing"
	if row.RecordState == "launched" {
		binding.Source = "launch-record"
	} else {
		binding.Source = "runtime-observation"
	}
	binding.Detail = "A live visible project lead is running without launch-record evidence of a native /goal command; relaunch from the generated /goal plan or treat this as an explicit unsupported fallback before claiming release readiness."
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
			rows[i].AgentPaneID = strings.TrimSpace(rows[i].Tmux.PaneID)
			rows[i].ManagedTarget = strings.TrimSpace(rows[i].Tmux.Target)
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
		rec.External = live.LaunchRecord.External
		rec.WakeAutoDrain = strings.TrimSpace(live.LaunchRecord.WakeInjectCmd) != ""
		rec.PreauthorizedActions = live.LaunchRecord.PreauthorizedActions
		rec.AdoptionMode = strings.TrimSpace(live.LaunchRecord.AdoptionMode)
		rec.LauncherPaneID = strings.TrimSpace(live.LaunchRecord.LauncherPaneID)
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
	if rec.Tmux != nil {
		rec.AgentPaneID = strings.TrimSpace(rec.Tmux.PaneID)
		rec.ManagedTarget = strings.TrimSpace(rec.Tmux.Target)
	}
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
