package schedproc

import (
	"fmt"
	"io"
	"os"
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
	// Clear verifies the owner's signed clear of a quarantine (the owner's
	// trust store); a quarantined seat without a verified clear is never
	// cleaned or dispatched to.
	Clear seatcommit.ClearVerifier

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
	if _, err := seatcommit.ReadLedger(c.Root, handle); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("the commit ledger of seat %s: %w", handle, err)
	}
	discarded, err := seatcommit.CleanSeat(c.Root, handle, c.Git, nil, c.Clear, now())
	if err == nil && discarded && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "seat_cleaned", Seat: handle, Reason: "an earlier attempt's uncommitted diff kept on the ledger and removed from the worktree"})
	}
	return err
}

var _ scheduler.SeatPreparer = (*CleanSeats)(nil)
