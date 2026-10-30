//go:build unix

package seatlaunch

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The canary is no real credential shape (briefs ruling 18).
const t17Canary = "OPENAI_API_KEY=t17canaryvalue0061\napi_key:\n  \"t17canaryvalue0062\""

// t17 row 1, the per-seat launch path: a seat request whose Apply AMQ
// refuses reaches the scheduler as a seat_launch_failed decision with the
// stage, AMQ's reason code and the detail (credential-free), on the task
// that waits for it, and the task's wait reason is the failure's in place
// of seat_starting. This is the path of the thirteen silent cancels of
// 2026-10-30. Reverted (settle keeps the failure in r.err and posts
// nothing), no decision names the failure and the task waits seat_starting
// until the reservation expires.
func TestPerSeatLaunchFailureReachesTheSchedulerWithItsReason(t *testing.T) {
	f := newLauncherFixture(t, "action_required")
	f.fake.apply.ReasonCode = scheduler.AMQBindingPresent
	f.fake.applyErr = "launch: the session binding is present\n" + t17Canary
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := &scheduler.Scheduler{Store: store, Now: time.Now, Launcher: f.l, Incarnation: "t", Config: scheduler.Config{MaxSeats: 1}}
	f.l.Post = s.Post
	a, err := store.Add(task.AddInput{Title: "a", Class: "script", Pool: &poolA}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ds, err := s.Tick(); err != nil || len(kinds(ds, "seat_requested")) != 1 {
		t.Fatalf("the request: %+v %v", ds, err)
	}
	f.l.Wait()
	ds, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	got := kinds(ds, scheduler.KindSeatLaunchFailed)
	if len(got) != 1 || got[0].TaskID != a.ID || got[0].Seat != "cto" || got[0].Stage != "apply_failed" || got[0].ReasonCode != scheduler.AMQBindingPresent ||
		!strings.Contains(got[0].Reason, "the session binding is present") {
		t.Fatalf("decisions %+v", ds)
	}
	if strings.Contains(got[0].Reason, "t17canaryvalue0061") || strings.Contains(got[0].Reason, "t17canaryvalue0062") {
		t.Fatalf("the failure decision carries the credential value: %s", got[0].Reason)
	}
	if w, _ := store.Get(a.ID); w.WaitReason != scheduler.AMQBindingPresent {
		t.Fatalf("the task waits %q", w.WaitReason)
	}
}

// t17 row 1, the roster launch: an Apply AMQ refuses names each roster
// seat in a seat_launch_failed decision (stage apply_failed, AMQ's reason
// code, the redacted detail) next to roster_launch_failed, and the state
// file carries the failures for status after the process ends; a seat of
// an applied roster that did not prove is a seat_launch_failed of stage
// seat_not_up.
func TestRosterLaunchFailuresAreSeatLaunchFailedDecisions(t *testing.T) {
	f := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "action_required", Disposition: "not_applied",
		ReasonCode: scheduler.AMQBindingPresent}, false)
	f.fake.applyErr = "launch: the session binding is present\n" + t17Canary
	statePath := filepath.Join(t.TempDir(), "scheduler.json")
	f.s.Config.StatePath = statePath
	ds, err := f.s.Tick()
	var failed *scheduler.RosterLaunchError
	if !errors.As(err, &failed) {
		t.Fatalf("tick error %v", err)
	}
	got := kinds(ds, scheduler.KindSeatLaunchFailed)
	if len(got) != 2 || got[0].Seat != "cto" || got[1].Seat != "dev" {
		t.Fatalf("decisions %+v", ds)
	}
	for _, d := range got {
		if d.Stage != "apply_failed" || d.ReasonCode != scheduler.AMQBindingPresent || !strings.Contains(d.Reason, "the session binding is present") || strings.Contains(d.Reason, "t17canaryvalue") {
			t.Fatalf("the decision %+v", d)
		}
	}
	st, err := scheduler.ReadState(statePath)
	if err != nil || len(st.LaunchFailures) != 2 || st.LaunchFailures[0].Code() != scheduler.AMQBindingPresent || strings.Contains(st.LaunchFailures[0].Detail, "t17canaryvalue") {
		t.Fatalf("state %+v %v", st.LaunchFailures, err)
	}

	g := newRosterFixture(t, launchapi.ApplyResultV1{ResultVersion: 1, Outcome: "applied", Disposition: "committed",
		Observations: []launchapi.ParticipantObservationV1{{Handle: "cto", Runnable: true, Resource: "tmux:v1:pane:%7"}, {Handle: "dev", Runnable: true, Resource: "tmux:v1:pane:%8"}}}, false)
	g.fake.onApply = func() {
		if p := prove(t, g.q, "cto", false, nil); !p.reply.OK {
			t.Errorf("cto's proof: %+v", p.reply)
		}
	}
	g.l.Options.Wait = 300 * time.Millisecond
	ds, err = g.s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if nu := kinds(ds, scheduler.KindSeatLaunchFailed); len(nu) != 1 || nu[0].Seat != "dev" || nu[0].Stage != "seat_not_up" || nu[0].ReasonCode == "" {
		t.Fatalf("decisions %+v", ds)
	}
}
