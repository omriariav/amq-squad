package schedproc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// CleanSeats is the scheduler's SeatPreparer: before an idle seat gets a new
// lease its worktree returns to the commit ledger's parent
// (seatcommit.CleanSeat), with any leftover diff recorded on the ledger as
// evidence and a seat_cleaned line in the decision log. A seat with no commit
// ledger (no recorded base) is left as it is: its commit step refuses
// base_unrecorded anyway.
type CleanSeats struct {
	Root string
	Git  string

	log io.Writer
	now func() time.Time
}

// SetLog gives the preparer the process's decision log.
func (c *CleanSeats) SetLog(w io.Writer, now func() time.Time) { c.log, c.now = w, now }

func (c *CleanSeats) PrepareSeat(handle string) error {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	present, err := c.checkSeatCompatibility(handle)
	if err != nil {
		return prepareError(handle, err)
	}
	if !present {
		return nil
	}
	discarded, markerDecision, err := seatcommit.CleanSeatDetailed(c.Root, handle, c.Git, nil, now())
	if markerDecision != "" && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "cleaner_marker_taken_over", Seat: handle, Reason: markerDecision})
	}
	if err != nil {
		return prepareError(handle, err)
	}
	if discarded && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "seat_cleaned", Seat: handle, Reason: "an earlier attempt's uncommitted diff kept on the ledger and removed from the worktree"})
	}
	return nil
}

// prepareError is a prepare failure as the scheduler reads it: a ledger
// refusal (sealchain.ErrTampered) is scheduler.LedgerTamperedError with
// the quarantine's seat, seq and slot when the copy was made
// (seatcommit.QuarantinedError), any other error is itself, named for the
// seat.
func prepareError(handle string, err error) error {
	named := fmt.Errorf("the commit ledger of seat %s: %w", handle, err)
	if !errors.Is(err, sealchain.ErrTampered) {
		return named
	}
	e := &scheduler.LedgerTamperedError{Handle: handle, Err: named}
	var q *seatcommit.QuarantinedError
	if errors.As(err, &q) {
		e.Seq, e.Quarantine = q.Seq, q.Slot
	}
	return e
}

// recoveryMark is a ledger's recovery record as the scheduler compares it.
func recoveryMark(r seatcommit.LedgerRecovery) scheduler.RecoveryMark {
	return scheduler.RecoveryMark{Seq: r.Seq, Slot: r.Slot}
}

// LedgerRecovery is the recovery the seat's sealed ledger records
// (scheduler.RecoveryReader): the scheduler's tick releases a held seat
// against it, never against a message.
func (c *CleanSeats) LedgerRecovery(handle string) (scheduler.RecoveryMark, bool) {
	slot, alarms, problems, ok := seatcommit.RecoveriesOf(c.Root, handle)
	var m scheduler.RecoveryMark
	if slot != nil {
		m = recoveryMark(*slot)
	}
	m.Alarms, m.Problems = alarms, problems
	return m, ok
}

// CheckSeatCompatibility is the read-only half of PrepareSeat. The
// scheduler uses it to clear a compatibility exclusion after an install.
func (c *CleanSeats) CheckSeatCompatibility(handle string) error {
	_, err := c.checkSeatCompatibility(handle)
	return err
}

func (c *CleanSeats) checkSeatCompatibility(handle string) (bool, error) {
	// No commit ledger (no recorded base) leaves the seat as it is; any other
	// read error (a tampered or unreadable ledger) is seat_prepare_failed:
	// no grant of a seat whose cleanliness is unknown (the fifth round's (g)).
	// A read that refuses as tampered quarantines the refused document first
	// (3.2.0-0c-A), and the reason names the copy.
	if _, err := seatcommit.ReadLedgerAs(c.Root, handle, "scheduler prepare"); os.IsNotExist(err) {
		return false, nil
	} else if errors.Is(err, sealchain.ErrNewerWriter) {
		return true, &scheduler.PrepareRefusal{Reason: seatcommit.ReasonNewerWriter, Detail: err.Error(), Route: task.RouteOwner}
	} else if errors.Is(err, sealchain.ErrSchemaMismatch) {
		return true, &scheduler.PrepareRefusal{Reason: seatcommit.ReasonSchemaMismatch, Detail: err.Error(), Route: task.RouteLead}
	} else if err != nil {
		return true, err
	}
	return true, nil
}

// CleanAttempt archives a stopped attempt and then cleans its worktree. The
// archive is idempotent by task and attempt, so a failed clean can retry.
// The scheduler holds the seat's reservation (ReserveCancel) for the round,
// so the clean takes none of its own (CleanSeatHeld).
func (c *CleanSeats) CleanAttempt(handle, taskID string, attempt int, outcome string) error {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	if _, err := seatcommit.ArchiveAttemptReason(c.Root, handle, c.Git, nil, taskID, attempt, outcome, now()); err != nil {
		return err
	}
	_, err := seatcommit.CleanSeatHeld(c.Root, handle, c.Git, nil, now())
	return err
}

// CleanStopped cleans a seat requested by the lead after the process check.
func (c *CleanSeats) CleanStopped(handle string) (bool, error) {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	store, err := task.Open(c.Root)
	if err != nil {
		return false, err
	}
	list, err := store.List()
	if err != nil {
		return false, err
	}
	latestTask, latest, found := latestEndedAttempt(list, handle)
	if found {
		if _, err := seatcommit.ArchiveAttemptReason(c.Root, handle, c.Git, nil, latestTask, latest.N, latest.Outcome, now()); err != nil {
			return false, err
		}
	}
	discarded, markerDecision, err := seatcommit.CleanSeatDetailed(c.Root, handle, c.Git, nil, now())
	if markerDecision != "" && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "cleaner_marker_taken_over", Seat: handle, Reason: markerDecision})
	}
	return discarded, err
}

// latestEndedAttempt is the seat's most recently ended attempt, the one a
// lead's `seat clean` archives under (3.2.0-2b-1 item 3; the rule, stated
// for Codex's G on #898): the greatest EndedAt wins; two attempts that
// ended at the same instant are ordered by the later grant, then by the
// higher attempt number under one task, then by task id. An attempt that
// never ended is not in Attempts (task.endAttempt appends at the end), so
// EndedAt is never zero here; a zero one would sort before every ended
// attempt and be chosen only when it is the seat's only attempt.
func latestEndedAttempt(list []task.Task, handle string) (string, task.Attempt, bool) {
	var latestTask string
	var latest task.Attempt
	found := false
	for _, t := range list {
		for _, a := range t.Attempts {
			if a.Holder != handle {
				continue
			}
			switch {
			case !found,
				a.EndedAt.After(latest.EndedAt),
				a.EndedAt.Equal(latest.EndedAt) && a.GrantedAt.After(latest.GrantedAt),
				a.EndedAt.Equal(latest.EndedAt) && a.GrantedAt.Equal(latest.GrantedAt) && t.ID == latestTask && a.N > latest.N,
				a.EndedAt.Equal(latest.EndedAt) && a.GrantedAt.Equal(latest.GrantedAt) && t.ID != latestTask && t.ID > latestTask:
				latestTask, latest, found = t.ID, a, true
			}
		}
	}
	return latestTask, latest, found
}

// ArchiveAttempt records a typed block's stopped tree immediately, before a
// lead can widen and before the seat can be cleaned or reused.
func (c *CleanSeats) ArchiveAttempt(handle string, t task.Task, attempt task.Attempt) error {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	_, err := seatcommit.ArchiveAttempt(c.Root, handle, c.Git, nil, t.ID, attempt.N, now())
	return err
}

var _ scheduler.SeatPreparer = (*CleanSeats)(nil)
var _ scheduler.SeatCompatibilityChecker = (*CleanSeats)(nil)
var _ scheduler.AttemptArchiver = (*CleanSeats)(nil)
var _ scheduler.RecoveryReader = (*CleanSeats)(nil)
var _ scheduler.AttemptCleaner = (*CleanSeats)(nil)
