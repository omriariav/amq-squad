package scheduler

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier rule 4, the claim path in the scheduler: when the grant
// changed after the start, the store refuses the lease of a gated class with
// no command, and the scheduler blocks the task for the lead with
// grants_invalid instead of failing its tick; another task still dispatches.
func TestSchedulerBlocksAGatedClassWithNoCommandAtTheGrant(t *testing.T) {
	h := newHarness(t, Config{LeaseDuration: 10 * time.Minute, IdleTimeout: time.Hour})
	h.s.Store.Verifiers = func(tk task.Task) (string, bool, error) { return "", tk.Class == "implement", nil }
	gated, err := h.store.Add(task.AddInput{Title: "gated", Class: "implement", Pool: &sonnet, Priority: 5}, h.clk.Now())
	if err != nil {
		t.Fatal(err)
	}
	h.add("free", &sonnet)
	h.up("seat-a", sonnet)
	d := h.tick()
	got := h.get(gated.ID)
	if got.State != task.StateBlocked || got.BlockReason != task.ReasonGrantsInvalid || got.BlockRoute != task.RouteLead || !strings.Contains(got.BlockDetail, "verifiers.implement") {
		t.Fatalf("the gated task: %s %s %s", got.State, got.BlockReason, got.BlockRoute)
	}
	if !has(d, "dispatched", "t2", "seat-a") {
		t.Fatalf("the other task was not dispatched: %+v", d)
	}
}
