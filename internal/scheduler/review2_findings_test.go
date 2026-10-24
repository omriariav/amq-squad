package scheduler

import (
	"errors"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Second Codex pass on 2b6090f5, finding 1 residual: arrival matches a
// reservation by the request id the seat carries back, never by pool.
func TestFinding1bArrivalMatchesByRequestId(t *testing.T) {
	t.Run("sequence A: a later request joins first", func(t *testing.T) {
		h := newHarness(t, Config{MaxSeats: 2, IdleTimeout: time.Hour})
		h.add("a", &sonnet)
		h.add("b", &sonnet)
		h.tick()
		if len(h.l.requests) != 2 {
			t.Fatalf("requests %+v", h.l.requests)
		}
		ra, rb := h.l.requests[0], h.l.requests[1]
		h.upFor("seat-b", sonnet, rb.ID) // B joins first, on its own reservation
		h.tick()
		if len(h.s.requests) != 1 || h.s.requests[0].req.ID != ra.ID {
			t.Fatalf("B's arrival consumed the wrong reservation: %+v", h.s.requests)
		}
		h.clk.Add(6 * time.Minute) // A's reservation times out; the launcher terminates A
		h.tick()
		if len(h.l.cancelled) != 1 || h.l.cancelled[0] != ra.ID {
			t.Fatalf("cancelled %v, want A's request %s", h.l.cancelled, ra.ID)
		}
		h.upFor("seat-a", sonnet, ra.ID) // A shows up after its cancellation
		h.tick()
		if h.s.seats["seat-a"] != nil || h.s.seats["seat-b"] == nil || len(h.l.closed) != 1 || h.l.closed[0] != "seat-a" {
			t.Fatalf("seats %v closed %v: the cancelled seat must be closed, not counted", h.s.seats, h.l.closed)
		}
	})
	t.Run("sequence B: a queued SeatUp after its cancellation", func(t *testing.T) {
		h := newHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour})
		h.add("a", &sonnet)
		h.tick()
		ra := h.l.requests[0]
		h.clk.Add(6 * time.Minute)
		h.tick() // cancellation confirmed, a replacement reservation for t1
		if len(h.l.requests) != 2 || h.l.requests[1].ID == ra.ID {
			t.Fatalf("requests %+v", h.l.requests)
		}
		h.upFor("seat-a", sonnet, ra.ID) // A's event, queued behind the drain
		d := h.tick()
		if !has(d, "seat_refused", "", "seat-a") || len(h.s.seats) != 0 || len(h.s.requests) != 1 || h.s.requests[0].req.ID != h.l.requests[1].ID {
			t.Fatalf("A consumed the replacement: decisions %+v seats %v requests %+v", d, h.s.seats, h.s.requests)
		}
		h.upFor("seat-r", sonnet, "seat-req-999") // an id this scheduler never issued
		if d := h.tick(); !has(d, "seat_refused", "", "seat-r") || h.s.seats["seat-r"] != nil {
			t.Fatalf("unknown request id: %+v", d)
		}
	})
}

// Second Codex pass, new P1: a seat the launcher could not close stays
// counted and closing, is retried every tick, and status sees it.
func TestFinding8UncloseableSeatStaysCountedAndRetried(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour})
	h.s.Config.StatePath = StatePathFor(t.TempDir())
	h.up("seat-1", sonnet)
	h.tick()
	h.l.closeErr = errors.New("kill failed")
	h.up("seat-2", sonnet) // unsolicited, at the cap
	h.tick()
	if st := h.s.seats["seat-2"]; st == nil || st.closing == "" || len(h.s.seats) != 2 {
		t.Fatalf("an uncloseable seat must stay counted and closing: %+v", h.s.seats)
	}
	h.add("work", &gpt) // needs a new seat; the cap is held by the closing seat
	d := h.tick()
	if !has(d, "seat_close_failed", "", "seat-2") || len(h.l.requests) != 0 {
		t.Fatalf("retry and cap: %+v requests %+v", d, h.l.requests)
	}
	if got := h.get("t1"); got.WaitReason != task.WaitSeatCap {
		t.Fatalf("t1 should wait on the cap: %+v", got)
	}
	if h.s.idleSeat(nil, task.Task{}, sonnet) == "seat-2" {
		t.Fatal("a closing seat was offered for dispatch")
	}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || st.LiveSeats != 2 || st.Seats[1].Handle != "seat-2" || st.Seats[1].Closing == "" {
		t.Fatalf("state for status: %+v %v", st, err)
	}
	h.l.closeErr = nil
	if d := h.tick(); !has(d, "seat_closed", "", "seat-2") || h.s.seats["seat-2"] != nil {
		t.Fatalf("the retry that works removes the seat: %+v", d)
	}
}
