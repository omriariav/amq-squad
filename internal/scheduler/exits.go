package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
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
// before the first Tick. The exits ledger is loaded first (the fourth pass,
// N3 (a)): adopting never overwrites the persisted checks of the previous
// incarnation.
func (s *Scheduler) AdoptClosing(handle string, pool task.Pool, at time.Time) {
	s.loadExits()
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
	// Seal: with Config.SealRoot the queue is a sealed file (the ruling of
	// the phase 5 re-pass): an HMAC tag keyed by the session seed and a
	// signed head, so a same-UID edit of the queue, an older copy or its
	// removal is ledger_tampered (dispatch stops, routed to the owner).
	Seal *sealchain.Link `json:"seal,omitempty"`
}

const (
	exitsLabel    = "amq-squad exits queue v1"
	exitsHeadName = "exits"
)

// errExitsNotLoaded: the queue is never written over a ledger not read yet.
var errExitsNotLoaded = errors.New("the exits ledger is not loaded yet")

// saveExits writes the queue (N3 (b): a failed write is recorded in
// exitsErr, and dispatch stops, exits_ledger_unwritable, until a write
// succeeds).
func (s *Scheduler) saveExits() error {
	err := s.writeExits()
	s.exitsErr = err
	return err
}

func (s *Scheduler) writeExits() error {
	p := s.Config.ExitsPath
	if p == "" || s.Exits == nil {
		return nil
	}
	if !s.exitsLoaded {
		return errExitsNotLoaded
	}
	l := exitsLedger{Schema: 1, Queue: append([]exitItem{}, s.exitQueue...)}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if root := s.Config.SealRoot; root != "" {
		key, err := sealchain.SessionKey(root)
		if err != nil {
			return err
		}
		_, err = sealchain.Write(root, exitsHeadName, key, exitsLabel, l, func(link *sealchain.Link) error {
			sealed := l
			sealed.Seal = link
			raw, _ := json.MarshalIndent(sealed, "", "  ")
			return atomicfile.Write(p, raw, 0o600)
		}, time.Now())
		return err
	}
	raw, _ := json.MarshalIndent(l, "", "  ")
	// The one durable writer (P2-4 of the phase 5 re-pass): an exclusive
	// temp file, fsync, rename, then the directory fsync, so a deferred
	// gone-only check survives a power loss after the write returns.
	return atomicfile.Write(p, raw, 0o600)
}

// checkExitsSeal checks the sealed exits queue against its head: a tag or
// chain that fails, a removed queue whose head says it existed, or a
// missing seed is ledger_tampered.
func (s *Scheduler) checkExitsSeal(root string, l exitsLedger, present bool) error {
	key, ok, err := sealchain.ReadKey(root)
	switch {
	case err != nil:
		return fmt.Errorf("%w: the exits ledger's session seed: %v", sealchain.ErrTampered, err)
	case !ok && present:
		return fmt.Errorf("%w: the session seed that seals the exits ledger is gone", sealchain.ErrTampered)
	case !ok:
		return nil // no seed, no queue yet: the first write seals it
	case !present:
		return sealchain.Absent(root, exitsHeadName, key)
	}
	body := l
	body.Seal = nil
	return sealchain.Check(root, exitsHeadName, key, exitsLabel, l.Seal, body)
}

// loadExits rebuilds the queue once (before adoption, else at the first
// tick): every pending check is queued again, and a gone seat's check holds
// its slot again. A ledger that cannot be read is retried every tick, and
// dispatch stops until it loads (N3 (b)).
func (s *Scheduler) loadExits() {
	if s.exitsLoaded {
		return
	}
	p := s.Config.ExitsPath
	if p == "" || s.Exits == nil {
		s.exitsLoaded = true
		return
	}
	raw, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		s.exitsErr = fmt.Errorf("the exits ledger %s cannot be read: %v", p, err)
		return
	}
	var l exitsLedger
	if err == nil {
		uerr := json.Unmarshal(raw, &l)
		if uerr == nil && l.Schema != 1 {
			uerr = fmt.Errorf("schema %d", l.Schema)
		}
		switch {
		case uerr != nil && s.Config.SealRoot != "":
			// A sealed record that does not decode, or carries another
			// schema, is ledger_tampered for the owner (the fix round's
			// item 25).
			s.exitsErr = sealchain.Tampered("the exits ledger "+p, uerr)
			return
		case uerr != nil:
			s.exitsErr = fmt.Errorf("the exits ledger %s cannot be parsed: %v", p, uerr)
			return
		}
	}
	if root := s.Config.SealRoot; root != "" {
		if serr := s.checkExitsSeal(root, l, err == nil); serr != nil {
			s.exitsErr = serr
			return
		}
	}
	s.exitsLoaded = true
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
// persisted). A decided gone check releases the seat's slot, but only once
// no other item for that handle is queued, and only after the decided items
// left the persisted ledger in one write (N3 (c) of the fourth pass).
func (s *Scheduler) runExits(now time.Time, log func(Decision)) error {
	queue := s.exitQueue
	s.exitQueue = nil
	defer func() {
		if s.saveExits() == nil {
			s.releaseDecided()
		}
	}()
	for i, it := range queue {
		if it.Why == whyCommit {
			s.exitQueue = append(s.exitQueue, it) // a running commit step, not a check
			continue
		}
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
		if it.Gone {
			if s.toRelease == nil {
				s.toRelease = map[string]bool{}
			}
			s.toRelease[it.Handle] = true
		}
	}
	return nil
}

// releaseDecided releases the slot of every gone seat whose checks are all
// decided and persisted as such.
func (s *Scheduler) releaseDecided() {
	var hs []string
	for h := range s.toRelease {
		if !s.exitPending(h) {
			hs = append(hs, h)
		}
	}
	sort.Strings(hs)
	for _, h := range hs {
		delete(s.toRelease, h)
		if g, ok := s.Launcher.(SeatGoneObserver); ok {
			g.SeatGone(h)
		}
	}
}

// Ready reports whether the start-up barrier is passed (the fourth pass):
// the exits ledger is loaded and writable, and every seat of the previous
// incarnation is closed with its close confirmed. Until then the scheduler
// dispatches nothing and its decision socket is not open.
func (s *Scheduler) Ready() bool {
	if !s.exitsLoaded || s.exitsErr != nil {
		return false
	}
	for _, st := range s.seats {
		if st.closing == closeRestart {
			return false
		}
	}
	return true
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
		st := s.seats[h]
		if s.Launcher == nil {
			delete(s.seats, h)
			continue
		}
		if st.awaitGone {
			open = true // closed; counted until its seat_gone confirms it
			continue
		}
		if err := s.Launcher.CloseSeat(h, closeRestart); err != nil {
			open = true
			log(Decision{Kind: "seat_close_failed", Seat: h, Reason: err.Error()})
			continue
		}
		// N2 of the fourth pass: a nil close confirms only for a launcher
		// that confirms synchronously; an AsyncCloser's seat stays closing
		// until its seat_gone.
		if s.closeUnconfirmed(h) {
			st.awaitGone = true
			open = true
			log(Decision{Kind: "seat_close_requested", Seat: h, Reason: closeRestart + "; waiting for its seat_gone"})
			continue
		}
		delete(s.seats, h)
		s.holdForExit(h)
		log(Decision{Kind: "seat_closed", Seat: h, Reason: closeRestart})
	}
	return open
}

// whyCommit marks a commit step running off the tick in the exits queue: the
// seat stays reserved while it runs, and a restart finds it (v3-5-verifier,
// the liveness ruling): the next incarnation's commit step for that task is
// the adoption, and a verifier that started and never recorded is never run
// again on the same snapshot (seatcommit's verifier_interrupted).
const whyCommit = "commit"

// commitRunning records a running commit step: its exits-queue entry (once)
// and a decision whenever its state changes; a step found in the queue from
// a previous incarnation is logged as adopted.
func (s *Scheduler) commitRunning(handle, taskID, state string, now time.Time, log func(Decision)) {
	if s.commitState == nil {
		s.commitState = map[string]string{}
	}
	it := exitItem{Handle: handle, TaskID: taskID, Why: whyCommit, At: now}
	prev := s.commitState[taskID]
	if !s.exitPendingItem(it) {
		s.queueExit(it)
	}
	if prev != state {
		s.commitState[taskID] = state
		log(Decision{Kind: "commit_running", TaskID: taskID, Seat: handle, Reason: state})
	}
}

// noteAdopted logs, once, a commit step the exits queue carries from a
// previous incarnation: this incarnation's step for the task is its
// adoption (deferred as verifier_interrupted when its verifier had started).
func (s *Scheduler) noteAdopted(handle, taskID string, log func(Decision)) {
	if _, seen := s.commitState[taskID]; seen || !s.exitPendingItem(exitItem{Handle: handle, TaskID: taskID, Why: whyCommit}) {
		return
	}
	if s.commitState == nil {
		s.commitState = map[string]string{}
	}
	s.commitState[taskID] = "adopted"
	log(Decision{Kind: "commit_adopted", TaskID: taskID, Seat: handle, Reason: "a commit step of the previous incarnation"})
}

// commitEnded drops a decided commit step's exits-queue entry.
func (s *Scheduler) commitEnded(taskID string) {
	delete(s.commitState, taskID)
	kept, dropped := s.exitQueue[:0], false
	for _, it := range s.exitQueue {
		if it.Why == whyCommit && it.TaskID == taskID {
			dropped = true
			if it.Gone {
				if s.toRelease == nil {
					s.toRelease = map[string]bool{}
				}
				s.toRelease[it.Handle] = true
			}
			continue
		}
		kept = append(kept, it)
	}
	s.exitQueue = kept
	if dropped && s.saveExits() == nil {
		s.releaseDecided()
	}
}

// closeUnconfirmed reports whether the launcher's nil close of a seat does
// not confirm its termination.
func (s *Scheduler) closeUnconfirmed(handle string) bool {
	a, ok := s.Launcher.(AsyncCloser)
	return ok && a.CloseUnconfirmed(handle)
}
