package scheduler

import (
	"errors"
	"testing"
	"time"
)

// Codex review of bc48a0e1, finding 1: a seat request that outlives the
// start timeout keeps its place under the cap until the launcher confirms
// the cancellation, so a late join can never exceed max_seats.
func TestFinding1SeatRequestTimeoutKeepsTheCapUntilCancelConfirmed(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour})
	h.add("a", &sonnet)
	h.tick()
	h.add("b", &gpt)
	h.tick()
	if len(h.l.requests) != 1 {
		t.Fatalf("requests: %+v", h.l.requests)
	}
	h.l.cancelErr = errors.New("seat still starting")
	h.clk.Add(6 * time.Minute) // past the 5-minute start timeout
	if d := h.tick(); !has(d, "seat_request_cancel_pending", "t1", "") {
		t.Fatalf("an unconfirmed cancellation: %+v", d)
	}
	if len(h.l.requests) != 1 {
		t.Fatalf("a second seat was requested before the first was cancelled: %+v", h.l.requests)
	}
	if got := h.get("t2"); got.WaitReason != "seat_cap" {
		t.Fatalf("t2 must keep waiting on the cap: %+v", got)
	}
	// The late seat of a request whose cancel is pending is refused and
	// closed, never admitted (2b-2 fix 3c (b)); the request keeps its place
	// under the cap, counted as closing.
	h.upFor("late-a", sonnet, h.l.requests[0].ID)
	h.tick()
	if len(h.s.seats) != 0 || len(h.s.requests) != 1 || len(h.l.requests) != 1 || len(h.l.closed) != 1 || h.l.closed[0] != "late-a" {
		t.Fatalf("seats %d requests %d closed %v", len(h.s.seats), len(h.s.requests), h.l.closed)
	}

	// A confirmed cancellation releases the reservation; a seat that joins
	// anyway past the cap is refused and closed.
	h2 := newHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour})
	h2.add("a", &sonnet)
	h2.tick()
	h2.add("b", &gpt)
	h2.clk.Add(6 * time.Minute)
	d := h2.tick()
	// t1 (first in order) takes the released room with a fresh request.
	if !has(d, "seat_request_cancelled", "t1", "") || len(h2.l.cancelled) != 1 || len(h2.l.requests) != 2 ||
		h2.l.requests[1].TaskID != "t1" || h2.l.requests[1].ID == h2.l.requests[0].ID {
		t.Fatalf("after a confirmed cancellation: %+v requests %+v", d, h2.l.requests)
	}
	h2.upFor("a-seat", sonnet, h2.l.requests[1].ID) // joins on t1's fresh reservation
	h2.upFor("stray", gpt, "")                      // no request id: unmanaged
	h2.tick()
	if len(h2.s.seats) != 1 || h2.s.seats["a-seat"] == nil || len(h2.l.closed) != 1 || h2.l.closed[0] != "stray" {
		t.Fatalf("over-cap join: seats %v closed %v", h2.s.seats, h2.l.closed)
	}
}
