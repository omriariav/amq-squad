package task

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrResetRefused is a reset the store refuses for its caller.
var ErrResetRefused = errors.New("reset refused")

// ResetBy is Reset for a named caller (brief 4-1, amendment 26 review): a
// lead resets only a task its own squad owns, which is a task the caller
// created (created_by), and never a block routed to the owner (a growth
// gate or another owner decision is answered by the owner, not reset by a
// lead). A task without a recorded creator predates the field and is
// treated as the squad's own.
func (s Store) ResetBy(id, caller string, pool *Pool, now time.Time) (Task, error) {
	caller = strings.TrimSpace(caller)
	if caller == "" {
		return Task{}, fmt.Errorf("%w: reset needs the caller's handle", ErrResetRefused)
	}
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateBlocked {
			return fmt.Errorf("task %s is %s; reset takes a blocked task", t.ID, t.State)
		}
		if t.BlockRoute == RouteOwner {
			return fmt.Errorf("%w: task %s is blocked for the owner (%s); only the owner answers it", ErrResetRefused, t.ID, t.BlockReason)
		}
		if t.CreatedBy != "" && t.CreatedBy != caller {
			return fmt.Errorf("%w: task %s belongs to %s, not %s", ErrResetRefused, t.ID, t.CreatedBy, caller)
		}
		t.State, t.Report, t.BlockReason, t.BlockRoute, t.BlockDetail, t.RefusedPaths, t.BlockAttempt = StatePending, nil, "", "", "", nil, 0
		if pool != nil {
			p := *pool
			t.Pool = &p
		}
		return nil
	})
}
