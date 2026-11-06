package task

import (
	"errors"
	"fmt"
	"time"
)

// ErrBlockChanged is a conditional reset refused because the task is no
// longer blocked for the reason the caller read (ResetIfBlocked).
var ErrBlockChanged = errors.New("block changed")

// ResetIfBlocked is Reset in one store mutation that resets the task only
// while it is blocked for reason and, when match is not nil, match accepts
// the task as it is inside the mutation (3.2.0-0c-B, Codex on b250b951 and
// the plan reviewer on af1a6124: the block must be the caller's own, e.g.
// the same quarantine slot): a caller that read the block and resets it
// later never clears a block another decision raised in between (the
// lead's reset and a new block of another reason, or of the same reason
// for another slot). Anything else is ErrBlockChanged and nothing moves.
func (s Store) ResetIfBlocked(id, reason string, match func(Task) bool, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateBlocked || t.BlockReason != reason {
			return fmt.Errorf("%w: task %s is %s (%s), not blocked %s", ErrBlockChanged, t.ID, t.State, t.BlockReason, reason)
		}
		if match != nil && !match(*t) {
			return fmt.Errorf("%w: task %s is blocked %s for another cause: %s", ErrBlockChanged, t.ID, reason, t.BlockDetail)
		}
		t.State, t.Report, t.BlockReason, t.BlockRoute, t.BlockDetail, t.RefusedPaths, t.BlockAttempt = StatePending, nil, "", "", "", nil, 0
		return nil
	})
}
