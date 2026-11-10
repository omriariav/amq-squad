package cli

import (
	"errors"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
)

func withDispatchSchedulerLive(t *testing.T, live bool) *int {
	t.Helper()
	previous := taskSchedulerLive
	calls := 0
	taskSchedulerLive = func(string) bool {
		calls++
		return live
	}
	t.Cleanup(func() { taskSchedulerLive = previous })
	return &calls
}

type dispatchCancelLauncher struct{}

func (dispatchCancelLauncher) RequestSeat(scheduler.SeatRequest) error       { return nil }
func (dispatchCancelLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (dispatchCancelLauncher) CloseSeat(string, string) error                { return nil }

type dispatchExitOK struct{}

func (dispatchExitOK) CheckExit(string, string, string) scheduler.ExitResult {
	return scheduler.ExitResult{OK: true}
}

type dispatchCancelCleaner struct{ err error }

func (*dispatchCancelCleaner) PrepareSeat(string) error { return nil }
func (c *dispatchCancelCleaner) CleanAttempt(string, string, int, string) error {
	return c.err
}

type dispatchGoneLiveness struct{}

func (dispatchGoneLiveness) SeatProcess(string, string) (bool, string, error) {
	return true, "the test seat is gone", nil
}

// Codex's P2 on the #909 re-pass (exits.go:375, dispatch.go:288): the
// scheduler's superseded shortcut assumes the boundary before any later
// lease archived and cleaned the seat, and the lead's dispatch granted
// with store.Grant directly. The dispatch now refuses a seat whose
// cancelled attempt is not cleaned yet, before it creates or leases
// anything and before any AMQ send; once the clean is recorded, the same
// dispatch leases. The refusal is the plan reviewer's exact words. Reverted (no check), the lease is granted over the
// cancelled attempt's worktree.
func TestDispatchRefusesASeatWhoseCancelledAttemptIsNotCleaned(t *testing.T) {
	livenessCalls := withDispatchSchedulerLive(t, true)
	now := time.Now().UTC()
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	store := dispatchTestStore(t, dir, "issue-96")
	mustTask(t, store, "cancelled on qa")
	if _, err := store.Grant("t1", "qa", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "qa", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cancel("t1", "cto", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to qa\n")
	dispatch := func() error {
		_, _, err := captureOutput(t, func() error {
			return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Next", "--body", "run", "--create-task", "--task-class", "script", "--json"})
		})
		return err
	}
	err := dispatch()
	if err == nil || err.Error() != "seat qa holds an uncleaned cancelled attempt of t1; run seat clean --seat qa first" {
		t.Fatalf("dispatch over an uncleaned cancellation: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused dispatch sent AMQ: %d calls", len(*calls))
	}
	if list, err := store.List(); err != nil || len(list) != 1 {
		t.Fatalf("a refused dispatch created a task: %d tasks, %v", len(list), err)
	}
	if _, err := store.MarkAttemptCleaned("t1", 1, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := dispatch(); err != nil {
		t.Fatalf("dispatch after the clean is recorded: %v", err)
	}
	got, err := store.Get("t2")
	if err != nil || got.Lease == nil || got.Lease.Holder != "qa" {
		t.Fatalf("the dispatch after the clean did not lease t2 to qa: %+v, %v", got.Lease, err)
	}
	if *livenessCalls != 2 {
		t.Fatalf("scheduler liveness reads = %d, want one per dispatch", *livenessCalls)
	}
}

// The plan reviewer's condition (1) on #909: the uncleaned check is inside
// the grant's store mutation, not only the dispatch's earlier read. qa
// holds X when the dispatch of Y reads the store (the early refusal
// passes: nothing on qa is cancelled); X is cancelled after that read and
// before the grant (the taskNow seam, which the dispatch calls for the
// grant's time); the grant's own mutation sees the uncleaned cancellation
// and refuses in the reviewer's words. Reverted (the separate pre-check
// only), Y is granted over X's uncleaned attempt.
func TestDispatchRefusesACancellationBetweenItsReadAndItsGrant(t *testing.T) {
	livenessCalls := withDispatchSchedulerLive(t, true)
	now := time.Now().UTC()
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	store := dispatchTestStore(t, dir, "issue-96")
	mustTask(t, store, "X on qa")
	if _, err := store.Grant("t1", "qa", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "qa", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	mustTask(t, store, "Y")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to qa\n")
	prevNow := taskNow
	cancelled := false
	taskNow = func() time.Time {
		if !cancelled {
			cancelled = true
			if _, err := store.Cancel("t1", "cto", "stop", now.Add(time.Second)); err != nil {
				t.Errorf("the cancel in the window: %v", err)
			}
		}
		return now.Add(2 * time.Second)
	}
	t.Cleanup(func() { taskNow = prevNow })
	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Y", "--body", "run", "--task", "t2"})
	})
	if !cancelled || err == nil || err.Error() != "seat qa holds an uncleaned cancelled attempt of t1; run seat clean --seat qa first" {
		t.Fatalf("cancelled=%v err=%v", cancelled, err)
	}
	if got, _ := store.Get("t2"); got.Lease != nil {
		t.Fatalf("Y was granted over X's uncleaned attempt: %+v", got.Lease)
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused dispatch sent AMQ: %d calls", len(*calls))
	}
	if *livenessCalls != 1 {
		t.Fatalf("scheduler liveness reads = %d, want 1", *livenessCalls)
	}
}

// With no live scheduler, dispatch keeps the compatibility grant. The next
// scheduler sees the later lease, drops the queued cancellation without
// closing or cleaning it, and records the current 3.2.x CleanedAt behavior.
func TestDispatchWithNoLiveSchedulerKeepsTheCompatGrant(t *testing.T) {
	livenessCalls := withDispatchSchedulerLive(t, false)
	now := time.Now().UTC()
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	store := dispatchTestStore(t, dir, "issue-96")
	mustTask(t, store, "cancelled on qa")
	if _, err := store.Grant("t1", "qa", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim("t1", "qa", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cancel("t1", "cto", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	mustTask(t, store, "compat task")
	previousNow := taskNow
	taskNow = func() time.Time { return now.Add(2 * time.Second) }
	t.Cleanup(func() { taskNow = previousNow })
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to qa\n")
	if _, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "compat", "--body", "run", "--task", "t2"})
	}); err != nil {
		t.Fatalf("compat dispatch: %v", err)
	}
	if *livenessCalls != 1 || len(*calls) != 1 {
		t.Fatalf("liveness reads=%d sends=%d", *livenessCalls, len(*calls))
	}
	if got, _ := store.Get("t2"); got.Lease == nil || got.Lease.Holder != "qa" {
		t.Fatalf("compat dispatch did not grant t2: %+v", got.Lease)
	}
	if got, _ := store.Get("t1"); got.Attempts[0].CleanedAt != nil {
		t.Fatalf("compat dispatch recorded the cancellation early: %+v", got.Attempts[0])
	}

	s, err := scheduler.New(scheduler.Options{
		Store: store, Now: func() time.Time { return now.Add(3 * time.Second) },
		Launcher: dispatchCancelLauncher{}, Exits: dispatchExitOK{}, Preparer: &dispatchCancelCleaner{},
		Config: scheduler.Config{IdleTimeout: time.Hour}, Incarnation: "compatdrop",
	})
	if err != nil {
		t.Fatal(err)
	}
	var all []scheduler.Decision
	for range 2 {
		d, err := s.Tick()
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, d...)
	}
	wantReason := "seat qa holds a later lease (t2); the queued item for t1 attempt 1 is dropped, and nothing is closed or cleaned for it"
	superseded := 0
	for _, d := range all {
		if d.Kind == "seat_clean_superseded" {
			superseded++
			if d.Reason != wantReason {
				t.Fatalf("superseded reason = %q", d.Reason)
			}
		}
		if d.Kind == "seat_closing" || d.Kind == "seat_closed" || d.Kind == "seat_cleaned" {
			t.Fatalf("compat drop acted on t1: %+v", all)
		}
	}
	if superseded != 1 {
		t.Fatalf("superseded decisions = %d: %+v", superseded, all)
	}
	if got, _ := store.Get("t1"); got.Attempts[0].CleanedAt == nil {
		t.Fatalf("current superseded behavior did not record CleanedAt: %+v", got.Attempts[0])
	}
}

// A live scheduler makes dispatch follow the cancel-clean lifecycle: before
// a tick and after a failed clean it refuses in the exact words, while a
// completed clean permits the next task.
func TestDispatchWithLiveSchedulerFollowsCancelCleaning(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cleanErr   error
		runTick    bool
		wantRefuse bool
	}{
		{name: "after cancel before tick", wantRefuse: true},
		{name: "after failed clean", runTick: true, cleanErr: errors.New("clean failed"), wantRefuse: true},
		{name: "after successful clean", runTick: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			livenessCalls := withDispatchSchedulerLive(t, true)
			now := time.Now().UTC()
			dir := t.TempDir()
			chdir(t, dir)
			writeDispatchTeam(t, dir)
			store := dispatchTestStore(t, dir, "issue-96")
			mustTask(t, store, "cancelled on qa")
			if _, err := store.Grant("t1", "qa", nil, time.Hour, now); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Claim("t1", "qa", nil, time.Hour, now); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Cancel("t1", "cto", "stop", now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if tc.runTick {
				s, err := scheduler.New(scheduler.Options{
					Store: store, Now: func() time.Time { return now.Add(2 * time.Second) },
					Launcher: dispatchCancelLauncher{}, Exits: dispatchExitOK{},
					Preparer: &dispatchCancelCleaner{err: tc.cleanErr}, Liveness: dispatchGoneLiveness{},
					Config: scheduler.Config{IdleTimeout: time.Hour}, Incarnation: "liveclean",
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.Tick(); err != nil {
					t.Fatal(err)
				}
			}
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to qa\n")
			_, _, err := captureOutput(t, func() error {
				return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "next", "--body", "run", "--create-task", "--task-class", "script"})
			})
			if tc.wantRefuse {
				want := "seat qa holds an uncleaned cancelled attempt of t1; run seat clean --seat qa first"
				if err == nil || err.Error() != want || len(*calls) != 0 {
					t.Fatalf("dispatch = %v, sends=%d", err, len(*calls))
				}
				if list, listErr := store.List(); listErr != nil || len(list) != 1 {
					t.Fatalf("refused dispatch changed tasks: %d, %v", len(list), listErr)
				}
			} else {
				if err != nil || len(*calls) != 1 {
					t.Fatalf("dispatch after clean = %v, sends=%d", err, len(*calls))
				}
				if got, getErr := store.Get("t2"); getErr != nil || got.Lease == nil || got.Lease.Holder != "qa" {
					t.Fatalf("task after clean = %+v, %v", got.Lease, getErr)
				}
			}
			if *livenessCalls != 1 {
				t.Fatalf("scheduler liveness reads = %d, want 1", *livenessCalls)
			}
		})
	}
}

func TestDispatchWithLiveSchedulerReturnsTheReservationRefusalUnwrapped(t *testing.T) {
	withDispatchSchedulerLive(t, true)
	now := time.Now().UTC()
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	store := dispatchTestStore(t, dir, "issue-96")
	mustTask(t, store, "cancelled on qa")
	if _, err := store.Grant("t1", "qa", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cancel("t1", "cto", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	mustTask(t, store, "next")
	later, nonce, _, err := store.ReserveCancel("qa", "t1", 1, now.Add(2*time.Second))
	if err != nil || later != "" || nonce == "" {
		t.Fatalf("reserve = %q, %q, %v", later, nonce, err)
	}
	t.Cleanup(func() { _ = store.ReleaseSeat("qa", nonce) })
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg to qa\n")
	_, _, err = captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "next", "--body", "run", "--task", "t2"})
	})
	want := "seat qa is being closed and cleaned; dispatch again after it finishes"
	if err == nil || err.Error() != want || len(*calls) != 0 {
		t.Fatalf("reserved dispatch = %v, sends=%d", err, len(*calls))
	}
	if got, _ := store.Get("t2"); got.State != taskstore.StatePending || got.Lease != nil {
		t.Fatalf("reserved dispatch leased t2: %+v", got)
	}
}
