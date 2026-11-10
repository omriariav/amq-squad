package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// R3 of the 2b-2 milestone B first pass: the exit check (the sealed
// ledger's evidence refs) runs on every terminal path of a seat, not
// only on a done report (the committer runs it there): a failed attempt, a
// seat that is gone, a lease that expired. And an expired claimed attempt is
// closed through the launcher, its termination confirmed, before the seat is
// cleaned or dispatched to again.

// ExitResult is the exit check's answer.
type ExitResult struct {
	OK       bool // nothing changed
	Deferred bool // the check could not run; the scheduler retries it next tick
	// Refused is a refusal at the exit (ledger_tampered): the seat is closed
	// and the lead is told; the commit step refuses the same way at the
	// seat's next done report, so nothing of it is committed.
	Refused    bool
	ReasonCode string
	Detail     string
}

// ExitChecker runs the exit check at a seat exit that is not a done report
// (2b-2's committer implements it with seatcommit.ExitCheck). why is failed,
// gone, expired or unclaimed.
type ExitChecker interface {
	CheckExit(handle, taskID, why string) ExitResult
}

// AttemptArchiver is the optional scheduler-side snapshot hook for a typed
// widen block. The scheduler calls it after the store ended the attempt.
type AttemptArchiver interface {
	ArchiveAttempt(handle string, t task.Task, attempt task.Attempt) error
}

// AttemptCleaner archives and cleans a stopped cancelled attempt after the
// seat process is confirmed gone. The caller holds the seat's reservation
// (task.Store.ReserveCancel) for the round; the clean takes none of its own.
type AttemptCleaner interface {
	CleanAttempt(handle, taskID string, attempt int, outcome string) error
}

// SeatExitRefusedNotifier is a LeadNotifier that also tells the lead about
// a refusal at a seat exit (a hint; the decisions log is the record).
type SeatExitRefusedNotifier interface {
	SeatExitRefused(handle, taskID, reason, detail string) error
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
	closeExpired   = "lease_expired"
	closeRestart   = "scheduler_restart"
	closeCancelled = "task_cancelled"
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
	// Deferrals counts a commit step's deferrals in a row (why commit),
	// persisted so the limit holds across restarts.
	Deferrals int `json:"deferrals,omitempty"`
	// Attempt is the launch attempt a gone check is for: the request id the
	// seat joined on (3.0.1 t17). One attempt's gone is checked once.
	Attempt     string `json:"attempt,omitempty"`
	TaskAttempt int    `json:"task_attempt,omitempty"`
	// Close marks a cancelled attempt that was its seat's last lease
	// (3.2.0-2b-1, the occupant match; Codex's P1 on #909): the seat may
	// still be working on it, so a seat the scheduler counts up is closed
	// and its termination confirmed before the check and the clean.
	// Persisted, so a restart finishes the close; cleared once the close is
	// confirmed (holdForExit).
	Close bool `json:"close,omitempty"`
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

func (s *Scheduler) queueExit(it exitItem) bool {
	if s.Exits == nil || it.Handle == "" {
		return false
	}
	// One exit check per gone attempt (3.0.1 t17, the reviewer's ruling): a
	// second SeatGone for the same seat and attempt (the liveness check's,
	// then a launcher's confirmation) is dropped here, before it runs a
	// second check or a second slot release. A repeated check by design (R3,
	// P1-9) is for another attempt, or a gone that names none.
	if it.Gone && it.Attempt != "" {
		key := it.Handle + "#" + it.Attempt
		if s.goneQueued[key] {
			return false
		}
		if s.goneQueued == nil {
			s.goneQueued = map[string]bool{}
		}
		s.goneQueued[key] = true
	}
	s.exitQueue = append(s.exitQueue, it)
	s.saveExits()
	return true
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
	// removal is ledger_tampered (dispatch stops, routed to the lead).
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
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	if root := s.Config.SealRoot; root != "" {
		current, err := verifyExitsLedger(root, p)
		if err != nil {
			return err
		}
		key, err := sealchain.SessionKey(root)
		if err != nil {
			return err
		}
		headBytes := current.head
		if current.migration != nil {
			if err := recordExitsLayoutMigration(root, current.migration.Seq); err != nil {
				return err
			}
			if afterExitsMigrationDecision != nil {
				if err := afterExitsMigrationDecision(root, current.migration.Seq); err != nil {
					return err
				}
			}
			headBytes, err = sealchain.CompleteRawHead(root, exitsHeadName, key, *current.migration, time.Now())
			if err != nil {
				return err
			}
		}
		l := exitsLedger{Schema: 1, Queue: append([]exitItem{}, s.exitQueue...)}
		body, err := json.MarshalIndent(l, "", "  ")
		if err != nil {
			return err
		}
		_, err = sealchain.WriteRawBeforeHead(root, exitsHeadName, key, exitsLabel, headBytes, body, sealchain.BinaryVersion, 1, func(raw []byte) error {
			return atomicfile.Write(p, raw, 0o600)
		}, func(seal sealchain.RawSeal) error {
			if !current.legacy {
				return nil
			}
			if err := recordExitsLayoutMigration(root, seal.Seq); err != nil {
				return err
			}
			if afterExitsMigrationDecision != nil {
				return afterExitsMigrationDecision(root, seal.Seq)
			}
			return nil
		}, time.Now())
		return err
	}
	l := exitsLedger{Schema: 1, Queue: append([]exitItem{}, s.exitQueue...)}
	raw, _ := json.MarshalIndent(l, "", "  ")
	// The one durable writer (P2-4 of the phase 5 re-pass): an exclusive
	// temp file, fsync, rename, then the directory fsync, so a deferred
	// gone-only check survives a power loss after the write returns.
	return atomicfile.Write(p, raw, 0o600)
}

type exitsRead struct {
	ledger    exitsLedger
	document  []byte
	head      []byte
	present   bool
	legacy    bool
	migration *sealchain.RawSeal
}

// verifyExitsLedger is the read-only exits verifier. It accepts an authenticated
// exact-successor raw document beside a legacy head but never completes that
// head. Only writeExits, under the scheduler process lock, may do so.
func verifyExitsLedger(root, path string) (exitsRead, error) {
	var out exitsRead
	document, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return out, fmt.Errorf("the exits ledger %s cannot be read: %v", path, err)
	}
	out.document, out.present = document, err == nil
	if root == "" {
		if !out.present {
			return out, nil
		}
		if err := json.Unmarshal(document, &out.ledger); err != nil {
			return out, fmt.Errorf("the exits ledger %s cannot be parsed: %v", path, err)
		}
		if out.ledger.Schema != 1 {
			return out, fmt.Errorf("the exits ledger %s cannot be parsed: schema %d", path, out.ledger.Schema)
		}
		return out, nil
	}
	key, ok, err := sealchain.ReadKey(root)
	switch {
	case err != nil:
		return out, fmt.Errorf("%w: the exits ledger's session seed: %v", sealchain.ErrTampered, err)
	case !ok && out.present:
		return out, fmt.Errorf("%w: the session seed that seals the exits ledger is gone", sealchain.ErrTampered)
	case !ok:
		return out, nil // no seed, no queue yet: the first write seals it
	}
	headBytes, herr := os.ReadFile(sealchain.HeadPath(root, exitsHeadName))
	if errors.Is(herr, os.ErrNotExist) {
		headBytes = nil
	} else if herr != nil {
		return out, fmt.Errorf("%w: the exits ledger head: %v", sealchain.ErrTampered, herr)
	}
	out.head = headBytes
	if !out.present {
		return out, sealchain.AbsentBytes(exitsHeadName, key, headBytes)
	}
	layout, err := sealchain.HeadLayout(exitsHeadName, key, headBytes)
	if err != nil {
		return out, err
	}
	if layout == sealchain.RawLayout {
		seal, err := sealchain.CheckRaw(exitsHeadName, key, exitsLabel, "the exits ledger", document, headBytes, 1, &out.ledger)
		if err != nil {
			return out, err
		}
		if out.ledger.Schema != 1 {
			return out, sealchain.SchemaError("the exits ledger", seal, 1, out.ledger.Schema)
		}
		return out, nil
	}
	// A crash may leave the authenticated next raw document beside its
	// verified legacy head. Admit only that exact successor without writing.
	// The locked writer rechecks it and durably records the migration before
	// it completes the head.
	if _, _, splitErr := sealchain.SplitRaw(document); splitErr == nil {
		seal, err := sealchain.CheckRawMigration(exitsHeadName, key, exitsLabel, "the exits ledger", document, headBytes, 1, &out.ledger)
		if err != nil {
			return out, err
		}
		if out.ledger.Schema != 1 {
			return out, sealchain.SchemaError("the exits ledger", seal, 1, out.ledger.Schema)
		}
		out.migration = &seal
		return out, nil
	}
	if err := json.Unmarshal(document, &out.ledger); err != nil {
		return out, sealchain.Tampered("the exits ledger", err)
	}
	if out.ledger.Schema != 1 {
		return out, sealchain.Tampered("the exits ledger", fmt.Errorf("schema %d", out.ledger.Schema))
	}
	body := out.ledger
	body.Seal = nil
	if err := sealchain.CheckLegacy(exitsHeadName, key, exitsLabel, out.ledger.Seal, body, headBytes); err != nil {
		return out, err
	}
	out.legacy = true
	return out, nil
}

func recordExitsLayoutMigration(root string, seq int) error {
	_, err := sealchain.RecordLayoutMigration(
		filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl"),
		"exits", "", seq,
		fmt.Sprintf("legacy exits ledger migrated to %s at link %d", sealchain.RawLayout, seq),
		time.Now(),
	)
	return err
}

// afterExitsMigrationDecision is the crash seam after the durable decision
// and before head completion. Production leaves it nil.
var afterExitsMigrationDecision func(root string, seq int) error

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
	read, err := verifyExitsLedger(s.Config.SealRoot, p)
	if err != nil {
		s.exitsErr = err
		return
	}
	l := read.ledger
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
	if err := s.saveExits(); err != nil {
		// The next tick re-reads and retries the locked writer. This is required
		// when the durable migration decision landed but head completion did not.
		s.exitsLoaded = false
	}
}

func (s *Scheduler) exitPendingItem(it exitItem) bool {
	for _, q := range s.exitQueue {
		if q.Handle == it.Handle && q.TaskID == it.TaskID && q.Why == it.Why && q.TaskAttempt == it.TaskAttempt {
			return true
		}
	}
	return false
}

// scanAttempts queues the exit check for every attempt that ended failed,
// expired or unclaimed and was not checked yet by this scheduler (a restart
// checks the store's history once).
func (s *Scheduler) scanAttempts(list []task.Task, now time.Time) error {
	return s.scanAttemptsLog(list, now, func(Decision) {})
}

func (s *Scheduler) scanAttemptsLog(list []task.Task, now time.Time, log func(Decision)) error {
	if s.exitSeen == nil {
		s.exitSeen = map[string]bool{}
	}
	s.dropRecordedCleans(list, log)
	for _, t := range list {
		for _, a := range t.Attempts {
			key := fmt.Sprintf("%s#%d#%s", t.ID, a.N, a.Holder)
			if a.Outcome == task.OutcomeBlocked && t.BlockAttempt == a.N &&
				(t.BlockReason == task.ReasonWidenRequested || t.BlockReason == task.ReasonGrowthGateRequired) {
				if !s.exitSeen[key] {
					if archiver, ok := s.Preparer.(AttemptArchiver); ok {
						if err := archiver.ArchiveAttempt(a.Holder, t, a); err != nil {
							return err
						}
					}
					s.exitSeen[key] = true
				}
				continue
			}
			if s.Exits == nil {
				continue
			}
			switch a.Outcome {
			case task.OutcomeFailed, task.OutcomeExpired, task.OutcomeUnclaimed:
			case task.OutcomeCancelled:
				// The cancel's clean (3.2.0-2b-1 item 1) runs once per
				// cancelled attempt and is recorded on the attempt
				// (CleanedAt, Codex's P1 on #898 and the plan reviewer's
				// note 1): a restart reads the record, never a memory.
				if a.CleanedAt != nil || s.exitSeen[key] {
					continue
				}
				if _, ok := s.Preparer.(AttemptCleaner); !ok {
					continue
				}
				// The occupant match: a cancellation the seat has worked
				// past (a later lease of the seat, ended or current) is not
				// its unresolved cancellation. It is judged in one place,
				// the cancel round's reservation (ReserveCancel reads the
				// leases under the store's lock, Codex's P1 on the #909
				// re-pass): the item queued here is dropped there before
				// anything acts on it, with one seat_clean_superseded line,
				// so the seat, which may be on another task, is neither
				// closed nor cleaned for it.
			default:
				continue
			}
			if s.exitSeen[key] {
				continue
			}
			s.exitSeen[key] = true
			it := exitItem{Handle: a.Holder, TaskID: t.ID, Why: a.Outcome, At: now, TaskAttempt: a.N}
			// A cancelled attempt with no exit item yet is close-required
			// (one whose seat holds a later lease is dropped by the cancel
			// round's reservation before its Close is acted on)
			// (Codex's P1 on #909): nothing the scheduler holds proves the
			// seat stopped working on it. A restart has seen no lease held
			// (a cancel that landed before the previous incarnation queued
			// its item leaves no Close on disk), and the lead's dispatch
			// grants a lease outside the tick. runExits closes a seat the
			// scheduler counts up, and the check and the clean wait for the
			// confirmed close; a seat it does not count (no live process
			// adopted at the restart, none joined since) has nothing to
			// close. An item already queued keeps its own persisted flag.
			if a.Outcome == task.OutcomeCancelled {
				it.Close = true
			}
			if !s.exitPendingItem(it) {
				s.queueExit(it)
			}
		}
	}
	return nil
}

// laterLease names a lease of the seat granted at or after the attempt
// ended (an ended attempt, or the seat's current lease), "" when there is
// none: the attempt is then the seat's last.
func laterLease(list []task.Task, holder, taskID string, a task.Attempt) string {
	return task.LaterLease(list, holder, taskID, a)
}

// dropRecordedCleans reconciles the exits queue with the store before
// anything acts on it (Codex's P2 on #909): a cancelled attempt's item
// whose attempt is recorded cleaned (cleaned_at) is a clean that finished
// before a crash lost the queue's write (MarkAttemptCleaned, then the
// deferred saveExits). The item is dropped with one line, never run again,
// and the slot it held is released once the queue without it is written.
func (s *Scheduler) dropRecordedCleans(list []task.Task, log func(Decision)) {
	cleaned := map[string]bool{}
	for _, t := range list {
		for _, a := range t.Attempts {
			if a.Outcome == task.OutcomeCancelled && a.CleanedAt != nil {
				cleaned[fmt.Sprintf("%s#%d", t.ID, a.N)] = true
			}
		}
	}
	if len(cleaned) == 0 {
		return
	}
	kept, dropped := s.exitQueue[:0], false
	for _, it := range s.exitQueue {
		if it.Why != task.OutcomeCancelled || !cleaned[fmt.Sprintf("%s#%d", it.TaskID, it.TaskAttempt)] {
			kept = append(kept, it)
			continue
		}
		dropped = true
		delete(s.cleanDeferred, cleanDeferredKey(it))
		delete(s.exitRefused, cleanDeferredKey(it))
		if it.Gone {
			if s.toRelease == nil {
				s.toRelease = map[string]bool{}
			}
			s.toRelease[it.Handle] = true
		}
		log(Decision{Kind: "seat_clean_replay_dropped", TaskID: it.TaskID, Seat: it.Handle, Reason: fmt.Sprintf("seat %s's clean of %s attempt %d is recorded on the attempt; its queued exit item is dropped, not run again", it.Handle, it.TaskID, it.TaskAttempt)})
	}
	s.exitQueue = kept
	if dropped && s.saveExits() == nil {
		s.releaseDecided()
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
	for _, it := range queue {
		if it.Why == whyCommit {
			s.exitQueue = append(s.exitQueue, it) // a running commit step, not a check
			continue
		}
		// P1-1 of the second pass: the exit check of a seat whose commit step
		// is pending would wait on the ledger that step holds; it is deferred
		// to a later tick without touching the ledger.
		if s.commitPending(it.Handle) {
			s.exitQueue = append(s.exitQueue, it)
			log(Decision{Kind: "exit_check_deferred", TaskID: it.TaskID, Seat: it.Handle, Reason: "a commit step of the seat is pending"})
			continue
		}
		// A cancelled attempt is one round per tick (cancelRound): the
		// seat reserved under the store's lock, the close or the check and
		// the clean outside it, the reservation released on the way out.
		if it.Why == task.OutcomeCancelled {
			if s.cancelRound(&it, now, log) {
				s.exitQueue = append(s.exitQueue, it)
			}
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
		case r.Refused:
			log(Decision{Kind: "exit_refused", TaskID: it.TaskID, Seat: it.Handle, Reason: r.ReasonCode + " at " + it.Why})
			if st := s.seats[it.Handle]; st != nil && st.closing == "" {
				st.closing = "exit_refused"
			}
			if n, ok := s.Lead.(SeatExitRefusedNotifier); ok && s.Lead != nil {
				if err := n.SeatExitRefused(it.Handle, it.TaskID, r.ReasonCode, r.Detail); err != nil {
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

// ReasonSeatIdentityUnproven is the stop of a cancelled attempt whose
// seat's launch identity is not seen gone: its clean waits for the lead.
const ReasonSeatIdentityUnproven = "seat_identity_unproven"

// seatIdentityGone reports whether the seat's launch identity is seen gone
// (Liveness: the accepted proof's pid and start stamp; a pid alone is not
// an identity, since pids are reused). No liveness hook, or an identity it
// cannot read, is not gone (fail closed).
func (s *Scheduler) seatIdentityGone(handle string) (bool, string) {
	if s.Liveness == nil {
		return false, "the scheduler has no process identity check"
	}
	gone, detail, err := s.Liveness.SeatProcess(handle, "")
	switch {
	case err != nil:
		return false, err.Error()
	case !gone:
		return false, "its launch identity (pid and start stamp) is still live"
	}
	return true, detail
}

// logIdentityUnproven is the one lead stop for a cancelled attempt whose
// seat is not seen gone, once per (code, seat, task, attempt).
func (s *Scheduler) logIdentityUnproven(it exitItem, why string, log func(Decision)) {
	if s.cleanDeferred == nil {
		s.cleanDeferred = map[string]string{}
	}
	key := cleanDeferredKey(it)
	if s.cleanDeferred[key] == ReasonSeatIdentityUnproven {
		return
	}
	s.cleanDeferred[key] = ReasonSeatIdentityUnproven
	log(Decision{Kind: "seat_may_be_writing", TaskID: it.TaskID, Seat: it.Handle, ReasonCode: ReasonSeatIdentityUnproven,
		Reason: fmt.Sprintf("seat %s may still be writing to its worktree; the lead runs seat clean after it stops (%s)", it.Handle, why)})
}

func cleanDeferredKey(it exitItem) string {
	return fmt.Sprintf("%s#%s#%d", it.Handle, it.TaskID, it.TaskAttempt)
}

// logCleanDeferred logs a cancelled attempt's deferred clean once per
// (seat, task, attempt, reason): the same reason on the next tick is
// silent; a changed reason, or a clean that then ran, starts over. The
// reason is compared by its refusal code (refusalCode), never its whole
// text, which can carry an advancing age (Codex's P2 on #909).
func (s *Scheduler) logCleanDeferred(it exitItem, reason string, log func(Decision)) {
	if s.cleanDeferred == nil {
		s.cleanDeferred = map[string]string{}
	}
	key, id := cleanDeferredKey(it), refusalCode(reason)
	if s.cleanDeferred[key] == id {
		return
	}
	s.cleanDeferred[key] = id
	log(Decision{Kind: "seat_clean_deferred", TaskID: it.TaskID, Seat: it.Handle, Reason: reason})
}

// reasonCode is a refusal code: a snake_case word with at least one
// underscore (seat_cleaning, seat_worktree_dirty, ledger_tampered).
var reasonCode = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)+$`)

// refusalCode is a refusal's stable identity for once-per-reason
// logging (Codex's P2 on #909): its code, the first ": "-separated segment
// of the text that is one (the store's sentinels and the clean's and the
// prepare's refusals all lead with theirs), so a text that carries an
// advancing age ("seat_cleaning: seat s1 is being cleaned by pid 42 for
// 12s") is one reason on every tick. A text with no code is its own
// identity.
func refusalCode(text string) string {
	for _, seg := range strings.Split(text, ": ") {
		if reasonCode.MatchString(seg) {
			return seg
		}
	}
	return text
}

// noteExitRefused records a cancelled attempt's refused check and reports
// whether it is new for its (seat, task, attempt, reason).
func (s *Scheduler) noteExitRefused(it exitItem, reason string) bool {
	if s.exitRefused == nil {
		s.exitRefused = map[string]string{}
	}
	key := cleanDeferredKey(it)
	if s.exitRefused[key] == reason {
		return false
	}
	s.exitRefused[key] = reason
	return true
}

// cancelRound is one tick's round on a cancelled attempt's item: it
// reports whether the item stays queued. The plan reviewer's ruling on
// #909: the store's lock is never held through a launcher close. Under the
// lock, ReserveCancel reads the seat's leases (a seat on a later lease is
// never closed, checked or cleaned for the attempt: the item is dropped)
// and takes the seat's reservation, the 2b-1 marker with this process's
// pid and start stamp, which every grant refuses ("seat H is being closed
// and cleaned; dispatch again after it finishes"). The lock is released;
// the close, or the check and the clean, run outside it; the reservation
// is released on every path out of the round. Between rounds the seat is
// held by its queued item (the scheduler grants nothing to it) and by the
// attempt being uncleaned (the lead's dispatch refuses it).
func (s *Scheduler) cancelRound(it *exitItem, now time.Time, log func(Decision)) bool {
	st := s.seats[it.Handle]
	// a seat closing for another reason (a restart, an expiry), or a
	// close sent and waiting for its seat_gone: nothing acts on the item
	// until that is confirmed (P1-9)
	if st != nil && (st.awaitGone || (st.closing != "" && st.closing != closeCancelled)) {
		return true
	}
	if cancelCloseHook != nil {
		cancelCloseHook(it.Handle)
	}
	later, nonce, decision, err := s.Store.ReserveCancel(it.Handle, it.TaskID, it.TaskAttempt, now)
	if decision != "" {
		log(Decision{Kind: "cleaner_marker_taken_over", TaskID: it.TaskID, Seat: it.Handle, Reason: decision})
	}
	switch {
	case err != nil:
		s.logCleanDeferred(*it, "reserve the seat: "+err.Error(), log)
		return true
	case later != "":
		return !s.dropSuperseded(*it, later, now, log)
	}
	defer func() {
		if err := s.Store.ReleaseSeat(it.Handle, nonce); err != nil {
			log(Decision{Kind: "seat_clean_deferred", TaskID: it.TaskID, Seat: it.Handle, Reason: "release the seat's reservation: " + err.Error()})
		}
	}()
	// A close-required attempt on a seat the scheduler counts up: the
	// seat is closed first, the same order as a gone seat; the check and
	// the clean wait for the confirmed close, on a later round (3.2.0-2b-1
	// item 1).
	if it.Close && st != nil {
		if st.closing == "" {
			st.closing = closeCancelled
			log(Decision{Kind: "seat_closing", TaskID: it.TaskID, Seat: it.Handle, Reason: closeCancelled})
		}
		if s.Launcher == nil {
			s.forgetClosed(it, log)
			return true
		}
		if err := s.Launcher.CloseSeat(it.Handle, closeCancelled); err != nil {
			log(Decision{Kind: "seat_close_failed", Seat: it.Handle, Reason: err.Error()})
			return true
		}
		if s.closeUnconfirmed(it.Handle) {
			st.awaitGone = true
			log(Decision{Kind: "seat_close_requested", Seat: it.Handle, Reason: closeCancelled + "; waiting for its seat_gone"})
			return true
		}
		s.forgetClosed(it, log)
		return true
	}
	r := s.Exits.CheckExit(it.Handle, it.TaskID, it.Why)
	switch {
	case r.Deferred:
		log(Decision{Kind: "exit_check_deferred", TaskID: it.TaskID, Seat: it.Handle, Reason: r.Detail})
		return true
	case r.Refused:
		// Codex's P2 (D) on #898: the hold is the seat's unresolved
		// cancellation, not one queue item. The refused item stays queued
		// (and persisted): the seat's slot is held and no grant reaches it,
		// whatever other item for the seat is decided, until the check
		// passes and the clean runs. The lead is told once per reason; the
		// next tick re-checks silently.
		if s.noteExitRefused(*it, r.ReasonCode+" at "+it.Why) {
			log(Decision{Kind: "exit_refused", TaskID: it.TaskID, Seat: it.Handle, Reason: r.ReasonCode + " at " + it.Why})
			if n, ok := s.Lead.(SeatExitRefusedNotifier); ok && s.Lead != nil {
				if err := n.SeatExitRefused(it.Handle, it.TaskID, r.ReasonCode, r.Detail); err != nil {
					log(Decision{Kind: "lead_notice_failed", TaskID: it.TaskID, Reason: err.Error()})
				}
			}
		}
		return true
	}
	// The archive and the clean, after the confirmed close and the passed
	// check; a clean that cannot run yet keeps the item (and the hold) and
	// says why once per reason (Codex's C on #898).
	cleaner, ok := s.Preparer.(AttemptCleaner)
	if !ok {
		s.logCleanDeferred(*it, "the scheduler has no attempt cleaner", log)
		return true
	}
	// The plan reviewer's identity rule on #909: the clean runs only once
	// the seat's launch identity (pid and start stamp) is seen gone,
	// whatever the launcher confirmed. An identity that is missing,
	// unreadable or still live fails closed: nothing is cleaned, the item
	// (and the seat's hold) stays, the lead is told once, the attempt stays
	// uncleaned, and the reservation is released like on any other path.
	if gone, why := s.seatIdentityGone(it.Handle); !gone {
		s.logIdentityUnproven(*it, why, log)
		return true
	}
	if err := cleaner.CleanAttempt(it.Handle, it.TaskID, it.TaskAttempt, it.Why); err != nil {
		s.logCleanDeferred(*it, err.Error(), log)
		return true
	}
	// The durable record (the plan reviewer's note 1): a restart reads it
	// before acting, and never redoes a finished clean.
	if _, err := s.Store.MarkAttemptCleaned(it.TaskID, it.TaskAttempt, now); err != nil {
		s.logCleanDeferred(*it, "record the completed clean: "+err.Error(), log)
		return true
	}
	delete(s.cleanDeferred, cleanDeferredKey(*it))
	delete(s.exitRefused, cleanDeferredKey(*it))
	log(Decision{Kind: "seat_cleaned", TaskID: it.TaskID, Seat: it.Handle, Reason: fmt.Sprintf("seat %s's work on %s attempt %d was archived and its worktree cleaned", it.Handle, it.TaskID, it.TaskAttempt)})
	if it.Gone {
		if s.toRelease == nil {
			s.toRelease = map[string]bool{}
		}
		s.toRelease[it.Handle] = true
	}
	return false
}

// cancelCloseHook is a test seam: it runs after the tick's snapshot of the
// store and before the round's locked read and reservation (a grant landing
// in that window is the row's case). Nil in production.
var cancelCloseHook func(handle string)

// forgetClosed is a confirmed cancel close: the seat is forgotten, its slot
// held for the pending check, and the item's Close cleared.
func (s *Scheduler) forgetClosed(it *exitItem, log func(Decision)) {
	delete(s.seats, it.Handle)
	s.holdForExit(it.Handle) // the other queued items of the seat
	s.reserve(it.Handle)
	it.Gone = true
	it.Close = false // the close is confirmed; a restart never repeats it
	log(Decision{Kind: "seat_closed", Seat: it.Handle, Reason: closeCancelled})
}

// dropSuperseded drops a queued cancelled item whose seat holds a later
// lease: the attempt is recorded cleaned (the boundary before that lease
// is the clean), a cancel close of the seat not yet requested is called
// off, a gone item's slot is released, and one line says so. It reports
// false when the record could not be written (the item stays queued and
// is never acted on).
func (s *Scheduler) dropSuperseded(it exitItem, later string, now time.Time, log func(Decision)) bool {
	if _, err := s.Store.MarkAttemptCleaned(it.TaskID, it.TaskAttempt, now); err != nil {
		s.logCleanDeferred(it, "record the superseded clean: "+err.Error(), log)
		return false
	}
	if st := s.seats[it.Handle]; st != nil && st.closing == closeCancelled && !st.awaitGone {
		st.closing = ""
	}
	if it.Gone {
		if s.toRelease == nil {
			s.toRelease = map[string]bool{}
		}
		s.toRelease[it.Handle] = true
	}
	delete(s.cleanDeferred, cleanDeferredKey(it))
	delete(s.exitRefused, cleanDeferredKey(it))
	if s.exitSeen == nil {
		s.exitSeen = map[string]bool{}
	}
	s.exitSeen[fmt.Sprintf("%s#%d#%s", it.TaskID, it.TaskAttempt, it.Handle)] = true
	log(Decision{Kind: "seat_clean_superseded", TaskID: it.TaskID, Seat: it.Handle, Reason: fmt.Sprintf("seat %s holds a later lease (%s); the queued item for %s attempt %d is dropped, and nothing is closed or cleaned for it", it.Handle, later, it.TaskID, it.TaskAttempt)})
	return true
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
			s.exitQueue[i].Close = false // the close is confirmed; a restart never repeats it
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

// commitEntry writes a commit step's exits-queue entry once, before the
// step starts.
func (s *Scheduler) commitEntry(handle, taskID string, now time.Time) {
	if it := (exitItem{Handle: handle, TaskID: taskID, Why: whyCommit, At: now}); !s.exitPendingItem(it) {
		s.queueExit(it)
	}
}

// commitPending reports whether a commit step of the seat is pending.
func (s *Scheduler) commitPending(handle string) bool {
	for _, it := range s.exitQueue {
		if it.Why == whyCommit && it.Handle == handle {
			return true
		}
	}
	return false
}

// commitDeferred counts one more deferral of a task's commit step in its
// exits entry (in memory with no exits ledger) and returns the count.
func (s *Scheduler) commitDeferred(taskID string) int {
	for i := range s.exitQueue {
		if it := &s.exitQueue[i]; it.Why == whyCommit && it.TaskID == taskID {
			it.Deferrals++
			s.saveExits()
			return it.Deferrals
		}
	}
	if s.commitDeferrals == nil {
		s.commitDeferrals = map[string]int{}
	}
	s.commitDeferrals[taskID]++
	return s.commitDeferrals[taskID]
}

// sweepCommits ends the commit steps whose task no longer waits for them
// (P2-1 of the second pass: a task cancelled while its step ran): the step
// is cancelled (its verifier's group killed, its result discarded) and its
// entry dropped once it ended, which frees the seat.
func (s *Scheduler) sweepCommits(list []task.Task, log func(Decision)) {
	waiting := map[string]bool{}
	for _, t := range list {
		if t.State == task.StateInProgress && t.Report != nil {
			waiting[t.ID] = true
		}
	}
	var stale []exitItem
	for _, it := range s.exitQueue {
		if it.Why == whyCommit && !waiting[it.TaskID] {
			stale = append(stale, it)
		}
	}
	for _, it := range stale {
		ended := true
		if c, ok := s.Committer.(CommitCanceller); ok {
			ended = c.CancelCommit(it.TaskID)
		}
		if !ended {
			if s.commitState[it.TaskID] != "cancelling" {
				if s.commitState == nil {
					s.commitState = map[string]string{}
				}
				s.commitState[it.TaskID] = "cancelling"
				log(Decision{Kind: "commit_cancelling", TaskID: it.TaskID, Seat: it.Handle, Reason: "the task no longer waits for its commit step"})
			}
			continue
		}
		s.commitEnded(it.TaskID)
		log(Decision{Kind: "commit_cancelled", TaskID: it.TaskID, Seat: it.Handle, Reason: "the step ended; its result is discarded"})
	}
}

// commitRunning records a running commit step's state: a decision whenever
// it changes.
func (s *Scheduler) commitRunning(handle, taskID, state string, now time.Time, log func(Decision)) {
	if s.commitState == nil {
		s.commitState = map[string]string{}
	}
	s.commitEntry(handle, taskID, now)
	prev := s.commitState[taskID]
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
	delete(s.commitDeferrals, taskID)
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
