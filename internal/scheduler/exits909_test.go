package scheduler

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex's pass on #909 (fb50818a): the persisted exit item against the
// durable record (P2-a), the once-per-reason logs under an advancing age
// (P2-b), and the persisted Close flag across restarts (P2-c).

// cancelledOn leases a new task to the seat (granted and claimed) and
// cancels it: the store's view of a cancel that landed on a seat's turn.
func cancelledOn(t *testing.T, store task.Store, seat string, at time.Time) task.Task {
	t.Helper()
	tk, err := store.Add(task.AddInput{Title: "cancelled", Class: "script", Pool: &sonnet}, at)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Grant(tk.ID, seat, &sonnet, time.Hour, at); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(tk.ID, seat, nil, time.Hour, at); err != nil {
		t.Fatal(err)
	}
	if tk, err = store.Cancel(tk.ID, "lead", "stop", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return tk
}

// P2-a (exits.go:450): a crash after MarkAttemptCleaned and before the
// deferred exits-ledger write left the cancelled attempt's item queued on
// disk. The next incarnation reconciles the loaded queue with the store
// before anything acts: the item is dropped with one line, the clean is
// not run again, and the queue on disk no longer carries it. Reverted
// (the reconcile), the restart cleans the recorded attempt a second time.
func TestRestartDropsAQueuedCleanTheAttemptRecordsAsDone(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(t.TempDir(), "exits.json")
	now := t0
	tk := cancelledOn(t, store, "seat-a", now)
	newScheduler := func(incarnation string, cleaner *attemptCleanRecorder) *Scheduler {
		s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: &fakeLauncher{}, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour, ExitsPath: ledger}, Incarnation: incarnation})
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	// The first incarnation queues the item and persists it (its clean
	// deferred), then "crashes" after the clean and its record: the record
	// is written here, the queue's removal never was.
	first := &attemptCleanRecorder{err: os.ErrDeadlineExceeded}
	if _, err := newScheduler("beforecrash", first).Tick(); err != nil {
		t.Fatal(err)
	}
	if l := readExitsLedger(t, ledger); len(l.Queue) != 1 || l.Queue[0].TaskID != tk.ID || l.Queue[0].Why != task.OutcomeCancelled {
		t.Fatalf("the crashed incarnation's queue on disk: %+v", l.Queue)
	}
	if _, err := store.MarkAttemptCleaned(tk.ID, 1, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	cleaner := &attemptCleanRecorder{}
	s := newScheduler("aftercrash", cleaner)
	decisions, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(cleaner.cleaned) != 0 || has(decisions, "seat_cleaned", tk.ID, "seat-a") {
		t.Fatalf("the restart cleaned a recorded attempt again: cleaned=%v decisions=%+v", cleaner.cleaned, decisions)
	}
	if n := count(decisions, "seat_clean_replay_dropped"); n != 1 || s.exitPending("seat-a") {
		t.Fatalf("the recorded item was not dropped once: %d lines, pending=%v, decisions=%+v", n, s.exitPending("seat-a"), decisions)
	}
	if l := readExitsLedger(t, ledger); len(l.Queue) != 0 {
		t.Fatalf("the dropped item is still on disk: %+v", l.Queue)
	}
	if d, err := s.Tick(); err != nil || has(d, "seat_clean_replay_dropped", "", "") || len(cleaner.cleaned) != 0 {
		t.Fatalf("the next tick: %+v, %v, cleaned=%v", d, err, cleaner.cleaned)
	}
}

// markerPreparer refuses as the real preparer does while a live cleaner
// holds the seat's marker: the store's own ReserveSeat, whose refusal text
// carries the marker's age (unclean.go: "being cleaned by pid N for 7s").
type markerPreparer struct {
	store task.Store
	now   func() time.Time
	texts []string
}

func (m *markerPreparer) refuse(handle string) error {
	_, _, err := m.store.ReserveSeat(handle, m.now())
	if err != nil {
		m.texts = append(m.texts, err.Error())
	}
	return err
}

func (m *markerPreparer) PrepareSeat(handle string) error { return m.refuse(handle) }
func (m *markerPreparer) CleanAttempt(handle, _ string, _ int, _ string) error {
	return m.refuse(handle)
}

// P2-b (exits.go:560, scheduler.go:875, unclean.go:171): the once-per-
// reason logs compared whole texts, and a live cleaner's refusal names its
// advancing age, so each tick read as a new reason. The identity is the
// refusal's code: one line for the prepare refusal, one for the deferred
// clean, over ticks whose texts all differ. Reverted (the whole text
// compared at either site), that site logs again on every tick.
func TestRefusalsWithAnAdvancingAgeLogOncePerReason(t *testing.T) {
	t.Run("prepare", func(t *testing.T) {
		h := newHarness(t, Config{IdleTimeout: time.Hour})
		if _, _, err := h.store.ReserveSeat("seat-a", h.clk.Now()); err != nil {
			t.Fatal(err) // a live cleaner (this test process) holds seat-a's marker
		}
		prepare := &markerPreparer{store: h.store, now: h.clk.Now}
		h.s.Preparer = prepare
		h.add("waiting", &sonnet)
		h.up("seat-a", sonnet)
		lines := 0
		for i := 0; i < 3; i++ {
			lines += count(h.tick(), "seat_prepare_failed")
			h.clk.Add(7 * time.Second)
		}
		if len(prepare.texts) != 3 || prepare.texts[0] == prepare.texts[1] || prepare.texts[1] == prepare.texts[2] {
			t.Fatalf("the refusal texts did not advance: %q", prepare.texts)
		}
		if lines != 1 {
			t.Fatalf("seat_prepare_failed lines = %d over %q", lines, prepare.texts)
		}
	})
	t.Run("deferred clean", func(t *testing.T) {
		// the refusal is the cancel round's own reservation (ReserveCancel)
		// meeting the live marker: the same store text, the same advancing age
		h := newHarness(t, Config{IdleTimeout: time.Hour})
		h.s.Exits = &exitRecorder{bad: map[string]bool{}}
		h.s.Liveness = goneLiveness{}
		h.s.Preparer = &attemptCleanRecorder{}
		cancelledOn(t, h.store, "seat-a", h.clk.Now())
		if _, _, err := h.store.ReserveSeat("seat-a", h.clk.Now()); err != nil {
			t.Fatal(err)
		}
		lines, texts := 0, []string{}
		for i := 0; i < 3; i++ {
			for _, d := range h.tick() {
				if d.Kind == "seat_clean_deferred" {
					lines++
				}
			}
			if _, _, err := h.store.ReserveSeat("seat-a", h.clk.Now()); err != nil {
				texts = append(texts, err.Error())
			}
			h.clk.Add(7 * time.Second)
		}
		if len(texts) != 3 || texts[0] == texts[1] || texts[1] == texts[2] {
			t.Fatalf("the refusal texts did not advance: %q", texts)
		}
		if lines != 1 {
			t.Fatalf("seat_clean_deferred lines = %d over %q", lines, texts)
		}
	})
}

func TestReasonIdentityIsTheRefusalCode(t *testing.T) {
	for text, want := range map[string]string{
		"seat_cleaning: seat s1 is being cleaned by pid 42 for 12s":                          "seat_cleaning",
		"the commit ledger of seat s1: seat_cleaning: seat s1 is being cleaned for 3s; x":    "seat_cleaning",
		"seat c1 worktree to the base tip 1b18c4bcce20: seat_worktree_dirty: seat c1: files": "seat_worktree_dirty",
		"archive busy": "archive busy",
	} {
		if got := refusalCode(text); got != want {
			t.Errorf("refusalCode(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestPrepareRefusalAcrossMinuteBoundaryLogsOnce(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	if _, _, err := h.store.ReserveSeat("seat-a", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	prepare := &markerPreparer{store: h.store, now: h.clk.Now}
	h.s.Preparer = prepare
	h.add("waiting", &sonnet)
	h.up("seat-a", sonnet)
	failed, suppressed := 0, 0
	for i := 0; i < 6; i++ {
		decisions := h.tick()
		failed += count(decisions, "seat_prepare_failed")
		suppressed += count(decisions, "seat_prepare_suppressed")
		h.clk.Add(25 * time.Second)
	}
	if len(prepare.texts) != 6 {
		t.Fatalf("refusal texts = %q", prepare.texts)
	}
	for i := 1; i < len(prepare.texts); i++ {
		if prepare.texts[i] == prepare.texts[i-1] {
			t.Fatalf("refusal texts did not advance: %q", prepare.texts)
		}
	}
	if failed != 1 || suppressed != 1 {
		t.Fatalf("prepare decisions = failed %d suppressed %d over %q", failed, suppressed, prepare.texts)
	}
}

// Meeting 5 of Addendum 3: PrepareSeat can pass while the cancel round's
// reservation still refuses the locked grant. Four ticks produce one full
// failure and one counted suppression, never a lease. Once the reservation
// is released, the next tick grants normally. Moving preparePassed before
// Grant loses the count line because it clears the first refusal too early.
func TestAReservedGrantRefusalIsLoggedOnceThenCounted(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	cancelled := cancelledOn(t, h.store, "seat-a", h.clk.Now())
	if _, err := h.store.MarkAttemptCleaned(cancelled.ID, 1, h.clk.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	later, nonce, _, err := h.store.ReserveCancel("seat-a", cancelled.ID, 1, h.clk.Now().Add(2*time.Second))
	if err != nil || later != "" || nonce == "" {
		t.Fatalf("reserve = %q, %q, %v", later, nonce, err)
	}
	t.Cleanup(func() { _ = h.store.ReleaseSeat("seat-a", nonce) })
	h.s.Preparer = &attemptCleanRecorder{}
	waiting := h.add("waiting", &sonnet)
	h.up("seat-a", sonnet)
	failed, suppressed := 0, 0
	for range 4 {
		d := h.tick()
		failed += count(d, "seat_prepare_failed")
		suppressed += count(d, "seat_prepare_suppressed")
		if has(d, "dispatched", waiting.ID, "seat-a") {
			t.Fatalf("reserved seat was dispatched: %+v", d)
		}
	}
	if failed != 1 || suppressed != 1 {
		t.Fatalf("reservation decisions = failed %d suppressed %d", failed, suppressed)
	}
	if got := h.get(waiting.ID); got.Lease != nil {
		t.Fatalf("reserved seat received a lease: %+v", got.Lease)
	}
	if err := h.store.ReleaseSeat("seat-a", nonce); err != nil {
		t.Fatal(err)
	}
	nonce = ""
	if d := h.tick(); !has(d, "dispatched", waiting.ID, "seat-a") {
		t.Fatalf("released seat was not granted: %+v", d)
	}
}

func TestSecondCancelledAttemptOfTheSameTaskIsQueuedBesideTheFirst(t *testing.T) {
	s := &Scheduler{exitQueue: []exitItem{{Handle: "seat-a", TaskID: "t1", Why: task.OutcomeCancelled, TaskAttempt: 1}}}
	second := exitItem{Handle: "seat-a", TaskID: "t1", Why: task.OutcomeCancelled, TaskAttempt: 2}
	if s.exitPendingItem(second) {
		t.Fatal("the second cancelled attempt matched the first attempt's queued exit")
	}
	s.exitQueue = append(s.exitQueue, second)
	if len(s.exitQueue) != 2 {
		t.Fatalf("queued exits = %+v", s.exitQueue)
	}
}

// P2-c (exits_test.go:156: the restart rows had no ExitsPath): the Close
// flag on disk. The first incarnation queues the cancelled attempt's
// close-required item and requests the close, which is not confirmed (an
// async closer) when it stops. A new scheduler over the same exits ledger,
// with the seat still up, closes it before any check or clean. Once that
// close is confirmed the flag is cleared on disk: a third incarnation loads
// the item with no Close, and closes nothing. Reverted (the flag not
// persisted), the second incarnation checks and cleans the live seat;
// reverted (the flag not cleared), the third closes the seat again.
func TestCancelCloseFlagSurvivesARestartAndIsClearedOnceConfirmed(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(t.TempDir(), "exits.json")
	now := t0
	tk := cancelledOn(t, store, "seat-a", now)
	newScheduler := func(incarnation string, launcher SeatLauncher, exits *exitRecorder, cleaner *attemptCleanRecorder) *Scheduler {
		s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: launcher, Exits: exits, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour, ExitsPath: ledger}, Incarnation: incarnation})
		if err != nil {
			t.Fatal(err)
		}
		s.AdoptUp("seat-a", sonnet, now.Add(2*time.Second))
		return s
	}
	// 1: the close is requested, not confirmed.
	exits, cleaner := &exitRecorder{bad: map[string]bool{}}, &attemptCleanRecorder{}
	d, err := newScheduler("first", &asyncHoldLauncher{&holdLauncher{fakeLauncher: &fakeLauncher{}}}, exits, cleaner).Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !has(d, "seat_closing", tk.ID, "seat-a") || !has(d, "seat_close_requested", "", "seat-a") || len(exits.calls) != 0 || len(cleaner.cleaned) != 0 {
		t.Fatalf("first incarnation: decisions=%+v exits=%v cleaned=%v", d, exits.calls, cleaner.cleaned)
	}
	if l := readExitsLedger(t, ledger); len(l.Queue) != 1 || !l.Queue[0].Close {
		t.Fatalf("the unfinished close is not on disk: %+v", l.Queue)
	}
	// 2: a restart loads the unfinished close and finishes it (a launcher
	// that confirms) before any check or clean.
	exits, cleaner = &exitRecorder{bad: map[string]bool{}}, &attemptCleanRecorder{}
	launcher := &holdLauncher{fakeLauncher: &fakeLauncher{}}
	d, err = newScheduler("second", launcher, exits, cleaner).Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !has(d, "seat_closing", tk.ID, "seat-a") || !has(d, "seat_closed", "", "seat-a") || len(exits.calls) != 0 || len(cleaner.cleaned) != 0 {
		t.Fatalf("the restart did not close before the check and the clean: decisions=%+v exits=%v cleaned=%v", d, exits.calls, cleaner.cleaned)
	}
	if l := readExitsLedger(t, ledger); len(l.Queue) != 1 || l.Queue[0].Close || !l.Queue[0].Gone {
		t.Fatalf("the confirmed close's flag is not cleared on disk: %+v", l.Queue)
	}
	// 3: another restart after the confirmed close closes nothing.
	exits, cleaner = &exitRecorder{bad: map[string]bool{}}, &attemptCleanRecorder{}
	launcher = &holdLauncher{fakeLauncher: &fakeLauncher{}}
	d, err = newScheduler("third", launcher, exits, cleaner).Tick()
	if err != nil {
		t.Fatal(err)
	}
	if has(d, "seat_closing", "", "seat-a") || len(launcher.closed) != 0 || !has(d, "seat_cleaned", tk.ID, "seat-a") {
		t.Fatalf("the third incarnation: decisions=%+v closed=%v cleaned=%v", d, launcher.closed, cleaner.cleaned)
	}
}

// The identity rule with no liveness hook: nothing proves the seat's
// identity gone, so the clean fails closed with one lead stop, never runs,
// and the attempt stays uncleaned. Reverted (no hook read as gone), it is
// cleaned.
func TestCancelledCleanWithNoIdentityCheckFailsClosed(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	h.s.Exits = &exitRecorder{bad: map[string]bool{}}
	cleaner := &attemptCleanRecorder{}
	h.s.Preparer = cleaner
	tk := cancelledOn(t, h.store, "seat-a", h.clk.Now())
	stops := 0
	for i := 0; i < 3; i++ {
		stops += count(h.tick(), "seat_may_be_writing")
	}
	if len(cleaner.cleaned) != 0 || stops != 1 || h.get(tk.ID).Attempts[0].CleanedAt != nil || !h.s.exitPending("seat-a") {
		t.Fatalf("no identity check: cleaned=%v stops=%d attempt=%+v pending=%v", cleaner.cleaned, stops, h.get(tk.ID).Attempts[0], h.s.exitPending("seat-a"))
	}
}

// Codex's P1 on the #909 re-pass (exits.go:503): a persisted close-required
// item outlived its occupant. The first incarnation queues A's
// close-required item and requests the close, unconfirmed, then stops. A
// lease on the same seat for B lands in between (a grant that skipped the
// dispatch refusal: an older binary's, or the store's own Grant). The
// restart adopts the seat up: the queued item is revalidated against the
// leases before anything acts, so it is dropped (seat_clean_superseded,
// once), A is recorded, the queue on disk no longer carries it, and B's
// seat is never closed, checked or cleaned for A. Reverted (no
// revalidation), the restart closes the seat under B.
func TestRestartDropsAQueuedCloseWhoseSeatHoldsALaterLease(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(t.TempDir(), "exits.json")
	now := t0
	a := cancelledOn(t, store, "seat-a", now)
	first, err := New(Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: &asyncHoldLauncher{&holdLauncher{fakeLauncher: &fakeLauncher{}}}, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: &attemptCleanRecorder{}, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour, ExitsPath: ledger}, Incarnation: "first"})
	if err != nil {
		t.Fatal(err)
	}
	first.AdoptUp("seat-a", sonnet, now.Add(2*time.Second))
	if d, err := first.Tick(); err != nil || !has(d, "seat_close_requested", "", "seat-a") {
		t.Fatalf("the first incarnation's close: %+v, %v", d, err)
	}
	if l := readExitsLedger(t, ledger); len(l.Queue) != 1 || !l.Queue[0].Close {
		t.Fatalf("the close-required item is not on disk: %+v", l.Queue)
	}
	b, err := store.Add(task.AddInput{Title: "later occupant", Class: "script", Pool: &sonnet}, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Grant(b.ID, "seat-a", &sonnet, time.Hour, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(b.ID, "seat-a", nil, time.Hour, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	launcher := &holdLauncher{fakeLauncher: &fakeLauncher{}}
	exits, cleaner := &exitRecorder{bad: map[string]bool{}}, &attemptCleanRecorder{}
	s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(4 * time.Second) }, Launcher: launcher, Exits: exits, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour, ExitsPath: ledger}, Incarnation: "second"})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp("seat-a", sonnet, now.Add(4*time.Second))
	superseded := 0
	for i := 0; i < 2; i++ {
		d, err := s.Tick()
		if err != nil {
			t.Fatal(err)
		}
		if has(d, "seat_closing", "", "seat-a") || has(d, "seat_closed", "", "seat-a") {
			t.Fatalf("the restart closed seat-a under its later lease: %+v", d)
		}
		superseded += count(d, "seat_clean_superseded")
	}
	if len(launcher.closed) != 0 || len(exits.calls) != 0 || len(cleaner.cleaned) != 0 || superseded != 1 {
		t.Fatalf("closed=%v exits=%v cleaned=%v superseded lines=%d", launcher.closed, exits.calls, cleaner.cleaned, superseded)
	}
	if l := readExitsLedger(t, ledger); len(l.Queue) != 0 {
		t.Fatalf("the superseded item is still on disk: %+v", l.Queue)
	}
	if got, _ := store.Get(a.ID); got.Attempts[0].CleanedAt == nil {
		t.Fatalf("A is not recorded: %+v", got.Attempts[0])
	}
	if got, _ := store.Get(b.ID); got.Lease == nil || got.Lease.Holder != "seat-a" {
		t.Fatalf("B's lease was disturbed: %+v", got.Lease)
	}
}

// reservingCloser is a launcher whose close of seat-a tries, during the
// close, the scheduler's own grant of B to the same seat, and a seat
// hook's no-wait store write (seat-c renewing its lease), and records what
// each returned and how long the write took.
type reservingCloser struct {
	*fakeLauncher
	store             task.Store
	taskID            string
	at                time.Time
	grantErr, hookErr error
	hookTook          time.Duration
	tried             bool
}

func (r *reservingCloser) CloseSeat(h, reason string) error {
	if h == "seat-a" && !r.tried {
		r.tried = true
		began := time.Now()
		_, _, r.hookErr = r.store.RenewNoWaitAttempt("seat-c", 1, time.Hour, 0, r.at)
		r.hookTook = time.Since(began)
		// the grant in its own goroutine: one that blocks on a held store
		// lock is a failure of the row, never a hang
		done := make(chan error, 1)
		go func() {
			_, err := r.store.Grant(r.taskID, "seat-a", &sonnet, time.Hour, r.at)
			done <- err
		}()
		select {
		case r.grantErr = <-done:
		case <-time.After(2 * time.Second):
			r.grantErr = errors.New("the grant blocked on the store's lock during the close")
		}
	}
	return r.fakeLauncher.CloseSeat(h, reason)
}

// The plan reviewer's ruling on #909: the store's lock is never held
// through a launcher close; the seat is reserved instead. During the
// cancel close a grant of the seat (the scheduler's own Grant) is refused
// by the reservation, at once, in the reviewer's words, and a seat hook's
// no-wait write to the store returns at once (not busy). After the round
// the reservation is gone. Reverted (no reservation), the grant lands;
// reverted (the lock held through the close), the hook's write is busy.
func TestAGrantDuringTheCancelCloseIsRefusedByTheReservation(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	cancelledOn(t, store, "seat-a", now)
	c, err := store.Add(task.AddInput{Title: "C", Class: "script", Pool: &sonnet}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Grant(c.ID, "seat-c", &sonnet, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(c.ID, "seat-c", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	b, err := store.Add(task.AddInput{Title: "B", Class: "script", Pool: &sonnet}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	launcher := &reservingCloser{fakeLauncher: &fakeLauncher{}, store: store, taskID: b.ID, at: now.Add(3 * time.Second)}
	s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(3 * time.Second) }, Launcher: launcher, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: &attemptCleanRecorder{}, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour}, Incarnation: "reserverow"})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp("seat-a", sonnet, now.Add(3*time.Second))
	d, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !launcher.tried || !has(d, "seat_closed", "", "seat-a") {
		t.Fatalf("the close did not run: %+v", d)
	}
	var reserved *task.ReservedSeatError
	if !errors.As(launcher.grantErr, &reserved) || launcher.grantErr.Error() != "seat seat-a is being closed and cleaned; dispatch again after it finishes" {
		t.Fatalf("the grant during the close: %v", launcher.grantErr)
	}
	if got, _ := store.Get(b.ID); got.Lease != nil {
		t.Fatalf("B was granted during the close: %+v", got.Lease)
	}
	if errors.Is(launcher.hookErr, task.ErrBusy) || launcher.hookTook > 2*time.Second {
		t.Fatalf("the hook's no-wait write during the close: %v after %s", launcher.hookErr, launcher.hookTook)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "reserved", "seat-a")); !os.IsNotExist(err) {
		t.Fatalf("the round left its reservation: %v", err)
	}
}

// The plan reviewer's condition 1 on #909: the close's lease check is a
// read under the store's lock taken immediately before the close, not the
// tick's snapshot. The seam lands a grant of B on seat-a after the tick
// read its list (and after the scan queued A's close-required item) but
// before the close: the locked read sees B, so seat-a is not closed and A's
// item is dropped (seat_clean_superseded). Reverted (the check against a
// read taken before the grant, as the tick's snapshot was), seat-a is
// closed under B.
func TestAGrantAfterTheTickSnapshotIsSeenBeforeTheCancelClose(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	cancelledOn(t, store, "seat-a", now)
	b, err := store.Add(task.AddInput{Title: "B", Class: "script", Pool: &sonnet}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	granted := false
	cancelCloseHook = func(h string) {
		if h != "seat-a" || granted {
			return
		}
		granted = true
		if _, err := store.Grant(b.ID, "seat-a", &sonnet, time.Hour, now.Add(3*time.Second)); err != nil {
			t.Errorf("the grant in the window: %v", err)
		}
	}
	t.Cleanup(func() { cancelCloseHook = nil })
	launcher := &holdLauncher{fakeLauncher: &fakeLauncher{}}
	exits, cleaner := &exitRecorder{bad: map[string]bool{}}, &attemptCleanRecorder{}
	s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(3 * time.Second) }, Launcher: launcher, Exits: exits, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour}, Incarnation: "windowrow"})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp("seat-a", sonnet, now.Add(3*time.Second))
	d, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if !granted || len(launcher.closed) != 0 || has(d, "seat_closing", "", "seat-a") || has(d, "seat_closed", "", "seat-a") {
		t.Fatalf("seat-a was closed under the grant that landed after the snapshot: granted=%v closed=%v decisions=%+v", granted, launcher.closed, d)
	}
	if !has(d, "seat_clean_superseded", "", "seat-a") || len(exits.calls) != 0 || len(cleaner.cleaned) != 0 {
		t.Fatalf("A's item was not dropped: decisions=%+v exits=%v cleaned=%v", d, exits.calls, cleaner.cleaned)
	}
}

// TestCancelReserveCrashHelper is the crashed scheduler of row (a): it
// takes a cancel reservation of the seat and exits without releasing it.
func TestCancelReserveCrashHelper(t *testing.T) {
	dir := os.Getenv("AMQ_TEST_CANCEL_RESERVE_DIR")
	if dir == "" {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, os.Getenv("AMQ_TEST_CANCEL_RESERVE_AT"))
	if err != nil {
		os.Exit(4)
	}
	later, nonce, _, err := task.Store{Dir: dir}.ReserveCancel("seat-a", "t1", 1, at)
	if err != nil || later != "" || nonce == "" {
		os.Exit(3)
	}
	os.Exit(0) // the crash: the reservation is never released
}

// The plan reviewer's condition (a) on #909: the reservation is the
// durable 2b-1 marker with the reserving process's pid and start stamp. A
// scheduler that crashes after reserving leaves its marker; the next
// incarnation's round proves that pid dead and takes the marker over (one
// cleaner_marker_taken_over line), and the cancelled attempt is cleaned
// and its reservation released. Reverted (no takeover of a dead marker),
// the seat stays reserved and nothing is cleaned.
func TestACrashedReservationIsTakenOverOnRestart(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	cancelledOn(t, store, "seat-a", now)
	child := exec.Command(os.Args[0], "-test.run=^TestCancelReserveCrashHelper$")
	child.Env = append(os.Environ(), "AMQ_TEST_CANCEL_RESERVE_DIR="+store.Dir, "AMQ_TEST_CANCEL_RESERVE_AT="+now.Add(2*time.Second).Format(time.RFC3339Nano))
	if out, err := child.CombinedOutput(); err != nil {
		t.Fatalf("the crashing scheduler: %v\n%s", err, out)
	}
	marker := filepath.Join(store.Dir, "reserved", "seat-a")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the crash left no reservation: %v", err)
	}
	cleaner := &attemptCleanRecorder{}
	s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(3 * time.Second) }, Launcher: &fakeLauncher{}, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour}, Incarnation: "aftercrash"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if count(d, "cleaner_marker_taken_over") != 1 || !has(d, "seat_cleaned", "t1", "seat-a") || len(cleaner.cleaned) != 1 {
		t.Fatalf("the stale reservation was not taken over: decisions=%+v cleaned=%v", d, cleaner.cleaned)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("the restart's round left its reservation: %v", err)
	}
}

// The plan reviewer's condition (b) on #909: the reservation is released
// on every path out of a round, the unknown-identity stop included. The
// attempt then stays cancelled and uncleaned, so the lead's dispatch
// refuses the seat (the uncleaned words) and the scheduler grants nothing
// to it (its queued item holds it): no reservation is left, and no grant
// lands. Reverted (no release), the reservation stays behind.
func TestTheUnknownIdentityStopLeavesNoReservationAndNoGrant(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	h.s.Exits = &exitRecorder{bad: map[string]bool{}}
	h.s.Liveness = unknownLiveness{}
	cleaner := &attemptCleanRecorder{}
	h.s.Preparer = cleaner
	a := cancelledOn(t, h.store, "seat-a", h.clk.Now())
	b := h.add("B", &sonnet)
	h.up("seat-a", sonnet)
	stops := 0
	for i := 0; i < 3; i++ {
		d := h.tick()
		stops += count(d, "seat_may_be_writing")
		if has(d, "dispatched", b.ID, "") {
			t.Fatalf("B was dispatched over the uncleaned cancellation: %+v", d)
		}
	}
	if stops != 1 || len(cleaner.cleaned) != 0 {
		t.Fatalf("stops=%d cleaned=%v", stops, cleaner.cleaned)
	}
	if _, err := os.Stat(filepath.Join(h.store.Dir, "reserved", "seat-a")); !os.IsNotExist(err) {
		t.Fatalf("the unknown-identity path left the reservation: %v", err)
	}
	_, err := h.store.GrantClean(b.ID, "seat-a", &sonnet, time.Hour, h.clk.Now())
	var uncleaned *task.UncleanedSeatError
	if !errors.As(err, &uncleaned) || uncleaned.Task != a.ID {
		t.Fatalf("the lead's grant after the stop: %v", err)
	}
	if got := h.get(b.ID); got.Lease != nil {
		t.Fatalf("B holds a lease: %+v", got.Lease)
	}
}

type unknownLiveness struct{}

func (unknownLiveness) SeatProcess(string, string) (bool, string, error) {
	return false, "", errors.New("the launch ledger's proof of seat-a cannot be read")
}
