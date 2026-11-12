package scheduler

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

type attemptArchiveRecorder struct{ calls []task.Attempt }

func (*attemptArchiveRecorder) PrepareSeat(string) error { return nil }
func (r *attemptArchiveRecorder) ArchiveAttempt(_ string, _ task.Task, a task.Attempt) error {
	r.calls = append(r.calls, a)
	return nil
}

type attemptCleanRecorder struct {
	cleaned []string
	err     error
}

func (*attemptCleanRecorder) PrepareSeat(string) error { return nil }
func (r *attemptCleanRecorder) CleanAttempt(handle, taskID string, attempt int, outcome string) error {
	r.cleaned = append(r.cleaned, fmt.Sprintf("%s/%s/%d/%s", handle, taskID, attempt, outcome))
	return r.err
}

// goneLiveness proves every seat's launch identity gone: the identity rule
// (exits.go seatIdentityGone) passes, so these rows test what follows it.
type goneLiveness struct{}

func (goneLiveness) SeatProcess(string, string) (bool, string, error) {
	return true, "test: the seat's launch identity is gone", nil
}

func TestCancelledAttemptClosesThenArchivesAndCleansBeforeSlotRelease(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	h.s.Liveness = goneLiveness{}
	launcher := &holdLauncher{fakeLauncher: h.l}
	h.s.Launcher = launcher
	h.s.Exits = &exitRecorder{bad: map[string]bool{}}
	cleaner := &attemptCleanRecorder{}
	h.s.Preparer = cleaner
	h.add("cancel me", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	if _, err := h.store.Claim("t1", "seat-a", nil, time.Hour, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Cancel("t1", "lead", "stop", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	first := h.tick()
	if len(cleaner.cleaned) != 0 || !has(first, "seat_closed", "", "seat-a") || len(launcher.reserved) != 1 {
		t.Fatalf("cancel close pass: decisions=%+v cleaned=%v reserved=%v", first, cleaner.cleaned, launcher.reserved)
	}
	second := h.tick()
	if len(cleaner.cleaned) != 1 || cleaner.cleaned[0] != "seat-a/t1/1/cancelled" || !has(second, "seat_cleaned", "t1", "seat-a") || len(launcher.released) != 1 {
		t.Fatalf("cancel clean pass: decisions=%+v cleaned=%v released=%v", second, cleaner.cleaned, launcher.released)
	}
	if got := h.get("t1"); len(got.Attempts) != 1 || got.Attempts[0].CleanedAt == nil {
		t.Fatalf("the finished clean was not recorded on the attempt: %+v", got.Attempts)
	}
	if d := h.tick(); has(d, "seat_cleaned", "t1", "seat-a") || len(cleaner.cleaned) != 1 {
		t.Fatalf("the clean ran again: decisions=%+v cleaned=%v", d, cleaner.cleaned)
	}
}

// Codex's P1 on #898 (exits.go:362) and the plan reviewer's note 1: a
// restart over a store with old cancellations in its history never closes
// or cleans a seat that has moved on. seat-a's old cancellation is
// superseded by its lease on a new task (the lease boundary cleaned it):
// recorded, nothing closed, checked or cleaned. seat-b's old cancellation
// has no later lease and no record of a clean, and seat-b is adopted up:
// Codex's P1 on #909, the seat may still be working on it, so it is closed
// first (its termination confirmed) and the clean completes once on the
// next tick. Both run through a new Scheduler over the same store, never a
// reset of maps. Reverted (the later-lease check), seat-a is closed and
// cleaned over its new task; reverted (the close rule), seat-b is cleaned
// in the first tick with no close.
func TestRestartDoesNotReplayOldCancellationOverNewOccupant(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	lease := func(title, seat string, at time.Time) task.Task {
		t.Helper()
		tk, err := store.Add(task.AddInput{Title: title, Class: "script", Pool: &sonnet}, at)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.Grant(tk.ID, seat, &sonnet, time.Hour, at); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Claim(tk.ID, seat, nil, time.Hour, at); err != nil {
			t.Fatal(err)
		}
		return tk
	}
	oldA := lease("old on a", "seat-a", now)
	oldB := lease("old on b", "seat-b", now)
	for _, id := range []string{oldA.ID, oldB.ID} {
		if _, err = store.Cancel(id, "lead", "superseded", now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	next := lease("new on a", "seat-a", now.Add(2*time.Second))
	launcher := &holdLauncher{fakeLauncher: &fakeLauncher{}}
	exits := &exitRecorder{bad: map[string]bool{}}
	cleaner := &attemptCleanRecorder{}
	s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(3 * time.Second) }, Launcher: launcher, Exits: exits, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour}, Incarnation: "restart"})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp("seat-a", sonnet, now.Add(3*time.Second))
	s.AdoptUp("seat-b", sonnet, now.Add(3*time.Second))
	decisions, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !has(decisions, "seat_clean_superseded", oldA.ID, "seat-a") || has(decisions, "seat_closing", "", "seat-a") || called(launcher.closed, "seat-a") || called(exits.calls, "seat-a/"+oldA.ID+"/cancelled") || called(cleaner.cleaned, "seat-a/"+oldA.ID+"/1/cancelled") {
		t.Fatalf("seat-a's superseded cancellation was replayed over its new task: decisions=%+v closed=%v exits=%v cleaned=%v", decisions, launcher.closed, exits.calls, cleaner.cleaned)
	}
	if !has(decisions, "seat_closing", oldB.ID, "seat-b") || !has(decisions, "seat_closed", "", "seat-b") || len(cleaner.cleaned) != 0 {
		t.Fatalf("seat-b was not closed before its clean: decisions=%+v closed=%v cleaned=%v", decisions, launcher.closed, cleaner.cleaned)
	}
	decisions, err = s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !has(decisions, "seat_cleaned", oldB.ID, "seat-b") || !called(cleaner.cleaned, "seat-b/"+oldB.ID+"/1/cancelled") || len(cleaner.cleaned) != 1 {
		t.Fatalf("seat-b's unfinished cancellation was not completed once after its close: decisions=%+v cleaned=%v", decisions, cleaner.cleaned)
	}
	for _, id := range []string{oldA.ID, oldB.ID} {
		got, err := store.Get(id)
		if err != nil || len(got.Attempts) != 1 || got.Attempts[0].CleanedAt == nil {
			t.Fatalf("%s's clean is not recorded on the attempt: %+v, %v", id, got.Attempts, err)
		}
	}
	if got, _ := store.Get(next.ID); got.Lease == nil || got.Lease.Holder != "seat-a" {
		t.Fatalf("seat-a's new lease was disturbed: %+v", got.Lease)
	}
}

func TestRestartFinishesUncleanedCancellationOnceAndPersistsIt(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	tk, err := store.Add(task.AddInput{Title: "cancelled", Class: "script", Pool: &sonnet}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Grant(tk.ID, "seat-a", &sonnet, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(tk.ID, "seat-a", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Cancel(tk.ID, "lead", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	cleaner := &attemptCleanRecorder{}
	newScheduler := func(incarnation string) *Scheduler {
		s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: &fakeLauncher{}, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour}, Incarnation: incarnation})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if _, err := newScheduler("restart1").Tick(); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(tk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaner.cleaned) != 1 || got.Attempts[0].CleanedAt == nil {
		t.Fatalf("first restart did not durably finish the clean: cleaned=%v attempt=%+v", cleaner.cleaned, got.Attempts[0])
	}
	if _, err := newScheduler("restart2").Tick(); err != nil {
		t.Fatal(err)
	}
	if len(cleaner.cleaned) != 1 {
		t.Fatalf("second restart replayed the clean: %v", cleaner.cleaned)
	}
}

func TestCancelledCleanDeferralLogsOncePerReason(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	h.s.Liveness = goneLiveness{}
	h.s.Exits = &exitRecorder{bad: map[string]bool{}}
	cleaner := &attemptCleanRecorder{err: errors.New("archive busy")}
	h.s.Preparer = cleaner
	tk := h.add("cancel me", &sonnet)
	if _, err := h.store.Grant(tk.ID, "seat-a", &sonnet, time.Hour, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Claim(tk.ID, "seat-a", nil, time.Hour, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Cancel(tk.ID, "lead", "stop", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if d := h.tick(); !has(d, "seat_clean_deferred", tk.ID, "seat-a") {
		t.Fatalf("first reason was not logged: %+v", d)
	}
	if d := h.tick(); has(d, "seat_clean_deferred", tk.ID, "seat-a") {
		t.Fatalf("same reason was logged again: %+v", d)
	}
	cleaner.err = errors.New("worktree locked")
	if d := h.tick(); !has(d, "seat_clean_deferred", tk.ID, "seat-a") {
		t.Fatalf("changed reason was not logged: %+v", d)
	}
}

func TestPrepareRefusalLogsOncePerReason(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	prepare := &preparer{err: errors.New("unclean marker held")}
	h.s.Preparer = prepare
	h.add("waiting", &sonnet)
	h.up("seat-a", sonnet)
	if d := h.tick(); !has(d, "seat_prepare_failed", "t1", "seat-a") {
		t.Fatalf("first refusal was not logged: %+v", d)
	}
	if d := h.tick(); has(d, "seat_prepare_failed", "t1", "seat-a") {
		t.Fatalf("same refusal was logged again: %+v", d)
	}
	prepare.err = errors.New("unclean marker owner changed")
	if d := h.tick(); !has(d, "seat_prepare_failed", "t1", "seat-a") {
		t.Fatalf("changed refusal was not logged: %+v", d)
	}
}

func TestRefusedCancellationHoldsSeatAgainstGoneReleaseAndCompetingGrant(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	h.s.Liveness = goneLiveness{}
	launcher := &holdLauncher{fakeLauncher: h.l}
	h.s.Launcher = launcher
	h.s.Exits = &exitRecorder{bad: map[string]bool{"seat-a": true}}
	h.s.Preparer = &attemptCleanRecorder{}
	tk := h.add("cancelled", &sonnet)
	if _, err := h.store.Grant(tk.ID, "seat-a", &sonnet, time.Hour, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Claim(tk.ID, "seat-a", nil, time.Hour, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Cancel(tk.ID, "lead", "stop", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if d := h.tick(); !has(d, "exit_refused", tk.ID, "seat-a") || !h.s.exitPending("seat-a") {
		t.Fatalf("cancel refusal did not become a durable hold: %+v", d)
	}
	h.s.queueExit(exitItem{Handle: "seat-a", Why: "gone", Gone: true, At: h.clk.Now(), Attempt: "late-gone"})
	next := h.add("competing", &sonnet)
	h.s.AdoptUp("seat-a", sonnet, h.clk.Now())
	d := h.tick()
	got := h.get(next.ID)
	if len(launcher.released) != 0 || !h.s.exitPending("seat-a") || got.Lease != nil || has(d, "dispatched", next.ID, "seat-a") {
		t.Fatalf("unresolved cancellation lost its hold: released=%v pending=%v next=%+v decisions=%+v", launcher.released, h.s.exitPending("seat-a"), got.Lease, d)
	}
	// Codex's C on #898: the hold's refusal is said once per reason; the
	// re-check on the next tick is silent.
	if has(d, "exit_refused", tk.ID, "seat-a") {
		t.Fatalf("the same refusal was logged again: %+v", d)
	}
	if d := h.tick(); has(d, "exit_refused", tk.ID, "seat-a") || has(d, "dispatched", next.ID, "seat-a") {
		t.Fatalf("the hold's third tick: %+v", d)
	}
	// The lead repairs the ledger: the check passes, the clean runs, the
	// attempt is recorded cleaned, and only then is the slot released.
	h.s.Exits = &exitRecorder{bad: map[string]bool{}}
	d = h.tick()
	if !has(d, "seat_cleaned", tk.ID, "seat-a") || h.s.exitPending("seat-a") || len(launcher.released) != 1 {
		t.Fatalf("the repaired check did not resolve the hold: decisions=%+v pending=%v released=%v", d, h.s.exitPending("seat-a"), launcher.released)
	}
	if got := h.get(tk.ID); got.Attempts[0].CleanedAt == nil {
		t.Fatalf("the clean is not recorded: %+v", got.Attempts)
	}
}

func TestScanAttemptsArchivesTypedBlockOnce(t *testing.T) {
	for _, reason := range []string{task.ReasonWidenRequested, task.ReasonGrowthGateRequired} {
		t.Run(reason, func(t *testing.T) {
			s := task.Store{Dir: t.TempDir()}
			now := time.Now()
			tk, err := s.Add(task.AddInput{Title: "typed", Class: "implement", DeclaredFiles: []string{"a.go"}}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Grant(tk.ID, "seat", nil, time.Minute, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Claim(tk.ID, "seat", nil, time.Minute, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BlockWithPaths(tk.ID, "seat", task.ReasonWidenRequested, "", task.RouteLead, []string{"b.go"}, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if reason == task.ReasonGrowthGateRequired {
				if _, err := s.Escalate(tk.ID, reason, "gate", now.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			list, _ := s.List()
			recorder := &attemptArchiveRecorder{}
			scheduler := Scheduler{Preparer: recorder}
			if err := scheduler.scanAttempts(list, now.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := scheduler.scanAttempts(list, now.Add(4*time.Second)); err != nil {
				t.Fatal(err)
			}
			if len(recorder.calls) != 1 || recorder.calls[0].N != list[0].BlockAttempt {
				t.Fatalf("archive calls = %+v, block attempt %d", recorder.calls, list[0].BlockAttempt)
			}
		})
	}
}
