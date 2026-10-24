package routing

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Lead ruling on the fix 3 design call: a tampered row on task A holds A
// with history_tampered (a block for the lead, cleared by the lead's task
// reset) while task B still routes; the hold and its clearance survive a
// restart, and only a torn or non-row tail stops routing for all.
func TestHoldTamperedTaskOthersRoute(t *testing.T) {
	h := newFix3(t, "seed-hold")
	a := ended("ta", "implement", "claude:sonnet:medium", task.OutcomeUnclaimed)
	h.cur = []task.Task{a}
	b := newTask("tb", "plan")
	if route := h.r.Route(b); route.Pool == nil {
		t.Fatalf("first route: %+v", route)
	}
	// A's own row is edited (same size): its tag fails.
	path := OutcomePath(h.root)
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, bytes.Replace(raw, []byte(`"holder":"s1"`), []byte(`"holder":"s2"`), 1), 0o600)
	h.clock = h.clock.Add(2 * FullCheckInterval)

	route := h.r.Route(a)
	if route.Block != HistoryTampered || route.Pool != nil || !strings.Contains(route.Detail, "task reset ta") {
		t.Fatalf("A: %+v", route)
	}
	if route := h.r.Route(b); route.Pool == nil {
		t.Fatalf("B must still route: %+v", route)
	}
	// The scheduler blocked A at the clock; still blocked (not reset), a
	// restarted router keeps the hold.
	a.State, a.BlockReason, a.UpdatedAt = task.StateBlocked, HistoryTampered, h.clock
	r2 := h.open("seed-hold")
	a.State = task.StatePending // as if routed again without a reset: updated_at not after the block
	if route := r2.Route(a); route.Block != HistoryTampered {
		t.Fatalf("A after a restart, not reset: %+v", route)
	}
	// The lead resets A: pending, updated_at after the block. A routes.
	a.State, a.BlockReason, a.UpdatedAt = task.StatePending, "", h.clock.Add(time.Minute)
	h.clock = h.clock.Add(2 * time.Minute)
	if route := r2.Route(a); route.Pool == nil || route.Block != "" {
		t.Fatalf("A after the lead's reset: %+v", route)
	}
	// The clearance survives a restart.
	r3 := h.open("seed-hold")
	if route := r3.Route(a); route.Pool == nil {
		t.Fatalf("A after a restart, cleared: %+v", route)
	}
	// A torn tail still stops routing for all.
	fh, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString(`{"schema":1,"task":"tb","att`)
	fh.Close()
	if route := r3.Route(b); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("torn tail, B: %+v", route)
	}
}
