//go:build unix

package seatlaunch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
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
	t.Setenv("HOME", t.TempDir()) // the grant read at launch never reaches the real HOME
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
	// Every pane question answered by a seam (no row reaches tmux; the
	// package's refusing stub is the proof): no pane is labelled, every
	// recorded pane is gone, unless a row says otherwise.
	f.l.PanesFor = func(string) (int, bool) { return 0, true }
	f.l.PaneGone = func(string) (bool, bool) { return true, true }
	f.l.BindingPane = func(string, string, string) (bool, bool) { return false, true }
	t.Cleanup(func() { noTmuxCalls(t) })
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
	f.fake.applyErr = "launch: the session binding is present\nOPENAI_API_KEY=t16canaryvalue0042\napi_key:\n  \"t16canaryvalue0043\""
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
	if strings.Contains(got[0].Reason, "t16canaryvalue0042") || strings.Contains(got[0].Reason, "t16canaryvalue0043") {
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

// Codex finding 2: the placement and refusal verdicts are each seat's own.
// Three roster seats: cto proves, dev's seat-exec refuses, ops is placed
// (its pane observed) and never proves. ops stays unresolved, and its
// cancel is confirmed only by its own pane's end; dev's refusal is final.
// Reverted (a launch-wide verdict), dev's refusal made ops "not placed",
// failed, and its cancel succeeded with its pane still there.
func TestRosterPlacementIsEachSeatsOwn(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"},
			{Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}, {Handle: "ops", Runnable: true, Resource: "tmux:v1:pane:%9"}}}, false)
	ops := f.q.Seats[0]
	ops.Handle, ops.Worktree, ops.Branch, ops.Role = "ops", ops.Worktree+"-ops", "seat/ops", "# Role\n\nYou are the ops seat.\n"
	f.q.Seats = append(f.q.Seats, ops)
	f.s.Config.Roster = append(f.s.Config.Roster, scheduler.RosterSeat{Handle: "ops", Pool: poolA})
	f.fake.onApply = func() {
		if p := prove(t, f.q, "cto", false, nil); !p.reply.OK {
			t.Errorf("cto's proof: %+v", p.reply)
		}
		publishExec(t, f.q, "dev", &launch.Exec{At: time.Now(), Outcome: "refused", ReasonCode: "identity_proof_failed", Detail: "refused"})
	}
	f.l.Options.Wait = 300 * time.Millisecond
	present := true
	f.l.PaneGone = func(string) (bool, bool) { return !present, true }
	ds, err := f.s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds(ds, "seat_up")) != 1 || len(kinds(ds, "seat_not_up")) != 2 {
		t.Fatalf("decisions %+v", ds)
	}
	reqs := map[string]scheduler.SeatRequest{}
	for _, r := range f.plans[0] {
		reqs[r.Handle] = r
	}
	if err := f.l.CancelSeatRequest(reqs["dev"]); err != nil {
		t.Fatalf("dev refused: its cancel is confirmed at once: %v", err)
	}
	if err := f.l.CancelSeatRequest(reqs["ops"]); err == nil {
		t.Fatal("ops's cancel was confirmed while its pane exists")
	}
	present = false
	if err := f.l.CancelSeatRequest(reqs["ops"]); err != nil {
		t.Fatalf("ops's pane ended: %v", err)
	}
}

// Codex finding 3: a roster launch whose Prepare names a roster seat's
// recorded pane that still exists (a pane that outlived its process) is
// refused before Apply with seat_replacement_resource_present naming the
// handle; AMQ never sees the Apply it would refuse. A recorded pane that
// is gone does not stop the launch. Reverted, the launch went to Apply and
// failed there.
func TestRosterRelaunchRefusedWhileARecordedPaneLingers(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}, false)
	f.fake.prepare.Observations = []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "none"}}
	var asked []string
	f.l.BindingPane = func(_, handle, res string) (bool, bool) { asked = append(asked, handle+" "+res); return true, true }
	ds, err := f.s.Tick()
	var failed *scheduler.RosterLaunchError
	if !errors.As(err, &failed) || failed.Stage != "resource_present" || failed.ReasonCode != scheduler.ReasonResourcePresent {
		t.Fatalf("tick error: %v", err)
	}
	got := kinds(ds, "roster_launch_failed")
	if len(got) != 1 || !strings.Contains(got[0].Reason, "cto (tmux:v1:pane:%7)") || strings.Contains(got[0].Reason, "dev (") || f.amqCalls("--apply") != 0 {
		t.Fatalf("decisions %+v; amq calls %q", ds, f.fake.calls)
	}
	if len(asked) != 1 || asked[0] != "cto tmux:v1:pane:%7" {
		t.Fatalf("panes asked: %v", asked)
	}
	g := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed"}, true)
	g.fake.prepare.Observations = f.fake.prepare.Observations
	if _, err := g.s.Tick(); err != nil || g.amqCalls("--apply") != 1 {
		t.Fatalf("a recorded pane that is gone: %v; amq calls %q", err, g.fake.calls)
	}
}

// t16 round 7 (Codex 4): a recorded pane is the binding's live resource only
// when the binding's own server lists it in the binding's session (id and
// name), with the seat's agent label and the server pid the binding recorded
// as its epoch. After a tmux server restart an unrelated pane can carry the
// old %N: in another session, under another server pid, or with another
// label it is not present, so a relaunch is not refused for ever (exit 17).
// Reverted (the %N alone), every such pane read as the seat's.
func TestRecordedPaneIsPresentOnlyInTheBindingsServerAndSession(t *testing.T) {
	var b amqBinding
	if err := json.Unmarshal([]byte(`{"backend":"tmux","instance_identity":"tmux-socket:/tmp/tmux-501/default","resources":{"resources":[
		{"opaque_id":"tmux:v1:session:$3:amq-proj-abc-s1-def"},{"opaque_id":"tmux:v1:epoch:4242"},{"opaque_id":"tmux:v1:pane:%7","agent":"cto"}]}}`), &b); err != nil {
		t.Fatal(err)
	}
	row := func(pid, sid, name, pane, agent string) string {
		return strings.Join([]string{pid, sid, name, pane, agent}, "\t")
	}
	for _, c := range []struct {
		name, listing string
		present       bool
	}{
		{"the seat's pane", row("4242", "$3", "amq-proj-abc-s1-def", "%7", "cto"), true},
		{"%7 in another session", row("4242", "$0", "work", "%7", ""), false},
		{"%7 after a server restart", row("5151", "$3", "amq-proj-abc-s1-def", "%7", "cto"), false},
		{"%7 labelled for another seat", row("4242", "$3", "amq-proj-abc-s1-def", "%7", "dev"), false},
		{"no %7", row("4242", "$3", "amq-proj-abc-s1-def", "%8", "dev"), false},
	} {
		if present, known := paneInBinding(b, "cto", "tmux:v1:pane:%7", c.listing+"\n"); !known || present != c.present {
			t.Errorf("%s: present %v known %v", c.name, present, known)
		}
	}
	if _, known := paneInBinding(amqBinding{}, "cto", "tmux:v1:pane:%7", ""); known {
		t.Error("a binding without a session resource is known")
	}
	if _, known := bindingPanePresent(t.TempDir(), "cto", "tmux:v1:pane:%7", nil); known {
		t.Error("a missing binding record is known")
	}
}

// t16 round 7 (Codex 6): amq's stderr is redacted at the source, with the
// multiline rules, so neither the staged record (Apply.FailureDetail) nor the
// printed report carries a credential value, a quoted value on the line
// after its key included. Reverted (the raw stderr kept), both carry it.
func TestAmqFailureDetailIsRedactedWhereItIsRead(t *testing.T) {
	q := testSquad(t)
	f := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "action_required", Disposition: "not_applied", ReasonCode: scheduler.AMQBindingPresent}}
	f.applyErr = "launch refused\napi_key:\n  \"t16canaryvalue0044\"\nOPENAI_API_KEY=t16canaryvalue0045"
	res, err := Launch(context.Background(), q, launchOpts(t, f, map[string]string{"sha256:a1": "trust_exact_subject"}))
	if err != nil || res.Stage != "apply_failed" {
		t.Fatalf("launch: %s %v", res.Stage, err)
	}
	if res.proof != nil {
		t.Cleanup(res.proof.close)
	}
	raw, err := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), "cto"), launch.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"staged record": string(raw), "result": res.FailureDetail} {
		if strings.Contains(text, "t16canaryvalue0044") || strings.Contains(text, "t16canaryvalue0045") {
			t.Errorf("the %s carries a credential value:\n%s", name, text)
		}
	}
	if !strings.Contains(string(raw), "launch refused") {
		t.Fatalf("the staged record lost the detail:\n%s", raw)
	}
}
