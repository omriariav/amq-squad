package scheduler

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

var opusHigh = task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}

// Brief 3.1.1-2 item 1, row 4 (the scheduler's half): a route to a pool no
// roster seat serves (t27's claude:opus:high on 2026-11-01) is the named
// wait no_roster_seat, with the pool in the state file for status, and no
// line in the decisions log on any tick. Reverted, every tick logged a
// seat_request_failed (8000+ lines in two and a half hours).
func TestNoRosterSeatIsANamedWaitNotALogLine(t *testing.T) {
	h, _ := newRosterHarness(t, Config{MaxSeats: 2, StatePath: filepath.Join(t.TempDir(), "scheduler.json")}, RosterSeat{Handle: "dev", Pool: sonnet})
	h.tick()
	tk := h.add("t27", &opusHigh)
	for i := 0; i < 50; i++ {
		if ds := h.tick(); count(ds, "seat_request_failed") > 0 {
			t.Fatalf("tick %d logged %+v", i, ds)
		}
		h.clk.Add(2 * time.Second)
	}
	if got := h.get(tk.ID); got.State != task.StatePending || got.WaitReason != WaitNoRosterSeat {
		t.Fatalf("t27: %+v", got)
	}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RouteWaits) != 1 || st.RouteWaits[0] != (RouteWait{TaskID: tk.ID, Reason: WaitNoRosterSeat, Pool: opusHigh}) {
		t.Fatalf("route waits %+v", st.RouteWaits)
	}
}

type failingLauncher struct {
	*fakeLauncher
	asked []time.Time
	clk   *clock
}

func (f *failingLauncher) RequestSeat(SeatRequest) error {
	f.asked = append(f.asked, f.clk.Now())
	return errors.New("amq: spawn refused (transient)")
}

// Row 5: a transient seat-request failure is logged once per backoff step,
// not every tick: over ten minutes of 2 s ticks the requests and the lines
// are at 0, 2, 6, 14, 30, 62, 126, 254 and 510 s (doubling from 2 s, capped
// at 5 minutes), and a change to the task starts the backoff over.
// Reverted, the 300 ticks asked and logged 300 times.
func TestTransientSeatRequestFailureBacksOff(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 2})
	fl := &failingLauncher{fakeLauncher: h.l, clk: h.clk}
	h.s.Launcher = fl
	tk := h.add("a", &sonnet)
	lines := 0
	for i := 0; i < 300; i++ {
		lines += count(h.tick(), "seat_request_failed")
		h.clk.Add(2 * time.Second)
	}
	var at []int
	for _, a := range fl.asked {
		at = append(at, int(a.Sub(t0)/time.Second))
	}
	want := []int{0, 2, 6, 14, 30, 62, 126, 254, 510}
	if lines != len(want) || len(at) != len(want) {
		t.Fatalf("%d lines, requests at %v s; want %v", lines, at, want)
	}
	for i := range want {
		if at[i] != want[i] {
			t.Fatalf("requests at %v s; want %v", at, want)
		}
	}
	if b := h.s.seatBackoff[tk.ID+"|"+sonnet.Key()]; b == nil || b.delay != SeatRequestBackoffMax {
		t.Fatalf("the backoff after ten minutes: %+v", b)
	}
	// a change to the task (any store mutation) starts it over
	if _, err := h.store.SetWait(tk.ID, task.WaitNoRoute, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if ds := h.tick(); count(ds, "seat_request_failed") != 1 {
		t.Fatalf("after the task changed: %+v", ds)
	}
}

type sizeCapCommitter struct{ calls int }

func (c *sizeCapCommitter) Commit(string, task.Task) CommitResult {
	c.calls++
	return CommitResult{ReasonCode: "size_cap_exceeded", Detail: "412 lines over a cap of 400"}
}

// Row (b) of the plan reviewer's additions, a pin: a task the commit step
// refused for the size cap is not dispatched again on its own. It stays
// blocked through any number of ticks, with its seat idle, until the lead's
// reset; then it is dispatched once.
func TestPolicyRefusedTaskIsNotRedispatchedWithoutTheLead(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	c := &sizeCapCommitter{}
	h.s.Committer = c
	if _, err := h.store.Add(task.AddInput{Title: "t27", Class: "implement", Pool: &sonnet, DeclaredFiles: []string{"src/"}}, t0); err != nil {
		t.Fatal(err)
	}
	h.up("seat-a", sonnet)
	if ds := h.tick(); !has(ds, "dispatched", "t1", "seat-a") {
		t.Fatalf("first dispatch: %+v", ds)
	}
	if _, err := h.store.Claim("t1", "seat-a", nil, 0, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Done("t1", "seat-a", "ok", "", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if ds := h.tick(); !has(ds, "commit_refused", "t1", "seat-a") {
		t.Fatalf("refusal: %+v", ds)
	}
	for i := 0; i < 100; i++ {
		h.clk.Add(2 * time.Second)
		if ds := h.tick(); has(ds, "dispatched", "t1", "") {
			t.Fatalf("tick %d dispatched the refused task with no lead decision: %+v", i, ds)
		}
	}
	if got := h.get("t1"); got.State != task.StateBlocked || got.BlockReason != "size_cap_exceeded" || c.calls != 1 {
		t.Fatalf("t1 %+v; commit calls %d", got, c.calls)
	}
	if _, err := h.store.Reset("t1", nil, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if ds := h.tick(); !has(ds, "dispatched", "t1", "seat-a") {
		t.Fatalf("after the lead's reset: %+v", ds)
	}
}
