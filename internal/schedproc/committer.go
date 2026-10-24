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
	Step         func(handle string, t task.Task) seatcommit.Result
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

var _ scheduler.Committer = (*Committer)(nil)
