package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/activity"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/worktreeplan"
)

// The status verb prints the 3.0 status contract (plan 4.6, R43;
// docs/status-contract.md): status --json is the schema 1 document itself,
// the human form is a table of the same rows. The per-member liveness
// helpers below it are shared with doctor, liveness and the operator view.

const statusUsage = `amq-squad status - the squad's seats, tasks, attention queue and gates

Usage:
  amq-squad status [--root DIR | --project DIR --profile P --session S] [--json]
  amq-squad status --fleet [--fleet-file PATH] [--json]

Reads the AMQ session root (default AM_ROOT): the task store, the seat
mailboxes and launch records, the evidence ledger, the gate store and the
scheduler state. --json prints the status contract, schema 1
(docs/status-contract.md); it never writes. --fleet reads every squad of
~/.amq-squad/fleet.yaml into one fleet document (plan 16.3).

Examples:
  amq-squad status
  amq-squad status --root ~/.agent-mail/squad --json
  amq-squad status --project ~/Code/app --session squad
`

// statusNow and statusPIDAlive are seams for tests.
var (
	statusNow      = func() time.Time { return time.Now().UTC() }
	statusPIDAlive func(int) bool
)

func runStatusWithVersion(args []string, version string) error {
	f := newStoreFlags("status")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, statusUsage) }
	fleetAll := f.fs.Bool("fleet", false, "every squad of the fleet registry: the fleet document (ruling 16)")
	fleetFile := fleetFileFlag(f.fs)
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("status takes no positional arguments; got %q", f.fs.Arg(0))
	}
	if *fleetAll {
		return runFleetStatus(os.Stdout, *fleetFile, version, *f.jsonOut)
	}
	_, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	defer release()
	project := ""
	if flagWasSet(f.fs, "project") {
		project, _ = resolveProjectDirFlag("", *f.project, true)
	}
	if project == "" {
		project, _ = os.Getwd()
	}
	st := buildStatusContract(root, project, version, statusNow())
	if *f.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(st)
	}
	return writeStatusContractTable(os.Stdout, st)
}

// buildStatusContract collects one session root into the contract.
func buildStatusContract(root, project, version string, now time.Time) status.Status {
	var gates []status.GateInput
	records, err := readHookGates(root, now)
	var warnings []string
	if err != nil {
		warnings = append(warnings, "gate store: "+err.Error())
	}
	if ids, err := (gatestore.Store{Root: root}).IDs(); err == nil {
		for _, id := range ids {
			if kind, legacy := (gatestore.Store{Root: root}).Legacy(id); legacy {
				warnings = append(warnings, "gate store: "+gatestore.LegacyLine(id, kind))
			}
		}
	}
	for _, g := range records {
		since := time.Time{}
		if info, statErr := os.Stat(hookExtPath(root, "gates", g.GateID+".json")); statErr == nil {
			since = info.ModTime().UTC()
		}
		gates = append(gates, status.GateInput{ID: g.GateID, Class: g.Class, Digest: g.Digest, State: g.State, RaisedBy: g.RaisedBy, Since: since,
			Question: gatestore.Plain(g.Text)})
	}
	in := status.Collect(status.CollectOptions{
		Root: root, Session: filepath.Base(root), ProjectPath: project, ProducerVersion: version,
		Now: now, Gates: gates, PIDAlive: statusPIDAlive,
	})
	in.Warnings = append(warnings, in.Warnings...)
	return withLaunchFailures(status.Build(in), in.Scheduler.LaunchFailures)
}

func writeStatusContractTable(out io.Writer, st status.Status) error {
	fmt.Fprintf(out, "Squad %s (root %s): %d needs you, %d working, %d ready, %d inactive\n",
		st.Session, st.Root, st.Counts.NeedsYou, st.Counts.Working, st.Counts.Ready, st.Counts.Inactive)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "BUCKET\tSTATE\tROW\tSEAT\tPOOL\tREASON\tTITLE")
	for _, t := range st.Tasks {
		pool := "-"
		if t.Pool != nil {
			pool = t.Pool.Runtime + ":" + t.Pool.Model + ":" + t.Pool.Effort
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.Bucket, t.State, t.ID, orDash(t.LeaseHolder), pool, t.ReasonCode, t.Title)
	}
	for _, s := range st.Seats {
		if s.TaskID != "" {
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Bucket, s.State, "seat", s.Handle, orDash(strings.Trim(s.Runtime+":"+s.Model+":"+s.Effort, ":")), s.ReasonCode, s.LastMessage)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	for _, a := range st.Attention {
		line := fmt.Sprintf("attention [%s] %s: %s", a.Severity, a.Kind, a.Subject)
		if a.Action != nil {
			line += "  (" + strings.Join(a.Action.Argv, " ") + ")"
		}
		fmt.Fprintln(out, line)
	}
	for _, msg := range st.Warnings {
		fmt.Fprintln(out, "warning: "+msg)
	}
	return nil
}

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

type statusLaunchSelection struct {
	Entry          launch.Entry
	Found          bool
	DuplicatePaths []string
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
		Role:    m.Role,
		Handle:  m.Handle,
		Binary:  m.Binary,
		Session: workstream,
		CWD:     m.EffectiveCWD(t.Project),
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

// profileInitCommand returns the creation command to suggest when reporting a
// missing team profile.
func profileInitCommand(profile string) string {
	if profile == "" || profile == squadnamespace.DefaultProfile {
		return "amq-squad team propose"
	}
	return "amq-squad migrate (phase 5); named profiles were removed in 3.0, and amq-squad team propose writes the one squad.yaml"
}

func readPresenceForEntry(agentDir string) (presenceFile, error) {
	data, err := os.ReadFile(presencePath(agentDir))
	if err != nil {
		return presenceFile{}, err
	}
	var pres presenceFile
	if err := json.Unmarshal(data, &pres); err != nil {
		return presenceFile{}, err
	}
	return pres, nil
}

func presencePath(agentDir string) string {
	return filepath.Join(agentDir, "presence.json")
}
