package scheduler

import (
	"os"
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
		s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: &fakeLauncher{}, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: cleaner, Config: Config{IdleTimeout: time.Hour, ExitsPath: ledger}, Incarnation: incarnation})
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
		h := newHarness(t, Config{IdleTimeout: time.Hour})
		h.s.Exits = &exitRecorder{bad: map[string]bool{}}
		cancelledOn(t, h.store, "seat-a", h.clk.Now())
		if _, _, err := h.store.ReserveSeat("seat-a", h.clk.Now()); err != nil {
			t.Fatal(err)
		}
		cleaner := &markerPreparer{store: h.store, now: h.clk.Now}
		h.s.Preparer = cleaner
		lines := 0
		for i := 0; i < 3; i++ {
			lines += count(h.tick(), "seat_clean_deferred")
			h.clk.Add(7 * time.Second)
		}
		if len(cleaner.texts) != 3 || cleaner.texts[0] == cleaner.texts[1] || cleaner.texts[1] == cleaner.texts[2] {
			t.Fatalf("the refusal texts did not advance: %q", cleaner.texts)
		}
		if lines != 1 {
			t.Fatalf("seat_clean_deferred lines = %d over %q", lines, cleaner.texts)
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
		if got := reasonIdentity(text); got != want {
			t.Errorf("reasonIdentity(%q) = %q, want %q", text, got, want)
		}
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
		s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: launcher, Exits: exits, Preparer: cleaner, Config: Config{IdleTimeout: time.Hour, ExitsPath: ledger}, Incarnation: incarnation})
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
