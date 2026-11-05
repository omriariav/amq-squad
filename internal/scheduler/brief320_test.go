package scheduler

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Runtime SeatActivity remains a liveness signal only. Evidence ingest owns
// renewal, so the scheduler cannot create a second renewal path.
func TestSeatActivityDoesNotRenewALease(t *testing.T) {
	h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
	tk, err := h.store.Add(task.AddInput{Title: "bounded", Class: "script", Pool: &gpt}, h.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	h.up("seat-a", gpt)
	h.tick()
	if _, err := h.store.Claim(tk.ID, "seat-a", nil, 10*time.Minute, h.clk.Now()); err != nil {
		t.Fatal(err)
	}

	h.clk.Add(9 * time.Minute)
	h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a", Attempt: 1})
	if ds := h.tick(); has(ds, "lease_renewed", tk.ID, "seat-a") {
		t.Fatalf("scheduler renewed from SeatActivity: %+v", ds)
	}
	h.clk.Add(time.Minute)
	if ds := h.tick(); !has(ds, "lease_expired", tk.ID, "seat-a") {
		t.Fatalf("SeatActivity prevented expiry: %+v", ds)
	}
}

func TestNoActivityStillExpires(t *testing.T) {
	h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
	tk := h.add("silent", &gpt)
	h.up("seat-a", gpt)
	h.tick()
	if _, err := h.store.Claim(tk.ID, "seat-a", nil, 10*time.Minute, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	h.clk.Add(10 * time.Minute)
	if ds := h.tick(); !has(ds, "lease_expired", tk.ID, "seat-a") {
		t.Fatalf("silent lease did not expire: %+v", ds)
	}
}
