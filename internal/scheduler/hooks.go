package scheduler

import "github.com/omriariav/amq-squad/v3/internal/task"

// The hooks the scheduler calls. Each is an interface that a later brief
// implements; the defaults here keep the scheduler usable on its own.

// SeatRequest asks for one new seat of a pool, for a task that waits on it.
type SeatRequest struct {
	Pool   task.Pool `json:"pool"`
	TaskID string    `json:"task_id"`
}

// SeatLauncher starts and stops seats (2b-2). RequestSeat is asynchronous:
// the seat joins later through a SeatUp event. The scheduler never launches.
type SeatLauncher interface {
	RequestSeat(req SeatRequest) error
	CloseSeat(handle, reason string) error
}

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
type DefaultRouter struct{ MaxAttempts int }

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
}

// Committer runs the harness-side commit step for a done report (2b-2's
// `amq-squad seat commit --seat H`). A nil Committer completes the report
// at once.
type Committer interface {
	Commit(handle string, t task.Task) CommitResult
}
