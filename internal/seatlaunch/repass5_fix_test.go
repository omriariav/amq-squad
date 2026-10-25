//go:build unix

package seatlaunch

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// The fix round's item 3 (ruling): an apply_ambiguous request with no proof
// no longer holds its slot forever. After the token window plus the close
// grace the launcher asks for the handle's panes: a confirmed absence
// releases the slot (apply_abandoned); a pane that exists, or an answer
// that is not known, keeps it held. Before the window ends nothing changes.
func TestAmbiguousRequestIsAbandonedOnlyWhenNoPaneExists(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.publish = false
	f.fake.apply.Observations = []launchapi.ParticipantObservationV1{
		{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%99"},
	}
	var mu sync.Mutex
	now := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	f.l.Options.ProofClock, f.l.Clock, f.l.CloseWait = clock, clock, time.Second
	panes, known := 1, true
	f.l.PanesFor = func(handle string) (int, bool) {
		if handle != "cto" {
			t.Errorf("panes asked for %q", handle)
		}
		return panes, known
	}
	req := scheduler.SeatRequest{ID: "seat-req-abandon", Pool: poolA}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	panes = 0
	if err := f.l.CancelSeatRequest(req); err == nil || !strings.Contains(err.Error(), "apply_ambiguous") {
		t.Fatalf("inside the token window: %v", err)
	}
	advance(61 * time.Second) // past the 60 s window and the 1 s grace
	panes, known = 1, true
	if err := f.l.CancelSeatRequest(req); err == nil {
		t.Fatal("a request whose pane exists was released")
	}
	panes, known = 0, false
	if err := f.l.CancelSeatRequest(req); err == nil {
		t.Fatal("a request whose panes are unknown was released")
	}
	panes, known = 0, true
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("no pane after the window: %v", err)
	}
	f.l.mu.Lock()
	r := f.l.reqs[req.ID]
	state, why := r.state, r.err
	f.l.mu.Unlock()
	if state != stateCancelled || why == nil || !strings.Contains(why.Error(), "apply_abandoned") {
		t.Fatalf("the abandoned request: %s %v", state, why)
	}
}
