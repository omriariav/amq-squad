//go:build unix

package seatlaunch

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// rosterFixture is a real scheduler in roster mode over the real launcher,
// whose AMQ is the fake CLI: Apply "starts" the seats by proving each one
// over the launch's socket, as seat-exec does.
type rosterFixture struct {
	q     Squad
	fake  *fakeAMQ
	l     *Launcher
	s     *scheduler.Scheduler
	mu    sync.Mutex
	plans [][]scheduler.SeatRequest
}

var poolDev = task.Pool{Runtime: "codex", Model: "gpt-6-astra", Effort: "medium"}

func newRosterFixture(t *testing.T, apply launchapi.ApplyResultV1, prove2 bool) *rosterFixture {
	t.Helper()
	f := &rosterFixture{q: testSquad(t)}
	f.fake = &fakeAMQ{t: t, prepare: preparedResult(), apply: apply}
	f.fake.onApply = func() {
		if !prove2 {
			return
		}
		for _, h := range []string{"cto", "dev"} {
			if p := prove(t, f.q, h, false, nil); !p.reply.OK {
				t.Errorf("the proof of %s was refused: %+v", h, p.reply)
			}
		}
	}
	opts := launchOpts(t, f.fake, map[string]string{"sha256:a1": "trust_exact_subject"})
	opts.Wait = 2 * time.Second
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.l = &Launcher{Options: opts, Root: f.q.SessionRoot(), CloseWait: 2 * time.Second,
		PlanRoster: func(reqs []scheduler.SeatRequest) (Squad, error) {
			f.mu.Lock()
			f.plans = append(f.plans, reqs)
			f.mu.Unlock()
			return f.q, nil
		}}
	f.s = &scheduler.Scheduler{Store: store, Now: time.Now, Launcher: f.l, Incarnation: "t",
		Config: scheduler.Config{MaxSeats: 2, Roster: []scheduler.RosterSeat{{Handle: "cto", Pool: poolA}, {Handle: "dev", Pool: poolDev}}}}
	f.l.Post = f.s.Post
	// Launch returns with the proof window open; close it before TempDir
	// is removed (cleanups run last in, first out), as proofLaunch does.
	t.Cleanup(func() {
		f.l.mu.Lock()
		defer f.l.mu.Unlock()
		for _, r := range f.l.reqs {
			if r.proof != nil {
				r.proof.close()
			}
		}
	})
	return f
}

func (f *rosterFixture) amqCalls(flag string) int {
	n := 0
	for _, c := range f.fake.calls {
		if slices.Contains(c, flag) {
			n++
		}
	}
	return n
}

func kinds(ds []scheduler.Decision, kind string) []scheduler.Decision {
	var out []scheduler.Decision
	for _, d := range ds {
		if d.Kind == kind {
			out = append(out, d)
		}
	}
	return out
}

// t16 row 1: the scheduler starts and launches the whole roster in one
// AMQ prepare and one apply; the one prepare request carries every roster
// seat, each with its own request id; every seat proves and is up in the
// first tick, a seat_up decision each. Reverted (a seat launched per
// request), nothing launches at start and no seat is up.
func TestRosterLaunchIsOnePrepareAndOneApplyForEverySeat(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}}}, true)
	ds, err := f.s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if p, a := f.amqCalls("--prepare"), f.amqCalls("--apply"); p != 1 || a != 1 {
		t.Fatalf("amq calls: %d prepare, %d apply, want one each: %q", p, a, f.fake.calls)
	}
	var handles []string
	for _, p := range f.fake.applied.Prepare.Intent.Participants {
		if p.Runnable { // the operator is a participant, never a seat
			handles = append(handles, p.Handle)
		}
	}
	if strings.Join(handles, ",") != "cto,dev" {
		t.Fatalf("the one prepare request carries %v, want both roster seats", handles)
	}
	if len(f.plans) != 1 || len(f.plans[0]) != 2 || f.plans[0][0].ID == f.plans[0][1].ID || f.plans[0][0].Handle != "cto" || f.plans[0][1].Handle != "dev" {
		t.Fatalf("roster plan: %+v", f.plans)
	}
	up := kinds(ds, "seat_up")
	if len(kinds(ds, "roster_launched")) != 1 || len(up) != 2 || up[0].Seat != "cto" || up[1].Seat != "dev" {
		t.Fatalf("decisions: %+v", ds)
	}
	if occ := f.s.Occupancy(); len(occ) != 0 {
		t.Fatalf("parked roster seats occupy a concurrency pool: %+v", occ)
	}
	// A later tick launches nothing more.
	if _, err := f.s.Tick(); err != nil {
		t.Fatal(err)
	}
	if f.amqCalls("--apply") != 1 {
		t.Fatalf("a second apply: %q", f.fake.calls)
	}
}

// t16 row 6: a roster apply that AMQ refuses ends the launch with one
// decision naming the stage, AMQ's reason code and the failure detail
// (credential-free), and the tick returns the roster launch error (the
// process exits with it). Reverted, the launcher kept the failure in
// memory and logged nothing.
func TestRosterApplyRefusalIsLoggedWithItsReason(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "action_required", Disposition: "not_applied",
		ReasonCode: scheduler.AMQBindingPresent}, false)
	f.fake.applyErr = "launch: the session binding is present\nOPENAI_API_KEY=t16canaryvalue0042"
	ds, err := f.s.Tick()
	var failed *scheduler.RosterLaunchError
	if !errors.As(err, &failed) || failed.Stage != "apply_failed" || failed.ReasonCode != scheduler.AMQBindingPresent {
		t.Fatalf("tick error: %v", err)
	}
	got := kinds(ds, "roster_launch_failed")
	if len(got) != 1 || !strings.Contains(got[0].Reason, "stage apply_failed") || !strings.Contains(got[0].Reason, scheduler.AMQBindingPresent) ||
		!strings.Contains(got[0].Reason, "the session binding is present") {
		t.Fatalf("decisions: %+v", ds)
	}
	if strings.Contains(got[0].Reason, "t16canaryvalue0042") {
		t.Fatalf("the failure decision carries the credential value: %s", got[0].Reason)
	}
	if len(kinds(ds, "seat_up")) != 0 {
		t.Fatalf("a seat counted up after a refused apply: %+v", ds)
	}
}

// t16: a roster seat that did not prove is settled by its own pane: the
// other roster seat's pane in the apply's observations is not a pane "for
// an unknown handle", so the request is not apply_ambiguous, and its cancel
// is confirmed once its own pane is gone. Reverted (one seat per launch),
// every other seat's pane made the request ambiguous and its cancel stayed
// unconfirmed until the token window ended.
func TestRosterSeatThatDidNotProveIsCancelledByItsOwnPane(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}}}, false)
	f.fake.onApply = func() {
		if p := prove(t, f.q, "cto", false, nil); !p.reply.OK {
			t.Errorf("cto's proof: %+v", p.reply)
		}
	}
	f.l.Options.Wait = 300 * time.Millisecond
	var asked []string
	f.l.PaneGone = func(res string) (bool, bool) { asked = append(asked, res); return true, true }
	ds, err := f.s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds(ds, "seat_up")) != 1 || len(kinds(ds, "seat_not_up")) != 1 || kinds(ds, "seat_not_up")[0].Seat != "dev" {
		t.Fatalf("decisions %+v", ds)
	}
	dev := f.plans[0][1]
	if err := f.l.CancelSeatRequest(dev); err != nil || len(asked) != 1 || asked[0] != "tmux:v1:pane:%8" {
		t.Fatalf("the cancel of dev's request: %v (panes asked %v)", err, asked)
	}
}
