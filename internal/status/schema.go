// Package status is the 3.0 status contract (plan 4.6, R43): the schema of
// `amq-squad status --json`, the classifier that gives every seat and task
// row one state from a closed list, and the table that maps those states to
// the four command-center buckets (plan 4.6, briefs ruling 6). amq-noc reads
// this JSON and nothing else (R44). docs/status-contract.md is the prose
// form of this file.
//
// Compatibility rule: schema_version 1 grows by additive fields only. A
// field is never removed or renamed and never changes its JSON type inside
// version 1; testdata/status-v1.golden.json is the reference and
// CheckAdditive enforces the rule in CI.
package status

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is the contract major version.
const SchemaVersion = 1

// State is the classifier state of a seat or task row: the five states of
// plan 4.6 plus unknown.
type State string

const (
	StateNeedsYou State = "needs_you" // an open typed gate, or a refusal routed to the owner
	StateBlocked  State = "blocked"   // stopped on a decision the lead owes
	StateWaiting  State = "waiting"   // queued, or leased but not yet claimed
	StateOnline   State = "online"    // live seat; task with a live claimed lease
	StateStale    State = "stale"     // lease expired, session ended, task closed
	StateUnknown  State = "unknown"   // no signal to classify on
)

// Bucket is one of the four command-center buckets (amendment 24).
type Bucket string

const (
	BucketNeedsYou Bucket = "needs_you"
	BucketWorking  Bucket = "working"
	BucketReady    Bucket = "ready"
	BucketInactive Bucket = "inactive"
)

// MarkerUnknown is the bucket marker of a row whose state is unknown.
const MarkerUnknown = "unknown"

// BucketFor is the bucket table of plan 4.6. routedToOwner is true when the
// row's blocker is addressed to the owner (an open typed gate, a refusal
// escalated to an owner-signed gate); hasTask is true for a task row and for
// a seat that holds a task lease.
//
//	needs_you                      -> needs_you
//	blocked, routed to the owner   -> needs_you
//	blocked, routed to the lead    -> working
//	waiting                        -> working
//	online, with a task            -> working
//	online, no task                -> ready
//	stale                          -> inactive
//	unknown                        -> inactive, marker "unknown"
func BucketFor(s State, routedToOwner, hasTask bool) (Bucket, string) {
	switch s {
	case StateNeedsYou:
		return BucketNeedsYou, ""
	case StateBlocked:
		if routedToOwner {
			return BucketNeedsYou, ""
		}
		return BucketWorking, ""
	case StateWaiting:
		return BucketWorking, ""
	case StateOnline:
		if hasTask {
			return BucketWorking, ""
		}
		return BucketReady, ""
	case StateStale:
		return BucketInactive, ""
	default:
		return BucketInactive, MarkerUnknown
	}
}

// BucketInfo names a bucket for the renderer, in display order.
type BucketInfo struct {
	ID    Bucket `json:"id"`
	Label string `json:"label"`
	Rule  string `json:"rule"`
}

// Buckets is the display order and wording of the four buckets.
var Buckets = []BucketInfo{
	{BucketNeedsYou, "Needs you", "an open typed gate, or a refusal routed to the owner"},
	{BucketWorking, "Working", "a live lease with a task"},
	{BucketReady, "Ready", "a live seat lease and no task"},
	{BucketInactive, "Inactive", "an expired lease or an ended session; unknown lands here with a marker"},
}

// Action is a suggested command: an argv array plus a stable action id,
// never a shell string (plan 4.6). Inputs name values the renderer asks
// the user for; each provided input is appended as its flag and value.
type Action struct {
	ID     string        `json:"id"`
	Label  string        `json:"label"`
	Argv   []string      `json:"argv"`
	Inputs []ActionInput `json:"inputs"`
}

// ActionInput is one user-supplied value of an Action.
type ActionInput struct {
	Name     string   `json:"name"`
	Flag     string   `json:"flag"`
	Required bool     `json:"required"`
	Choices  []string `json:"choices"`
}

// Pool is the runtime x model x effort key of a task or seat.
type Pool struct {
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// Status is the whole `status --json` document.
type Status struct {
	SchemaVersion   int       `json:"schema_version"`
	Producer        string    `json:"producer"`
	ProducerVersion string    `json:"producer_version"`
	GeneratedAt     time.Time `json:"generated_at"`
	Root            string    `json:"root"`
	Session         string    `json:"session"`
	ProjectPath     string    `json:"project_path"`
	// LeadHandle is the handle of the seat whose role is lead, null when no
	// seat has that role (additive; 4-3 acceptance 7, from 2b-4's row 8). In
	// the fleet form, fleet.yaml's lead handle fills it when it is null.
	LeadHandle   *string      `json:"lead_handle"`
	Capabilities []string     `json:"capabilities"`
	Buckets      []BucketInfo `json:"buckets"`
	Counts       Counts       `json:"counts"`
	Seats        []Seat       `json:"seats"`
	Tasks        []Task       `json:"tasks"`
	Attention    []Attention  `json:"attention"`
	Gates        []Gate       `json:"gates"`
	Actions      Actions      `json:"actions"`
	Scheduler    Scheduler    `json:"scheduler"`
	Warnings     []string     `json:"warnings"`
	// Lead is the root's lead from its lead record (brief 31-1: lead.json
	// beside the lead lock), null when no lead has taken the lock.
	Lead *Lead `json:"lead"`
	// Decisions is the tail of the scheduler's decisions log, oldest first
	// (at most DecisionsTail lines), refusals such as exit_refused included.
	Decisions []Decision `json:"decisions"`
	// Train is the merge train's last runs (its audit log) and the owner
	// alerts.
	Train Train `json:"train"`
}

// DecisionsTail is how many decisions the document carries.
const DecisionsTail = 30

// Lead is the holder of the root's lead lock, from lead.json. Live is its
// pid alive with the recorded start stamp (a lock-free read; status never
// probes the lock itself).
type Lead struct {
	Handle     string    `json:"handle"`
	PID        int       `json:"pid"`
	Start      string    `json:"start"` // the raw start stamp of PID
	Pane       string    `json:"pane"`  // its tmux pane, "" outside tmux
	RecordedAt time.Time `json:"recorded_at"`
	Live       bool      `json:"live"`
	Attach     string    `json:"attach"` // tmux attach -t <pane>, "" without a pane
}

// Decision is one line of the decisions log.
type Decision struct {
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	Seat       string    `json:"seat"`
	TaskID     string    `json:"task_id"`
	Reason     string    `json:"reason"`
	ReasonCode string    `json:"reason_code"`
	Stage      string    `json:"stage"`
}

// Train is the merge train's view: its last two runs, newest first, and the
// owner alerts (ungated tags and releases, hung verifications).
type Train struct {
	Runs   []TrainRun   `json:"runs"`
	Alerts []TrainAlert `json:"alerts"`
}

// TrainRun is one run of the audit log. Done: it stopped, it was a plan, or
// every PR of its batch merged.
type TrainRun struct {
	Run        string    `json:"run"`
	StartedAt  time.Time `json:"started_at"`
	LastAt     time.Time `json:"last_at"`
	LastKind   string    `json:"last_kind"`
	Repository string    `json:"repository"`
	Base       string    `json:"base"`
	PRs        []int     `json:"prs"`
	Merged     []int     `json:"merged"`
	Refusal    string    `json:"refusal"` // the newest refusal or stop of the run, "" for none
	Done       bool      `json:"done"`
	DryRun     bool      `json:"dry_run"`
}

// TrainAlert is one owner alert.
type TrainAlert struct {
	ID     string    `json:"id"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail"`
	GateID string    `json:"gate_id"`
	At     time.Time `json:"at"`
}

// SeatLaunch is a seat's last launch from its records (brief 31-1): the
// staged record, the apply outcome, seat-exec's outcome and the accepted
// proof, AMQ's admitted wake, and the launch budget's marks.
type SeatLaunch struct {
	LaunchID   string `json:"launch_id"`
	Outcome    string `json:"outcome"` // staged | applied | apply_failed | exec_refused | proof_expired | live
	ReasonCode string `json:"reason_code"`
	// Wake is the wake AMQ admitted (its Prepare echo), null when none was
	// recorded.
	Wake   *SeatWake    `json:"wake"`
	Timing LaunchTiming `json:"timing"`
}

// SeatWake is AMQ's admitted wake for a seat.
type SeatWake struct {
	Mode        string `json:"mode"` // enabled | disabled
	RequireWake bool   `json:"require_wake"`
	Injector    string `json:"injector"`
	AuditReason string `json:"audit_reason"`
}

// LaunchCeiling binds the start to seat live (brief 31-1, plan 7.3); the
// first message has no ceiling.
const LaunchCeiling = 10 * time.Second

// LaunchTiming is the launch budget of one seat: the start (launch.json's
// start, AMQ_SQUAD_START), seat live (the accepted proof's stamp) and the
// first AMQ message (the scheduler's seat_first_message), and the segments
// up to seat live from the records, in order: prepare (the start to the
// staged record's started_at), mint (to the proof ticket's issued_at),
// apply (to AMQ's apply outcome), seat_exec (to the accepted proof).
type LaunchTiming struct {
	Start            *time.Time      `json:"start"`
	SeatLive         *time.Time      `json:"seat_live"`
	FirstMessage     *time.Time      `json:"first_message"`
	ToSeatLiveMS     *int64          `json:"to_seat_live_ms"`
	ToFirstMessageMS *int64          `json:"to_first_message_ms"`
	CeilingMS        int64           `json:"ceiling_ms"`
	OverCeiling      bool            `json:"over_ceiling"`
	SlowestSegment   string          `json:"slowest_segment"`
	Segments         []LaunchSegment `json:"segments"`
}

// LaunchSegment is one segment up to seat live; MS is null when a record
// that bounds it is missing.
type LaunchSegment struct {
	Name string `json:"name"`
	MS   *int64 `json:"ms"`
}

// Counts is the number of rows (seats with no task, plus tasks) per bucket.
type Counts struct {
	All      int `json:"all"`
	NeedsYou int `json:"needs_you"`
	Working  int `json:"working"`
	Ready    int `json:"ready"`
	Inactive int `json:"inactive"`
}

// Seat is one seat row.
type Seat struct {
	Handle         string     `json:"handle"`
	Role           string     `json:"role"`
	Runtime        string     `json:"runtime"`
	Model          string     `json:"model"`
	Effort         string     `json:"effort"`
	State          State      `json:"state"`
	Live           bool       `json:"live"` // the session is up or its lease is live, whatever the bucket
	ReasonCode     string     `json:"reason_code"`
	Bucket         Bucket     `json:"bucket"`
	BucketMarker   string     `json:"bucket_marker"`
	TaskID         string     `json:"task_id"`
	ProjectPath    string     `json:"project_path"`
	LastActivityAt *time.Time `json:"last_activity_at"`
	LastMessageAt  *time.Time `json:"last_message_at"`
	LastMessage    string     `json:"last_message"`
	PromptState    string     `json:"prompt_state"` // running | idle | unknown
	TokensIn       *int64     `json:"tokens_in"`
	TokensOut      *int64     `json:"tokens_out"`
	// Worktree and Branch are the seat's from its launch record, "" before
	// one (brief 31-1).
	Worktree string `json:"worktree"`
	Branch   string `json:"branch"`
	// Launch is the seat's last launch, null when it has no staged record.
	Launch *SeatLaunch `json:"launch"`
	// Warnings are the seat's warnings (brief 31-1), absent when none: a
	// Claude seat that ran with no hook report (claude_hooks_silent), its
	// worktree's Claude allow rules (claude_allow_rules_present), a
	// CLAUDE.md that imports a file outside its worktree
	// (claude_external_includes_pending). Each was a launch refusal before.
	// Then: a seat over the launch ceiling (launch_budget_exceeded, naming
	// its slowest segment), a seat whose admitted wake is off (wake_off,
	// AMQ's audit reason: a Codex seat without AMQ's dialog hook, a pi
	// seat), a seat at an open harness dialog (seat_stalled_at_dialog) and a
	// pi seat with no key for its provider (pi_provider_key_missing). None
	// is a refusal.
	Warnings []string `json:"warnings,omitempty"`
	// AcknowledgedWarnings (brief 31-2) are the seat's warnings the owner
	// cleared with the screen's clear key for the seat's current launch,
	// absent when none. A cleared warning is listed here instead of in
	// Warnings; the same condition on a later launch is in Warnings again.
	// A clear never changes the seat's state.
	AcknowledgedWarnings []string `json:"acknowledged_warnings,omitempty"`
}

// Task is one task row, joined to its lease holder.
type Task struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	Class          string     `json:"class"`
	TaskState      string     `json:"task_state"` // the store state: pending | in_progress | blocked | completed | cancelled
	State          State      `json:"state"`
	ReasonCode     string     `json:"reason_code"`
	Bucket         Bucket     `json:"bucket"`
	BucketMarker   string     `json:"bucket_marker"`
	Pool           *Pool      `json:"pool"`
	LeaseHolder    string     `json:"lease_holder"`
	LeaseExpiresAt *time.Time `json:"lease_expires_at"`
	LeaseClaimed   bool       `json:"lease_claimed"`
	Attempt        int        `json:"attempt"`
	WaitReason     string     `json:"wait_reason"`
	BlockReason    string     `json:"block_reason"`
	DependsOn      []string   `json:"depends_on"`
	ProjectPath    string     `json:"project_path"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	// Evidence and the PR binding (brief 31-1): the task's evidence line
	// and the PR the commit step (or the train's rebase) bound to it.
	Evidence     string `json:"evidence"`
	PRRepository string `json:"pr_repository"`
	PRNumber     int    `json:"pr_number"`
	PRHead       string `json:"pr_head"`
	// WaitHolders are, for a pending task waiting on a file lock
	// (wait_reason file_lock), the tasks holding it: the in-progress or
	// blocked tasks whose declared files overlap its own (the task store's
	// lock rule), [] otherwise. Empty under file_lock means no task holds
	// the lock any more: the wait reason is stale.
	WaitHolders []string `json:"wait_holders"`
}

// Attention is one item of the attention queue: a stable id, what kind of
// thing needs looking at, how urgent, since when, and what to run.
type Attention struct {
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	Severity string    `json:"severity"` // high | medium | low
	Subject  string    `json:"subject"`
	Since    time.Time `json:"since"`
	Seat     string    `json:"seat"`
	TaskID   string    `json:"task_id"`
	GateID   string    `json:"gate_id"`
	Bucket   Bucket    `json:"bucket"`
	Action   *Action   `json:"action"`
}

// Gate is one gate record of the gate store.
type Gate struct {
	ID           string `json:"id"`
	ActionClass  string `json:"action_class"`
	TargetDigest string `json:"target_digest"`
	State        string `json:"state"` // open | accepted | expired
	RaisedBy     string `json:"raised_by"`
}

// Actions are document-level actions.
type Actions struct {
	NewTask Action `json:"new_task"`
}

// Scheduler is the scheduler-side view: the seat cap and the usage-limit stop.
type Scheduler struct {
	MaxSeats        int        `json:"max_seats"`
	LiveSeats       int        `json:"live_seats"`
	UsageLimitSince *time.Time `json:"usage_limit_since"`
	UsageLimitUntil *time.Time `json:"usage_limit_until"`
	// The scheduler process from its launch record (brief 31-1): its pid,
	// its own admission, the squad launch's start (AMQ_SQUAD_START, the
	// launch budget's first mark) and whether the pid is alive. Zero and
	// null before a scheduler ran.
	PID         int        `json:"pid"`
	StartedAt   *time.Time `json:"started_at"`
	LaunchStart *time.Time `json:"launch_start"`
	Live        bool       `json:"live"`
}

// ValidateAction checks the argv rule of plan 4.6: a stable id, a non-empty
// argv whose program is a bare name (no spaces or shell syntax), and no shell
// interpreter running a command string.
func ValidateAction(a Action) error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("action %q: empty id", a.Label)
	}
	if len(a.Argv) == 0 || a.Argv[0] == "" || strings.ContainsAny(a.Argv[0], " \t\n;|&$`'\"<>()") {
		prog := ""
		if len(a.Argv) > 0 {
			prog = a.Argv[0]
		}
		return fmt.Errorf("action %s: argv[0] %q is not a bare program name", a.ID, prog)
	}
	switch filepath.Base(a.Argv[0]) {
	case "sh", "bash", "zsh", "dash", "fish", "env", "eval":
		return fmt.Errorf("action %s: argv runs a shell (%s)", a.ID, a.Argv[0])
	}
	for _, in := range a.Inputs {
		if in.Name == "" || !strings.HasPrefix(in.Flag, "--") {
			return fmt.Errorf("action %s: input %q needs a name and a --flag", a.ID, in.Name)
		}
	}
	return nil
}
