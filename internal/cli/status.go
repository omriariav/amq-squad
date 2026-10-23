package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/activity"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/state"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/worktreeplan"
)

// statusState is the precise state vocabulary emitted by `amq-squad status`.
// Definitions:
//   - live:    launch-record PID alive AND binary matches, or a fresh active
//     presence for the handle; the agent is running.
//   - stale:   live signals exist on disk (launch record or presence) but none
//     verify as usable for this handle.
//   - missing: no launch record and no usable presence. The member is
//     configured but has never run in the resolved session.
//
// 3.0 (2a-2): the wake-live state and the wake and pane inputs are cut
// (manifest C10: "liveness vocabulary kept, pane/wake inputs cut").
type statusState string

const (
	statusStateLive          statusState = "live"
	statusStateStale         statusState = "stale"
	statusStateMissing       statusState = "missing"
	statusStateDuplicateLive statusState = "duplicate_live"
)

type statusSignals struct {
	AgentPID    int       `json:"agent_pid,omitempty"`
	AgentAlive  bool      `json:"agent_alive,omitempty"`
	BinaryMatch bool      `json:"binary_match,omitempty"`
	Presence    string    `json:"presence,omitempty"`
	LastSeen    time.Time `json:"last_seen,omitempty"`
}

// statusEnvelopeData is the kind="status" payload: resolved team-home,
// workstream, profile, and the per-member records.
type statusEnvelopeData struct {
	TeamHome         string                      `json:"team_home"`
	Workstream       string                      `json:"workstream"`
	Profile          string                      `json:"profile,omitempty"`
	Namespace        squadnamespace.Ref          `json:"namespace"`
	Operator         statusOperatorView          `json:"operator"`
	Capabilities     team.Capabilities           `json:"capabilities"`
	Orchestrated     bool                        `json:"orchestrated,omitempty"`
	Lead             string                      `json:"lead,omitempty"`
	LeadHandle       string                      `json:"lead_handle,omitempty"`
	Autonomous       team.AutonomousStatus       `json:"autonomous"`
	Warnings         []statusWarning             `json:"warnings,omitempty"`
	ExternalEvidence []state.ExternalEvidenceRow `json:"external_evidence,omitempty"`
	Records          []statusRecord              `json:"records"`
}

type statusWarning struct {
	Kind             string `json:"kind"`
	Session          string `json:"session"`
	Detail           string `json:"detail"`
	SuggestedCommand string `json:"suggested_command,omitempty"`
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
	Required  bool   `json:"required"`
	Owner     string `json:"owner,omitempty"`
	Unread    int    `json:"unread"`
	OpenGates *int   `json:"open_gates,omitempty"`
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
	// ClassificationError is fail-visible source evidence for callers that
	// must distinguish an ordinary missing member from an unreadable or
	// otherwise unclassifiable identity.
	ClassificationError string        `json:"classification_error,omitempty"`
	Signals             statusSignals `json:"signals"`
	liveness            agentLiveness
	// LaunchapiObservation is gh#766's structured launchapi liveness detail:
	// the session Inspect's ParticipantObservationV1 matched to this member's
	// handle. Corroborates/explains Detail only -- never changes
	// status/record_state.
	LaunchapiObservation *launchapiObservationJSON `json:"launchapi_observation,omitempty"`
	// Activity is an additive, honest busy/progress signal. Heartbeat-file
	// entries come from an agent-written activity.json; task-store entries only
	// seed current-task ownership and must not be treated as liveness.
	Activity *activity.Snapshot `json:"activity,omitempty"`
	// Worktree is the additive per-mutator Git isolation contract. Review-only
	// members omit it; unplanned mutation-capable members carry state
	// "unplanned" in human output and an empty durable state in JSON.
	Worktree *worktreeplan.MemberStatus `json:"worktree,omitempty"`
	// PreauthorizedActions surfaces the in-scope worker actions amq-squad
	// pre-authorized at launch (#296), mirroring launch.Record.PreauthorizedActions.
	PreauthorizedActions []string `json:"preauthorized_actions,omitempty"`
}

func runStatusWithVersion(args []string, version string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	sessionName := fs.String("session", "", "AMQ workstream session name (default: the team's workstream)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned status envelope instead of the human table")
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, sessionName, profileFlag)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad status - live state of this project's team in one workstream

Usage:
  amq-squad status [--session NAME] [--project DIR] [--profile NAME] [--json]

Prints each configured team member's state in the workstream (the team's
workstream unless --session names another), from the launch-record PID +
binary match and fresh presence.

Examples:
  amq-squad status
  amq-squad status --project ~/Code/app
  amq-squad status --session issue-96 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionName, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	if !team.ExistsProfile(ctx.ProjectDir, ctx.Profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", ctx.Profile, profileInitCommand(ctx.Profile))
	}
	return executeStatus(statusExecution{
		ProjectDir:       ctx.ProjectDir,
		RequestedSession: ctx.Session,
		ExplicitSession:  flagWasSet(fs, "session"),
		Profile:          ctx.Profile,
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
	s.ProjectDir = canonicalFilesystemPath(s.ProjectDir)
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
	now := time.Now()
	if s.Probe.Now != nil {
		now = s.Probe.Now()
	}
	warnings, err := statusWarnings(t.Project, s.Profile, workstream, now)
	if err != nil {
		return fmt.Errorf("scan status warnings: %w", err)
	}

	rows := buildStatusRows(t, s.Profile, workstream, s.Probe)
	if entries, scanErr := launch.ScanEntries(t.Project); scanErr == nil {
		warnings = append(warnings, statusUnmanagedLaunchRecordWarningsFromEntries(t, s.Profile, workstream, entries)...)
	}
	warnings = append(warnings, statusLaunchapiInspectWarnings(workstream, rows)...)
	if s.JSON {
		ns := squadnamespace.Resolve(t.Project, s.Profile, workstream)
		for i := range rows {
			rows[i].Namespace = ns
		}
		orchestrated, lead, leadHandle := orchestrationStatusFields(t)
		return writeJSONEnvelope(s.Out, "status", statusEnvelopeData{
			TeamHome:         t.Project,
			Workstream:       workstream,
			Profile:          s.Profile,
			Namespace:        ns,
			Operator:         statusOperatorForTeam(t, ns),
			Capabilities:     team.EffectiveCapabilities(t),
			Orchestrated:     orchestrated,
			Lead:             lead,
			LeadHandle:       leadHandle,
			Autonomous:       team.EffectiveAutonomousStatus(t),
			Warnings:         warnings,
			ExternalEvidence: statusExternalEvidence(t, s.Profile, workstream, rows, now),
			Records:          rows,
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
	fmt.Fprintln(s.Out)
	return writeStatusTable(s.Out, rows, policy)
}

func writeStatusTable(out io.Writer, rows []statusRecord, policy outputPolicy) error {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ROLE\tHANDLE\tBINARY\tSESSION\tSTATUS\tWORKTREE\tBRANCH\tBASE..HEAD\tGIT\tSCOPE\tHANDOFF\tDETAIL")
	for _, r := range rows {
		worktree, branch, baseHead, gitState, scope, handoff := statusWorktreeColumns(r.Worktree)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", r.Role, r.Handle, r.Binary, r.Session, colorStatus(policy, string(r.Status)), worktree, branch, baseHead, gitState, scope, handoff, r.Detail)
	}
	return w.Flush()
}

func statusWorktreeColumns(status *worktreeplan.MemberStatus) (string, string, string, string, string, string) {
	if status == nil {
		return "-", "-", "-", "-", "-", "-"
	}
	state := printableWorktreeState(status.State)
	worktree := state + ":" + status.Worktree
	gitState := "clean"
	switch {
	case status.Drifted:
		gitState = "drifted"
	case status.Dirty:
		gitState = "dirty"
	case !status.Registered && status.State != "":
		gitState = "unregistered"
	}
	handoff := shortWorktreeSHA(status.HandoffSHA)
	baseHead := shortWorktreeSHA(status.BaseSHA) + ".." + shortWorktreeSHA(status.CurrentHEAD)
	return worktree, status.Branch, baseHead, gitState, strings.Join(status.Scope, ","), handoff
}

func statusWarnings(projectDir, profile, session string, now time.Time) ([]statusWarning, error) {
	var warnings []statusWarning
	taskWarnings, err := statusTaskWarnings(projectDir, profile, session)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, taskWarnings...)
	warnings = append(warnings, statusAgedOperatorGateWarnings(projectDir, profile, session, now)...)
	return warnings, nil
}

func statusAgedOperatorGateWarnings(projectDir, profile, session string, now time.Time) []statusWarning {
	ns := squadnamespace.Resolve(projectDir, profile, session)
	root := strings.TrimSpace(ns.AMQRoot)
	if root == "" {
		return nil
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}
	baseRoot := root
	if squadnamespace.ProfilesEqual(profile, team.DefaultProfile) {
		baseRoot = filepath.Dir(root)
	}
	data, err := buildOperatorStatusData(operatorExecution{
		ProjectDir: projectDir,
		Profile:    profile,
		Session:    session,
		BaseRoot:   baseRoot,
		Probe: state.Probe{
			Now: func() time.Time { return now },
		},
		Now: func() time.Time { return now },
	})
	if err != nil {
		return nil
	}
	return statusWarningsForAgedOperatorGates(data)
}

func statusWarningsForAgedOperatorGates(data operatorStatusEnvelopeData) []statusWarning {
	var warnings []statusWarning
	for _, item := range data.Attention {
		if !isOpenGateAttention(item) {
			continue
		}
		escalation := state.OperatorGateEscalation(item.Escalation)
		if state.OperatorGateEscalationRank(escalation) < state.OperatorGateEscalationRank(state.OperatorGateEscalationReminder) {
			continue
		}
		kind := "operator_gate_" + strings.ReplaceAll(string(escalation), "-", "_")
		detail := fmt.Sprintf("operator gate %s aged to %s after %s: %s; durable AMQ for handle %s requires visible escalation",
			item.Thread, escalation, item.Age, item.Subject, printableHandle(data.Operator.Handle))
		warnings = append(warnings, statusWarning{
			Kind:             kind,
			Session:          item.Session,
			Detail:           detail,
			SuggestedCommand: item.Inspect,
		})
	}
	return warnings
}

func statusTaskWarnings(projectDir, profile, session string) ([]statusWarning, error) {
	tasks, err := taskstore.ListForProfile(projectDir, profile, session)
	if err != nil {
		return nil, err
	}
	var warnings []statusWarning
	for _, t := range tasks {
		if t.Status == taskstore.StatusCompletedPendingReconcile {
			warnings = append(warnings, statusWarning{
				Kind:    "task_legacy_status",
				Session: session,
				Detail: fmt.Sprintf(
					"task %s retains legacy status completed_pending_reconcile; simple mode does not inspect completion evidence or run reconciliation, so use task list and make the next transition explicitly",
					t.ID,
				),
			})
		}
	}
	return warnings, nil
}

func statusCanonicalThread(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	lower := strings.ToLower(raw)
	if !strings.HasPrefix(lower, "p2p/") {
		return raw
	}
	pair := strings.TrimPrefix(lower, "p2p/")
	parts := strings.Split(pair, "__")
	if len(parts) != 2 {
		return lower
	}
	return canonicalP2PThread(stripThreadRole(parts[0]), stripThreadRole(parts[1]))
}

func stripThreadRole(handle string) string {
	handle = strings.TrimSpace(handle)
	if before, _, ok := strings.Cut(handle, ":"); ok {
		return before
	}
	return handle
}

func printableHandle(handle string) string {
	if strings.TrimSpace(handle) == "" {
		return "<unknown>"
	}
	return handle
}

func statusOperatorForTeam(t team.Team, ns squadnamespace.Ref) statusOperatorView {
	op := team.EffectiveOperator(t)
	contract := team.OperatorContractForMode(op.InteractionMode)
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
		Required: op.PollRequired,
		Owner:    contract.PollOwner,
	}
	return out
}

func buildStatusRows(t team.Team, profile, workstream string, probe duplicateLaunchProbe) []statusRecord {
	members := orderedTeamMembers(t.Members)
	launchEntries, launchScanErr := launch.ScanEntries(t.Project)
	rows := make([]statusRecord, 0, len(members))
	for _, m := range members {
		rows = append(rows, classifyMemberStatusFromEntries(t, profile, m, workstream, probe, launchEntries, launchScanErr))
	}
	attachStatusActivities(t.Project, profile, workstream, rows, probe.Now())
	attachStatusWorktrees(t, profile, workstream, rows)
	return rows
}
func attachStatusWorktrees(t team.Team, profile, workstream string, rows []statusRecord) {
	service, err := worktreeplan.NewService(t, profile, workstream, worktreeplan.ExecGit{}, nil)
	if err != nil {
		return
	}
	inspection, err := service.Inspect()
	if err != nil {
		for i := range rows {
			member, ok := statusTeamMember(t, rows[i].Role)
			if !ok || team.EffectiveActorMode(t, member) != team.ActorModeImplementation {
				continue
			}
			rows[i].Worktree = &worktreeplan.MemberStatus{
				Role: rows[i].Role, Handle: rows[i].Handle, CWD: rows[i].CWD,
				Worktree: rows[i].CWD, Drifted: true, Detail: err.Error(),
			}
		}
		return
	}
	byRole := map[string]worktreeplan.MemberStatus{}
	for _, status := range inspection.Members {
		byRole[status.Role] = status
	}
	for i := range rows {
		if status, ok := byRole[rows[i].Role]; ok {
			copy := status
			rows[i].Worktree = &copy
		}
	}
}

func statusTeamMember(t team.Team, role string) (team.Member, bool) {
	for _, member := range t.Members {
		if member.Role == role {
			return member, true
		}
	}
	return team.Member{}, false
}

func attachStatusActivities(projectDir, profile, workstream string, rows []statusRecord, now time.Time) {
	activeTasks := activeTasksByAssignee(projectDir, profile, workstream)
	for i := range rows {
		if strings.TrimSpace(rows[i].AgentDir) != "" {
			if act, ok, err := activity.Read(rows[i].AgentDir, now, activity.DefaultStaleAfter); err == nil && ok {
				rows[i].Activity = &act
				continue
			} else if err != nil {
				act := activity.UnknownSnapshot(rows[i].Handle, "activity heartbeat unreadable")
				rows[i].Activity = &act
				continue
			}
		}
		if task, ok := activeTasks[rows[i].Handle]; ok {
			rows[i].Activity = taskStoreActivity(task, now)
		}
	}
}

func activeTasksByAssignee(projectDir, profile, workstream string) map[string]taskstore.Task {
	tasks, err := taskstore.ListForProfile(projectDir, profile, workstream)
	if err != nil {
		return nil
	}
	out := map[string]taskstore.Task{}
	for _, t := range tasks {
		if (t.Status != taskstore.StatusInProgress && t.Status != taskstore.StatusCompletedPendingReconcile) || strings.TrimSpace(t.AssignedTo) == "" {
			continue
		}
		cur, ok := out[t.AssignedTo]
		if !ok || t.UpdatedAt.After(cur.UpdatedAt) || (t.UpdatedAt.Equal(cur.UpdatedAt) && t.ID > cur.ID) {
			out[t.AssignedTo] = t
		}
	}
	return out
}

func taskStoreActivity(t taskstore.Task, now time.Time) *activity.Snapshot {
	detail := strings.TrimSpace(t.Title)
	if detail == "" {
		detail = "current task from task store; no fresh activity heartbeat"
	}
	act := activity.TaskStoreSnapshot(t.AssignedTo, t.ID, detail, t.UpdatedAt, now, activity.DefaultStaleAfter)
	return &act
}

func statusExternalEvidence(t team.Team, profile, workstream string, rows []statusRecord, now time.Time) []state.ExternalEvidenceRow {
	root := firstStatusRoot(rows)
	if strings.TrimSpace(root) == "" {
		return nil
	}
	operatorHandle := team.DefaultOperatorHandle
	if op := team.EffectiveOperator(t); op.Enabled && strings.TrimSpace(op.Handle) != "" {
		operatorHandle = strings.TrimSpace(op.Handle)
	}
	probe := state.Probe{
		PIDAlive:     func(int) bool { return false },
		ProcessMatch: func(int, func(string) bool) bool { return false },
		Now:          func() time.Time { return now },
	}
	snap, err := state.BuildWithThresholds(t.Project, filepath.Dir(root), probe, state.Thresholds{OperatorHandle: operatorHandle})
	if err != nil {
		return nil
	}
	namespaceID := squadnamespace.ID(profile, workstream)
	for _, sess := range snap.Sessions {
		if sess.NamespaceID == namespaceID {
			return sess.Coordination.ExternalEvidence
		}
	}
	return nil
}

// statusLaunchapiInspectWarnings surfaces gh#737's session-level warning
// when launchapi.Inspect itself errored (I/O, not a successful
// binding_missing result) for this session -- per cto's ruling, no member's
// verdict is clamped on a call error, but the failure must still be visible.
// Deduplicated to at most one warning per distinct evidence string even
// though every row in a launchapi-launched roster independently observes
// the same session-level failure.
func statusLaunchapiInspectWarnings(session string, rows []statusRecord) []statusWarning {
	seen := make(map[string]bool)
	var warnings []statusWarning
	for _, row := range rows {
		if row.liveness.InspectSignal.Outcome != launchapiInspectCallFailed {
			continue
		}
		evidence := strings.TrimSpace(row.liveness.InspectSignal.Evidence)
		if seen[evidence] {
			continue
		}
		seen[evidence] = true
		warnings = append(warnings, statusWarning{
			Kind:    "launchapi_inspect_failed",
			Session: session,
			Detail:  "session-level launchapi.Inspect call failed; per-seat liveness falls back to existing pane/launch-record/wake/presence signals unchanged: " + evidence,
		})
	}
	return warnings
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

type statusLaunchSelection struct {
	Entry          launch.Entry
	Found          bool
	DuplicatePaths []string
}

func statusUnmanagedLaunchRecordWarningsFromEntries(t team.Team, profile, workstream string, entries []launch.Entry) []statusWarning {
	configuredHandles := make(map[string]bool, len(t.Members))
	for _, member := range t.Members {
		configuredHandles[strings.TrimSpace(memberHandle(member))] = true
	}
	var warnings []statusWarning
	for _, entry := range entries {
		rec := entry.Record
		if strings.TrimSpace(rec.Session) != strings.TrimSpace(workstream) || !squadnamespace.ProfilesEqual(rec.TeamProfile, profile) {
			continue
		}
		if strings.TrimSpace(rec.TeamHome) != "" && !sameResolvedDir(rec.TeamHome, t.Project) {
			continue
		}
		handle := strings.TrimSpace(rec.Handle)
		if configuredHandles[handle] {
			continue
		}
		warnings = append(warnings, statusWarning{
			Kind:    "unmanaged_launch_record",
			Session: workstream,
			Detail: fmt.Sprintf(
				"unmanaged launch record for unconfigured handle %q (recorded role %q): %s",
				handle, strings.TrimSpace(rec.Role), launch.ExistingPath(entry.AgentDir),
			),
		})
	}
	sort.Slice(warnings, func(i, j int) bool { return warnings[i].Detail < warnings[j].Detail })
	return warnings
}

func selectStatusLaunchRecord(t team.Team, profile string, m team.Member, workstream string, probe duplicateLaunchProbe, entries []launch.Entry) statusLaunchSelection {
	return selectLaunchRecordWithRuntimeProbe(t, profile, m, workstream, launchRuntimeProbeFromDuplicate(probe), entries)
}

// selectLaunchRecordWithRuntimeProbe is the record-first identity selector
// shared by status, down, and simple-start reconciliation. Callers may differ
// in how they observe runtime liveness, but durable candidacy remains one exact
// profile/session/team-home/handle pipeline.
func selectLaunchRecordWithRuntimeProbe(t team.Team, profile string, m team.Member, workstream string, runtimeProbe launchRuntimeProbe, entries []launch.Entry) statusLaunchSelection {
	handle := memberHandle(m)
	var candidates []launch.Entry
	for _, entry := range entries {
		rec := entry.Record
		if strings.TrimSpace(rec.Session) != strings.TrimSpace(workstream) || !squadnamespace.ProfilesEqual(rec.TeamProfile, profile) {
			continue
		}
		if strings.TrimSpace(rec.TeamHome) != "" && !sameResolvedDir(rec.TeamHome, t.Project) {
			continue
		}
		// A role label is mutable roster metadata, not actor identity. Selecting
		// by role alone can alias a different mailbox into this member's status
		// and down target. The durable identity key is profile/session/handle.
		if strings.TrimSpace(rec.Handle) != strings.TrimSpace(handle) {
			continue
		}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 {
		return statusLaunchSelection{}
	}
	sort.Slice(candidates, func(i, j int) bool {
		return launch.ExistingPath(candidates[i].AgentDir) < launch.ExistingPath(candidates[j].AgentDir)
	})
	var live []launch.Entry
	for _, entry := range candidates {
		if classifyLaunchRuntimeIdentity(entry.Record, "", runtimeProbe).PIDLive {
			live = append(live, entry)
		}
	}
	if len(live) > 1 {
		paths := make([]string, 0, len(live))
		for _, entry := range live {
			paths = append(paths, launch.ExistingPath(entry.AgentDir))
		}
		sort.Strings(paths)
		return statusLaunchSelection{DuplicatePaths: paths}
	}
	if len(live) == 1 {
		return statusLaunchSelection{Entry: live[0], Found: true}
	}
	canonicalRoot := squadnamespace.AMQRoot(t.Project, profile, workstream)
	for _, entry := range candidates {
		if sameResolvedDir(entry.Record.Root, canonicalRoot) {
			return statusLaunchSelection{Entry: entry, Found: true}
		}
	}
	return statusLaunchSelection{Entry: candidates[0], Found: true}
}

func classifyMemberStatusFromEntries(t team.Team, profile string, m team.Member, workstream string, probe duplicateLaunchProbe, entries []launch.Entry, scanErr error) statusRecord {
	rec := statusRecord{
		Role:        m.Role,
		Handle:      m.Handle,
		Binary:      m.Binary,
		Session:     workstream,
		CWD:         m.EffectiveCWD(t.Project),
		SpawnOrigin: m.SpawnOrigin,
		SpawnDepth:  m.SpawnDepth,
	}
	if scanErr != nil {
		rec.Status = statusStateMissing
		rec.RecordState = "missing"
		rec.Detail = "scan launch records: " + scanErr.Error()
		rec.ClassificationError = rec.Detail
		return rec
	}
	selection := selectStatusLaunchRecord(t, profile, m, workstream, probe, entries)
	if len(selection.DuplicatePaths) > 0 {
		rec.Handle = memberHandle(m)
		rec.Status = statusStateDuplicateLive
		rec.RecordState = string(statusStateDuplicateLive)
		rec.Detail = "duplicate_live: multiple authoritative live launch records: " + strings.Join(selection.DuplicatePaths, ", ")
		return rec
	}
	if selection.Found {
		selected := selection.Entry.Record
		rec.Handle = selected.Handle
		if strings.TrimSpace(rec.Handle) == "" {
			rec.Handle = memberHandle(m)
		}
		if strings.TrimSpace(selected.CWD) != "" {
			rec.CWD = selected.CWD
		}
		rec.Root = absoluteAMQRoot(t.Project, selected.Root)
		rec.AgentDir = selection.Entry.AgentDir
	} else {
		env, err := resolveAMQEnvForTeamProfile(rec.CWD, profile, workstream, m.Handle)
		if err != nil {
			rec.Status = statusStateMissing
			rec.RecordState = "missing"
			rec.Detail = "amq env unresolved: " + err.Error()
			rec.ClassificationError = rec.Detail
			return rec
		}
		if env.Me != "" {
			rec.Handle = env.Me
		}
		rec.Root = absoluteAMQRoot(rec.CWD, env.Root)
		rec.AgentDir = filepath.Join(rec.Root, "agents", rec.Handle)
	}
	root := rec.Root

	// Consume the single shared liveness classifier: one disk read + probe checks
	// returning the verdict, signals, detail and status state. The rollup wrapper
	// layers gh#737's session-level launchapi Inspect floor on top.
	live := classifyAgentLivenessForRollup(t.Project, rec.AgentDir, root, profile, rec.Handle, m.Role, m.Binary, workstream, rec.CWD, probe)
	rec.liveness = live
	rec.LaunchapiObservation = live.LaunchapiObservation
	rec.ClassificationError = live.SourceError
	if live.LaunchFound {
		rec.PreauthorizedActions = live.LaunchRecord.PreauthorizedActions
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
	if rec.ClassificationError != "" {
		rec.Detail = rec.ClassificationError
	}
	return rec
}

func statusRecordState(live agentLiveness) string {
	switch live.Status {
	case statusStateLive:
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
	if presenceMismatched {
		parts = append(parts, "fresh presence for unrelated handle")
	}
	if len(parts) == 0 {
		return "stale signals on disk"
	}
	return strings.Join(parts, "; ")
}
