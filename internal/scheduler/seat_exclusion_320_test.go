package scheduler

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

type compatibilitySeatPreparer struct {
	bad    bool
	reason string
	calls  map[string]int
}

type mixedLedgerPreparer struct{}

func (mixedLedgerPreparer) PrepareSeat(handle string) error {
	switch handle {
	case "seat-a":
		return tamperErr(handle, "7")
	case "seat-b":
		return &PrepareRefusal{Reason: "newer_writer", Detail: "written by v9.0.0", Route: task.RouteOwner}
	default:
		return nil
	}
}

func (mixedLedgerPreparer) CheckSeatCompatibility(handle string) error {
	return mixedLedgerPreparer{}.PrepareSeat(handle)
}

func TestOneTickSeparatesTamperedIncompatibleAndHealthySeats(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 3, LeaseDuration: time.Hour, IdleTimeout: time.Hour})
	h.s.Preparer = mixedLedgerPreparer{}
	blocked := h.add("blocked on tamper", &sonnet)
	healthy := h.add("healthy dispatch", &sonnet)
	h.up("seat-a", sonnet)
	h.up("seat-b", sonnet)
	h.up("seat-c", sonnet)
	all := h.tick()
	if countOf(all, "seat_prepare_failed", "seat-a") != 1 || countOf(all, "blocked", "seat-a") != 1 {
		t.Fatalf("tampered seat did not raise exactly one alarm and block: %+v", all)
	}
	if countOf(all, "seat_excluded", "seat-b") != 1 || has(all, "seat_prepare_failed", "", "seat-b") {
		t.Fatalf("incompatible seat raised a tamper alarm or was not excluded: %+v", all)
	}
	if !has(all, "dispatched", healthy.ID, "seat-c") {
		t.Fatalf("healthy seat did not dispatch in the same tick: %+v", all)
	}
	if got := h.get(blocked.ID); got.State != task.StateBlocked || got.BlockReason != ReasonLedgerTampered {
		t.Fatalf("tampered task = %+v", got)
	}
}

func (p *compatibilitySeatPreparer) compatibility(handle string) error {
	p.calls[handle]++
	if handle == "seat-a" && p.bad {
		reason := p.reason
		if reason == "" {
			reason = "newer_writer"
		}
		return &PrepareRefusal{Reason: reason, Detail: "written by a newer amq-squad (v9.0.0)", Route: task.RouteOwner}
	}
	return nil
}

func TestSeatExclusionDecisionCarriesCompatibilityReason(t *testing.T) {
	for _, reason := range []string{"newer_writer", "schema_mismatch"} {
		t.Run(reason, func(t *testing.T) {
			h := newHarness(t, Config{MaxSeats: 1, LeaseDuration: time.Hour, IdleTimeout: time.Hour})
			p := &compatibilitySeatPreparer{bad: true, reason: reason, calls: map[string]int{}}
			h.s.Preparer = p
			h.add("one", &sonnet)
			h.up("seat-a", sonnet)
			var got Decision
			for _, d := range h.tick() {
				if d.Kind == "seat_excluded" {
					got = d
				}
			}
			if got.ReasonCode != reason {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func (p *compatibilitySeatPreparer) PrepareSeat(handle string) error {
	return p.compatibility(handle)
}

func (p *compatibilitySeatPreparer) CheckSeatCompatibility(handle string) error {
	return p.compatibility(handle)
}

// E6a: an incompatible seat is held, not the task. The healthy peer takes
// successive work, the hold is logged once, and a verified read after an
// install clears it for the next task.
func TestCompatibilityRefusalExcludesSeatUntilLedgerVerifies(t *testing.T) {
	h := newHarness(t, Config{MaxSeats: 2, LeaseDuration: time.Hour, IdleTimeout: time.Hour})
	p := &compatibilitySeatPreparer{bad: true, calls: map[string]int{}}
	h.s.Preparer = p
	h.add("one", &sonnet)
	h.add("two", &sonnet)
	h.up("seat-a", sonnet)
	h.up("seat-b", sonnet)
	all := h.tick()
	if !has(all, "seat_excluded", "", "seat-a") || !has(all, "dispatched", "t1", "seat-b") {
		t.Fatalf("first dispatch = %+v", all)
	}
	if got := h.get("t1"); got.State == task.StateBlocked || got.Lease == nil || got.Lease.Holder != "seat-b" || got.Attempt != 1 {
		t.Fatalf("task was blocked or charged to the excluded seat: %+v", got)
	}
	harnessPass(t, h.store, "t1", h.clk.Now())
	if _, err := h.store.Done("t1", "seat-b", "ok", "", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	all = append(all, h.tick()...)
	if !has(all, "dispatched", "t2", "seat-b") || count(all, "seat_excluded") != 1 {
		t.Fatalf("healthy seat did not take successive work or exclusion repeated: %+v", all)
	}
	if got := h.get("t2"); got.Lease == nil || got.Lease.Holder != "seat-b" || got.Attempt != 1 {
		t.Fatalf("second task = %+v", got)
	}

	// The installed reader now verifies seat-a's ledger. seat-b stays busy,
	// so the cleared seat takes new work immediately.
	p.bad = false
	h.add("three", &sonnet)
	cleared := h.tick()
	if !has(cleared, "seat_exclusion_cleared", "", "seat-a") || !has(cleared, "dispatched", "t3", "seat-a") {
		t.Fatalf("cleared exclusion = %+v", cleared)
	}
}
