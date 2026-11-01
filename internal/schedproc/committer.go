package schedproc

import (
	"context"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// DefaultInline is how long Commit waits for a step it just started before
// it answers running: a step with no verifier usually decides within it.
const DefaultInline = 2 * time.Second

// Committer runs `amq-squad seat commit` for the scheduler (briefs ruling
// 4): Step is the same function the verb runs (the CLI passes it), and the
// scheduler records the result (Complete or RefuseCommit).
//
// The step runs off the scheduler's tick (v3-5-verifier, the liveness
// ruling): Commit starts it in its own goroutine, one per task, and answers
// running (or waiting for a verifier slot) until it ends; each tick asks
// again and gets the result once it is there. The tick meanwhile dispatches,
// cancels and checks other seats; the seat stays held by its task's report.
type Committer struct {
	Step func(ctx context.Context, handle string, t task.Task) seatcommit.Result
	// ExitStep is the exit check of a seat exit off the done path
	// (seatcommit.ExitCheck); nil checks nothing there.
	ExitStep func(handle, taskID, why string) seatcommit.Result
	// Inline is how long Commit waits for a step it starts (default
	// DefaultInline; negative: not at all).
	Inline time.Duration
	// Root is the session root: a running step that waits for a verifier
	// slot says so (seatcommit.VerifierWaiting).
	Root string

	mu   sync.Mutex
	jobs map[string]*commitJob
}

// commitJob is one running commit step.
type commitJob struct {
	handle string
	done   chan struct{}
	res    seatcommit.Result
	cancel context.CancelFunc
	final  *scheduler.CommitResult // the decided result until Applied
}

// Running states of a commit step.
const (
	StateRunning       = "running"
	StateWaitingVerify = "waiting_for_verifier_slot"
)

func (c *Committer) Commit(handle string, t task.Task) scheduler.CommitResult {
	c.mu.Lock()
	if c.jobs == nil {
		c.jobs = map[string]*commitJob{}
	}
	j, wait := c.jobs[t.ID], time.Duration(0)
	if j == nil {
		ctx, cancel := context.WithCancel(context.Background())
		j = &commitJob{handle: handle, done: make(chan struct{}), cancel: cancel}
		c.jobs[t.ID] = j
		go func() {
			r := c.Step(ctx, handle, t)
			j.res = r
			close(j.done)
		}()
		if wait = c.Inline; wait == 0 {
			wait = DefaultInline
		}
	}
	c.mu.Unlock()
	if !c.finished(j, wait) {
		return scheduler.CommitResult{Running: true, Detail: c.state(handle)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if j.final != nil {
		return *j.final // decided; not applied yet (P2-3): the application is retried, never the step
	}
	res := j.res
	if res.Deferred {
		// A deferral runs the step again at the next tick; the scheduler
		// counts deferrals in the step's exits entry (P2-4).
		delete(c.jobs, t.ID)
		j.cancel()
		return scheduler.CommitResult{Deferred: true, ReasonCode: res.ReasonCode, Detail: res.Detail}
	}
	final := scheduler.CommitResult{ReasonCode: res.ReasonCode, Detail: res.Detail, Route: res.Route, RefusedPaths: res.OutsidePaths()}
	if res.OK {
		final = scheduler.CommitResult{OK: true, PRRepository: res.PRRepository, PRNumber: res.PRNumber, PRHead: res.PRHead}
	}
	j.final = &final
	return final
}

// Applied forgets a decided step once the scheduler's store transaction
// for its result committed (P2-3 of the second pass).
func (c *Committer) Applied(taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if j := c.jobs[taskID]; j != nil {
		j.cancel()
		delete(c.jobs, taskID)
	}
}

// Decide retains a decision the scheduler made for a task's step (the
// deferral limit's refusal, the third round's item 5) like every decided
// result: the next Commit returns it, and it goes only at Applied, so a
// failed application is retried, never a new step started.
func (c *Committer) Decide(taskID string, res scheduler.CommitResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.jobs == nil {
		c.jobs = map[string]*commitJob{}
	}
	done := make(chan struct{})
	close(done)
	r := res
	c.jobs[taskID] = &commitJob{done: done, cancel: func() {}, final: &r}
}

// CancelCommit cancels the running step of a task that no longer waits for
// it (cancelled, P2-1 of the second pass): its verifier's group is killed
// and its result discarded. ended reports that no step of the task runs.
func (c *Committer) CancelCommit(taskID string) (ended bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	j := c.jobs[taskID]
	if j == nil {
		return true
	}
	j.cancel()
	if c.finished(j, 0) {
		delete(c.jobs, taskID)
		return true
	}
	return false
}

// CheckExit is the exit check at a seat exit that is not a done report (R3
// of the milestone B first pass): ExitStep is seatcommit.ExitCheck with the
// verb's options; a refusal there closes the seat.
func (c *Committer) CheckExit(handle, taskID, why string) scheduler.ExitResult {
	if c.ExitStep == nil {
		return scheduler.ExitResult{OK: true}
	}
	res := c.ExitStep(handle, taskID, why)
	switch {
	case res.Deferred:
		return scheduler.ExitResult{Deferred: true, Detail: res.Detail}
	case !res.OK:
		// a refusal (ledger_tampered) closes the seat, never OK
		return scheduler.ExitResult{Refused: true, ReasonCode: res.ReasonCode, Detail: res.Detail}
	}
	return scheduler.ExitResult{OK: true}
}

// finished waits up to wait for the job.
func (c *Committer) finished(j *commitJob, wait time.Duration) bool {
	if wait <= 0 {
		select {
		case <-j.done:
			return true
		default:
			return false
		}
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-j.done:
		return true
	case <-t.C:
		return false
	}
}

// state is a running step's state for the scheduler's status.
func (c *Committer) state(handle string) string {
	if c.Root != "" {
		for _, w := range seatcommit.VerifierWaiting(c.Root) {
			if w.Seat == handle {
				return StateWaitingVerify
			}
		}
	}
	return StateRunning
}

var (
	_ scheduler.Committer   = (*Committer)(nil)
	_ scheduler.ExitChecker = (*Committer)(nil)
)
