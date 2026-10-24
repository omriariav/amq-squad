package fleet

import (
	"context"
	"testing"
	"time"
)

// Reviewer ruling (unknown liveness is alive): when the start time of a
// live foreign mayor pid cannot be read, its reservation stays counted
// inside the phase window, and at the window it is released with the reason
// liveness_unknown, not incarnation_gone or phase_stalled. This runs the
// real incarnationAlive with the process probes stubbed.
func TestUnknownLivenessHoldsUntilTheWindow(t *testing.T) {
	const foreign = 4244
	prevAlive, prevStart := pidAlive, pidStartTime
	t.Cleanup(func() { pidAlive, pidStartTime = prevAlive, prevStart })
	pidAlive = func(pid int) bool { return pid == foreign || prevAlive(pid) }
	pidStartTime = func(pid int) (time.Time, bool) {
		if pid == foreign {
			return time.Time{}, false // the start time cannot be read
		}
		return prevStart(pid)
	}
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/other"))
	orphan(t, f, "other", foreign, at.Add(-time.Hour), at)
	f.m.Now = func() time.Time { return at.Add(time.Minute) }
	if r := mustStart(t, f.m, f.request(t, "app", false)); r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("unknown liveness released the reservation inside the window: %+v", r)
	}
	if rel := released(t, f); len(rel) != 0 {
		t.Fatalf("released inside the window: %+v", rel)
	}
	f.m.Now = func() time.Time { return at.Add(PhaseWindow) }
	if res, err := f.m.Retry(context.Background()); err != nil || res["app"].Outcome != OutcomeStarted {
		t.Fatalf("at the window: %+v %v", res, err)
	}
	if rel := released(t, f); len(rel) != 1 || rel[0].Name != "other" || rel[0].Reason != ReleaseLivenessUnknown {
		t.Fatalf("released = %+v, want other liveness_unknown", rel)
	}
	if got := f.m.incarnationAlive(foreign, at.Add(-time.Hour)); got != LivenessUnknown {
		t.Fatalf("liveness = %v, want unknown", got)
	}
}
