package scheduler

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// A failed seat launch is a decision with its reason, and a gone seat is
// seen each tick (3.0.1 t17, reviewer rulings 2026-10-30). On 2026-10-30
// (session v31) the reviewer seat's launch failed thirteen times in 27
// minutes and nothing recorded why: the launcher kept the failure to
// itself, the scheduler logged seat_requested and, after the token window,
// seat_request_cancelled with no reason, and status showed seat_starting
// for ever. Now the launcher posts every failed or unresolved launch as a
// SeatLaunchFailed event, the scheduler logs it (seat_launch_failed, with
// the stage, AMQ's reason code and the redacted detail), status shows the
// reason, and a second failure of a pool with the same reason blocks the
// waiting task for the lead instead of retrying.

const (
	// KindSeatLaunchFailed is the decision of a failed seat launch.
	KindSeatLaunchFailed = "seat_launch_failed"
	// GoneProcessExited is the reason of a SeatGone the liveness check
	// posts: the seat's proved process identity no longer matches.
	GoneProcessExited = "process_exited"
	// launchFailureRepeat is the failure count with one reason that blocks
	// the pool's waiting task.
	launchFailureRepeat = 2
)

// LaunchFailure is the last launch failure of one pool (the failed
// request's pool): the state file carries it for status until a seat of the
// pool comes up.
type LaunchFailure struct {
	Pool       task.Pool `json:"pool"`
	Handle     string    `json:"handle,omitempty"`
	RequestID  string    `json:"request_id,omitempty"`
	Stage      string    `json:"stage"`
	ReasonCode string    `json:"reason_code,omitempty"`
	Detail     string    `json:"detail,omitempty"` // redacted
	At         time.Time `json:"at"`
	// Count is how many launch attempts in a row failed with this reason
	// (one launch attempt counts once, whatever number of its seats failed).
	Count int `json:"count"`
	// LaunchID is the attempt that failed (another seat of the same attempt
	// is not a repeat); Seq orders the failures, and a block remembers the
	// one it was blocked on. Both persist in the state file and are restored
	// at a restart (Codex finding 2), so a repeat across a restart still
	// counts and a block is still recognised.
	LaunchID string `json:"launch_id,omitempty"`
	Seq      int    `json:"seq"`
}

// Code is the failure's reason: AMQ's reason code, else the stage.
func (f LaunchFailure) Code() string { return firstNonEmpty(f.ReasonCode, f.Stage) }

// launchBlock is a task the scheduler blocked on a repeated failure.
type launchBlock struct {
	pool string
	seq  int // the failure it was blocked on
}

// LaunchBlockedTask is a task the scheduler blocked on a repeated launch
// failure, as the state file keeps it: the pool and the failure's Seq.
type LaunchBlockedTask struct {
	TaskID string `json:"task_id"`
	Pool   string `json:"pool"`
	Seq    int    `json:"seq"`
}

// RestoreLaunchFailures takes the launch failures and the blocks on them
// a previous incarnation left in its state file (Codex finding 2): without
// them a task blocked on a failure stays blocked after a later relaunch
// succeeds (the reset no longer recognises it), and the repeat count starts
// over at every restart. Call before the first Tick; it returns how many
// failures and blocks it took.
func (s *Scheduler) RestoreLaunchFailures(st State) (failures, blocks int) {
	for _, f := range st.LaunchFailures {
		if s.launchFailures == nil {
			s.launchFailures = map[string]*LaunchFailure{}
		}
		f := f
		s.launchFailures[f.Pool.Key()] = &f
		if f.Seq > s.failSeq {
			s.failSeq = f.Seq
		}
		failures++
	}
	for _, b := range st.LaunchBlocked {
		if s.launchBlocked == nil {
			s.launchBlocked = map[string]launchBlock{}
		}
		s.launchBlocked[b.TaskID] = launchBlock{pool: b.Pool, seq: b.Seq}
		if b.Seq > s.failSeq {
			s.failSeq = b.Seq
		}
		blocks++
	}
	return failures, blocks
}

// launchBlockedList is the state file's view of the blocks, by task.
func (s *Scheduler) launchBlockedList() []LaunchBlockedTask {
	var out []LaunchBlockedTask
	for id, b := range s.launchBlocked {
		out = append(out, LaunchBlockedTask{TaskID: id, Pool: b.pool, Seq: b.seq})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

// SeatLiveness answers, read only, whether a live seat's process still
// runs by its proved identity (the launch ledger's accepted proof: pid and
// raw start stamp; never a bare signal 0 on a pid that may be reused).
// gone with err nil: the identity no longer matches (detail says how). A
// non-nil err: unknown, nothing is posted. requestID is the request the seat
// joined on ("" when unknown).
type SeatLiveness interface {
	SeatProcess(handle, requestID string) (gone bool, detail string, err error)
}

// checkLiveness asks the liveness hook about every live roster seat and
// posts SeatGone (process_exited) for each whose process is gone; the tick
// applies them at once, so the gone-seat handling (the exit check, then
// the relaunch path for its pool's tasks) runs in this tick.
func (s *Scheduler) checkLiveness(now time.Time, log func(Decision)) {
	if s.Liveness == nil || !s.rosterMode() {
		return
	}
	handles := make([]string, 0, len(s.seats))
	for h, st := range s.seats {
		if st.closing == "" && !st.awaitGone {
			handles = append(handles, h)
		}
	}
	sort.Strings(handles)
	for _, h := range handles {
		gone, detail, err := s.Liveness.SeatProcess(h, s.seats[h].requestID)
		if err != nil {
			// Logged when the answer changes, not every tick.
			if s.livenessNote == nil {
				s.livenessNote = map[string]string{}
			}
			if note := err.Error(); s.livenessNote[h] != note {
				s.livenessNote[h] = note
				log(Decision{Kind: "seat_liveness_unknown", Seat: h, Reason: note})
			}
			continue
		}
		delete(s.livenessNote, h)
		if gone {
			s.Post(Event{Kind: SeatGone, At: now, Handle: h, RequestID: s.seats[h].requestID, ReasonCode: GoneProcessExited, Detail: detail})
		}
	}
}

// goneRepeat says why a SeatGone is not this seat's first end, or "": the
// request it names was already seen gone; it names an earlier request than
// the live seat's (a late gone of the seat's previous launch); or it names
// no request and the handle was already seen gone and has not come up
// since. Such a gone is ignored whole. The first gone of a handle this
// incarnation never knew (after a restart) is not a repeat: its exit check
// runs (R3, P1-9). A gone with no request id after a new seat of the same
// handle came up cannot be told from that seat's own end, so a producer
// names the request (the liveness check does).
func (s *Scheduler) goneRepeat(ev Event) string {
	st := s.seats[ev.Handle]
	if ev.RequestID != "" {
		if s.goneReqs[ev.RequestID] {
			return "a repeat: the seat of request " + ev.RequestID + " was already seen gone"
		}
		if st != nil && st.requestID != "" && st.requestID != ev.RequestID {
			return "stale: it names request " + ev.RequestID + ", the live seat is request " + st.requestID
		}
		return ""
	}
	if st == nil && s.goneHandles[ev.Handle] {
		return "a repeat: the seat was already seen gone and has not come up since"
	}
	return ""
}

// noteGone records a seat's first end.
func (s *Scheduler) noteGone(ev Event) {
	if s.goneHandles == nil {
		s.goneHandles, s.goneReqs = map[string]bool{}, map[string]bool{}
	}
	s.goneHandles[ev.Handle] = true
	if ev.RequestID != "" {
		s.goneReqs[ev.RequestID] = true
	}
}

// noteLaunchFailure logs a SeatLaunchFailed as seat_launch_failed and keeps
// it as its pool's last failure: a second launch attempt that fails with
// the same reason counts the repeat.
func (s *Scheduler) noteLaunchFailure(ev Event, log func(Decision)) {
	detail := redact.Block(strings.TrimSpace(ev.Detail))
	stage := firstNonEmpty(ev.Stage, "launch_error")
	taskID := ""
	for _, r := range s.requests {
		if r.req.ID == ev.RequestID {
			taskID = r.req.TaskID
		}
	}
	log(Decision{Kind: KindSeatLaunchFailed, TaskID: taskID, Seat: ev.Handle, Pool: ev.Pool.Key(), Stage: stage, ReasonCode: ev.ReasonCode, Reason: detail})
	if s.failedReqs == nil {
		s.failedReqs = map[string]bool{}
	}
	s.failedReqs[ev.RequestID] = true
	if s.launchFailures == nil {
		s.launchFailures = map[string]*LaunchFailure{}
	}
	key := ev.Pool.Key()
	f := &LaunchFailure{Pool: ev.Pool, Handle: ev.Handle, RequestID: ev.RequestID, Stage: stage, ReasonCode: ev.ReasonCode, Detail: detail,
		At: ev.At, Count: 1, LaunchID: ev.LaunchID}
	prev := s.launchFailures[key]
	if prev != nil && prev.Code() == f.Code() {
		f.Count = prev.Count
		if ev.LaunchID != "" && ev.LaunchID == prev.LaunchID {
			f.Seq = prev.Seq // another seat of the same attempt: not a repeat
			s.launchFailures[key] = f
			return
		}
		f.Count++
	}
	s.failSeq++
	f.Seq = s.failSeq
	s.launchFailures[key] = f
}

// clearLaunchFailures forgets the failures of the pools a seat that came up
// serves.
func (s *Scheduler) clearLaunchFailures(p task.Pool) {
	for k, f := range s.launchFailures {
		if f.Pool == p || (s.rosterMode() && PoolServes(p, f.Pool)) {
			delete(s.launchFailures, k)
		}
	}
}

// failureFor is the last launch failure of a pool a task routes to (in
// roster mode, of a roster pool that serves it).
func (s *Scheduler) failureFor(p task.Pool) *LaunchFailure {
	var out *LaunchFailure
	for _, f := range s.launchFailures {
		if f.Pool != p && !(s.rosterMode() && PoolServes(f.Pool, p)) {
			continue
		}
		if out == nil || f.Seq > out.Seq {
			out = f
		}
	}
	return out
}

// waitForSeat is the wait of a task whose pool's seat is starting: the
// pool's last launch failure reason when there is one (status shows it in
// place of seat_starting), else seat_starting.
func (s *Scheduler) waitForSeat(t task.Task, p task.Pool, now time.Time) error {
	if f := s.failureFor(p); f != nil {
		return s.wait(t, f.Code(), now)
	}
	return s.wait(t, task.WaitSeatStarting, now)
}

// blockOnLaunchFailure blocks a task for the lead when its pool's launch
// failed twice in a row with one reason: the block's reason is that reason
// and its route the lead; no seat is requested again. A task the scheduler
// blocked so that is pending again with no failure since was reset by the
// lead: that is the lead's retry, and the pool's count starts over.
func (s *Scheduler) blockOnLaunchFailure(t task.Task, p task.Pool, now time.Time, log func(Decision)) (*task.Task, error) {
	f := s.failureFor(p)
	if b, ok := s.launchBlocked[t.ID]; ok {
		delete(s.launchBlocked, t.ID)
		if f != nil && f.Seq <= b.seq {
			delete(s.launchFailures, f.Pool.Key())
			log(Decision{Kind: "seat_launch_retry", TaskID: t.ID, Pool: f.Pool.Key(), Reason: "the task blocked on " + f.Code() + " is pending again with no failure since: the lead's retry"})
			return nil, nil
		}
	}
	if f == nil || f.Count < launchFailureRepeat {
		return nil, nil
	}
	next := "reset the task (amq-squad task reset " + t.ID + "), which tries the launch once more"
	if s.rosterMode() {
		next = RelaunchVerb + ", which relaunches the roster in one apply"
	}
	detail := fmt.Sprintf("the launch of seat %s (pool %s) failed %d times in a row with reason %s at stage %s: %s; not retried: the lead's decision (fix the cause, then %s)",
		firstNonEmpty(f.Handle, "-"), f.Pool.Key(), f.Count, f.Code(), f.Stage, firstNonEmpty(f.Detail, "no detail"), next)
	got, err := s.Store.Block(t.ID, "", f.Code(), detail, task.RouteLead, now)
	if err != nil {
		return nil, err
	}
	if s.launchBlocked == nil {
		s.launchBlocked = map[string]launchBlock{}
	}
	s.launchBlocked[t.ID] = launchBlock{pool: f.Pool.Key(), seq: f.Seq}
	log(Decision{Kind: "blocked", TaskID: t.ID, Pool: f.Pool.Key(), Stage: f.Stage, ReasonCode: f.ReasonCode, Reason: f.Code()})
	return &got, nil
}

// settleRosterFailure applies the failed roster launch's events (the
// launcher's seat_launch_failed per request) and notes a failure for every
// request the launcher posted none for (a launch that failed before its
// requests were taken), then writes the state, so status shows the reason
// after the process ends with the failure.
func (s *Scheduler) settleRosterFailure(reqs []SeatRequest, out RosterOutcome, e *RosterLaunchError, now time.Time, log func(Decision)) {
	s.failedReqs = map[string]bool{}
	cfg := s.cfg()
	if err := s.applyEvents(now, cfg, log); err != nil {
		log(Decision{Kind: "tick_error", Reason: err.Error()})
	}
	attempt := out.LaunchID
	if attempt == "" && len(reqs) > 0 {
		attempt = "roster-" + reqs[0].ID
	}
	for _, r := range reqs {
		if !s.failedReqs[r.ID] {
			s.noteLaunchFailure(Event{Kind: SeatLaunchFailed, At: now, Handle: r.Handle, Pool: r.Pool, RequestID: r.ID, LaunchID: attempt,
				Stage: e.Stage, ReasonCode: e.ReasonCode, Detail: e.Detail}, log)
		}
	}
	if err := s.writeState(now, cfg); err != nil {
		log(Decision{Kind: "tick_error", Reason: err.Error()})
	}
}

// launchFailureList is the state file's view, by pool.
func (s *Scheduler) launchFailureList() []LaunchFailure {
	out := make([]LaunchFailure, 0, len(s.launchFailures))
	for _, f := range s.launchFailures {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pool.Key() < out[j].Pool.Key() })
	if len(out) == 0 {
		return nil
	}
	return out
}
