package task

import (
	"fmt"
	"strings"
)

// The 3.0 task schema vocabulary (docs/task-store-design.md). 2b-2, 2b-4,
// 2b-5, 3-2 and 4-4 code against these names.

// Store states: five, two of them terminal.
const (
	StatePending    = "pending"     // queued: waits for its dependencies, a file lock, a route or a seat (see wait_reason)
	StateInProgress = "in_progress" // a seat holds the lease (claimed or not yet claimed)
	StateBlocked    = "blocked"     // stopped on a decision the lead (or, routed, the owner) owes; no lease
	StateCompleted  = "completed"   // terminal
	StateCancelled  = "cancelled"   // terminal
)

// Classes is the closed class list of plan 6.1, in table order.
var Classes = []string{"extract", "summarize", "script", "implement", "plan", "review"}

// ValidClass reports whether c is in the closed class list.
func ValidClass(c string) bool {
	for _, k := range Classes {
		if c == k {
			return true
		}
	}
	return false
}

// Wait reasons of a pending task (wait_reason). 2b-4 adds the routing ones
// (privacy_class, cost_cap, no_entry) through the scheduler's router hook.
const (
	WaitDependencies = "dependencies"  // a depends_on task is not completed
	WaitFileLock     = "file_lock"     // declared_files overlap an active task's
	WaitNoRoute      = "no_route"      // no pool: the router found no entry, or the lead must assign one
	WaitSeatStarting = "seat_starting" // a seat for the pool was requested and is not up yet
	WaitSeatCap      = "seat_cap"      // the pool has no idle seat and the grant's max_seats is reached
	WaitUsageLimit   = "usage_limit"   // a usage-limit event stopped new seats in this window
)

// Attempt outcomes (attempts[].outcome).
const (
	OutcomeCompleted     = "completed"      // the done report committed
	OutcomeUnclaimed     = "unclaimed"      // the lease expired before the seat claimed it (S6 N4)
	OutcomeExpired       = "expired"        // a claimed lease expired without renewal
	OutcomeFailed        = "failed"         // the seat reported failure
	OutcomeReleased      = "released"       // the lead or the seat released the lease
	OutcomeBlocked       = "blocked"        // the seat or the commit step blocked the task
	OutcomeCancelled     = "cancelled"      // the lead cancelled the task during the attempt
	OutcomeCommitRefused = "commit_refused" // the harness commit step refused the diff (2b-2)
)

// Block routes (block_route): who owes the decision.
const (
	RouteLead  = "lead"
	RouteOwner = "owner"
)

// Pool is the runtime x model x effort key (plan 4.5).
type Pool struct {
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// Key is the pool's canonical "runtime:model:effort" string.
func (p Pool) Key() string { return p.Runtime + ":" + p.Model + ":" + p.Effort }

// ParsePool parses "runtime:model:effort". Model and effort may be empty
// ("pi:qwen3.6:27b:low" keeps colons inside the model: the first field is
// the runtime, the last the effort).
func ParsePool(s string) (Pool, error) {
	s = strings.TrimSpace(s)
	first, last := strings.Index(s, ":"), strings.LastIndex(s, ":")
	if first <= 0 || first == last {
		return Pool{}, fmt.Errorf("pool %q: want runtime:model:effort", s)
	}
	p := Pool{Runtime: s[:first], Model: s[first+1 : last], Effort: s[last+1:]}
	if p.Model == "" {
		return Pool{}, fmt.Errorf("pool %q: model is empty", s)
	}
	return p, nil
}
