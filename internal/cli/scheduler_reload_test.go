package cli

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// waitTask waits until the task satisfies ok (or 10 s pass).
func waitTask(t *testing.T, store task.Store, id string, ok func(task.Task) bool) task.Task {
	t.Helper()
	var got task.Task
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		got, _ = store.Get(id)
		if ok(got) {
			return got
		}
	}
	t.Fatalf("task %s: %+v", id, got)
	return got
}

// Brief 31-2 PR 2, rows A and 4 through the verbs: `scheduler run` over a
// squad.yaml of one seat (impl-1); a task pinned to the pool of a seat not
// yet in squad.yaml is refused by the router (not before). Then squad.yaml
// gains rev-1 (a Codex model no registry entry and no table chain names),
// and `scheduler relaunch --start STAMP` reaches the scheduler over its
// socket: the relaunch compiles squad.yaml again, the real roster plan
// (PlanRoster) finds rev-1, the launch's request for rev-1 carries the key
// press (its start and added_at) while impl-1's carries the relaunch
// decision's time, and a task pinned to rev-1's pool now
// dispatches to rev-1 (after). Reverted (no reload), the second launch has
// impl-1 only and the pinned task is refused again.
func TestARelaunchAfterASquadYAMLEditLaunchesTheNewSeatAndAdmitsItsPool(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(proj, ".agent-mail", "s1")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSquadProject(t, proj, fixtureSquadYAML, "implementer", "lead")
	for _, d := range []string{"wt-impl-1", filepath.Join(team.SeatWorktreesDir, "rev-1")} {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	rev := task.Pool{Runtime: "codex", Model: "gpt-9-nova", Effort: "high"}
	fake := &cliFakeLauncher{}
	ticks := make(chan []scheduler.Decision, 1000)
	prev := []any{schedulerAMQLauncher, schedulerSendRun, schedulerStarted, schedulerOnTick, schedulerDecideSeams}
	t.Cleanup(func() {
		schedulerAMQLauncher = prev[0].(func(*schedproc.Slots, string, seatlaunch.Options) scheduler.SeatLauncher)
		schedulerSendRun = prev[1].(func(context.Context, string, []string, []string) ([]byte, error))
		schedulerStarted = prev[2].(func(*scheduler.Scheduler))
		schedulerOnTick = prev[3].(func([]scheduler.Decision))
		schedulerDecideSeams = prev[4].(func(*schedproc.DecideOptions))
	})
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher {
		fake.sl = sl
		return fake
	}
	schedulerSendRun = func(context.Context, string, []string, []string) ([]byte, error) { return []byte("{}"), nil }
	schedulerStarted = func(s *scheduler.Scheduler) { fake.mu.Lock(); fake.s = s; fake.mu.Unlock() }
	schedulerOnTick = func(ds []scheduler.Decision) {
		select {
		case ticks <- ds:
		default:
		}
	}
	// The relaunch comes from the lead (this test process): R4's proof is
	// stood in for, as in the schedproc rows; cli
	// TestClearKeyIsProvedFromTheLeadsDescendant runs the real proof.
	lead := panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	schedulerDecideSeams = func(d *schedproc.DecideOptions) {
		d.LeadPane = func(string, string) (panechain.Pane, error) { return lead, nil }
		d.Reaches = func(_ int, _ string, panes []panechain.Pane) (*panechain.Pane, bool) { return &panes[0], true }
		d.StartToken = func(int) (string, bool) { return "s", true }
		d.Peer = func(*net.UnixConn) (int, bool) { return 4242, true }
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := captureOutput(t, func() error {
			return runSchedulerRun(ctx, []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
		})
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitDecision(t, ticks, "roster_launched")
	before, err := store.Add(task.AddInput{Title: "before", Class: "implement", Pool: &rev}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := waitTask(t, store, before.ID, func(x task.Task) bool { return x.State == task.StateBlocked }); got.Lease != nil || got.BlockReason != routing.BlockPinUnresolved {
		t.Fatalf("before the reload: %+v", got)
	}
	// squad.yaml gains rev-1, as team apply --add-seat writes it
	writeSquadProject(t, proj, fixtureSquadYAML+"  - handle: rev-1\n    role: implementer\n    runtime: codex\n    model: gpt-9-nova\n    effort: high\n")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if rec, _ := schedproc.ReadLaunchRecord(root); rec.DecisionSocket != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no decision socket")
		}
	}
	keyPress := time.Now().Add(-2 * time.Second).UTC().Format(time.RFC3339Nano)
	out, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"relaunch", "--root", root, "--reason", "add rev-1", "--start", keyPress})
	})
	if err != nil || !strings.Contains(out, "accepted") {
		t.Fatalf("relaunch: %v %s", err, out)
	}
	waitDecision(t, ticks, "roster_reloaded")
	after, err := store.Add(task.AddInput{Title: "after", Class: "implement", Pool: &rev}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := waitTask(t, store, after.ID, func(x task.Task) bool { return x.Lease != nil }); got.Lease.Holder != "rev-1" {
		t.Fatalf("after the reload: %+v", got)
	}
	if got, _ := store.Get(before.ID); got.State != task.StateBlocked {
		t.Fatalf("the task refused before the reload: %+v", got)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	var impl, revReq *scheduler.SeatRequest
	for i := range fake.reqs {
		switch fake.reqs[i].Handle {
		case "impl-1":
			impl = &fake.reqs[i]
		case "rev-1":
			revReq = &fake.reqs[i]
		}
	}
	pressed, _ := time.Parse(time.RFC3339Nano, keyPress)
	if revReq == nil || revReq.Pool != rev || !revReq.Start.Equal(pressed) || !revReq.AddedAt.Equal(pressed) ||
		impl == nil || !impl.Start.After(pressed) || time.Since(impl.Start) > time.Minute || !impl.AddedAt.IsZero() {
		t.Fatalf("the relaunch's requests: %+v", fake.reqs)
	}
	found := false
	for _, s := range fake.sl.Squad.Seats {
		found = found || s.Handle == "rev-1"
	}
	if !found {
		t.Fatalf("the slots' squad after the reload: %+v", fake.sl.Squad.Seats)
	}
}

// Codex P2 1 on 31-2 PR 2, the verb's half: `scheduler relaunch --start`
// refuses start_out_of_range by name, with nothing sent to the socket, for
// a stamp more than 5 s in the future or older than the launch record's
// start; an in-range stamp is sent, normalized to UTC. Reverted (the stamp
// only parsed), both are sent.
func TestSchedulerRelaunchBoundsItsStart(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("AM_ROOT", root)
	var sent []schedproc.RelaunchRequest
	prev := schedulerRelaunchSend
	t.Cleanup(func() { schedulerRelaunchSend = prev })
	schedulerRelaunchSend = func(_, _ string, req schedproc.RelaunchRequest) (schedproc.DecideReply, error) {
		sent = append(sent, req)
		return schedproc.DecideReply{OK: true, Detail: "accepted"}, nil
	}
	launch := time.Now().Add(-time.Hour).UTC()
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 1, StartedAt: launch, Start: &launch, DecisionSocket: "/sock"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []time.Time{time.Now().Add(time.Minute), launch.Add(-time.Second)} {
		_, _, err := captureOutput(t, func() error {
			return runScheduler([]string{"relaunch", "--start", bad.Format(time.RFC3339Nano)})
		})
		if ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), schedproc.RefusedStartOutOfRange) || len(sent) != 0 {
			t.Fatalf("start %s: %v, sent %+v", bad, err, sent)
		}
	}
	ok := time.Now().Add(-time.Second).In(time.FixedZone("x", 3600))
	if _, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"relaunch", "--start", ok.Format(time.RFC3339Nano)})
	}); err != nil || len(sent) != 1 || sent[0].Start != ok.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("an in-range start: %v, sent %+v", err, sent)
	}
}

// The plan reviewer's settled reading, the verb's half: the floor of
// `scheduler relaunch --start` is the start of the launch running now, the
// last relaunch's start from the scheduler's state when one ran. A press
// after the scheduler's launch start but before the last relaunch is
// refused start_out_of_range with nothing sent; one after it is sent.
// Reverted (the floor the launch record's start), the stale press is sent.
func TestSchedulerRelaunchStartFloorIsTheLastRelaunch(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("AM_ROOT", root)
	var sent []schedproc.RelaunchRequest
	prev := schedulerRelaunchSend
	t.Cleanup(func() { schedulerRelaunchSend = prev })
	schedulerRelaunchSend = func(_, _ string, req schedproc.RelaunchRequest) (schedproc.DecideReply, error) {
		sent = append(sent, req)
		return schedproc.DecideReply{OK: true, Detail: "accepted"}, nil
	}
	launch := time.Now().Add(-time.Hour).UTC()
	last := time.Now().Add(-10 * time.Second).UTC()
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 1, StartedAt: launch, Start: &launch, DecisionSocket: "/sock"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(scheduler.State{UpdatedAt: time.Now(), LastRelaunchAt: &last})
	if err := os.MkdirAll(filepath.Dir(scheduler.StatePathFor(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scheduler.StatePathFor(root), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"relaunch", "--start", last.Add(-5 * time.Second).Format(time.RFC3339Nano)})
	})
	if ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), schedproc.RefusedStartOutOfRange) || len(sent) != 0 {
		t.Fatalf("a press before the last relaunch: %v, sent %+v", err, sent)
	}
	if _, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"relaunch", "--start", time.Now().Add(-time.Second).Format(time.RFC3339Nano)})
	}); err != nil || len(sent) != 1 {
		t.Fatalf("a press after it: %v, sent %+v", err, sent)
	}
}
