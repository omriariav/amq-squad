package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

type cliFakeLauncher struct {
	mu   sync.Mutex
	s    *scheduler.Scheduler
	reqs []scheduler.SeatRequest
	sl   *schedproc.Slots
}

func (f *cliFakeLauncher) RequestSeat(r scheduler.SeatRequest) error {
	q, err := f.sl.Plan(r) // the real slot choice
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	s := f.s
	f.mu.Unlock()
	s.Post(scheduler.Event{Kind: scheduler.SeatUp, At: time.Now(), Handle: q.Seats[0].Handle, Pool: r.Pool, RequestID: r.ID})
	return nil
}
func (f *cliFakeLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (f *cliFakeLauncher) CloseSeat(string, string) error                { return nil }

// Ruling 15 through the verb: `amq-squad scheduler run` starts on a seeded
// store, takes the seat from the typed seat list, dispatches to it through
// the (fake) launcher, rings it with an AMQ message from the scheduler
// mailbox, stops starting seats on a usage-limit event, and returns on
// cancel; the state file names the lead.
func TestSchedulerRunVerbDispatchesRingsAndStops(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(proj, ".agent-mail", "s1")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(proj, "roles", "impl.md"), "# Role: implementer\n\nYou implement tasks.\n")
	seats := map[string]any{"project_root": proj, "session": "s1", "seats": []map[string]any{
		{"handle": "impl-1", "runtime": "claude", "model": "sonnet", "effort": "medium", "role": "implementer", "role_file": filepath.Join(proj, "roles", "impl.md"), "worktree": filepath.Join(proj, "wt-impl-1")},
	}}
	raw, _ := json.Marshal(seats)
	seatsFile := filepath.Join(proj, "seats.json")
	mustWrite(t, seatsFile, string(raw))
	if err := os.MkdirAll(filepath.Join(proj, "wt-impl-1"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, _ := task.Open(root)
	pool := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	if _, err := store.Add(task.AddInput{Title: "first", Class: "implement", Pool: &pool}, time.Now()); err != nil {
		t.Fatal(err)
	}
	fake := &cliFakeLauncher{}
	var sendMu sync.Mutex
	var sends [][]string
	limited := make(chan struct{})
	var once sync.Once
	ticks := make(chan []scheduler.Decision, 1000)
	prev := []any{schedulerAMQLauncher, schedulerLimits, schedulerSendRun, schedulerStarted, schedulerOnTick}
	t.Cleanup(func() {
		schedulerAMQLauncher = prev[0].(func(*schedproc.Slots, string, seatlaunch.Options) scheduler.SeatLauncher)
		schedulerLimits, _ = prev[1].(scheduler.UsageLimitSource)
		schedulerSendRun = prev[2].(func(context.Context, string, []string, []string) ([]byte, error))
		schedulerStarted = prev[3].(func(*scheduler.Scheduler))
		schedulerOnTick = prev[4].(func([]scheduler.Decision))
	})
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher {
		fake.sl = sl
		return fake
	}
	schedulerLimits = scheduler.UsageLimitFunc(func(time.Time) ([]scheduler.UsageLimitEvent, error) {
		select {
		case <-limited:
			var out []scheduler.UsageLimitEvent
			once.Do(func() { out = []scheduler.UsageLimitEvent{{At: time.Now(), Provider: "claude", Source: "test"}} })
			return out, nil
		default:
			return nil, nil
		}
	})
	schedulerSendRun = func(_ context.Context, _ string, args, env []string) ([]byte, error) {
		if args[0] == "drain" {
			return nil, nil // the per-tick drain of the lead's decisions: none
		}
		sendMu.Lock()
		sends = append(sends, append(append([]string(nil), args...), envValue(env, "AM_ME"), envValue(env, "AM_ROOT")))
		sendMu.Unlock()
		return []byte("{}"), nil
	}
	schedulerStarted = func(s *scheduler.Scheduler) { fake.mu.Lock(); fake.s = s; fake.mu.Unlock() }
	schedulerOnTick = func(ds []scheduler.Decision) { ticks <- ds }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := captureOutput(t, func() error {
			return runSchedulerRun(ctx, []string{"--seats", seatsFile, "--lead", "lead", "--tick", "20ms"}, os.Stdout)
		})
		done <- err
	}()
	waitDecision(t, ticks, "dispatched")
	close(limited)
	waitDecision(t, ticks, "usage_limit")
	if _, err := store.Add(task.AddInput{Title: "second", Class: "implement", Pool: &pool}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if got, _ := store.Get("t2"); got.WaitReason == task.WaitUsageLimit || time.Now().After(deadline) {
			break
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got, _ := store.Get("t2"); got.WaitReason != task.WaitUsageLimit {
		t.Fatalf("t2 = %+v", got)
	}
	fake.mu.Lock()
	if len(fake.reqs) != 1 {
		t.Fatalf("seat requests: %+v", fake.reqs)
	}
	fake.mu.Unlock()
	sendMu.Lock()
	defer sendMu.Unlock()
	if len(sends) != 1 || !strings.Contains(strings.Join(sends[0], " "), "send --me scheduler --to impl-1 --kind todo --subject task t1: first") ||
		sends[0][len(sends[0])-2] != "scheduler" || sends[0][len(sends[0])-1] != root {
		t.Fatalf("doorbell sends: %q", sends)
	}
	if st, _ := scheduler.ReadState(scheduler.StatePathFor(root)); st.Lead != "lead" {
		t.Fatalf("state: %+v", st)
	}
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// waitDecision waits for a decision of that kind; an unknown kind waits for
// the next tick.
func waitDecision(t *testing.T, ticks <-chan []scheduler.Decision, kind string) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ds := <-ticks:
			if !strings.Contains(kind, "_or_tick") {
				for _, d := range ds {
					if d.Kind == kind {
						return
					}
				}
				continue
			}
			return
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}
