package scheduler

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// 2b-2 fix 3c (b): a SeatUp for a request whose cancel failed (queued
// before or after the failure) is refused and the seat closed; the request
// stays closing and the cancel keeps being retried.
func TestSeatUpOfAClosingRequestIsRefused(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour})
	h.add("a", &sonnet)
	h.tick()
	id := h.l.requests[0].ID
	h.l.cancelErr = errors.New("close not confirmed")
	h.clk.Add(6 * time.Minute)
	h.tick() // the first failed cancel
	h.upFor("seat-a", sonnet, id)
	d := h.tick()
	if h.s.seats["seat-a"] != nil || !has(d, "seat_refused", "", "seat-a") {
		t.Fatalf("a closing request's seat was admitted: %+v seats %v", d, h.s.seats)
	}
	if len(h.l.closed) != 1 || len(h.s.requests) != 1 {
		t.Fatalf("closed %v requests %d", h.l.closed, len(h.s.requests))
	}
	if !has(h.tick(), "seat_request_cancel_pending", "t1", "") {
		t.Fatal("the cancel stopped being retried")
	}
}

// 2b-2 fix 3c (c): from the first failed cancel, the request is shown as
// closing in the scheduler state (the status contract's closing attention).
func TestFailedCancelShowsAsClosing(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 1, IdleTimeout: time.Hour, StatePath: filepath.Join(t.TempDir(), "scheduler.json")})
	h.add("a", &sonnet)
	h.tick()
	h.l.cancelErr = errors.New("pid 42 still running")
	h.clk.Add(6 * time.Minute)
	h.tick()
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, sv := range st.Seats {
		if sv.RequestID == h.l.requests[0].ID && sv.Closing == "pid 42 still running" {
			return
		}
	}
	t.Fatalf("no closing row for the request: %+v", st.Seats)
}
