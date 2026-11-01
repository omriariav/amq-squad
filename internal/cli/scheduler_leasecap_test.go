package cli

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// leaseCapSquad is lead (pi) and impl-1 under squad.yaml's max_seats 4: a
// lease cap of 2 at the start (two squad seats).
const leaseCapSquad = "schema: 1\nsession: s1\nlead: lead\nmax_seats: 4\nseats:\n" +
	"  - handle: lead\n    role: lead\n    runtime: pi\n" +
	"  - handle: impl-1\n    role: implementer\n    runtime: claude\n    model: sonnet\n    effort: medium\n    worktree: wt-impl-1\n"

const leaseCapAdded = "  - handle: rev-1\n    role: implementer\n    runtime: codex\n    model: gpt-9-nova\n    effort: high\n" +
	"  - handle: rev-2\n    role: implementer\n    runtime: claude\n    model: opus\n    effort: high\n"

var (
	capImpl = task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	capRev1 = task.Pool{Runtime: "codex", Model: "gpt-9-nova", Effort: "high"}
	capRev2 = task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}
)

// leaseCapRun starts `scheduler run` over leaseCapSquad with extra flags,
// adds rev-1 and rev-2 to squad.yaml once the roster launched, relaunches
// from the (stood-in) lead, and returns once the reload's tick is seen,
// with that tick's decisions.
func leaseCapRun(t *testing.T, extra ...string) (root string, store task.Store, fake *cliFakeLauncher, reloaded []scheduler.Decision) {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root = filepath.Join(proj, ".agent-mail", "s1")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSquadProject(t, proj, leaseCapSquad, "implementer", "lead")
	for _, d := range []string{"wt-impl-1", filepath.Join(team.SeatWorktreesDir, "rev-1"), filepath.Join(team.SeatWorktreesDir, "rev-2")} {
		if err := os.MkdirAll(filepath.Join(proj, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if store, err = task.Open(root); err != nil {
		t.Fatal(err)
	}
	fake = &cliFakeLauncher{}
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
			return runSchedulerRun(ctx, append([]string{"--project", proj, "--tick", "20ms"}, extra...), os.Stdout)
		})
		done <- err
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitDecision(t, ticks, "roster_launched")
	writeSquadProject(t, proj, leaseCapSquad+leaseCapAdded)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if rec, _ := schedproc.ReadLaunchRecord(root); rec.DecisionSocket != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no decision socket")
		}
	}
	if out, _, err := captureOutput(t, func() error {
		return runScheduler([]string{"relaunch", "--root", root, "--reason", "add rev-1 and rev-2"})
	}); err != nil || !strings.Contains(out, "accepted") {
		t.Fatalf("relaunch: %v %s", err, out)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ds := <-ticks:
			for _, d := range ds {
				if d.Kind == "roster_reloaded" {
					return root, store, fake, ds
				}
			}
		case <-deadline:
			t.Fatal("no roster_reloaded")
		}
	}
}

func leasedHolders(t *testing.T, store task.Store, ids []string, want int) map[string]string {
	t.Helper()
	got := map[string]string{}
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		got = map[string]string{}
		for _, id := range ids {
			if x, _ := store.Get(id); x.Lease != nil {
				got[id] = x.Lease.Holder
			}
		}
		if len(got) >= want {
			break
		}
	}
	return got
}

// Brief 31-2 PR 2, the lease cap through the verbs: `scheduler run` over a
// squad of two seats (lead and impl-1) under squad.yaml's max_seats 4 holds
// at most 2 leases at once. squad.yaml gains rev-1 and rev-2 and the lead
// relaunches: the reload reads the grant again and recomputes the cap from
// the new squad's four seats (bounded by max_seats 4), in the swap's one
// step, so three tasks, one pinned to each roster seat, hold leases at
// once and each added seat gets its lease. Reverted (the cap fixed at 2),
// the third task waits seat_cap.
func TestAReloadRecomputesTheLeaseCapFromTheNewSquad(t *testing.T) {
	_, store, _, reloaded := leaseCapRun(t)
	if !slices.ContainsFunc(reloaded, func(d scheduler.Decision) bool {
		return d.Kind == "roster_reloaded" && strings.Contains(d.Reason, "at most 4 leases at once")
	}) || slices.ContainsFunc(reloaded, func(d scheduler.Decision) bool { return d.Kind == scheduler.RosterOverCap }) {
		t.Fatalf("the reload's decisions: %+v", reloaded)
	}
	var ids []string
	for _, p := range []task.Pool{capImpl, capRev1, capRev2} {
		x, err := store.Add(task.AddInput{Title: "t " + p.Key(), Class: "implement", Pool: &p}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, x.ID)
	}
	got := leasedHolders(t, store, ids, 3)
	if got[ids[0]] != "impl-1" || got[ids[1]] != "rev-1" || got[ids[2]] != "rev-2" {
		t.Fatalf("leases at once: %v (want impl-1, rev-1, rev-2)", got)
	}
}

// Brief 31-2 PR 2, over the cap: the same add under `scheduler run
// --max-seats 2` (the narrowing the reload keeps). Every seat launches
// (the add is never refused), roster_over_cap is logged, and the state
// status reads carries 3 live seats over max_seats 2, which the screen
// shows as a warning line (pi screen.test.ts "over the lease cap"); two
// tasks hold leases and the third waits seat_cap.
func TestAReloadOverTheLeaseCapLaunchesEverySeatAndWarns(t *testing.T) {
	root, store, fake, reloaded := leaseCapRun(t, "--max-seats", "2")
	if !slices.ContainsFunc(reloaded, func(d scheduler.Decision) bool {
		return d.Kind == scheduler.RosterOverCap && d.Seat == "rev-2" && strings.Contains(d.Reason, "rev-2 launched, leases capped at 2")
	}) {
		t.Fatalf("no roster_over_cap: %+v", reloaded)
	}
	var ids []string
	for _, p := range []task.Pool{capImpl, capRev1, capRev2} {
		x, err := store.Add(task.AddInput{Title: "t " + p.Key(), Class: "implement", Pool: &p}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, x.ID)
	}
	if got := leasedHolders(t, store, ids, 2); len(got) != 2 {
		t.Fatalf("leases at once: %v", got)
	}
	time.Sleep(100 * time.Millisecond) // a few more ticks: the third still waits
	if third, _ := store.Get(ids[2]); third.Lease != nil || third.WaitReason != task.WaitSeatCap {
		t.Fatalf("the third task: %+v", third)
	}
	fake.mu.Lock()
	launched := map[string]bool{}
	for _, r := range fake.reqs {
		launched[r.Handle] = true
	}
	fake.mu.Unlock()
	if !launched["rev-1"] || !launched["rev-2"] {
		t.Fatalf("an added seat did not launch: %v", launched)
	}
	st, err := scheduler.ReadState(scheduler.StatePathFor(root))
	if err != nil || st.MaxSeats != 2 || st.LiveSeats != 3 {
		t.Fatalf("the state status reads: max %d, live %d (%v)", st.MaxSeats, st.LiveSeats, err)
	}
}
