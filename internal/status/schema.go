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
	SchemaVersion   int          `json:"schema_version"`
	Producer        string       `json:"producer"`
	ProducerVersion string       `json:"producer_version"`
	GeneratedAt     time.Time    `json:"generated_at"`
	Root            string       `json:"root"`
	Session         string       `json:"session"`
	ProjectPath     string       `json:"project_path"`
	Capabilities    []string     `json:"capabilities"`
	Buckets         []BucketInfo `json:"buckets"`
	Counts          Counts       `json:"counts"`
	Seats           []Seat       `json:"seats"`
	Tasks           []Task       `json:"tasks"`
	Attention       []Attention  `json:"attention"`
	Gates           []Gate       `json:"gates"`
	Actions         Actions      `json:"actions"`
	Scheduler       Scheduler    `json:"scheduler"`
	Warnings        []string     `json:"warnings"`
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
