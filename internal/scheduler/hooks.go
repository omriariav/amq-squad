package scheduler

import "github.com/omriariav/amq-squad/v3/internal/task"

// The hooks the scheduler calls. Each is an interface that a later brief
// implements; the defaults here keep the scheduler usable on its own.

// SeatRequest asks for one new seat of a pool, for a task that waits on it.
// The launcher carries ID into the launch (the launch record) and back in
// the seat's SeatUp event (Event.RequestID): arrival matches a reservation
// by id, never by pool.
type SeatRequest struct {
	ID     string    `json:"id"` // seat-req-<incarnation>-<n>: unique across scheduler restarts
	Pool   task.Pool `json:"pool"`
	TaskID string    `json:"task_id"`
	// Handle names the roster seat of a roster launch's request (3.0.1 t16);
	// empty on an on-demand request, where the slots pick the seat.
	Handle string `json:"handle,omitempty"`
}

// SeatLauncher starts and stops seats (2b-2). RequestSeat is asynchronous:
// the seat joins later through a SeatUp event carrying the request id. The
// scheduler never launches. CancelSeatRequest cancels a request that has not
// joined; nil means the launcher confirms that no seat will join for that id
// (cancelled or terminated). A request keeps its place under the seat cap
// until that confirmation. A later SeatUp with a cancelled or unknown id, an
// id of a previous scheduler incarnation, or no id is refused and closed
// through the launcher: never dispatched; counted as closing while its
// closure is unconfirmed (CloseSeat may fail; the scheduler retries it every
// tick).
type SeatLauncher interface {
	RequestSeat(req SeatRequest) error
	CancelSeatRequest(req SeatRequest) error
	CloseSeat(handle, reason string) error
}

// SenderHandle is the scheduler process's own AMQ mailbox, the sender of its
// doorbells and lead notices (2b-2 milestone B). It is not a seat.
const SenderHandle = "scheduler"

// Doorbell tells a seat that it holds a new lease (an AMQ message). It is a
// hint only: a lost or unread doorbell loses nothing, because the seat sees
// its lease through `context` and the lease expires if never claimed.
type Doorbell interface {
	Ring(handle string, t task.Task) error
}

// Route is the router's answer for a pending task: the pool to dispatch to,
// or a wait reason, or a block (no retry left).
type Route struct {
	Pool   *task.Pool
	Wait   string // a task.Wait* reason (2b-4 adds its own) when Pool is nil
	Block  string // a block reason code; the task blocks for the lead
	Detail string
}

// Router picks the pool for a pending task's next attempt and applies the
// retry rule of plan 6.2 (2b-4: a retry moves one tier up and never repeats
// an entry; the tier is fixed per task). It sees the task's attempts.
type Router interface {
	Route(t task.Task) Route
}

// DefaultRouter dispatches on the task's own pool and blocks a task whose
// seat-side failures (failed or expired attempts) reach MaxAttempts.
// Unclaimed and released attempts do not count: they are seat or lead
// events, not failures of the work.
type DefaultRouter struct {
	MaxAttempts int
}

func (r DefaultRouter) Route(t task.Task) Route {
	max := r.MaxAttempts
	if max <= 0 {
		max = 3
	}
	failures := 0
	for _, a := range t.Attempts {
		if a.Outcome == task.OutcomeFailed || a.Outcome == task.OutcomeExpired {
			failures++
		}
	}
	if failures >= max {
		return Route{Block: "retries_exhausted", Detail: "no retry left after the failed attempts"}
	}
	if t.Pool == nil {
		return Route{Wait: task.WaitNoRoute}
	}
	p := *t.Pool
	return Route{Pool: &p}
}

// CommitResult is the harness commit step's answer for a done report.
type CommitResult struct {
	OK         bool
	ReasonCode string // on a refusal, e.g. outside_declared_files
	Detail     string
	Route      string // task.RouteLead (default) or task.RouteOwner (a growth gate)
	// Deferred is a step that could not run to a decision (a git or push
	// failure): nothing is written, the report stays, and the next tick runs
	// the step again. The committer turns a deferral that repeats into a
	// refusal.
	Deferred bool
	// Running is a step still running off the tick (Detail names its state:
	// running, waiting for a verifier slot); the next tick asks again.
	Running bool
	// Quarantine: the refusal quarantines the seat (global_config_changed):
	// the store refuses it any new lease until the owner clears it, and the
	// scheduler closes it.
	Quarantine bool
	// PRNumber and PRHead: the PR the commit step opened (0 when none) and
	// the head it pushed; the scheduler binds them to the task (3-2).
	PRRepository string
	PRNumber     int
	PRHead       string
	// RefusedPaths are a refusal's exact paths outside the declared files
	// (outside_declared_files); the store records them with the block
	// (task.RefusedPaths, 3.0.1 t16 round 7).
	RefusedPaths []string
}

// SeatPreparer readies an idle seat for a new lease (2b-2 milestone B: the
// seat's worktree returns to the core's commit parent, so an earlier
// attempt's uncommitted diff never rides into the next task). An error keeps
// the seat out of this tick's dispatch.
type SeatPreparer interface {
	PrepareSeat(handle string) error
}

// LeadNotifier tells the lead that the commit step refused a task (2b-2:
// the store record is written first, then this AMQ message to the lead's
// handle). Like the doorbell it is a hint: a lost message loses nothing,
// because the blocked task in the store is the truth.
type LeadNotifier interface {
	CommitRefused(t task.Task) error
}

// Committer runs the harness-side commit step for a done report (2b-2's
// `amq-squad seat commit --seat H`). A nil Committer completes the report
// at once.
type Committer interface {
	Commit(handle string, t task.Task) CommitResult
}

// CommitApplier is a Committer told when a decided step's result is in the
// store: until then it keeps the result (the application is retried, never
// the step).
type CommitApplier interface {
	Applied(taskID string)
}

// CommitDecider is a Committer that retains a result the scheduler decided
// for a step (the deferral limit's refusal) until Applied.
type CommitDecider interface {
	Decide(taskID string, res CommitResult)
}

// CommitCanceller is a Committer that cancels the running step of a task
// that no longer waits for it; ended reports that no step of it runs.
type CommitCanceller interface {
	CancelCommit(taskID string) (ended bool)
}

// Reason codes of the commit step's deferral limit (the strings
// internal/seatcommit uses).
const (
	ReasonCommitFailed        = "commit_failed"
	ReasonVerifierInterrupted = "verifier_interrupted"
)

// DefaultMaxCommitDeferrals is the commit step's deferral limit (Config.MaxCommitDeferrals).
const DefaultMaxCommitDeferrals = 5
