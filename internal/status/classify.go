package status

import (
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The classifier and the attention queue (plan 4.6: "the five-state
// classifier and the attention queue move from amq-noc into amq-squad"),
// reduced to what the contract needs. Build is pure: Collect gathers the
// inputs from disk, Build turns them into the contract document.

// SeatInput is what Collect observed about one seat.
type SeatInput struct {
	Handle, Role, Runtime, Model, Effort, ProjectPath string
	Live                                              bool // a live process, a fresh presence or recent evidence
	HasSignal                                         bool // any launch record, presence or evidence at all
	LastActivity                                      *time.Time
	LastMessageAt                                     *time.Time
	LastMessage                                       string
	PromptState                                       string
	TokensIn                                          *int64
	TokensOut                                         *int64
}

// GateInput is one gate record (verified by the caller against its answer).
type GateInput struct {
	ID, Class, Digest, State, RaisedBy string
	Since                              time.Time
}

// ThreadInput is one AMQ thread that waits on the owner (the thread triage
// of internal/state).
type ThreadInput struct {
	ID, Seat, Subject string
	Since             time.Time
}

// Input is everything Build needs.
type Input struct {
	Root, Session, ProjectPath, ProducerVersion string
	Now                                         time.Time
	Tasks                                       []task.Task
	Seats                                       []SeatInput
	Gates                                       []GateInput
	NeedsYou                                    []ThreadInput
	Scheduler                                   scheduler.State
	Capabilities                                []string
	Warnings                                    []string
}

// BaseCapabilities are the features every schema 1 producer fills.
var BaseCapabilities = []string{"tasks", "leases", "pools", "attention", "gates", "buckets", "new_task_action"}

const lastMessageLimit = 200

// Build classifies every task and seat, builds the attention queue, counts
// the buckets and returns the contract document. The same input gives the
// same output.
func Build(in Input) Status {
	s := Status{
		SchemaVersion: SchemaVersion, Producer: "amq-squad", ProducerVersion: in.ProducerVersion,
		GeneratedAt: in.Now.UTC(), Root: in.Root, Session: in.Session, ProjectPath: in.ProjectPath,
		Capabilities: append(append([]string{}, BaseCapabilities...), in.Capabilities...),
		Buckets:      Buckets, Seats: []Seat{}, Tasks: []Task{}, Attention: []Attention{}, Gates: []Gate{},
		Warnings: append([]string{}, in.Warnings...),
		Actions:  Actions{NewTask: NewTaskAction(in.Root)},
		Scheduler: Scheduler{MaxSeats: in.Scheduler.MaxSeats, LiveSeats: in.Scheduler.LiveSeats,
			UsageLimitSince: in.Scheduler.UsageLimitSince, UsageLimitUntil: in.Scheduler.UsageLimitUntil},
	}
	openGateBy := map[string]GateInput{}
	for _, g := range sortedGates(in.Gates) {
		s.Gates = append(s.Gates, Gate{ID: g.ID, ActionClass: g.Class, TargetDigest: g.Digest, State: g.State, RaisedBy: g.RaisedBy})
		if g.State == "open" {
			if _, seen := openGateBy[g.RaisedBy]; !seen {
				openGateBy[g.RaisedBy] = g
			}
			s.Attention = append(s.Attention, Attention{
				ID: "gate:" + g.ID, Kind: "gate", Severity: "high", Since: g.Since, Seat: g.RaisedBy, GateID: g.ID,
				Subject: "gate " + g.ID + ": " + g.Class + " " + shortDigest(g.Digest), Bucket: BucketNeedsYou,
			})
		}
	}
	asked := map[string]ThreadInput{}
	for _, th := range in.NeedsYou {
		if _, seen := asked[th.Seat]; !seen {
			asked[th.Seat] = th
		}
		s.Attention = append(s.Attention, Attention{
			ID: "thread:" + th.ID, Kind: "question", Severity: "high", Since: th.Since, Seat: th.Seat,
			Subject: th.Subject, Bucket: BucketNeedsYou,
		})
	}
	tasks := append([]task.Task(nil), in.Tasks...)
	task.DispatchOrder(tasks)
	for _, t := range tasks {
		row := classifyTask(t, in.Now)
		row.Bucket, row.BucketMarker = BucketFor(row.State, t.State == task.StateBlocked && t.BlockRoute == task.RouteOwner, true)
		s.Tasks = append(s.Tasks, row)
		if a := taskAttention(t, row, in.Root); a != nil {
			s.Attention = append(s.Attention, *a)
		}
	}
	for _, in2 := range sortedSeats(in.Seats) {
		row, routed := classifySeat(in2, in.Tasks, openGateBy, asked, in.Now)
		row.Bucket, row.BucketMarker = BucketFor(row.State, routed, row.TaskID != "")
		s.Seats = append(s.Seats, row)
		if row.State == StateStale && row.TaskID != "" {
			s.Attention = append(s.Attention, Attention{
				ID: "seat:" + row.Handle + ":stale", Kind: "seat_stale", Severity: "medium", Since: deref(in2.LastActivity),
				Seat: row.Handle, TaskID: row.TaskID, Subject: "seat " + row.Handle + " holds " + row.TaskID + " with no live signal",
				Bucket: row.Bucket, Action: taskShowAction(in.Root, row.TaskID),
			})
		}
	}
	if since := in.Scheduler.UsageLimitSince; since != nil && (in.Scheduler.UsageLimitUntil == nil || in.Now.Before(*in.Scheduler.UsageLimitUntil)) {
		s.Attention = append(s.Attention, Attention{
			ID: "scheduler:usage_limit", Kind: "usage_limit", Severity: "medium", Since: *since,
			Subject: "a usage-limit event stopped new seats", Bucket: BucketWorking,
		})
	}
	sortAttention(s.Attention)
	for _, t := range s.Tasks {
		s.Counts.add(t.Bucket)
	}
	for _, st := range s.Seats {
		if st.TaskID == "" {
			s.Counts.add(st.Bucket)
		}
	}
	return s
}

func (c *Counts) add(b Bucket) {
	c.All++
	switch b {
	case BucketNeedsYou:
		c.NeedsYou++
	case BucketWorking:
		c.Working++
	case BucketReady:
		c.Ready++
	default:
		c.Inactive++
	}
}

// classifyTask gives a task row its state and reason code.
func classifyTask(t task.Task, now time.Time) Task {
	row := Task{
		ID: t.ID, Title: t.Title, Class: t.Class, TaskState: t.State, Attempt: t.Attempt, WaitReason: t.WaitReason,
		BlockReason: t.BlockReason, DependsOn: append([]string{}, t.DependsOn...), ProjectPath: t.ProjectPath,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
	if t.Pool != nil {
		row.Pool = &Pool{Runtime: t.Pool.Runtime, Model: t.Pool.Model, Effort: t.Pool.Effort}
	}
	if t.Lease != nil {
		exp := t.Lease.ExpiresAt
		row.LeaseHolder, row.LeaseExpiresAt, row.LeaseClaimed = t.Lease.Holder, &exp, t.Lease.ClaimedAt != nil
	}
	switch t.State {
	case task.StatePending:
		row.State, row.ReasonCode = StateWaiting, firstNonEmpty(t.WaitReason, "queued")
	case task.StateInProgress:
		switch {
		case t.Lease == nil:
			row.State, row.ReasonCode = StateUnknown, "lease_missing"
		case t.Report != nil:
			row.State, row.ReasonCode = StateOnline, "commit_pending"
		case !now.Before(t.Lease.ExpiresAt):
			row.State, row.ReasonCode = StateStale, "lease_expired"
		case t.Lease.ClaimedAt == nil:
			row.State, row.ReasonCode = StateWaiting, "lease_unclaimed"
		default:
			row.State, row.ReasonCode = StateOnline, "lease_claimed"
		}
	case task.StateBlocked:
		row.State, row.ReasonCode = StateBlocked, firstNonEmpty(t.BlockReason, "blocked")
		if t.BlockRoute == task.RouteOwner {
			row.State = StateNeedsYou
		}
	case task.StateCompleted:
		row.State, row.ReasonCode = StateStale, "task_completed"
	case task.StateCancelled:
		row.State, row.ReasonCode = StateStale, "task_cancelled"
	default:
		row.State, row.ReasonCode = StateUnknown, "state_unknown"
	}
	return row
}

// classifySeat gives a seat row its state; routed is true when its blocker
// is addressed to the owner.
func classifySeat(in SeatInput, tasks []task.Task, gates map[string]GateInput, asked map[string]ThreadInput, now time.Time) (Seat, bool) {
	row := Seat{
		Handle: in.Handle, Role: in.Role, Runtime: in.Runtime, Model: in.Model, Effort: in.Effort, ProjectPath: in.ProjectPath,
		LastActivityAt: in.LastActivity, LastMessageAt: in.LastMessageAt, LastMessage: truncate(in.LastMessage, lastMessageLimit),
		PromptState: firstNonEmpty(in.PromptState, "unknown"), TokensIn: in.TokensIn, TokensOut: in.TokensOut,
	}
	held := task.HeldBy(in.Handle, tasks)
	if held != nil {
		row.TaskID = held.ID
		if row.ProjectPath == "" {
			row.ProjectPath = held.ProjectPath
		}
	}
	switch {
	case gates[in.Handle].ID != "":
		row.State, row.ReasonCode = StateNeedsYou, "gate_open"
		return row, true
	case asked[in.Handle].ID != "":
		row.State, row.ReasonCode = StateNeedsYou, "question_to_owner"
		return row, true
	case held != nil && held.State == task.StateBlocked:
		row.State, row.ReasonCode = StateBlocked, firstNonEmpty(held.BlockReason, "blocked")
		return row, held.BlockRoute == task.RouteOwner
	case !in.Live && in.HasSignal:
		row.State, row.ReasonCode = StateStale, "no_live_signal"
	case !in.Live:
		row.State, row.ReasonCode = StateUnknown, "no_signal"
	case held != nil && held.Lease != nil && held.Lease.ClaimedAt == nil:
		row.State, row.ReasonCode = StateWaiting, "lease_unclaimed"
	case held != nil && held.Lease != nil && !now.Before(held.Lease.ExpiresAt):
		row.State, row.ReasonCode = StateStale, "lease_expired"
	case held != nil:
		row.State, row.ReasonCode = StateOnline, "lease_claimed"
	default:
		row.State, row.ReasonCode = StateOnline, "idle"
	}
	return row, false
}

// taskAttention is the attention item of a task row, if any.
func taskAttention(t task.Task, row Task, root string) *Attention {
	a := &Attention{TaskID: t.ID, Seat: row.LeaseHolder, Since: t.UpdatedAt, Bucket: row.Bucket, Action: taskShowAction(root, t.ID)}
	switch {
	case t.State == task.StateBlocked:
		a.ID, a.Kind, a.Severity = "task:"+t.ID+":blocked", "task_blocked", "medium"
		if t.BlockRoute == task.RouteOwner {
			a.Severity = "high"
		}
		a.Subject = t.ID + " blocked (" + t.BlockReason + "): " + t.Title
		if t.Report != nil {
			a.Seat = t.Report.By
		}
	case row.ReasonCode == "lease_expired":
		a.ID, a.Kind, a.Severity = "task:"+t.ID+":lease_expired", "lease_expired", "medium"
		a.Subject = t.ID + " lease held by " + row.LeaseHolder + " expired: " + t.Title
		a.Action = &Action{ID: "task.reconcile", Label: "list expired leases", Argv: []string{"amq-squad", "task", "reconcile", "--root", root}, Inputs: []ActionInput{}}
	case t.State == task.StatePending && waitNeedsAttention(t.WaitReason):
		a.ID, a.Kind, a.Severity = "task:"+t.ID+":wait:"+t.WaitReason, "task_waiting", "low"
		if t.WaitReason == task.WaitNoRoute {
			a.Severity = "medium"
		}
		a.Subject = t.ID + " waits (" + t.WaitReason + "): " + t.Title
	default:
		return nil
	}
	return a
}

// waitNeedsAttention: dependencies, file locks and a starting seat resolve
// by themselves; the other waits need a decision or name a stop.
func waitNeedsAttention(reason string) bool {
	switch reason {
	case "", task.WaitDependencies, task.WaitFileLock, task.WaitSeatStarting:
		return false
	}
	return true
}

func taskShowAction(root, id string) *Action {
	return &Action{ID: "task.show", Label: "show the task", Argv: []string{"amq-squad", "task", "show", id, "--root", root}, Inputs: []ActionInput{}}
}

// NewTaskAction is the "new task" action: the renderer runs the argv plus
// the inputs the user gave; the scheduler dispatches the task.
func NewTaskAction(root string) Action {
	return Action{
		ID: "task.new", Label: "new task", Argv: []string{"amq-squad", "task", "add", "--root", root},
		Inputs: []ActionInput{
			{Name: "title", Flag: "--title", Required: true, Choices: []string{}},
			{Name: "class", Flag: "--class", Required: true, Choices: append([]string{}, task.Classes...)},
		},
	}
}

var severityRank = map[string]int{"high": 0, "medium": 1, "low": 2}

// sortAttention orders by severity, then oldest first, then id.
func sortAttention(items []Attention) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if severityRank[a.Severity] != severityRank[b.Severity] {
			return severityRank[a.Severity] < severityRank[b.Severity]
		}
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		return a.ID < b.ID
	})
}

func sortedGates(in []GateInput) []GateInput {
	out := append([]GateInput(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedSeats(in []SeatInput) []SeatInput {
	out := append([]SeatInput(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

func shortDigest(d string) string {
	d = strings.TrimPrefix(d, "sha256:")
	if len(d) > 12 {
		d = d[:12]
	}
	return d
}

// truncate cuts s to at most n bytes on a rune boundary, on one line.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n]
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func deref(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
