package schedproc

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
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
	if _, err := seatcommit.ReadLedgerAs(c.Root, handle, "scheduler prepare"); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("the commit ledger of seat %s: %w", handle, err)
	}
	discarded, markerDecision, err := seatcommit.CleanSeatDetailed(c.Root, handle, c.Git, nil, now())
	if markerDecision != "" && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "cleaner_marker_taken_over", Seat: handle, Reason: markerDecision})
	}
	if err == nil && discarded && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "seat_cleaned", Seat: handle, Reason: "an earlier attempt's uncommitted diff kept on the ledger and removed from the worktree"})
	}
	return err
}

// CleanAttempt archives a stopped attempt and then cleans its worktree. The
// archive is idempotent by task and attempt, so a failed clean can retry.
func (c *CleanSeats) CleanAttempt(handle, taskID string, attempt int, outcome string) error {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	if _, err := seatcommit.ArchiveAttemptReason(c.Root, handle, c.Git, nil, taskID, attempt, outcome, now()); err != nil {
		return err
	}
	_, markerDecision, err := seatcommit.CleanSeatDetailed(c.Root, handle, c.Git, nil, now())
	if markerDecision != "" && c.log != nil {
		logLine(c.log, now(), scheduler.Decision{Kind: "cleaner_marker_taken_over", Seat: handle, Reason: markerDecision})
	}
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
	var latestTask string
	var latest task.Attempt
	for _, t := range list {
		for _, a := range t.Attempts {
			if a.Holder == handle && (latestTask == "" || a.EndedAt.After(latest.EndedAt)) {
				latestTask, latest = t.ID, a
			}
		}
	}
	if latestTask != "" {
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
var _ scheduler.AttemptCleaner = (*CleanSeats)(nil)
