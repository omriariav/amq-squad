package scheduler

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 3.2.0 rows 1, 3, 6 and 8: only activity for the current attempt
// renews, the first renewal is recorded, and the budget warning and spent
// decisions are emitted once from store facts.
func TestAttemptActivityRenewsUntilBudgetAndSilenceStillExpires(t *testing.T) {
	h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
	tk, err := h.store.Add(task.AddInput{Title: "bounded", Class: "script", Pool: &gpt, Budget: 25 * time.Minute}, h.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	h.up("seat-a", gpt)
	h.tick()
	if _, err := h.store.Claim(tk.ID, "seat-a", nil, 10*time.Minute, h.clk.Now()); err != nil {
		t.Fatal(err)
	}

	// A stale attempt is not activity for this lease.
	h.clk.Add(8 * time.Minute)
	h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a", Attempt: 2})
	if ds := h.tick(); has(ds, "lease_renewed", tk.ID, "seat-a") {
		t.Fatalf("wrong attempt renewed: %+v", ds)
	}

	// The current attempt renews and records its first activity exactly once.
	h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a", Attempt: 1})
	if ds := h.tick(); !has(ds, "lease_renewed", tk.ID, "seat-a") {
		t.Fatalf("first activity was not recorded: %+v", ds)
	}
	h.clk.Add(8 * time.Minute)
	h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a", Attempt: 1})
	if ds := h.tick(); has(ds, "lease_renewed", tk.ID, "seat-a") || !has(ds, "task_budget_warning", tk.ID, "seat-a") {
		t.Fatalf("second activity or warning: %+v", ds)
	}

	// Steady activity is capped at the budget. At the bound the ordinary
	// expiry is classified as budget spent, not as an unbounded renewal.
	h.clk.Add(8 * time.Minute)
	h.s.Post(Event{Kind: SeatActivity, At: h.clk.Now(), Handle: "seat-a", Attempt: 1})
	h.tick()
	h.clk.Add(time.Minute)
	ds := h.tick()
	found := false
	for _, d := range ds {
		found = found || d.Kind == "task_budget_spent" && d.TaskID == tk.ID && strings.Contains(d.Reason, "25m")
	}
	if !found {
		t.Fatalf("budget spent decision: %+v", ds)
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
