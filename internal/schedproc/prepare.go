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
	// No commit ledger (no recorded base) leaves the seat as it is; any other
	// read error (a tampered or unreadable ledger) is seat_prepare_failed:
	// no grant of a seat whose cleanliness is unknown (the fifth round's (g)).
	// A read that refuses as tampered quarantines the refused document first
	// (3.2.0-0c-A), and the reason names the copy.
	// A read or a write that refuses as tampered (0c-A) is the scheduler's
	// typed block (3.2.0-0c-B): the error carries the seat, the quarantined
	// seq and the slot for its detail.
	if _, err := seatcommit.ReadLedgerAs(c.Root, handle, "scheduler prepare"); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return prepareError(handle, err)
	}
	discarded, err := seatcommit.CleanSeat(c.Root, handle, c.Git, nil, now())
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
var _ scheduler.AttemptArchiver = (*CleanSeats)(nil)
