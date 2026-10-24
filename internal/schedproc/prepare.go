package schedproc

import (
	"io"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
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
	if _, err := seatcommit.ReadLedger(c.Root, handle); err != nil {
		return nil
	}
	discarded, err := seatcommit.CleanSeat(c.Root, handle, c.Git, nil, now(), "the worktree held an uncommitted diff when the seat was given a new lease")
	if err == nil && discarded && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "seat_cleaned", Seat: handle, Reason: "an earlier attempt's uncommitted diff kept on the ledger and removed from the worktree"})
	}
	return err
}

var _ scheduler.SeatPreparer = (*CleanSeats)(nil)
