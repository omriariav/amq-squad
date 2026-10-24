package scheduler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// closeExpired is the closing reason of a seat whose claimed lease expired;
// closeRestart is a seat a previous incarnation left running.
const (
	closeExpired = "lease_expired"
	closeRestart = "scheduler_restart"
)

// exitItem is one queued exit check. Gone marks a check queued at a
// seat_gone: until it is decided the seat's slot stays held (N3 of the
// third pass), so no replacement launches over the seat's evidence.
type exitItem struct {
	Handle string    `json:"handle"`
	TaskID string    `json:"task_id,omitempty"`
	Why    string    `json:"why"`
	Gone   bool      `json:"gone,omitempty"`
	At     time.Time `json:"at"`
}

// SeatGoneObserver is a SeatLauncher told when a gone seat may be forgotten
// (P1-6: a slot whose close was not confirmed is released only at its
// seat_gone; N3: and only once the exit check of that gone is decided).
type SeatGoneObserver interface {
	SeatGone(handle string)
}

// SeatReserver is a SeatLauncher that can hold a seat's slot (N2: a seat a
// previous incarnation left running, N3: a gone seat whose exit check is
// pending, both rebuilt at a restart).
type SeatReserver interface {
	Reserve(handle string)
}

// exitPending reports whether a seat's exit check is queued or deferred: the
// seat stays reserved (no clean, no lease) until it is decided (N3).
func (s *Scheduler) exitPending(handle string) bool {
	for _, it := range s.exitQueue {
		if it.Handle == handle {
			return true
		}
	}
	return false
}

func (s *Scheduler) reserve(handle string) {
	if r, ok := s.Launcher.(SeatReserver); ok {
		r.Reserve(handle)
	}
}

// AdoptClosing counts a seat a previous incarnation launched and that is
// still alive (the launch ledger, N2): its slot is held, it is closed
// through the launcher and confirmed gone before the first dispatch, never
// dispatched to, and its exit check runs once the close is confirmed. Call
// before the first Tick.
func (s *Scheduler) AdoptClosing(handle string, pool task.Pool, at time.Time) {
	if s.seats == nil {
		s.seats = map[string]*seat{}
	}
	if s.seats[handle] == nil {
		s.seats[handle] = &seat{pool: pool, freeSince: at, lastActivity: at, closing: closeRestart}
		s.reserve(handle)
		s.queueExit(exitItem{Handle: handle, Why: "restart", At: at})
	}
}

func (s *Scheduler) queueExit(it exitItem) {
	if s.Exits == nil || it.Handle == "" {
		return
	}
	s.exitQueue = append(s.exitQueue, it)
	s.saveExits()
}

// The exits ledger (N3): the queue of pending exit checks, written at every
// change and rebuilt at a restart, so a deferred check, and the slot a gone
// seat's check holds, survive the process.

type exitsLedger struct {
	Schema int        `json:"schema"`
	Queue  []exitItem `json:"queue"`
}

func (s *Scheduler) saveExits() {
	p := s.Config.ExitsPath
	if p == "" {
		return
	}
	raw, _ := json.MarshalIndent(exitsLedger{Schema: 1, Queue: append([]exitItem{}, s.exitQueue...)}, "", "  ")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return
	}
	tmp := p + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		os.Rename(tmp, p)
	}
}

// loadExits rebuilds the queue once, at the first tick: every pending check
// is queued again, and a gone seat's check holds its slot again.
func (s *Scheduler) loadExits() {
	if s.exitsLoaded {
		return
	}
	s.exitsLoaded = true
	p := s.Config.ExitsPath
	if p == "" || s.Exits == nil {
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var l exitsLedger
	if json.Unmarshal(raw, &l) != nil {
		return
	}
	for _, it := range l.Queue {
		if s.exitPendingItem(it) {
			continue
		}
		if it.Gone {
			s.reserve(it.Handle)
		}
		s.exitQueue = append(s.exitQueue, it)
	}
	s.saveExits()
}

func (s *Scheduler) exitPendingItem(it exitItem) bool {
	for _, q := range s.exitQueue {
		if q.Handle == it.Handle && q.TaskID == it.TaskID && q.Why == it.Why {
			return true
		}
	}
	return false
}

// scanAttempts queues the exit check for every attempt that ended failed,
// expired or unclaimed and was not checked yet by this scheduler (a restart
// checks the store's history once).
func (s *Scheduler) scanAttempts(list []task.Task, now time.Time) {
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
			it := exitItem{Handle: a.Holder, TaskID: t.ID, Why: a.Outcome, At: now}
			if !s.exitPendingItem(it) {
				s.queueExit(it)
			}
		}
	}
}

// runExits runs the queued exit checks; a deferred one stays queued (and
// persisted). A decided gone check releases the seat's slot.
func (s *Scheduler) runExits(now time.Time, log func(Decision)) error {
	queue := s.exitQueue
	s.exitQueue = nil
	defer s.saveExits()
	for i, it := range queue {
		// P1-9: a seat being closed (an expired claimed lease, a restart) is
		// checked only once its termination is confirmed and it is forgotten.
		if st := s.seats[it.Handle]; st != nil && st.closing != "" {
			s.exitQueue = append(s.exitQueue, it)
			continue
		}
		r := s.Exits.CheckExit(it.Handle, it.TaskID, it.Why)
		switch {
		case r.Deferred:
			s.exitQueue = append(s.exitQueue, it)
			log(Decision{Kind: "exit_check_deferred", TaskID: it.TaskID, Seat: it.Handle, Reason: r.Detail})
			continue
		case r.Quarantine:
			if err := s.Store.Quarantine(task.Quarantine{Handle: it.Handle, Reason: r.ReasonCode, Detail: r.Detail, TaskID: it.TaskID, At: now}); err != nil {
				s.exitQueue = append(s.exitQueue, queue[i:]...)
				return err
			}
			log(Decision{Kind: "seat_quarantined", TaskID: it.TaskID, Seat: it.Handle, Reason: r.ReasonCode + " at " + it.Why})
			if st := s.seats[it.Handle]; st != nil && st.closing == "" {
				st.closing = "quarantined"
			}
			if n, ok := s.Lead.(SeatQuarantineNotifier); ok && s.Lead != nil {
				if err := n.SeatQuarantined(it.Handle, it.TaskID, r.ReasonCode, r.Detail); err != nil {
					log(Decision{Kind: "lead_notice_failed", TaskID: it.TaskID, Reason: err.Error()})
				}
			}
		}
		if it.Gone && !s.exitPending(it.Handle) {
			if g, ok := s.Launcher.(SeatGoneObserver); ok {
				g.SeatGone(it.Handle)
			}
		}
	}
	return nil
}

// holdForExit keeps a closed seat's slot until its pending exit check is
// decided (N3): the launcher freed it at the confirmed close.
func (s *Scheduler) holdForExit(h string) {
	if !s.exitPending(h) {
		return
	}
	s.reserve(h)
	for i := range s.exitQueue {
		if s.exitQueue[i].Handle == h {
			s.exitQueue[i].Gone = true
		}
	}
	s.saveExits()
}

// closeAdopted closes the seats a previous incarnation left running (N2),
// before any dispatch of this tick; it reports whether any is still open.
func (s *Scheduler) closeAdopted(log func(Decision)) bool {
	open := false
	var handles []string
	for h, st := range s.seats {
		if st.closing == closeRestart {
			handles = append(handles, h)
		}
	}
	sort.Strings(handles)
	for _, h := range handles {
		if s.Launcher == nil {
			delete(s.seats, h)
			continue
		}
		if err := s.Launcher.CloseSeat(h, closeRestart); err != nil {
			open = true
			log(Decision{Kind: "seat_close_failed", Seat: h, Reason: err.Error()})
			continue
		}
		delete(s.seats, h)
		s.holdForExit(h)
		log(Decision{Kind: "seat_closed", Seat: h, Reason: closeRestart})
	}
	return open
}

// closeUnconfirmed reports whether the launcher's nil close of a seat does
// not confirm its termination.
func (s *Scheduler) closeUnconfirmed(handle string) bool {
	a, ok := s.Launcher.(AsyncCloser)
	return ok && a.CloseUnconfirmed(handle)
}
