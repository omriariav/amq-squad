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

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
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

// LaunchRoster is the roster launch (3.0.1 t16) through the real roster
// plan: every planned seat comes up with its request id.
func (f *cliFakeLauncher) LaunchRoster(reqs []scheduler.SeatRequest) scheduler.RosterOutcome {
	q, err := f.sl.PlanRoster(reqs)
	if err != nil {
		return scheduler.RosterOutcome{Stage: "launch_error", Detail: err.Error()}
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, reqs...)
	s := f.s
	f.mu.Unlock()
	out := scheduler.RosterOutcome{Stage: scheduler.RosterApplied}
	for i, seat := range q.Seats {
		if s == nil {
			continue // a fixture that never wires the scheduler in
		}
		s.Post(scheduler.Event{Kind: scheduler.SeatUp, At: time.Now(), Handle: seat.Handle, Pool: reqs[i].Pool, RequestID: reqs[i].ID})
		out.Seats = append(out.Seats, scheduler.RosterSeatState{Handle: seat.Handle, Up: true})
	}
	return out
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

// bindRun is one `scheduler run` over a seat repository (seatRepo: seat s1,
// a bare origin, the owner's merge grant of o/r unless noGrant) through the
// fake launcher and a fake gh that logs its calls: the seat is dispatched
// t1, reports done, and the scheduler's commit step runs. It returns the
// task after the step, the start line, the fake gh's calls and the seat's
// head.
func bindRun(t *testing.T, noGrant bool, extra ...string) (task.Task, string, string, string) {
	t.Helper()
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(proj, ".agent-mail", "s1")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	repo, wt := seatRepo(t, root)
	if noGrant {
		// No owner grants file: the defaults, no merge grant; the launch
		// records the default target (no repository, no push).
		if err := os.Remove(filepath.Join(home, ".amq-squad", "grants.yaml")); err != nil {
			t.Fatal(err)
		}
		if _, err := seatcommit.RecordWorktreeBase("", root, "s1", wt, "seat/s1", seatcommit.Target{BaseBranch: "main"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(t, filepath.Join(proj, "roles", "impl.md"), "# Role: implementer\n\nYou implement tasks.\n")
	seats := map[string]any{"project_root": proj, "session": "s1", "seats": []map[string]any{
		{"handle": "s1", "runtime": "claude", "model": "sonnet", "effort": "medium", "role": "implementer", "role_file": filepath.Join(proj, "roles", "impl.md"), "worktree": wt, "branch": "seat/s1"},
	}}
	raw, _ := json.Marshal(seats)
	seatsFile := filepath.Join(proj, "seats.json")
	mustWrite(t, seatsFile, string(raw))
	ghLog := filepath.Join(t.TempDir(), "gh.log")
	gh := filepath.Join(t.TempDir(), "gh")
	mustWrite(t, gh, "#!/bin/sh\necho \"$*\" >> '"+ghLog+"'\nif [ \"$2\" = list ]; then echo '[]'; exit 0; fi\necho https://github.com/o/r/pull/7\n")
	os.Chmod(gh, 0o755)
	store, _ := task.Open(root)
	pool := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	if _, err := store.Add(task.AddInput{Title: "bind", Class: "implement", Pool: &pool, DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now()); err != nil {
		t.Fatal(err)
	}
	fake := &cliFakeLauncher{}
	ticks := make(chan []scheduler.Decision, 1000)
	prev := []any{schedulerAMQLauncher, schedulerLimits, schedulerSendRun, schedulerStarted, schedulerOnTick, seatCommitGh}
	t.Cleanup(func() {
		schedulerAMQLauncher = prev[0].(func(*schedproc.Slots, string, seatlaunch.Options) scheduler.SeatLauncher)
		schedulerLimits, _ = prev[1].(scheduler.UsageLimitSource)
		schedulerSendRun = prev[2].(func(context.Context, string, []string, []string) ([]byte, error))
		schedulerStarted = prev[3].(func(*scheduler.Scheduler))
		schedulerOnTick = prev[4].(func([]scheduler.Decision))
		seatCommitGh = prev[5].(string)
	})
	seatCommitGh = gh
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher {
		fake.sl = sl
		return fake
	}
	schedulerLimits = scheduler.UsageLimitFunc(func(time.Time) ([]scheduler.UsageLimitEvent, error) { return nil, nil })
	schedulerSendRun = func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		if args[0] == "drain" {
			return nil, nil
		}
		return []byte("{}"), nil
	}
	schedulerStarted = func(s *scheduler.Scheduler) { fake.mu.Lock(); fake.s = s; fake.mu.Unlock() }
	schedulerOnTick = func(ds []scheduler.Decision) {
		select {
		case ticks <- ds:
		default:
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var stdout string
	go func() {
		o, _, err := captureOutput(t, func() error {
			return runSchedulerRun(ctx, append([]string{"--seats", seatsFile, "--lead", "lead", "--tick", "20ms"}, extra...), os.Stdout)
		})
		stdout = o
		done <- err
	}()
	waitDecision(t, ticks, "dispatched")
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// bound\n")
	harnessPass(t, store, "t1", time.Now())
	if _, err := store.Done("t1", "s1", "ok", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	var got task.Task
	for deadline := time.Now().Add(15 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if got, _ = store.Get("t1"); got.State == task.StateCompleted || got.State == task.StateBlocked || time.Now().After(deadline) {
			break
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(ghLog)
	return got, stdout, string(calls), gitT(t, wt, "rev-parse", "HEAD")
}

// 3.0.1 t14: under a merge grant the scheduler's commit step opens (or
// finds) the pushed seat branch's draft PR and binds it to the task with no
// flag passed: the task completes with pr_number and pr_head, the train's
// input (task_unbound otherwise).
func TestSchedulerCommitStepBindsByDefaultUnderAMergeGrant(t *testing.T) {
	got, out, calls, head := bindRun(t, false)
	if got.State != task.StateCompleted || got.PRNumber != 7 || got.PRHead != head || got.PRRepository != "o/r" {
		t.Fatalf("binding: state %s pr %d head %q repo %q (want completed, 7, %s, o/r)\n%s", got.State, got.PRNumber, got.PRHead, got.PRRepository, head, out)
	}
	if !strings.Contains(calls, "pr list --head seat/s1") || !strings.Contains(calls, "pr create") {
		t.Fatalf("gh calls:\n%s", calls)
	}
	if !strings.Contains(out, "PR binding on (merge grant o/r)") {
		t.Fatalf("start line:\n%s", out)
	}
}

// Without a merge grant there is no PR to open: binding is off, and the
// compatibility --open-pr does not turn it on (no gh call; the task
// completes unbound with the commit, not pushed).
func TestSchedulerCommitStepOpensNoPRWithoutAMergeGrant(t *testing.T) {
	got, out, calls, _ := bindRun(t, true, "--open-pr")
	if got.State != task.StateCompleted || got.PRNumber != 0 || got.PRHead != "" {
		t.Fatalf("without a grant: state %s pr %d head %q\n%s", got.State, got.PRNumber, got.PRHead, out)
	}
	if calls != "" {
		t.Fatalf("gh called without a merge grant:\n%s", calls)
	}
	if !strings.Contains(out, "PR binding off (no merge grant)") {
		t.Fatalf("start line:\n%s", out)
	}
}

// --no-open-pr under a merge grant is refused with the reason, before the
// scheduler starts (no state file); without a grant it is accepted.
func TestSchedulerNoOpenPRRefusedUnderAMergeGrant(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	ownerGrant(t, filepath.Join(home, "origin.git"))
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(proj, ".agent-mail", "s1")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(proj, "roles", "impl.md"), "# Role: implementer\n\nYou implement tasks.\n")
	seats := map[string]any{"project_root": proj, "session": "s1", "seats": []map[string]any{
		{"handle": "s1", "runtime": "claude", "model": "sonnet", "effort": "medium", "role": "implementer", "role_file": filepath.Join(proj, "roles", "impl.md"), "worktree": filepath.Join(proj, "wt-s1")},
	}}
	raw, _ := json.Marshal(seats)
	seatsFile := filepath.Join(proj, "seats.json")
	mustWrite(t, seatsFile, string(raw))
	// Bounded: a scheduler that is not refused starts and returns nil here.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(ctx, []string{"--seats", seatsFile, "--lead", "lead", "--no-open-pr"}, os.Stdout)
	})
	if err == nil || !strings.Contains(err.Error(), "o/r has a merge grant") || !strings.Contains(err.Error(), "merge train needs each completed task bound to its PR") {
		t.Fatalf("--no-open-pr under a merge grant: %v", err)
	}
	if _, serr := os.Stat(scheduler.StatePathFor(root)); serr == nil {
		t.Fatal("the refused scheduler wrote its state")
	}
	// Without a grant the off-switch is the choice the start line prints.
	if err := os.Remove(filepath.Join(home, ".amq-squad", "grants.yaml")); err != nil {
		t.Fatal(err)
	}
	g, _ := grants.Load(proj)
	on, binding, err := commitStepOpensPR(g, true)
	if on || err != nil || binding != "off (--no-open-pr)" {
		t.Fatalf("--no-open-pr without a grant: %v %q %v", on, binding, err)
	}
}
