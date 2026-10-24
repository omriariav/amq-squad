package scheduler

import (
	"fmt"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// R3 of the 2b-2 milestone B first pass: the exit check (amendment 28's
// config diff and the quarantine) runs on every terminal path of a seat, not
// only on a done report (the committer runs it there): a failed attempt, a
// seat that is gone, a lease that expired. And an expired claimed attempt is
// closed through the launcher, its termination confirmed, before the seat is
// cleaned or dispatched to again.

// ExitResult is the exit check's answer.
type ExitResult struct {
	OK         bool // nothing changed
	Deferred   bool // the check could not run; the scheduler retries it next tick
	Quarantine bool // the seat is quarantined (no new lease) and closed
	ReasonCode string
	Detail     string
}

// ExitChecker runs the exit check at a seat exit that is not a done report
// (2b-2's committer implements it with seatcommit.ExitCheck). why is failed,
// gone, expired or unclaimed.
type ExitChecker interface {
	CheckExit(handle, taskID, why string) ExitResult
}

// SeatQuarantineNotifier is a LeadNotifier that also tells the lead about a
// quarantine at a seat exit (a hint; the store record is the truth).
type SeatQuarantineNotifier interface {
	SeatQuarantined(handle, taskID, reason, detail string) error
}

// AsyncCloser is a SeatLauncher whose nil CloseSeat does not confirm the
// seat's termination (the external stand-in writes a close file the runner
// obeys): for a seat closed after an expired claimed lease the scheduler
// then waits for the seat's SeatGone before it forgets the seat.
type AsyncCloser interface {
	CloseUnconfirmed(handle string) bool
}

// closeExpired is the closing reason of a seat whose claimed lease expired.
const closeExpired = "lease_expired"

type exitItem struct{ handle, taskID, why string }

func (s *Scheduler) queueExit(handle, taskID, why string) {
	if s.Exits == nil || handle == "" {
		return
	}
	s.exitQueue = append(s.exitQueue, exitItem{handle: handle, taskID: taskID, why: why})
}

// scanAttempts queues the exit check for every attempt that ended failed,
// expired or unclaimed and was not checked yet by this scheduler (a restart
// checks the store's history once).
func (s *Scheduler) scanAttempts(list []task.Task) {
	if s.Exits == nil {
		return
	}
	if s.exitSeen == nil {
		s.exitSeen = map[string]bool{}
	}
	for _, t := range list {
		for _, a := range t.Attempts {
			switch a.Outcome {
			case task.OutcomeFailed, task.OutcomeExpired, task.OutcomeUnclaimed:
			default:
				continue
			}
			key := fmt.Sprintf("%s#%d#%s", t.ID, a.N, a.Holder)
			if s.exitSeen[key] {
				continue
			}
			s.exitSeen[key] = true
			s.queueExit(a.Holder, t.ID, a.Outcome)
		}
	}
}

// runExits runs the queued exit checks; a deferred one stays queued.
func (s *Scheduler) runExits(now time.Time, log func(Decision)) error {
	queue := s.exitQueue
	s.exitQueue = nil
	for _, it := range queue {
		r := s.Exits.CheckExit(it.handle, it.taskID, it.why)
		switch {
		case r.Deferred:
			s.exitQueue = append(s.exitQueue, it)
			log(Decision{Kind: "exit_check_deferred", TaskID: it.taskID, Seat: it.handle, Reason: r.Detail})
		case r.Quarantine:
			if err := s.Store.Quarantine(task.Quarantine{Handle: it.handle, Reason: r.ReasonCode, Detail: r.Detail, TaskID: it.taskID, At: now}); err != nil {
				return err
			}
			log(Decision{Kind: "seat_quarantined", TaskID: it.taskID, Seat: it.handle, Reason: r.ReasonCode + " at " + it.why})
			if st := s.seats[it.handle]; st != nil && st.closing == "" {
				st.closing = "quarantined"
			}
			if n, ok := s.Lead.(SeatQuarantineNotifier); ok && s.Lead != nil {
				if err := n.SeatQuarantined(it.handle, it.taskID, r.ReasonCode, r.Detail); err != nil {
					log(Decision{Kind: "lead_notice_failed", TaskID: it.taskID, Reason: err.Error()})
				}
			}
		}
	}
	return nil
}

// closeUnconfirmed reports whether the launcher's nil close of a seat does
// not confirm its termination.
func (s *Scheduler) closeUnconfirmed(handle string) bool {
	a, ok := s.Launcher.(AsyncCloser)
	return ok && a.CloseUnconfirmed(handle)
}
