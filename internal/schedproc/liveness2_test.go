package schedproc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The rows of the second fix round on the liveness ruling (v3-5-verifier).

// liveRig is a scheduler with the real Committer over stand-in steps, the
// fake launcher (a requested seat joins at once) and an exits ledger.
type liveRig struct {
	t     *testing.T
	root  string
	store task.Store
	s     *scheduler.Scheduler
	c     *Committer
	l     *fakeLauncher
	now   time.Time
}

func newLiveRig(t *testing.T, c *Committer) *liveRig {
	t.Helper()
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	r := &liveRig{t: t, root: root, store: store, c: c, l: &fakeLauncher{}, now: time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)}
	r.restart()
	return r
}

// restart is a new scheduler incarnation on the same store and exits ledger.
func (r *liveRig) restart() {
	r.s = &scheduler.Scheduler{Store: r.store, Now: func() time.Time { return r.now }, Launcher: r.l, Committer: r.c, Exits: r.c,
		Config: scheduler.Config{ExitsPath: filepath.Join(r.root, "exits.json"), MaxCommitDeferrals: 3}}
	r.l.mu.Lock()
	r.l.s = r.s
	r.l.mu.Unlock()
}

// tick runs one tick with a deadline (a tick that waits fails the row).
func (r *liveRig) tick(onHang func()) ([]scheduler.Decision, error) {
	r.t.Helper()
	type out struct {
		d   []scheduler.Decision
		err error
	}
	done := make(chan out, 1)
	go func() {
		d, err := r.s.Tick()
		done <- out{d, err}
	}()
	select {
	case o := <-done:
		return o.d, o.err
	case <-time.After(10 * time.Second):
		onHang()
		<-done
		r.t.Fatal("the tick waited")
		return nil, nil
	}
}

// reported dispatches a new task to a fresh seat and files its done report.
func (r *liveRig) reported(title string) (task.Task, string) {
	r.t.Helper()
	tk, err := r.store.Add(task.AddInput{Title: title, Class: "summarize", DeclaredFiles: []string{title + ".go"}, Pool: &sonnet}, r.now)
	if err != nil {
		r.t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if got, _ := r.store.Get(tk.ID); got.Lease != nil {
			h := got.Lease.Holder
			if _, err := r.store.Claim(tk.ID, h, nil, time.Hour, r.now); err != nil {
				r.t.Fatal(err)
			}
			if _, err := r.store.Done(tk.ID, h, "", "", r.now); err != nil {
				r.t.Fatal(err)
			}
			return tk, h
		}
		if _, err := r.tick(func() {}); err != nil {
			r.t.Fatal(err)
		}
	}
	r.t.Fatal("the task was never dispatched")
	return tk, ""
}

func (r *liveRig) exitsRaw() string {
	raw, _ := os.ReadFile(filepath.Join(r.root, "exits.json"))
	return string(raw)
}

// P1-1: nothing on the tick waits for a running step's ledger. A 10-minute
// stand-in holds seat A's step; A's seat is gone, and its exit check (which
// would wait on the ledger that step holds) is deferred without touching it;
// B is dispatched meanwhile.
func TestTickNeverWaitsOnARunningStepsSeat(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	c := &Committer{Inline: 20 * time.Millisecond,
		Step: func(ctx context.Context, _ string, _ task.Task) seatcommit.Result {
			select {
			case <-release:
			case <-ctx.Done():
			case <-time.After(10 * time.Minute):
			}
			return seatcommit.Result{OK: true}
		},
		ExitStep: func(string, string, string) seatcommit.Result {
			<-release // the ledger flock the running step holds
			return seatcommit.Result{OK: true}
		}}
	r := newLiveRig(t, c)
	_, seatA := r.reported("a")
	if _, err := r.tick(func() { close(release) }); err != nil { // the step starts and runs
		t.Fatal(err)
	}
	r.s.Post(scheduler.Event{Kind: scheduler.SeatGone, At: r.now, Handle: seatA})
	b, err := r.store.Add(task.AddInput{Title: "b", Class: "summarize", Pool: &sonnet}, r.now)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := false
	for i := 0; i < 3 && !dispatched; i++ {
		if _, err := r.tick(func() { close(release) }); err != nil {
			t.Fatal(err)
		}
		got, _ := r.store.Get(b.ID)
		dispatched = got.Lease != nil
	}
	if !dispatched {
		t.Fatal("B was not dispatched while A's step ran")
	}
}

// P2-1: a task cancelled while its step runs: the step is cancelled, its
// result discarded, its entry dropped, and the seat leases again within two
// ticks.
func TestCancelDuringARunningStepFreesTheSeat(t *testing.T) {
	var cancelled atomic.Bool
	c := &Committer{Inline: 20 * time.Millisecond,
		Step: func(ctx context.Context, _ string, _ task.Task) seatcommit.Result {
			select {
			case <-ctx.Done():
				cancelled.Store(true)
				return seatcommit.Result{Deferred: true, ReasonCode: seatcommit.ReasonStepCancelled}
			case <-time.After(10 * time.Minute):
				return seatcommit.Result{OK: true}
			}
		},
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := newLiveRig(t, c)
	a, seatA := r.reported("a")
	if _, err := r.tick(func() {}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.store.Cancel(a.ID, "lead", "no longer needed", r.now); err != nil {
		t.Fatal(err)
	}
	next, err := r.store.Add(task.AddInput{Title: "next", Class: "summarize", Pool: &sonnet}, r.now)
	if err != nil {
		t.Fatal(err)
	}
	leased := ""
	for i := 0; i < 2 && leased == ""; i++ {
		if _, err := r.tick(func() {}); err != nil {
			t.Fatal(err)
		}
		if got, _ := r.store.Get(next.ID); got.Lease != nil {
			leased = got.Lease.Holder
		}
	}
	if leased != seatA || !cancelled.Load() {
		t.Fatalf("after the cancel: the next task leased to %q (want %s), the step cancelled %v", leased, seatA, cancelled.Load())
	}
	if strings.Contains(r.exitsRaw(), `"why": "commit"`) {
		t.Fatal("the cancelled step's entry stayed")
	}
}

// P2-3: a decided result is kept until the store transaction commits: a
// transient CompleteBound failure retries the application at the next tick,
// never the step (one step, one push).
func TestFailedApplicationRetriesTheApplicationNotTheStep(t *testing.T) {
	var steps atomic.Int32
	c := &Committer{Inline: time.Second,
		Step: func(context.Context, string, task.Task) seatcommit.Result {
			steps.Add(1) // the step's push and PR stand-in
			return seatcommit.Result{OK: true}
		},
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := newLiveRig(t, c)
	a, _ := r.reported("a")
	// The store refuses the next write once (its directory read-only).
	dir := task.DirFor(r.root)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := r.tick(func() {})
	os.Chmod(dir, 0o700)
	if err == nil {
		t.Skip("the store accepted a write into a read-only directory (running as root)")
	}
	if steps.Load() != 1 {
		t.Fatalf("the first tick did not reach the step (%d steps)", steps.Load())
	}
	if _, err := r.tick(func() {}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.store.Get(a.ID)
	if got.State != task.StateCompleted || steps.Load() != 1 {
		t.Fatalf("after the retried application: %s, %d steps", got.State, steps.Load())
	}
}

// P2-4: the deferral count lives in the step's exits entry: two restarts
// keep it, and at the limit the task blocks verifier_interrupted for the
// lead, so the lead's reset applies.
func TestDeferralCountSurvivesRestarts(t *testing.T) {
	c := &Committer{Inline: time.Second,
		Step: func(context.Context, string, task.Task) seatcommit.Result {
			return seatcommit.Result{Deferred: true, ReasonCode: seatcommit.ReasonVerifierInterrupted, Detail: "interrupted"}
		},
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := newLiveRig(t, c)
	a, _ := r.reported("a")
	for i := 0; i < 3; i++ {
		if i > 0 {
			r.restart()
		}
		if _, err := r.tick(func() {}); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := r.store.Get(a.ID)
	if got.State != task.StateBlocked || got.BlockReason != seatcommit.ReasonVerifierInterrupted || got.BlockRoute != task.RouteLead {
		t.Fatalf("after three deferrals across two restarts: %s %s %s", got.State, got.BlockReason, got.BlockRoute)
	}
	if _, err := r.store.Reset(a.ID, nil, r.now); err != nil {
		t.Fatalf("the lead's reset: %v", err)
	}
}

// The small item: the step's exits entry is written before the step starts.
func TestCommitEntryIsWrittenBeforeTheStepStarts(t *testing.T) {
	var r *liveRig
	var sawEntry atomic.Bool
	c := &Committer{Inline: time.Second,
		Step: func(context.Context, string, task.Task) seatcommit.Result {
			sawEntry.Store(strings.Contains(r.exitsRaw(), `"why": "commit"`))
			return seatcommit.Result{OK: true}
		},
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r = newLiveRig(t, c)
	r.reported("a")
	if _, err := r.tick(func() {}); err != nil {
		t.Fatal(err)
	}
	if !sawEntry.Load() {
		t.Fatal("the step started before its exits entry was written")
	}
}
