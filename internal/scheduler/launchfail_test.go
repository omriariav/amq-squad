package scheduler

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The canary is no real credential shape (briefs ruling 18): a named key
// with a lowercase test value, one on the line after its key.
const t17Canary = "OPENAI_API_KEY=t17canaryvalue0051\napi_key:\n  \"t17canaryvalue0052\""

func failed(req SeatRequest, handle, launchID, stage, code, detail string, at time.Time) Event {
	return Event{Kind: SeatLaunchFailed, At: at, Handle: handle, Pool: req.Pool, RequestID: req.ID, LaunchID: launchID, Stage: stage, ReasonCode: code, Detail: detail}
}

func decisionsOf(ds []Decision, kind string) []Decision {
	var out []Decision
	for _, d := range ds {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out
}

// t17 rows 1 (scheduler side): a failed launch is a seat_launch_failed
// decision with the stage, AMQ's reason code and the redacted detail; the
// waiting task's reason is the failure's in place of seat_starting (what
// status shows), and the state file carries it; a second failure of the
// pool with the same reason blocks the task for the lead with that reason,
// and no seat is requested again until the lead resets the task. Reverted
// (the failure not noted, the wait reason seat_starting, no block), the
// thirteen silent cancels of 2026-10-30 come back: seat_requested and
// seat_request_cancelled each start timeout, no reason anywhere.
func TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask(t *testing.T) {
	statePath := t.TempDir() + "/scheduler.json"
	h := newHarness(t, Config{MaxSeats: 2, SeatStartTimeout: time.Minute, StatePath: statePath})
	a := h.add("a", &sonnet)
	if ds := h.tick(); !has(ds, "seat_requested", a.ID, "") || len(h.l.requests) != 1 {
		t.Fatalf("first request: %+v", ds)
	}
	h.s.Post(failed(h.l.requests[0], "s-a", "L1", "apply_failed", AMQBindingPresent, "launch: the session binding is present\n"+t17Canary, h.clk.Now()))
	ds := h.tick()
	got := decisionsOf(ds, KindSeatLaunchFailed)
	if len(got) != 1 || got[0].TaskID != a.ID || got[0].Seat != "s-a" || got[0].Pool != sonnet.Key() || got[0].Stage != "apply_failed" ||
		got[0].ReasonCode != AMQBindingPresent || !strings.Contains(got[0].Reason, "the session binding is present") {
		t.Fatalf("the failure decision: %+v", ds)
	}
	if strings.Contains(got[0].Reason, "t17canaryvalue0051") || strings.Contains(got[0].Reason, "t17canaryvalue0052") {
		t.Fatalf("the failure decision carries the credential value: %s", got[0].Reason)
	}
	if w := h.get(a.ID).WaitReason; w != AMQBindingPresent {
		t.Fatalf("the waiting task's reason is %q, want the failure's %q in place of seat_starting", w, AMQBindingPresent)
	}
	st, err := ReadState(statePath)
	if err != nil || len(st.LaunchFailures) != 1 || st.LaunchFailures[0].Code() != AMQBindingPresent || st.LaunchFailures[0].Count != 1 ||
		st.LaunchFailures[0].Handle != "s-a" || strings.Contains(st.LaunchFailures[0].Detail, "t17canaryvalue") {
		t.Fatalf("state %+v %v", st.LaunchFailures, err)
	}
	// The start timeout: the reservation is cancelled and the seat requested
	// once more (the first failure is logged, so the retry is not silent).
	h.clk.Add(2 * time.Minute)
	if ds := h.tick(); !has(ds, "seat_request_cancelled", "", "") || count(ds, "seat_requested") != 1 || len(h.l.requests) != 2 {
		t.Fatalf("the one retry: %+v", ds)
	}
	h.s.Post(failed(h.l.requests[1], "s-a", "L2", "apply_failed", AMQBindingPresent, "launch: the session binding is present", h.clk.Now()))
	ds = h.tick()
	if !has(ds, KindSeatLaunchFailed, a.ID, "s-a") || !has(ds, "blocked", a.ID, "") {
		t.Fatalf("the second failure: %+v", ds)
	}
	b := h.get(a.ID)
	if b.State != task.StateBlocked || b.BlockReason != AMQBindingPresent || b.BlockRoute != task.RouteLead ||
		!strings.Contains(b.BlockDetail, "failed 2 times in a row") || !strings.Contains(b.BlockDetail, "amq-squad task reset "+a.ID) {
		t.Fatalf("the blocked task: %+v", b)
	}
	// Never a silent retry: no seat is requested while the task is blocked.
	var later []Decision
	for i := 0; i < 3; i++ {
		h.clk.Add(2 * time.Minute)
		later = append(later, h.tick()...)
	}
	if count(later, "seat_requested") != 0 || len(h.l.requests) != 2 {
		t.Fatalf("a retry after the block: %+v", later)
	}
	// The lead's reset is the lead's retry: the seat is requested once more.
	if _, err := h.store.Reset(a.ID, nil, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if ds := h.tick(); !has(ds, "seat_launch_retry", a.ID, "") || count(ds, "seat_requested") != 1 {
		t.Fatalf("after the lead's reset: %+v", ds)
	}
}

// t17: the repeat is counted per reason and per launch attempt: a second
// failure with another reason starts the count over (the task waits with
// the new reason), two seats of one pool failing in one launch are one
// failure, and a seat of the pool that comes up clears it.
func TestLaunchFailureRepeatIsPerReasonAndPerAttempt(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 4, SeatStartTimeout: time.Minute})
	a := h.add("a", &sonnet)
	h.tick()
	h.s.Post(failed(h.l.requests[0], "s-a", "L1", "prepare_refused", "runtime_unavailable", "x", h.clk.Now()))
	h.tick()
	h.clk.Add(2 * time.Minute)
	h.tick()
	h.s.Post(failed(h.l.requests[1], "s-a", "L2", "apply_failed", AMQBindingPresent, "y", h.clk.Now()))
	if ds := h.tick(); has(ds, "blocked", a.ID, "") || h.get(a.ID).WaitReason != AMQBindingPresent {
		t.Fatalf("another reason blocked the task: %+v; %+v", ds, h.get(a.ID))
	}
	// Two seats of the pool in one attempt (L3), the same reason: one count.
	r := h.l.requests[1]
	h.s.Post(failed(r, "s-a", "L3", "seat_not_up", "no_proof", "", h.clk.Now()))
	h.s.Post(failed(r, "s-b", "L3", "seat_not_up", "no_proof", "", h.clk.Now()))
	h.tick()
	if f := h.s.failureFor(sonnet); f == nil || f.Count != 1 || f.Code() != "no_proof" {
		t.Fatalf("one attempt counted %+v", f)
	}
	h.upFor("s-a", sonnet, r.ID)
	if ds := h.tick(); !has(ds, "dispatched", a.ID, "s-a") || h.s.failureFor(sonnet) != nil {
		t.Fatalf("a seat of the pool came up: %+v; failure %+v", ds, h.s.failureFor(sonnet))
	}
}

// failingRoster is fakeRoster whose seats that do not come up post their
// failure, as the real launcher does; tag sets apart the launch ids of
// another incarnation's fake.
type failingRoster struct {
	*fakeRoster
	tag string
}

func (f failingRoster) LaunchRoster(reqs []SeatRequest) RosterOutcome {
	out := f.fakeRoster.LaunchRoster(reqs)
	out.LaunchID = f.tag + out.LaunchID
	for _, r := range reqs {
		if f.notUp[r.Handle] && out.Stage == RosterApplied {
			f.s.Post(failed(r, r.Handle, out.LaunchID, "seat_not_up", "no_proof", "no accepted proof within 90s", f.s.Now()))
		}
	}
	return out
}

// t17 rows 1 (roster): a roster seat that fails at the start launch and
// again at the lead's relaunch, with the same reason, blocks its pool's task
// for the lead with that reason (the block names the relaunch verb); the
// first failure's reason is the task's wait reason and is named in the
// relaunch block. Reverted (no block on the repeat), the task goes back to
// waiting on the reservation and then to the relaunch block, a loop with
// no reason.
func TestRosterSeatFailingTwiceBlocksForTheLead(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, SeatStartTimeout: time.Minute, IdleTimeout: time.Hour}, rosterAB...)
	fr.notUp = map[string]bool{"s-b": true}
	h.s.Launcher = failingRoster{fakeRoster: fr}
	b := h.add("b", &gpt)
	ds := h.tick()
	if !has(ds, "seat_not_up", "", "s-b") || !has(ds, KindSeatLaunchFailed, "", "s-b") || h.get(b.ID).WaitReason != "no_proof" {
		t.Fatalf("the start launch: %+v; b %+v", ds, h.get(b.ID))
	}
	h.clk.Add(2 * time.Minute)
	ds = h.tick()
	if !has(ds, ReasonSeatRelaunch, b.ID, "s-b") || !strings.Contains(h.get(b.ID).BlockDetail, "last launch failed at stage seat_not_up with reason no_proof") {
		t.Fatalf("the relaunch block: %+v; b %+v", ds, h.get(b.ID))
	}
	h.s.RequestRelaunch("s-b never came up")
	ds = h.tick()
	if len(fr.launches) != 2 || !has(ds, KindSeatLaunchFailed, "", "s-b") || !has(ds, "task_reset", b.ID, "") || !has(ds, "blocked", b.ID, "") {
		t.Fatalf("the relaunch: %+v; launches %d", ds, len(fr.launches))
	}
	got := h.get(b.ID)
	if got.State != task.StateBlocked || got.BlockReason != "no_proof" || got.BlockRoute != task.RouteLead || !strings.Contains(got.BlockDetail, RelaunchVerb) {
		t.Fatalf("the task: %+v", got)
	}
	// The lead fixes the cause and relaunches: s-b comes up, the failure
	// clears, and the task blocked on it returns to pending and is
	// dispatched.
	fr.notUp = nil
	h.s.RequestRelaunch("the cause is fixed")
	ds = h.tick()
	if len(fr.launches) != 3 || !has(ds, "task_reset", b.ID, "") || !has(ds, "dispatched", b.ID, "s-b") || h.s.failureFor(gpt) != nil {
		t.Fatalf("the second relaunch: %+v; launches %d", ds, len(fr.launches))
	}
}

// t17: a roster launch that fails as a whole logs each seat's failure (the
// outcome's stage, reason and detail when the launcher posted none) and
// writes the state before the process ends with it, so status shows why.
// Reverted, the state file was never written and no seat_launch_failed
// named the seats.
func TestFailedRosterLaunchNamesEachSeatAndWritesTheState(t *testing.T) {
	statePath := t.TempDir() + "/scheduler.json"
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, StatePath: statePath}, rosterAB...)
	fr.fail = RosterOutcome{Stage: "prepare_refused", ReasonCode: "runtime_unavailable", Detail: "codex is not installed\n" + t17Canary}
	ds, err := h.s.Tick()
	var e *RosterLaunchError
	if !errors.As(err, &e) {
		t.Fatalf("tick error %v", err)
	}
	got := decisionsOf(ds, KindSeatLaunchFailed)
	if len(got) != 2 || got[0].Seat != "s-a" || got[1].Seat != "s-b" || got[1].Stage != "prepare_refused" || got[1].ReasonCode != "runtime_unavailable" ||
		!strings.Contains(got[1].Reason, "codex is not installed") || strings.Contains(got[1].Reason, "t17canaryvalue") {
		t.Fatalf("decisions %+v", ds)
	}
	st, err := ReadState(statePath)
	if err != nil || len(st.LaunchFailures) != 2 || st.LaunchFailures[0].Code() != "runtime_unavailable" {
		t.Fatalf("state %+v %v", st, err)
	}
}

// fakeLiveness answers the per-tick check from a table.
type fakeLiveness struct {
	gone  map[string]bool
	errs  map[string]error
	asked []string
}

func (f *fakeLiveness) SeatProcess(handle, requestID string) (bool, string, error) {
	f.asked = append(f.asked, handle+"@"+requestID)
	if err := f.errs[handle]; err != nil {
		return false, "", err
	}
	if f.gone[handle] {
		return true, "pid 4242 is gone", nil
	}
	return false, "", nil
}

// t17 row 2 (scheduler side): each tick asks the liveness hook about every
// live roster seat by the request it joined on; a seat whose process is
// gone is posted SeatGone (process_exited) and applied in the same tick, so
// its pool's task takes the relaunch path at once; a live seat is
// untouched; an unknown answer posts nothing and is logged once. Reverted
// (no check), the dead parked seat stays "idle" and its pool's task is
// never dispatched or blocked.
func TestGoneRosterSeatProcessIsPostedGoneWithinOneTick(t *testing.T) {
	h, _ := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour}, rosterAB...)
	lv := &fakeLiveness{gone: map[string]bool{}, errs: map[string]error{}}
	h.s.Liveness = lv
	h.tick() // the roster launch
	lv.asked = nil
	h.tick()
	if strings.Join(lv.asked, ",") != "s-a@seat-req-t-1,s-b@seat-req-t-2" {
		t.Fatalf("asked %v", lv.asked)
	}
	lv.gone["s-a"] = true
	lv.errs["s-b"] = errors.New("the start stamp cannot be read")
	a := h.add("a", &sonnet)
	ds := h.tick()
	gone := decisionsOf(ds, "seat_gone")
	if len(gone) != 1 || gone[0].Seat != "s-a" || gone[0].ReasonCode != GoneProcessExited || !strings.Contains(gone[0].Reason, "pid 4242") {
		t.Fatalf("decisions %+v", ds)
	}
	if !has(ds, ReasonSeatRelaunch, a.ID, "s-a") || h.s.seats["s-b"] == nil || !has(ds, "seat_liveness_unknown", "", "s-b") {
		t.Fatalf("decisions %+v", ds)
	}
	if ds := h.tick(); has(ds, "seat_liveness_unknown", "", "s-b") || has(ds, "seat_gone", "", "s-b") || h.s.seats["s-b"] == nil {
		t.Fatalf("the unknown seat on the next tick: %+v", ds)
	}
}

// restarted is a second incarnation over the same task store and state
// file, its launch failures restored as schedproc.Run restores them.
func restarted(t *testing.T, h *harness, tag string, notUp map[string]bool) (*Scheduler, *fakeRoster) {
	t.Helper()
	s := &Scheduler{Store: h.store, Now: h.clk.Now, Doorbell: h.bell, Config: h.s.Config, Incarnation: tag}
	fr := &fakeRoster{fakeLauncher: &fakeLauncher{}, s: s, notUp: notUp}
	s.Launcher = failingRoster{fakeRoster: fr, tag: tag}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	s.RestoreLaunchFailures(st)
	return s, fr
}

// Codex finding 2 on t17: the launch failures and the blocks on them live
// in the state file and are restored at a restart. A task blocked on a
// repeated failure is recovered by a successful launch after a restart;
// a first failure before a restart and a second after it is a repeat and
// blocks. Reverted (memory only), the block is not recognised after the
// restart and the task stays blocked, and the count starts over at one.
func TestLaunchFailuresAndBlocksSurviveARestart(t *testing.T) {
	statePath := t.TempDir() + "/scheduler.json"
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, SeatStartTimeout: time.Minute, IdleTimeout: time.Hour, StatePath: statePath}, rosterAB...)
	fr.notUp = map[string]bool{"s-b": true}
	h.s.Launcher = failingRoster{fakeRoster: fr, tag: "a"}
	b := h.add("b", &gpt)
	h.tick()
	h.clk.Add(2 * time.Minute)
	h.tick()
	h.s.RequestRelaunch("s-b never came up")
	h.tick()
	if got := h.get(b.ID); got.State != task.StateBlocked || got.BlockReason != "no_proof" {
		t.Fatalf("before the restart: %+v", got)
	}
	st, _ := ReadState(statePath)
	if len(st.LaunchBlocked) != 1 || st.LaunchBlocked[0].TaskID != b.ID || len(st.LaunchFailures) != 1 || st.LaunchFailures[0].Count != 2 || st.LaunchFailures[0].Seq == 0 {
		t.Fatalf("the state file: %+v", st)
	}
	// The restart: the roster launches at start and every seat comes up.
	s2, _ := restarted(t, h, "b", nil)
	ds, err := s2.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !has(ds, "task_reset", b.ID, "") || !has(ds, "dispatched", b.ID, "s-b") {
		t.Fatalf("after the restart: %+v; b %+v", ds, h.get(b.ID))
	}

	// A first failure, a restart, the same failure again: a repeat.
	statePath2 := t.TempDir() + "/scheduler.json"
	g, gr := newRosterHarness(t, Config{MaxSeats: 2, SeatStartTimeout: time.Minute, IdleTimeout: time.Hour, StatePath: statePath2}, rosterAB...)
	gr.notUp = map[string]bool{"s-b": true}
	g.s.Launcher = failingRoster{fakeRoster: gr, tag: "a"}
	c := g.add("c", &gpt)
	g.tick()
	if got := g.get(c.ID); got.State != task.StatePending || got.WaitReason != "no_proof" {
		t.Fatalf("after the first failure: %+v", got)
	}
	s3, _ := restarted(t, g, "c", map[string]bool{"s-b": true})
	if _, err := s3.Tick(); err != nil {
		t.Fatal(err)
	}
	if got := g.get(c.ID); got.State != task.StateBlocked || got.BlockReason != "no_proof" || !strings.Contains(got.BlockDetail, "failed 2 times") {
		t.Fatalf("the repeat across the restart: %+v", got)
	}

	// Blocked, a restart, and the seat fails again at the start launch: the
	// new failure is later than the block (the sequence is restored), so the
	// reset task blocks again, never taken for the lead's retry.
	statePath3 := t.TempDir() + "/scheduler.json"
	k, kr := newRosterHarness(t, Config{MaxSeats: 2, SeatStartTimeout: time.Minute, IdleTimeout: time.Hour, StatePath: statePath3}, rosterAB...)
	kr.notUp = map[string]bool{"s-b": true}
	k.s.Launcher = failingRoster{fakeRoster: kr, tag: "a"}
	d := k.add("d", &gpt)
	k.tick()
	k.clk.Add(2 * time.Minute)
	k.tick()
	k.s.RequestRelaunch("s-b never came up")
	k.tick()
	s4, _ := restarted(t, k, "d", map[string]bool{"s-b": true})
	ds, err = s4.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if has(ds, "seat_launch_retry", d.ID, "") || !has(ds, "task_reset", d.ID, "") || !has(ds, "blocked", d.ID, "") {
		t.Fatalf("a failure after the restart: %+v", ds)
	}
	if got := k.get(d.ID); got.State != task.StateBlocked || !strings.Contains(got.BlockDetail, "failed 3 times") {
		t.Fatalf("the task: %+v", got)
	}
}

// observingRoster is fakeRoster that counts the slot releases the
// scheduler asks for (SeatGoneObserver).
type observingRoster struct {
	*fakeRoster
	released []string
}

func (o *observingRoster) SeatGone(h string) { o.released = append(o.released, h) }

// t17, the reviewer's ruling on the double fire: the exit queue checks one
// gone attempt once. A liveness SeatGone, then a launcher's confirmation for
// the same handle and attempt (its request id): one seat_gone, one exit
// check, one slot release, the held task as it was; the second is dropped
// and logged (exit_check_duplicate). The same handle with a new attempt
// (a new seat of it, gone in turn) queues again. Reverted (queueExit takes
// every item), the confirmation runs a second check and a second release.
func TestASecondSeatGoneForOneAttemptIsCheckedOnce(t *testing.T) {
	h, fr := newRosterHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour}, rosterAB...)
	obs := &observingRoster{fakeRoster: fr}
	h.s.Launcher = obs
	ex := &exitRecorder{}
	h.s.Exits = ex
	lv := &fakeLiveness{gone: map[string]bool{}, errs: map[string]error{}}
	h.s.Liveness = lv
	h.tick() // the roster launch
	b := h.add("b", &gpt)
	if ds := h.tick(); !has(ds, "dispatched", b.ID, "s-b") {
		t.Fatalf("dispatch: %+v", ds)
	}
	if _, err := h.store.Claim(b.ID, "s-b", nil, time.Hour, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	lv.gone["s-b"] = true
	ds := h.tick()
	if count(ds, "seat_gone") != 1 || len(ex.calls) != 1 || len(obs.released) != 1 {
		t.Fatalf("the liveness gone: %+v; exit checks %v; released %v", ds, ex.calls, obs.released)
	}
	before := h.get(b.ID)
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "s-b", RequestID: "seat-req-t-2"}) // the launcher's confirmation
	ds = h.tick()
	if count(ds, "exit_check_duplicate") != 1 || len(ex.calls) != 1 || len(obs.released) != 1 {
		t.Fatalf("the confirmation: %+v; exit checks %v; released %v", ds, ex.calls, obs.released)
	}
	after := h.get(b.ID)
	if after.State != before.State || len(after.Attempts) != len(before.Attempts) || (after.Lease == nil) != (before.Lease == nil) || after.UpdatedAt != before.UpdatedAt {
		t.Fatalf("the task changed: before %+v after %+v", before, after)
	}
	// A new attempt of the handle: a new seat comes up and is gone in turn.
	lv.gone["s-b"] = false
	h.up("s-b", gpt)
	h.tick()
	lv.gone["s-b"] = true
	ds = h.tick()
	if count(ds, "seat_gone") != 1 || count(ds, "exit_check_duplicate") != 0 || len(ex.calls) != 2 || len(obs.released) != 2 {
		t.Fatalf("a new attempt: %+v; exit checks %v; released %v", ds, ex.calls, obs.released)
	}
	// A gone that names no request, of a live seat, is for the seat's own
	// attempt: the confirmation naming that request is the duplicate.
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "s-a"})
	h.tick()
	h.s.Post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "s-a", RequestID: "seat-req-t-1"})
	ds = h.tick()
	if count(ds, "exit_check_duplicate") != 1 || len(ex.calls) != 3 || len(obs.released) != 3 {
		t.Fatalf("a gone with no request: %+v; exit checks %v; released %v", ds, ex.calls, obs.released)
	}
}
