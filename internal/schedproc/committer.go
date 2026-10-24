package schedproc

import (
	"fmt"
	"sync"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// DefaultMaxDeferrals is how many ticks in a row the commit step may defer
// (a git or push failure) before it refuses with commit_failed for the lead.
const DefaultMaxDeferrals = 5

// Committer runs `amq-squad seat commit` for the scheduler (briefs ruling
// 4): Step is the same function the verb runs (the CLI passes it), and the
// scheduler records the result (Complete or RefuseCommit, the quarantine).
type Committer struct {
	Step func(handle string, t task.Task) seatcommit.Result
	// ExitStep is the exit check of a seat exit off the done path
	// (seatcommit.ExitCheck); nil checks nothing there.
	ExitStep     func(handle, taskID, why string) seatcommit.Result
	MaxDeferrals int

	mu       sync.Mutex
	deferred map[string]int
}

func (c *Committer) Commit(handle string, t task.Task) scheduler.CommitResult {
	res := c.Step(handle, t)
	c.mu.Lock()
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
		return scheduler.CommitResult{OK: true, PRNumber: res.PRNumber, PRHead: res.PRHead}
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
	}
	return scheduler.ExitResult{OK: true}
}

var (
	_ scheduler.Committer   = (*Committer)(nil)
	_ scheduler.ExitChecker = (*Committer)(nil)
)
