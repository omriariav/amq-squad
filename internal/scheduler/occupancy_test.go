package scheduler

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Phase 5 re-pass, ruling 2: the seat view a router's concurrency cap counts
// names every seat once: a live seat, a seat still closing after its claimed
// lease expired (its task is pending again, its process not yet gone), and a
// pending seat request.
func TestRepassOccupancyNamesLiveClosingAndRequestedSeats(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 4, LeaseDuration: time.Minute})
	h.add("one", &sonnet)
	h.up("seat-a", sonnet)
	if d := h.tick(); !has(d, "dispatched", "t1", "seat-a") {
		t.Fatalf("dispatch: %+v", d)
	}
	if v := h.s.Occupancy(); len(v) != 1 || v[0].Handle != "seat-a" || v[0].Closing != "" {
		t.Fatalf("live seat: %+v", v)
	}
	if _, err := h.store.Claim("t1", "seat-a", nil, time.Minute, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	h.l.closeErr = errors.New("not closed yet") // the close cannot be done yet: the seat stays closing
	h.clk.Add(2 * time.Minute)
	h.add("two", &gpt)
	h.tick()
	if got, _ := h.store.Get("t1"); got.State != task.StatePending {
		t.Fatalf("t1 = %s", got.State)
	}
	v := h.s.Occupancy()
	var closing, requested bool
	for _, s := range v {
		closing = closing || (s.Handle == "seat-a" && s.Closing != "")
		requested = requested || (strings.HasPrefix(s.Handle, "request ") && s.Pool == gpt)
	}
	if !closing || !requested {
		t.Fatalf("seat view after the expiry: %+v", v)
	}
}
