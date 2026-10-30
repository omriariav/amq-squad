package scheduler

import (
	"testing"
	"time"
)

// Lead ruling on 2b6090f5's second pass: a SeatUp with no request id is
// refused outright, below the cap too, closed through the launcher, never
// counted, and recorded for status (attention seat:<handle>:unmanaged).
func TestSeatUpWithoutRequestIdIsRefused(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 12, IdleTimeout: time.Hour})
	h.s.Config.StatePath = StatePathFor(t.TempDir())
	h.add("work", &sonnet)
	h.tick() // one reservation for t1
	h.upFor("stray", sonnet, "")
	d := h.tick()
	if !has(d, "seat_refused", "", "stray") || h.s.seats["stray"] != nil || len(h.l.closed) != 1 || h.l.closed[0] != "stray" {
		t.Fatalf("no-id seat: decisions %+v seats %v closed %v", d, h.s.seats, h.l.closed)
	}
	if len(h.s.requests) != 1 {
		t.Fatalf("the unmanaged seat took the reservation: %+v", h.s.requests)
	}
	if got := h.get("t1"); got.Lease != nil {
		t.Fatalf("t1 was dispatched to an unmanaged seat: %+v", got.Lease)
	}
	st, err := ReadState(h.s.Config.StatePath)
	if err != nil || len(st.Refused) != 1 || st.Refused[0].Handle != "stray" || st.Refused[0].Reason != RefusedUnmanaged || st.LiveSeats != 0 {
		t.Fatalf("state: %+v %v", st, err)
	}
}
