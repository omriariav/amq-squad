package schedproc

import (
	"fmt"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// DefaultMaxDeferrals is how many ticks in a row the commit step may defer
// (a git or push failure) before it refuses with commit_failed for the lead.
const DefaultMaxDeferrals = 5

// DefaultInline is how long Commit waits for a step it just started before
// it answers running: a step with no verifier usually decides within it.
const DefaultInline = 2 * time.Second

// Committer runs `amq-squad seat commit` for the scheduler (briefs ruling
// 4): Step is the same function the verb runs (the CLI passes it), and the
// scheduler records the result (Complete or RefuseCommit, the quarantine).
//
// The step runs off the scheduler's tick (v3-5-verifier, the liveness
// ruling): Commit starts it in its own goroutine, one per task, and answers
// running (or waiting for a verifier slot) until it ends; each tick asks
// again and gets the result once it is there. The tick meanwhile dispatches,
// cancels and checks other seats; the seat stays held by its task's report.
type Committer struct {
	Step func(handle string, t task.Task) seatcommit.Result
	// ExitStep is the exit check of a seat exit off the done path
	// (seatcommit.ExitCheck); nil checks nothing there.
	ExitStep     func(handle, taskID, why string) seatcommit.Result
	MaxDeferrals int
	// Inline is how long Commit waits for a step it starts (default
	// DefaultInline; negative: not at all).
	Inline time.Duration
	// Root is the session root: a running step that waits for a verifier
	// slot says so (seatcommit.VerifierWaiting).
	Root string

	mu       sync.Mutex
	deferred map[string]int
	jobs     map[string]*commitJob
}

// commitJob is one running commit step.
type commitJob struct {
	handle string
	done   chan struct{}
	res    seatcommit.Result
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
		j = &commitJob{handle: handle, done: make(chan struct{})}
		c.jobs[t.ID] = j
		go func() {
			r := c.Step(handle, t)
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
	delete(c.jobs, t.ID)
	res := j.res
	defer c.mu.Unlock()
	if c.deferred == nil {
		c.deferred = map[string]int{}
	}
	if res.Deferred {
		c.deferred[t.ID]++
		max := c.MaxDeferrals
		if max <= 0 {
			max = DefaultMaxDeferrals
		}
		if c.deferred[t.ID] < max {
			return scheduler.CommitResult{Deferred: true, Detail: res.Detail}
		}
		delete(c.deferred, t.ID)
		return scheduler.CommitResult{ReasonCode: seatcommit.ReasonCommitFailed, Route: task.RouteLead,
			Detail: fmt.Sprintf("the commit step deferred %d ticks in a row; last: %s", max, res.Detail)}
	}
	delete(c.deferred, t.ID)
	if res.OK {
		return scheduler.CommitResult{OK: true, PRRepository: res.PRRepository, PRNumber: res.PRNumber, PRHead: res.PRHead}
	}
	return scheduler.CommitResult{ReasonCode: res.ReasonCode, Detail: res.Detail, Route: res.Route, Quarantine: res.Quarantine}
}

// CheckExit is the exit check at a seat exit that is not a done report (R3
// of the milestone B first pass): ExitStep is seatcommit.ExitCheck with the
// verb's options; a quarantine there is recorded like the done path's.
func (c *Committer) CheckExit(handle, taskID, why string) scheduler.ExitResult {
	if c.ExitStep == nil {
		return scheduler.ExitResult{OK: true}
	}
	res := c.ExitStep(handle, taskID, why)
	switch {
	case res.Deferred:
		return scheduler.ExitResult{Deferred: true, Detail: res.Detail}
	case res.Quarantine:
		return scheduler.ExitResult{Quarantine: true, ReasonCode: res.ReasonCode, Detail: res.Detail}
	case !res.OK:
		// a refusal the exit step does not name (ledger_tampered above all)
		// is a block routed to the owner that quarantines the seat, never OK
		return scheduler.ExitResult{Quarantine: true, ReasonCode: res.ReasonCode, Detail: res.Detail}
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
