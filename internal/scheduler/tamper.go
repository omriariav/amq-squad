package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
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
// quarantined seq, the quarantine path and the hold's alarm id in its
// detail; the seat is held
// out of every dispatch, so no further prepare of it is attempted and no
// further line is logged, until the lead's recovery is in the seat's
// sealed ledger. The release is a reconciliation, not a message (Codex's
// P2 on #904): every tick asks the preparer (RecoveryReader) what recovery
// the seat's ledger records, and a hold whose exact slot the record names,
// or whose alarm a decision by alarm released, is released, its tasks
// returned to pending; a decision line lost, a
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
	// Alarm is the hold's durable id (newAlarmID: random, never a counter,
	// so it is unique across restarts), kept in the state file with the
	// hold, written into the block's detail and the stop line, and named by
	// the lead's decision by alarm (scheduler recover --alarm) and by the
	// release's line (the plan reviewer's ruling on Codex's delta pass on
	// c267382d).
	Alarm string `json:"alarm"`
}

// RecoveryMark is what a seat's sealed ledger records of the lead's
// recoveries, as the scheduler reads it: the chain's own record (the
// quarantined seq and slot it continued from, and the alarm when a
// decision by alarm made it, SlotAlarm), and the alarm id of every
// decision by alarm the ledger keeps (one record per alarm, never
// replaced; the plan reviewer's ruling on Codex's pass on 254f2c2c).
type RecoveryMark struct {
	Seq       int      `json:"seq"`
	Slot      string   `json:"slot"`
	SlotAlarm string   `json:"slot_alarm,omitempty"`
	Alarms    []string `json:"alarms,omitempty"`
}

// newAlarmID is a hold's alarm id: eight hex characters from the system's
// random source (unique across restarts; a counter would not be).
func newAlarmID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("scheduler: the system's random source failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// RecoveryReader is a preparer that reads the recovery a seat's sealed
// ledger records (schedproc's CleanSeats through seatcommit.RecoveryOf):
// the quarantined seq and slot path it continued from, or the alarm a
// decision by alarm released; ok false when the ledger does not verify or
// records none.
type RecoveryReader interface {
	LedgerRecovery(handle string) (RecoveryMark, bool)
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
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), reasonIdentity(err.Error()), log)
	return nil, nil
}

// logOnce logs first once per its task (and the task record's change),
// seat, kind and reason identity, then count once more when it repeats,
// then counts: the prepare failures and the release retries (Codex and the
// plan reviewer on c9826c31). The identity is the reason with its
// per-attempt parts masked (reasonIdentity), never the raw text, so a
// failure that names a fresh temp file each tick is one failure (Codex on
// b250b951); the logged record keeps the full text. preparePassed clears a
// seat's keys.
func (s *Scheduler) logOnce(first, count Decision, change, identity string, log func(Decision)) {
	if s.prepareFails == nil {
		s.prepareFails = map[string]*prepareFailure{}
	}
	key := prepareKey(first.Seat, first.TaskID) + "\x00" + first.Kind
	f := s.prepareFails[key]
	if f == nil || f.task != change || f.reason != identity {
		s.prepareFails[key] = &prepareFailure{task: change, reason: identity, count: 1}
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
	if s.tampered == nil {
		s.tampered = map[string]*LedgerTamper{}
	}
	tm := s.tampered[handle]
	if tm == nil {
		tm = &LedgerTamper{Handle: handle, Seq: e.Seq, Quarantine: e.Quarantine, At: now.UTC(), Alarm: newAlarmID()}
		tm.Detail = tamperDetail(handle, e, tm.Alarm)
		s.tampered[handle] = tm
		s.publishHolds()
	}
	detail := tamperDetail(handle, e, tm.Alarm)
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
// characters; the path is what the lead needs), the seq and the hold's
// alarm id (the screen's words name the decision by alarm with it), then
// the refusal.
func tamperDetail(handle string, e *LedgerTamperedError, alarm string) string {
	seq := e.Seq
	if seq == "" {
		seq = "unknown"
	}
	if e.Quarantine == "" {
		return fmt.Sprintf("seat %s: the refused document could not be quarantined (seq %s; alarm %s): %v", handle, seq, alarm, e.Err)
	}
	return fmt.Sprintf("seat %s: quarantined at %s (seq %s; alarm %s): %v", handle, e.Quarantine, seq, alarm, e.Err)
}

// publishHolds copies the held seats' alarm ids where the decision
// socket reads them (HeldAlarm), under mu: the hold map is the tick's own.
func (s *Scheduler) publishHolds() {
	m := make(map[string]string, len(s.tampered))
	for h, tm := range s.tampered {
		m[h] = tm.Alarm
	}
	s.mu.Lock()
	s.heldAlarms = m
	s.mu.Unlock()
}

// HeldAlarm is the alarm id of the hold seat handle is held by now, as
// the last tick left it (the decision by alarm is refused for any other).
func (s *Scheduler) HeldAlarm(handle string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.heldAlarms[handle]
	return a, ok
}

// tamperedSeat reports whether a seat is held by a tamper alarm (never
// idle, never prepared, never granted).
func (s *Scheduler) tamperedSeat(handle string) bool { return s.tampered[handle] != nil }

// The store calls of the release (test seams: a read or a reset that
// fails once, Codex on c9826c31).
var (
	storeGet   = func(st task.Store, id string) (task.Task, error) { return st.Get(id) }
	storeReset = func(st task.Store, id string, match func(task.Task) bool, now time.Time) (task.Task, error) {
		return st.ResetIfBlocked(id, ReasonLedgerTampered, match, now)
	}
)

// applyRecoveries reconciles every held seat against its ledger's
// recovery record, each tick: a hold whose exact slot the record names,
// or whose alarm a decision by alarm released, is released and its tasks
// returned to pending, the resets first and the hold deleted after. A
// task the store no longer has is skipped; any other read error, or a
// reset that fails, keeps the hold for the next tick (a task already
// returned is skipped then) and is logged once per task, seat and reason,
// then counted (ledger_release_failed, ledger_release_suppressed): a
// persistent store error never floods the log and never loses the hold.
func (s *Scheduler) applyRecoveries(now time.Time, log func(Decision)) error {
	defer s.publishHolds()
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
		rec, ok := reader.LedgerRecovery(h)
		if !ok || !tm.covers(rec) {
			continue
		}
		seq, slot := rec.Seq, rec.Slot
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
			if !tm.names(t) {
				continue // reset by the lead already, or blocked for something else
			}
			if _, err := storeReset(s.Store, id, tm.names, now); errors.Is(err, task.ErrNotFound) || errors.Is(err, task.ErrBlockChanged) {
				// gone, or no longer this hold's block: another decision
				// (the lead's reset, a new block of another reason or slot)
				// came between the read and the reset, and it stands (Codex
				// on b250b951, the plan reviewer on af1a6124)
				continue
			} else if err != nil {
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
		what := fmt.Sprintf("the lead's recovery from quarantine seq %d (%s)", seq, slot)
		if slices.Contains(rec.Alarms, tm.Alarm) {
			what = "the lead's decision by alarm"
		}
		log(Decision{Kind: "ledger_hold_released", Seat: h, Reason: fmt.Sprintf("the commits ledger of seat %s records %s; alarm %s is released and the seat is grantable again", h, what, tm.Alarm)})
	}
	return nil
}

// releaseFailed is a release's store failure, logged once per task, seat
// and reason identity, then counted.
func (s *Scheduler) releaseFailed(id, h, reason string, log func(Decision)) {
	s.logOnce(Decision{Kind: "ledger_release_failed", TaskID: id, Seat: h, Reason: reason},
		Decision{Kind: "ledger_release_suppressed", TaskID: id, Seat: h, Reason: "the release keeps failing; further repeats are counted, not logged, until the reason changes: " + reason}, "", reasonIdentity(reason), log)
}

// The per-attempt parts of a failure's text: a path (a temp file or
// directory named fresh each attempt: amq-squad-shadow-<random>, an
// atomic write's temp) and any run of digits (a random suffix, a pid, a
// byte count).
var (
	reasonPath   = regexp.MustCompile(`[^\s:'"()]*/[^\s:'"()]*`)
	reasonDigits = regexp.MustCompile(`[0-9]+`)
)

// reasonIdentity is a failure's suppression identity: its text with every
// path and every run of digits masked. Two failures that differ only in a
// temp name are one failure; two that say different things are two (a
// changed reason is logged again).
func reasonIdentity(reason string) string {
	return reasonDigits.ReplaceAllString(reasonPath.ReplaceAllString(reason, "<path>"), "#")
}

// covers reports whether a recovery record answers this hold (the plan
// reviewer's rulings on af1a6124 and on Codex's delta pass on c267382d): a
// decision by alarm that names this hold's alarm, or a record that names
// this hold's exact slot. A seq never answers a hold: slot names are
// unique by construction (commits.<h>.seq<N>[.k]), a seq is not, and the
// head's seq the hold was named by was never verified (the incident was
// itself a lowered seq). A decision by alarm for any other alarm (an
// earlier hold's, landing late) answers nothing, so it never releases a
// newer hold. A hold with no slot is answered only by its alarm.
//
// The records are kept per alarm, so a decision for an earlier alarm that
// is sealed after a newer one never takes the newer one's record away,
// whatever the interleaving. The lock order is the ledger lock, then the
// scheduler's mutex: the decision's guard (schedproc) reads HeldAlarm
// under s.mu while it holds the ledger lock; the tick never holds s.mu
// while it calls the preparer or the reader (every s.mu section is a
// copy of a field), so it never takes them the other way round. The tick
// changes the holds (s.tampered) without the ledger lock; a hold that
// changes between a guard and its seal costs nothing, since the record
// sealed names its own alarm only.
func (tm *LedgerTamper) covers(rec RecoveryMark) bool {
	for _, a := range rec.Alarms {
		if a == tm.Alarm {
			return true
		}
	}
	return tm.Quarantine != "" && rec.Slot == tm.Quarantine && rec.SlotAlarm == ""
}

// names reports whether a task's block is this hold's own: blocked
// ledger_tampered with the detail the alarm wrote (tamperDetail), which
// names the hold's alarm id. A tamper block of another alarm, or written
// by another writer (the commit step's refusal names no alarm), is not
// this hold's and its release leaves it (the plan reviewer on af1a6124).
func (tm *LedgerTamper) names(t task.Task) bool {
	return t.State == task.StateBlocked && t.BlockReason == ReasonLedgerTampered && tm.Alarm != "" &&
		strings.Contains(t.BlockDetail, "; alarm "+tm.Alarm+")")
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
		if tm.Alarm == "" {
			// a hold an earlier build of this branch wrote without an id;
			// its blocks name none, so the lead resets their tasks by hand
			tm.Alarm = newAlarmID()
		}
		s.tampered[tm.Handle] = &tm
	}
	s.publishHolds()
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
