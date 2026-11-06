package scheduler

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// A tamper alarm stops the lead once (3.2.0-0c-B rules 1 to 3). Before
// this the scheduler's prepare retried a tampered seat every tick (1,949
// identical seat_prepare_failed lines over 65 minutes on 2026-11-05) and
// the task stayed pending with no escalation. Now a prepare failure whose
// cause is the ledger's tamper check (LedgerTamperedError, which the
// preparer raises from seatcommit's refusal) is a typed block on the task,
// ledger_tampered, routed to the lead, raised once, with the seat, the
// quarantined seq and the quarantine path in its detail; the seat is held
// out of every dispatch, so no further prepare of it is attempted and no
// further line is logged, until the lead's recovery is in the seat's
// sealed ledger. The release is a reconciliation, not a message (Codex's
// P2 on #904): every tick asks the preparer (RecoveryReader) what recovery
// the seat's ledger records, and a hold whose slot or seq the record covers
// is released, its tasks returned to pending; a decision line lost, a
// crash before the state write or a reset that failed loses nothing, since
// the next tick asks again. A clear of a warning never makes the seat
// grantable: only the recovery does. Any other prepare failure keeps its
// path (the task stays pending and the seat is tried again next tick) but
// is logged once per task, seat and reason, then once more as a count
// line, as the seat request backoff of brief 3.1.1-2 did.

// ReasonLedgerTampered is the typed block of a task whose seat's commits
// ledger failed its check at prepare (seatcommit.ReasonLedgerTampered, the
// commit step's code for the same failure).
const ReasonLedgerTampered = "ledger_tampered"

// LedgerTamperedError is a preparer's refusal of a seat whose commits
// ledger failed its check: the seat, the head seq the quarantine slot is
// named by, the slot's path ("" when the copy itself failed; the error
// says so) and the refusal.
type LedgerTamperedError struct {
	Handle, Seq, Quarantine string
	Err                     error
}

func (e *LedgerTamperedError) Error() string { return e.Err.Error() }
func (e *LedgerTamperedError) Unwrap() error { return e.Err }

// LedgerTamper is a seat held out of dispatch by a tamper alarm, as the
// state file keeps it across a restart (the alarm is raised once, not once
// per incarnation) and status reads it.
type LedgerTamper struct {
	Handle     string    `json:"handle"`
	Seq        string    `json:"seq,omitempty"`
	Quarantine string    `json:"quarantine,omitempty"`
	Detail     string    `json:"detail"`
	At         time.Time `json:"at"`
	// Tasks are the tasks blocked ledger_tampered on this seat: the
	// recovery returns them to pending.
	Tasks []string `json:"tasks,omitempty"`
}

// RecoveryReader is a preparer that reads the recovery a seat's sealed
// ledger records (schedproc's CleanSeats through seatcommit.RecoveryOf):
// the quarantined seq and slot path it continued from; ok false when the
// ledger does not verify or records none.
type RecoveryReader interface {
	LedgerRecovery(handle string) (seq int, slot string, ok bool)
}

// prepareFailure is the last non-tamper prepare failure of one task on
// one seat: the task's record's last change, the reason and how many
// ticks it repeated. Keyed per task and seat (Codex's P2 on #904: two
// pending tasks alternating on one failing seat are two keys, one line
// and one count line each).
type prepareFailure struct {
	task   string
	reason string
	count  int
}

func prepareKey(handle, taskID string) string { return handle + "\x00" + taskID }

// prepareFailed takes a preparer's error for a task and a seat. A tamper
// is the typed block (the task returned is the blocked one); any other
// failure is logged once per task, seat and reason, then counted.
func (s *Scheduler) prepareFailed(t task.Task, handle string, err error, now time.Time, log func(Decision)) (*task.Task, error) {
	var tampered *LedgerTamperedError
	if errors.As(err, &tampered) {
		return s.blockOnTamper(t, handle, tampered, now, log)
	}
	s.logOnce(Decision{Kind: "seat_prepare_failed", TaskID: t.ID, Seat: handle, Reason: err.Error()},
		Decision{Kind: "seat_prepare_suppressed", TaskID: t.ID, Seat: handle, Reason: "the same prepare failure repeats; further repeats are counted, not logged, until the task, the seat or the reason changes: " + err.Error()},
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), log)
	return nil, nil
}

// logOnce logs first once per its task (and the task record's change),
// seat, kind and reason, then count once more when it repeats, then
// counts: the prepare failures and the release retries (Codex and the plan
// reviewer on c9826c31). preparePassed clears a seat's keys.
func (s *Scheduler) logOnce(first, count Decision, change string, log func(Decision)) {
	if s.prepareFails == nil {
		s.prepareFails = map[string]*prepareFailure{}
	}
	key := prepareKey(first.Seat, first.TaskID) + "\x00" + first.Kind
	f := s.prepareFails[key]
	if f == nil || f.task != change || f.reason != first.Reason {
		s.prepareFails[key] = &prepareFailure{task: change, reason: first.Reason, count: 1}
		log(first)
		return
	}
	f.count++
	if f.count == 2 {
		log(count)
	}
}

// preparePassed forgets a seat's repeated prepare failures once a prepare
// of it succeeds (the next failure is logged again).
func (s *Scheduler) preparePassed(handle string) {
	for k := range s.prepareFails {
		if strings.HasPrefix(k, prepareKey(handle, "")) {
			delete(s.prepareFails, k)
		}
	}
}

// blockOnTamper raises the typed block: one seat_prepare_failed line with
// the reason code, the block in the store, and the seat held.
func (s *Scheduler) blockOnTamper(t task.Task, handle string, e *LedgerTamperedError, now time.Time, log func(Decision)) (*task.Task, error) {
	detail := tamperDetail(handle, e)
	if s.tampered == nil {
		s.tampered = map[string]*LedgerTamper{}
	}
	tm := s.tampered[handle]
	if tm == nil {
		tm = &LedgerTamper{Handle: handle, Seq: e.Seq, Quarantine: e.Quarantine, Detail: detail, At: now.UTC()}
		s.tampered[handle] = tm
	}
	tm.Tasks = append(tm.Tasks, t.ID)
	s.preparePassed(handle)
	log(Decision{Kind: "seat_prepare_failed", TaskID: t.ID, Seat: handle, ReasonCode: ReasonLedgerTampered, Reason: detail})
	got, err := s.Store.Block(t.ID, "", ReasonLedgerTampered, detail, task.RouteLead, now)
	if err != nil {
		return nil, err
	}
	log(Decision{Kind: "blocked", TaskID: t.ID, Seat: handle, Reason: ReasonLedgerTampered})
	return &got, nil
}

// tamperDetail is the typed block's detail and the decision's reason: the
// seat, the quarantine path first (the screen's record line is cut at 120
// characters; the path is what the lead needs), the seq, then the refusal.
func tamperDetail(handle string, e *LedgerTamperedError) string {
	seq := e.Seq
	if seq == "" {
		seq = "unknown"
	}
	if e.Quarantine == "" {
		return fmt.Sprintf("seat %s: the refused document could not be quarantined (seq %s): %v", handle, seq, e.Err)
	}
	return fmt.Sprintf("seat %s: quarantined at %s (seq %s): %v", handle, e.Quarantine, seq, e.Err)
}

// tamperedSeat reports whether a seat is held by a tamper alarm (never
// idle, never prepared, never granted).
func (s *Scheduler) tamperedSeat(handle string) bool { return s.tampered[handle] != nil }

// The store calls of the release (test seams: a read or a reset that
// fails once, Codex on c9826c31).
var (
	storeGet   = func(st task.Store, id string) (task.Task, error) { return st.Get(id) }
	storeReset = func(st task.Store, id string, now time.Time) (task.Task, error) { return st.Reset(id, nil, now) }
)

// applyRecoveries reconciles every held seat against its ledger's
// recovery record, each tick: a hold whose slot the record names, or
// whose seq the record's is at or past, is released and its tasks
// returned to pending, the resets first and the hold deleted after. A
// task the store no longer has is skipped; any other read error, or a
// reset that fails, keeps the hold for the next tick (a task already
// returned is skipped then) and is logged once per task, seat and reason,
// then counted (ledger_release_failed, ledger_release_suppressed): a
// persistent store error never floods the log and never loses the hold.
func (s *Scheduler) applyRecoveries(now time.Time, log func(Decision)) error {
	reader, ok := s.Preparer.(RecoveryReader)
	if !ok || len(s.tampered) == 0 {
		return nil
	}
	handles := make([]string, 0, len(s.tampered))
	for h := range s.tampered {
		handles = append(handles, h)
	}
	sort.Strings(handles)
	for _, h := range handles {
		tm := s.tampered[h]
		seq, slot, ok := reader.LedgerRecovery(h)
		if !ok || !tm.covers(seq, slot) {
			continue
		}
		kept := false
		for _, id := range tm.Tasks {
			t, err := storeGet(s.Store, id)
			if errors.Is(err, task.ErrNotFound) {
				continue // gone
			}
			if err != nil {
				s.releaseFailed(id, h, "the task's record could not be read; the hold stays and the release is retried next tick: "+err.Error(), log)
				kept = true
				continue
			}
			if t.State != task.StateBlocked || t.BlockReason != ReasonLedgerTampered {
				continue // reset by the lead already
			}
			if _, err := storeReset(s.Store, id, now); err != nil {
				s.releaseFailed(id, h, "the task could not be returned to pending; the hold stays and the release is retried next tick: "+err.Error(), log)
				kept = true
				continue
			}
			log(Decision{Kind: "task_reset", TaskID: id, Seat: h, Reason: "the commits ledger of seat " + h + " was recovered by the lead; the task returns to pending"})
		}
		if kept {
			continue // the hold stays; the next tick tries again
		}
		delete(s.tampered, h)
		s.preparePassed(h)
		log(Decision{Kind: "ledger_hold_released", Seat: h, Reason: fmt.Sprintf("the commits ledger of seat %s records the lead's recovery from quarantine seq %d (%s); the seat is grantable again", h, seq, slot)})
	}
	return nil
}

// releaseFailed is a release's store failure, logged once per task, seat
// and reason, then counted.
func (s *Scheduler) releaseFailed(id, h, reason string, log func(Decision)) {
	s.logOnce(Decision{Kind: "ledger_release_failed", TaskID: id, Seat: h, Reason: reason},
		Decision{Kind: "ledger_release_suppressed", TaskID: id, Seat: h, Reason: "the release keeps failing; further repeats are counted, not logged, until the reason changes: " + reason}, "", log)
}

// covers reports whether a recovery record (its quarantined seq and slot)
// answers this hold: the slot the hold named, or any seq at or past the
// hold's (a hold of an unknown seq is answered by its slot only).
func (tm *LedgerTamper) covers(seq int, slot string) bool {
	if tm.Quarantine != "" && slot == tm.Quarantine {
		return true
	}
	n, err := strconv.Atoi(tm.Seq)
	return err == nil && seq >= n
}

// RestoreLedgerTampers takes the tamper alarms a previous incarnation
// left in its state file: a held seat stays held across a restart, so the
// alarm is raised once, never once per incarnation. Call before the first
// Tick; it returns how many it took.
func (s *Scheduler) RestoreLedgerTampers(st State) int {
	for _, tm := range st.LedgerTampered {
		if s.tampered == nil {
			s.tampered = map[string]*LedgerTamper{}
		}
		tm := tm
		s.tampered[tm.Handle] = &tm
	}
	return len(st.LedgerTampered)
}

// ledgerTamperList is the state file's view of the held seats.
func (s *Scheduler) ledgerTamperList() []LedgerTamper {
	var out []LedgerTamper
	for _, tm := range s.tampered {
		out = append(out, *tm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}
