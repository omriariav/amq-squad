package scheduler

import (
	"strings"
	"testing"
	"time"
)

// Final review item 1: request ids carry the scheduler incarnation, so a
// seat requested before a restart never consumes a reservation of the next
// scheduler, in any pool.
func TestFinding9PreviousIncarnationArrivalIsRefused(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour})
	h.add("a", &sonnet)
	first, err := New(Options{Store: h.store, Now: h.clk.Now, Launcher: h.l, Config: h.s.Config})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Tick(); err != nil {
		t.Fatal(err)
	}
	old := h.l.requests[0]
	// The scheduler restarts: a second instance on the same store requests
	// its own seat for t1, which is still pending.
	second, err := New(Options{Store: h.store, Now: h.clk.Now, Launcher: h.l, Config: h.s.Config})
	if err != nil {
		t.Fatal(err)
	}
	if first.Incarnation == second.Incarnation || !strings.HasPrefix(old.ID, "seat-req-"+first.Incarnation+"-") {
		t.Fatalf("incarnations %q %q, id %q", first.Incarnation, second.Incarnation, old.ID)
	}
	if _, err := second.Tick(); err != nil {
		t.Fatal(err)
	}
	fresh := h.l.requests[1]
	if fresh.ID == old.ID {
		t.Fatalf("the restarted scheduler reissued the old id %s", old.ID)
	}
	// The first instance's delayed seat arrives at the second.
	second.Post(Event{Kind: SeatUp, At: h.clk.Now(), Handle: "old-seat", Pool: gpt, RequestID: old.ID})
	d, err := second.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !has(d, "seat_refused", "", "old-seat") || second.seats["old-seat"] != nil || len(second.requests) != 1 || second.requests[0].req.ID != fresh.ID {
		t.Fatalf("a previous incarnation's seat: decisions %+v seats %v requests %+v", d, second.seats, second.requests)
	}
	if len(h.l.closed) != 1 || h.l.closed[0] != "old-seat" || second.refused[0].Reason != RefusedStaleIncarnation {
		t.Fatalf("closed %v refused %+v", h.l.closed, second.refused)
	}
}

// Final review item 4: a nil launcher is a test mode; the production
// constructor refuses it.
func TestNewRefusesANilLauncher(t *testing.T) {
	if _, err := New(Options{}); err == nil || !strings.Contains(err.Error(), "SeatLauncher is required") {
		t.Fatalf("New without a launcher: %v", err)
	}
}
