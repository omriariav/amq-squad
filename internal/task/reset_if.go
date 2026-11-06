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
// while it is blocked for reason (3.2.0-0c-B, Codex on b250b951): a caller
// that read the block and resets it later never clears a block another
// decision raised in between (the lead's reset and a new block of another
// reason). Any other state or reason is ErrBlockChanged and nothing moves.
func (s Store) ResetIfBlocked(id, reason string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateBlocked || t.BlockReason != reason {
			return fmt.Errorf("%w: task %s is %s (%s), not blocked %s", ErrBlockChanged, t.ID, t.State, t.BlockReason, reason)
		}
		t.State, t.Report, t.BlockReason, t.BlockRoute, t.BlockDetail, t.RefusedPaths, t.BlockAttempt = StatePending, nil, "", "", "", nil, 0
		return nil
	})
}
