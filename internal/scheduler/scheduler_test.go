package scheduler

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

var (
	t0     = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	sonnet = task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	gpt    = task.Pool{Runtime: "codex", Model: "gpt-5.4", Effort: "medium"}
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type fakeLauncher struct {
	requests  []SeatRequest
	closed    []string
	cancelled []string
	cancelErr error // a cancellation the launcher cannot confirm yet
	closeErr  error // a close the launcher cannot do yet
}

func (f *fakeLauncher) CancelSeatRequest(r SeatRequest) error {
	if f.cancelErr != nil {
		return f.cancelErr
	}
	f.cancelled = append(f.cancelled, r.ID)
	return nil
}

func (f *fakeLauncher) RequestSeat(r SeatRequest) error {
	f.requests = append(f.requests, r)
	return nil
}
func (f *fakeLauncher) CloseSeat(h, _ string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closed = append(f.closed, h)
	return nil
}

// fakeBell records the doorbells. A seat in the middle of a turn may drop
// one (S5 lead loop run 2); the test drops them all.
type fakeBell struct{ rung []string }

func (f *fakeBell) Ring(h string, t task.Task) error { f.rung = append(f.rung, h+":"+t.ID); return nil }

type harness struct {
	t     *testing.T
	clk   *clock
	store task.Store
	s     *Scheduler
	l     *fakeLauncher
	bell  *fakeBell
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, clk: &clock{now: t0}, store: store, l: &fakeLauncher{}, bell: &fakeBell{}}
	h.s = &Scheduler{Store: store, Now: h.clk.Now, Launcher: h.l, Doorbell: h.bell, Config: cfg, Incarnation: "t"}
	return h
}

func (h *harness) add(title string, p *task.Pool) task.Task {
	h.t.Helper()
	got, err := h.store.Add(task.AddInput{Title: title, Class: "script", Pool: p}, h.clk.Now())
	if err != nil {
		h.t.Fatal(err)
	}
	return got
}

func (h *harness) tick() []Decision {
	h.t.Helper()
	d, err := h.s.Tick()
	if err != nil {
		h.t.Fatal(err)
	}
	return d
}

// up is a seat the launcher started on a reservation: the test puts the
// reservation in place (as RequestSeat would have) and the seat joins with
// its id.
func (h *harness) up(handle string, p task.Pool) {
	id := "test-" + handle
	h.s.requests = append(h.s.requests, request{req: SeatRequest{ID: id, Pool: p}, at: h.clk.Now()})
	h.upFor(handle, p, id)
}

// upFor is a seat the launcher started for a request: it carries the id.
func (h *harness) upFor(handle string, p task.Pool, requestID string) {
	h.s.Post(Event{Kind: SeatUp, At: h.clk.Now(), Handle: handle, Pool: p, RequestID: requestID})
}

func (h *harness) get(id string) task.Task {
	h.t.Helper()
	got, err := h.store.Get(id)
	if err != nil {
		h.t.Fatal(err)
	}
	return got
}

func has(ds []Decision, kind, taskID, seat string) bool {
	for _, d := range ds {
		if d.Kind == kind && (taskID == "" || d.TaskID == taskID) && (seat == "" || d.Seat == seat) {
			return true
		}
	}
	return false
}

// Acceptance 1 (S6 N4): a task leased to a seat that never claims it
// returns to the pool at expires_at and is re-dispatched to another seat.
func TestUnclaimedLeaseReturnsToThePoolAndGoesToAnotherSeat(t *testing.T) {
	h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
	h.add("N4", &sonnet)
	h.up("seat-a", sonnet)
	if d := h.tick(); !has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("first dispatch: %+v", d)
	}
	h.up("seat-b", sonnet)
	h.clk.Add(10*time.Minute - time.Second)
	if d := h.tick(); has(d, "lease_expired", "t1", "") {
		t.Fatalf("expired a second early: %+v", d)
	}
	if got := h.get("t1"); got.Lease.Holder != "seat-a" {
		t.Fatalf("lease moved before expiry: %+v", got.Lease)
	}
	h.clk.Add(time.Second) // exactly expires_at
	d := h.tick()
	if !has(d, "lease_expired", "t1", "seat-a") || !has(d, "dispatched", "t1", "seat-b") {
		t.Fatalf("at expires_at want expiry and re-dispatch to seat-b: %+v", d)
	}
	got := h.get("t1")
	if got.Lease.Holder != "seat-b" || got.Attempt != 2 || got.Attempts[0].Outcome != task.OutcomeUnclaimed || got.Attempts[0].Holder != "seat-a" {
		t.Fatalf("after re-dispatch: %+v", got)
	}
	if _, err := h.store.Claim("t1", "seat-a", nil, 0, h.clk.Now()); err == nil {
		t.Fatal("the lapsed seat claimed the re-dispatched task")
	}
}

// Acceptance 2 (S5 lead loop run 2): the doorbell reaches a seat in the
// middle of a turn and is dropped; the lease, not the mail, decides.
func TestDoorbellDroppedMidTurnLosesNothing(t *testing.T) {
	t.Run("the seat claims at its next turn", func(t *testing.T) {
		h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
		h.add("work", &sonnet)
		h.up("seat-a", sonnet)
		h.tick()
		if len(h.bell.rung) != 1 {
			t.Fatalf("doorbell: %v", h.bell.rung)
		}
		h.bell.rung = nil // dropped in the middle of the turn
		// Tool activity during the old turn does not renew an unclaimed lease.
		h.clk.Add(3 * time.Minute)
		h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a"})
		h.tick()
		if got := h.get("t1"); !got.Lease.ExpiresAt.Equal(t0.Add(10 * time.Minute)) {
			t.Fatalf("activity renewed an unclaimed lease: %+v", got.Lease)
		}
		// Next turn: context shows the leased task; the seat claims it.
		snap, _ := task.Snapshot(h.store.Dir)
		held := task.HeldBy("seat-a", snap)
		if held == nil || held.ID != "t1" {
			t.Fatalf("context would not show the lease: %+v", held)
		}
		h.clk.Add(time.Minute)
		if _, err := h.store.Claim("", "seat-a", nil, 10*time.Minute, h.clk.Now()); err != nil {
			t.Fatal(err)
		}
		h.clk.Add(8 * time.Minute) // past the original expiry; the claim moved it
		h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a"})
		if d := h.tick(); has(d, "lease_expired", "t1", "") || !has(d, "lease_renewed", "t1", "seat-a") {
			t.Fatalf("claimed lease: %+v", d)
		}
		harnessPass(t, h.store, "t1", h.clk.Now())
		if _, err := h.store.Done("t1", "seat-a", "ok", "", h.clk.Now()); err != nil {
			t.Fatal(err)
		}
		if got := h.get("t1"); got.State != task.StateCompleted || got.Attempt != 1 {
			t.Fatalf("completed: %+v", got)
		}
	})
	t.Run("the turn outlasts the lease", func(t *testing.T) {
		h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
		h.add("work", &sonnet)
		h.up("seat-a", sonnet)
		h.tick()
		h.up("seat-b", sonnet)
		h.clk.Add(12 * time.Minute)
		if d := h.tick(); !has(d, "dispatched", "t1", "seat-b") {
			t.Fatalf("re-dispatch: %+v", d)
		}
		if _, err := h.store.Claim("", "seat-a", nil, 0, h.clk.Now()); err == nil {
			t.Fatal("the late seat took a second lease on the task")
		}
		if got := h.get("t1"); got.State != task.StateInProgress || got.Lease.Holder != "seat-b" {
			t.Fatalf("one holder: %+v", got)
		}
	})
}

// Acceptance 4: the seat cap and the usage-limit stop.
func TestSeatCapAndUsageLimitStop(t *testing.T) {
	t.Run("max_seats 3", func(t *testing.T) {
		h := newHarness(t, Config{MaxSeats: 3})
		for i := 1; i <= 4; i++ {
			h.add(fmt.Sprintf("t%d", i), &sonnet)
		}
		h.tick()
		if len(h.l.requests) != 3 {
			t.Fatalf("seat requests: %+v", h.l.requests)
		}
		if got := h.get("t4"); got.WaitReason != task.WaitSeatCap {
			t.Fatalf("the fourth request should wait seat_cap: %+v", got)
		}
		for i := 1; i <= 3; i++ {
			if got := h.get(fmt.Sprintf("t%d", i)); got.WaitReason != task.WaitSeatStarting {
				t.Fatalf("t%d: %+v", i, got)
			}
		}
		h.tick() // no duplicate requests while the seats start
		if len(h.l.requests) != 3 {
			t.Fatalf("duplicate seat requests: %+v", h.l.requests)
		}
	})
	t.Run("one usage-limit event stops new seats", func(t *testing.T) {
		h := newHarness(t, Config{MaxSeats: 12})
		var events []UsageLimitEvent
		h.s.Limits = UsageLimitFunc(func(since time.Time) ([]UsageLimitEvent, error) {
			var out []UsageLimitEvent
			for _, e := range events {
				if !e.At.Before(since) {
					out = append(out, e)
				}
			}
			return out, nil
		})
		h.add("running", &sonnet)
		h.add("idle seat work", &sonnet)
		h.up("seat-a", sonnet)
		h.up("seat-b", sonnet)
		h.tick()
		if _, err := h.store.Claim("t1", "seat-a", nil, 10*time.Minute, h.clk.Now()); err != nil {
			t.Fatal(err)
		}
		harnessPass(t, h.store, "t2", h.clk.Now())
		if _, err := h.store.Done("t2", "seat-b", "", "", h.clk.Now()); err != nil {
			t.Fatal(err)
		}
		h.clk.Add(time.Minute)
		events = append(events, UsageLimitEvent{At: h.clk.Now(), Provider: "claude", Seat: "seat-a", ResetAt: h.clk.Now().Add(4 * time.Hour), Source: "fixture"})
		h.add("needs a new seat", &gpt)
		h.add("fits the idle seat", &sonnet)
		d := h.tick()
		if !has(d, "usage_limit", "", "seat-a") {
			t.Fatalf("usage event not applied: %+v", d)
		}
		if len(h.l.requests) != 0 {
			t.Fatalf("a seat was requested after the usage-limit event: %+v", h.l.requests)
		}
		if got := h.get("t3"); got.WaitReason != task.WaitUsageLimit {
			t.Fatalf("t3: %+v", got)
		}
		if !has(d, "dispatched", "t4", "seat-b") {
			t.Fatalf("running seats keep working after the stop: %+v", d)
		}
		if got := h.get("t1"); got.Lease == nil || got.Lease.Holder != "seat-a" {
			t.Fatalf("a running seat lost its lease: %+v", got)
		}
		h.clk.Add(4 * time.Hour)
		h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a"})
		h.tick()
		// (seat-a's claimed lease expired in the four hours, so the seat is
		// closed through the launcher (R3 of the 2b-2 B first pass) and t1 and
		// t4 ask for seats too; the order of the requests is the dispatch
		// order.)
		requested := false
		for _, r := range h.l.requests {
			requested = requested || (r.Pool == gpt && r.TaskID == "t3")
		}
		if !requested {
			t.Fatalf("after the window reset a seat should be requested for t3: %+v", h.l.requests)
		}
	})
}

// Acceptance 5: an idle seat closes after the configured timeout (R18).
func TestIdleSeatClosesAfterTimeout(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: 10 * time.Minute, LeaseDuration: time.Hour})
	h.add("one", &sonnet)
	h.up("seat-a", sonnet)
	h.up("seat-b", sonnet)
	h.tick()
	h.clk.Add(9 * time.Minute)
	if d := h.tick(); has(d, "seat_closed", "", "") {
		t.Fatalf("closed early: %+v", d)
	}
	h.clk.Add(time.Minute)
	d := h.tick()
	if !has(d, "seat_closed", "", "seat-b") || has(d, "seat_closed", "", "seat-a") {
		t.Fatalf("want seat-b closed and seat-a (holding t1) kept: %+v", d)
	}
	if !reflect.DeepEqual(h.l.closed, []string{"seat-b"}) {
		t.Fatalf("launcher closes: %v", h.l.closed)
	}
}

type refusing struct{ calls int }

func (r *refusing) Commit(_ string, t task.Task) CommitResult {
	r.calls++
	if r.calls == 1 {
		return CommitResult{ReasonCode: "outside_declared_files", Detail: "README.md"}
	}
	return CommitResult{OK: true}
}

func TestCommitStepAndRetryHook(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	c := &refusing{}
	h.s.Committer = c
	if _, err := h.store.Add(task.AddInput{Title: "edit", Class: "implement", Pool: &sonnet, DeclaredFiles: []string{"src/"}}, t0); err != nil {
		t.Fatal(err)
	}
	h.up("seat-a", sonnet)
	h.tick()
	if _, err := h.store.Claim("t1", "seat-a", nil, 0, t0); err != nil {
		t.Fatal(err)
	}
	harnessPass(t, h.store, "t1", t0)
	if _, err := h.store.Done("t1", "seat-a", "ok", "", t0); err != nil {
		t.Fatal(err)
	}
	if d := h.tick(); !has(d, "commit_refused", "t1", "seat-a") {
		t.Fatalf("refusal: %+v", d)
	}
	if got := h.get("t1"); got.State != task.StateBlocked || got.BlockReason != "outside_declared_files" {
		t.Fatalf("blocked: %+v", got)
	}
	if _, err := h.store.Widen("t1", "lead", "README belongs to it", []string{"README.md"}, 0, t0); err != nil {
		t.Fatal(err)
	}
	harnessPass(t, h.store, "t1", t0) // the re-armed attempt's own verdict
	if d := h.tick(); !has(d, "completed", "t1", "seat-a") {
		t.Fatalf("re-armed commit: %+v", d)
	}

	// Retry rule through the router hook: three failed attempts block.
	h.add("flaky", &sonnet)
	for i := 0; i < 3; i++ {
		h.tick()
		got := h.get("t2")
		if got.Lease == nil {
			t.Fatalf("attempt %d not dispatched: %+v", i+1, got)
		}
		if _, err := h.store.Claim("t2", got.Lease.Holder, nil, 0, h.clk.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := h.store.Fail("t2", got.Lease.Holder, "tests red", h.clk.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if d := h.tick(); !has(d, "blocked", "t2", "") {
		t.Fatalf("retries exhausted: %+v", d)
	}
	if got := h.get("t2"); got.BlockReason != "retries_exhausted" || got.BlockRoute != task.RouteLead {
		t.Fatalf("t2: %+v", got)
	}
}

func TestNoRouteAndFileLockWaits(t *testing.T) {
	h := newHarness(t, Config{})
	h.add("unrouted", nil)
	if _, err := h.store.Add(task.AddInput{Title: "a", Class: "implement", Pool: &sonnet, DeclaredFiles: []string{"x/"}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Add(task.AddInput{Title: "b", Class: "implement", Pool: &sonnet, DeclaredFiles: []string{"x/y.go"}}, t0); err != nil {
		t.Fatal(err)
	}
	h.up("s1", sonnet)
	h.up("s2", sonnet)
	h.tick()
	if got := h.get("t1"); got.WaitReason != task.WaitNoRoute {
		t.Fatalf("t1: %+v", got)
	}
	if got := h.get("t3"); got.WaitReason != task.WaitFileLock || got.State != task.StatePending {
		t.Fatalf("t3 must wait for t2's file lock: %+v", got)
	}
}

func TestBoundDependencyGetsPlainMergeWait(t *testing.T) {
	h := newHarness(t, Config{})
	dep, err := h.store.Add(task.AddInput{Title: "dependency", Class: "implement", Pool: &sonnet}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.BindPR(dep.ID, "o/r", 31, strings.Repeat("a", 40), t0); err != nil {
		t.Fatal(err)
	}
	child, err := h.store.Add(task.AddInput{Title: "child", Class: "implement", Pool: &sonnet, DependsOn: []string{dep.ID}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	h.tick()
	if got, want := h.get(child.ID).WaitReason, "waiting for t1's PR #31 to merge"; got != want {
		t.Fatalf("scheduler wait reason = %q, want %q", got, want)
	}
}

// Acceptance 3: the same store and the same event sequence give the same
// decisions, 100 runs, with the events of each step posted from goroutines
// in shuffled order and timing (run under -race).
//
// The default is 10 runs so `go test ./...` stays fast (each run costs about
// 0.6 s of fsyncs); AMQ_SQUAD_DETERMINISM_RUNS=100 runs the acceptance count.
func TestDeterministicDecisions(t *testing.T) {
	runs := 10
	if v, err := strconv.Atoi(os.Getenv("AMQ_SQUAD_DETERMINISM_RUNS")); err == nil && v > 0 {
		runs = v
	}
	var first []Decision
	for run := 0; run < runs; run++ {
		got := scenario(t, int64(run))
		if run == 0 {
			first = got
			if len(first) < 15 {
				t.Fatalf("scenario too small to prove much: %+v", first)
			}
			ups := 0
			for _, d := range first {
				if d.Kind == "seat_up" {
					ups++
				}
			}
			if ups != 4 {
				t.Fatalf("the four requested seats must join on their ids: %+v", first)
			}
			continue
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d diverged:\nfirst %+v\ngot   %+v", run, first, got)
		}
	}
}

func scenario(t *testing.T, seed int64) []Decision {
	h := newHarness(t, Config{MaxSeats: 4, LeaseDuration: 10 * time.Minute, IdleTimeout: 15 * time.Minute})
	rng := rand.New(rand.NewSource(seed))
	for i := 0; i < 6; i++ {
		p := &sonnet
		if i%3 == 2 {
			p = &gpt
		}
		h.add(fmt.Sprintf("w%d", i), p)
	}
	post := func(evs ...Event) {
		var wg sync.WaitGroup
		for _, i := range rng.Perm(len(evs)) {
			ev, delay := evs[i], time.Duration(rng.Intn(200))*time.Microsecond
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(delay)
				runtime.Gosched()
				h.s.Post(ev)
			}()
		}
		wg.Wait()
	}
	var log []Decision
	step := func() { log = append(log, h.tick()...) }
	step() // requests seats
	at := h.clk.Now()
	// Each launched seat carries its request id back (seat-req-t-1..4 for t1..t4).
	post(Event{Kind: SeatUp, At: at, Handle: "s-a", Pool: sonnet, RequestID: "seat-req-t-1"}, Event{Kind: SeatUp, At: at, Handle: "s-b", Pool: sonnet, RequestID: "seat-req-t-2"}, Event{Kind: SeatUp, At: at, Handle: "s-c", Pool: gpt, RequestID: "seat-req-t-3"}, Event{Kind: SeatUp, At: at, Handle: "s-d", Pool: sonnet, RequestID: "seat-req-t-4"})
	step()
	h.clk.Add(time.Minute)
	for _, id := range []string{"t1", "t2"} {
		if got := h.get(id); got.Lease != nil {
			if _, err := h.store.Claim(id, got.Lease.Holder, nil, 10*time.Minute, h.clk.Now()); err != nil {
				t.Fatal(err)
			}
		}
	}
	at = h.clk.Now()
	post(Event{Kind: SeatActivity, At: at, Handle: "s-a"}, Event{Kind: SeatActivity, At: at, Handle: "s-b"}, Event{Kind: SeatActivity, At: at, Handle: "s-c"})
	step()
	h.clk.Add(10 * time.Minute) // unclaimed leases lapse
	post(Event{Kind: SeatGone, At: h.clk.Now(), Handle: "s-d"}, Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "s-a"})
	step()
	h.clk.Add(20 * time.Minute)
	step()
	// Times differ between runs only through the fake clock, which is fixed.
	return log
}

func TestStateFileForStatus(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 5})
	h.s.Config.StatePath = StatePathFor(t.TempDir())
	h.s.Limits = UsageLimitFunc(func(time.Time) ([]UsageLimitEvent, error) {
		return []UsageLimitEvent{{At: t0, Provider: "claude"}}, nil
	})
	h.up("s1", sonnet)
	h.tick()
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || st.MaxSeats != 5 || st.LiveSeats != 1 || st.UsageLimitSince == nil || st.UsageLimitUntil != nil || len(st.Seats) != 1 {
		t.Fatalf("state: %+v %v", st, err)
	}
	if !h.s.UsageLimited(t0.Add(48 * time.Hour)) {
		t.Fatal("an event without a reset time must stop new seats until restart")
	}
	if _, err := ReadState("/nonexistent/scheduler.json"); err != nil {
		t.Fatal(err)
	}
}

func TestSourceErrorsSurface(t *testing.T) {
	h := newHarness(t, Config{})
	h.s.Limits = UsageLimitFunc(func(time.Time) ([]UsageLimitEvent, error) { return nil, errors.New("boom") })
	if _, err := h.s.Tick(); err == nil {
		t.Fatal("a failing usage-limit source was ignored")
	}
}

// unpreparedBell is a doorbell whose seat could not be put on the task's
// branch (3.0.2 t23): it rings nothing and says so.
type unpreparedBell struct{ asked []string }

func (b *unpreparedBell) Ring(h string, t task.Task) error {
	b.asked = append(b.asked, h+":"+t.ID)
	return &LeaseUnpreparedError{Err: errors.New("the base fetch failed")}
}

// 3.0.2 t23, the lead's ruling: when the doorbell cannot put the seat on the
// task's branch, the seat is not rung, the lease is released and the task
// blocks for the lead as task_branch_unprepared with the error; a seat never
// works on the resting branch. Any other doorbell error stays a logged hint.
// Fails when the scheduler only logs the unprepared ring.
func TestUnpreparedLeaseBlocksTheTaskForTheLead(t *testing.T) {
	h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
	bell := &unpreparedBell{}
	h.s.Doorbell = bell
	h.add("work", &sonnet)
	h.up("seat-a", sonnet)
	d := h.tick()
	got := h.get("t1")
	if !has(d, "blocked", "t1", "seat-a") || has(d, "doorbell_failed", "t1", "") || len(bell.asked) != 1 {
		t.Fatalf("decisions %+v, asked %v", d, bell.asked)
	}
	if got.State != task.StateBlocked || got.BlockReason != ReasonTaskBranchUnprepared || got.BlockRoute != task.RouteLead ||
		!strings.Contains(got.BlockDetail, "the base fetch failed") || got.Lease != nil || len(got.Attempts) != 1 || got.Attempts[0].Holder != "seat-a" {
		t.Fatalf("the task after an unprepared ring: %+v", got)
	}
	if snap, _ := task.Snapshot(h.store.Dir); task.HeldBy("seat-a", snap) != nil {
		t.Fatal("the seat still holds a lease")
	}
}
