package fleet

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Codex review of d13ded88 (second pass). One row per item; each fails
// with its fix reverted (docs/v3-spike/4-3-evidence/c6-revert-check.txt).

type blockingLauncher struct {
	entered chan struct{}
	release chan struct{}
}

func (l *blockingLauncher) Launch(ctx context.Context, _ LaunchRequest) error {
	close(l.entered)
	<-l.release
	return nil
}

// P1 2: a reservation counts toward max_squads until launched or expired,
// whatever Live says, so a second start cannot slip in while the first is
// launching.
func TestReservationCountsRegardlessOfLive(t *testing.T) {
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	notLive := func(Squad) bool { return false } // a liveness probe that sees no seat yet
	bl := &blockingLauncher{entered: make(chan struct{}), release: make(chan struct{})}
	first := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: &fakeWriter{}, Launcher: bl, Live: notLive, Session: true}
	done := make(chan Result, 1)
	app, web := f.request(t, "app", false), f.request(t, "web", false)
	go func() {
		r, err := first.Start(context.Background(), app)
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-bl.entered
	reg, _ := Read(f.m.RegistryPath)
	if len(reg.Squads) != 1 || reg.Squads[0].Phase != PhaseWaitingSeats || reg.Squads[0].Reservation == "" || reg.Squads[0].Incarnation == "" || reg.Squads[0].MayorPID == "" || reg.Squads[0].PhaseSince == "" {
		t.Fatalf("the launching row: %+v", reg.Squads)
	}
	second := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: &fakeWriter{}, Launcher: &fakeLauncher{}, Live: notLive, Session: true}
	if r := mustStart(t, second, web); r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("a start while another launches under max_squads 1: %+v", r)
	}
	close(bl.release)
	if r := <-done; r.Outcome != OutcomeStarted {
		t.Fatalf("first start: %+v", r)
	}
	reg, _ = Read(f.m.RegistryPath)
	if len(reg.Squads) != 1 || reg.Squads[0].Phase != "" || reg.Squads[0].Reservation != "" {
		t.Fatalf("the launched row keeps reservation fields: %+v", reg.Squads)
	}
}

// orphan writes an in-flight reservation row for name, reserved by the
// incarnation pid/start, in phase since phaseSince.
func orphan(t *testing.T, f *mayorFixture, name string, pid int, start, phaseSince time.Time) {
	t.Helper()
	s := f.request(t, name, false).Squad
	s.Repository = "owner/" + name
	s.Phase, s.Reservation, s.Incarnation = PhaseWaitingSeats, "0123456789abcdef01234567", "fedcba9876543210fedcba98"
	s.MayorPID, s.MayorStart, s.PhaseSince = strconv.Itoa(pid), start.UTC().Format(time.RFC3339Nano), phaseSince.UTC().Format(time.RFC3339)
	if err := Update(f.m.RegistryPath, func(r *Registry) error { r.Squads = append(r.Squads, s); return nil }); err != nil {
		t.Fatal(err)
	}
}

func released(t *testing.T, f *mayorFixture) []Released {
	t.Helper()
	reg, err := Read(f.m.RegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	return reg.Released
}

// Ruling (reservation expiry): a reservation whose mayor incarnation is
// gone (its pid dead, or reused with another start time) is released under
// the lock at the next admission, recorded as incarnation_gone, even inside
// its phase window.
func TestReleaseWhenTheIncarnationIsGone(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/dead"))
	f.m.Now = func() time.Time { return at }
	const deadPID = 4242
	f.m.IncarnationAlive = func(pid int, _ time.Time) bool { return pid != deadPID }
	orphan(t, f, "dead", deadPID, at.Add(-time.Hour), at.Add(-time.Minute)) // one minute into its phase
	if r := mustStart(t, f.m, f.request(t, "app", false)); r.Outcome != OutcomeStarted {
		t.Fatalf("a dead mayor's reservation held its slot: %+v", r)
	}
	if got := f.registered(t); len(got) != 1 || got[0] != "app" {
		t.Fatalf("registry = %v", got)
	}
	if rel := released(t, f); len(rel) != 1 || rel[0].Name != "dead" || rel[0].Reason != ReleaseIncarnationGone || rel[0].Phase != PhaseWaitingSeats {
		t.Fatalf("released = %+v", rel)
	}
}

// Ruling: an alive but stuck mayor whose phase has not advanced for one
// window (10 minutes) releases its row, recorded as phase_stalled; inside
// the window the row holds its slot.
func TestReleaseWhenThePhaseStalls(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/stuck"))
	f.m.IncarnationAlive = func(int, time.Time) bool { return true } // the stuck mayor is alive
	orphan(t, f, "stuck", 4243, at.Add(-time.Hour), at)
	f.m.Now = func() time.Time { return at.Add(PhaseWindow - time.Second) }
	app := f.request(t, "app", false)
	if r := mustStart(t, f.m, app); r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("inside the window the reservation must hold its slot: %+v", r)
	}
	f.m.Now = func() time.Time { return at.Add(PhaseWindow) }
	if res, err := f.m.Retry(context.Background()); err != nil || res["app"].Outcome != OutcomeStarted {
		t.Fatalf("after one window: %+v %v", res, err)
	}
	if rel := released(t, f); len(rel) != 1 || rel[0].Name != "stuck" || rel[0].Reason != ReleasePhaseStalled {
		t.Fatalf("released = %+v", rel)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type slowWriter struct {
	c    *clock
	step time.Duration
}

func (w slowWriter) Propose(p, _ string) (string, error) {
	w.c.add(w.step)
	return "sha256:" + filepath.Base(p), nil
}
func (w slowWriter) Apply(string, string) error { return nil }

type slowSeats struct {
	c       *clock
	step    time.Duration
	entered chan struct{}
	release chan struct{}
}

func (l slowSeats) Launch(context.Context, LaunchRequest) error {
	l.c.add(l.step)
	close(l.entered)
	<-l.release
	return nil
}

// Ruling: a slow roster launch that keeps advancing phases keeps its row,
// although its total time is past one window: each phase has its own.
func TestSlowAdvancingLaunchKeepsItsRow(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	step := PhaseWindow * 8 / 10 // each phase takes 8 of its 10 minutes
	seats := slowSeats{c: c, step: step, entered: make(chan struct{}), release: make(chan struct{})}
	slow := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: slowWriter{c: c, step: step}, Launcher: seats, Now: c.now, Session: true}
	done := make(chan Result, 1)
	go func() {
		r, err := slow.Start(context.Background(), f.request(t, "app", false))
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-seats.entered
	// 16 minutes after the reservation, 8 into waiting_seats
	other := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: &fakeWriter{}, Launcher: &fakeLauncher{}, Now: c.now, Session: true}
	if r := mustStart(t, other, f.request(t, "web", false)); r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("a slow but advancing launch lost its slot: %+v", r)
	}
	close(seats.release)
	if r := <-done; r.Outcome != OutcomeStarted {
		t.Fatalf("the slow launch: %+v", r)
	}
	reg, _ := Read(f.m.RegistryPath)
	if len(reg.Squads) != 1 || reg.Squads[0].Name != "app" || len(reg.Released) != 0 {
		t.Fatalf("registry = %+v released %+v", reg.Squads, reg.Released)
	}
}

// P2 6: admission runs before the adapter check, so a full fleet reports
// max_squads (and records its waiting row) even with no adapters wired.
func TestFullFleetReportsMaxSquadsBeforeAdapters(t *testing.T) {
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	mustStart(t, f.m, f.request(t, "app", false))
	f.m.Squads, f.m.Launcher = nil, nil
	r, err := f.m.Start(context.Background(), f.request(t, "web", false))
	if err != nil || r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("full fleet, no adapters: %+v %v, want waiting max_squads", r, err)
	}
	if reg, _ := Read(f.m.RegistryPath); len(reg.Waiting) != 1 || reg.Waiting[0].Name != "web" {
		t.Fatalf("waiting row: %+v", reg.Waiting)
	}
	// with room, the missing adapters are an error and the reservation is released
	f.stopped["app"] = true
	if _, err := f.m.Start(context.Background(), f.request(t, "web", false)); err == nil {
		t.Fatal("a start with no adapters succeeded")
	}
	if got := f.registered(t); len(got) != 1 {
		t.Fatalf("the reservation of a start with no adapters stayed: %v", got)
	}
}

type partialLauncher struct{}

func (partialLauncher) Launch(context.Context, LaunchRequest) error {
	return &PartialLaunchError{Live: []string{"lead"}, Err: errors.New("seat dev did not start")}
}

// New P1: a partial launch (a seat live) keeps the row, counted, in phase
// launch_failed_partial with its live seats, until the operator resolves it.
func TestPartialLaunchKeepsItsSlot(t *testing.T) {
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	f.m.Launcher = partialLauncher{}
	r := mustStart(t, f.m, f.request(t, "app", false))
	if r.Outcome != OutcomeRefused || r.Reason != ReasonLaunchPartial {
		t.Fatalf("partial launch: %+v", r)
	}
	reg, _ := Read(f.m.RegistryPath)
	if len(reg.Squads) != 1 || reg.Squads[0].Phase != PhaseFailedPartial || reg.Squads[0].LiveSeats != "lead" {
		t.Fatalf("the partial row: %+v", reg.Squads)
	}
	f.m.Launcher = f.launcher
	f.stopped["app"] = true // Live says nothing runs: the partial row still counts
	if r := mustStart(t, f.m, f.request(t, "web", false)); r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("a start beside a partial launch under max_squads 1: %+v", r)
	}
}

// New P2: a late cleanup deletes its own reservation by id, never another
// squad's row of the same name.
func TestLateCleanupDeletesByReservation(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	f := newMayor(t, fleetGrant(4, "owner/app"))
	replacement := f.request(t, "app", false).Squad
	replacement.Phase, replacement.Reservation, replacement.Incarnation = PhaseReserved, "bbbbbbbbbbbbbbbbbbbbbbbb", "cccccccccccccccccccccccc"
	replacement.MayorPID, replacement.MayorStart, replacement.PhaseSince = "4242", at.Format(time.RFC3339Nano), at.Format(time.RFC3339)
	if err := Update(f.m.RegistryPath, func(r *Registry) error { r.Squads = append(r.Squads, replacement); return nil }); err != nil {
		t.Fatal(err)
	}
	original := replacement
	original.Reservation = "aaaaaaaaaaaaaaaaaaaaaaaa" // the earlier, released reservation
	if err := f.m.setPhase(original, "", ""); err != nil {
		t.Fatal(err)
	}
	reg, _ := Read(f.m.RegistryPath)
	if len(reg.Squads) != 1 || reg.Squads[0].Reservation != replacement.Reservation {
		t.Fatalf("the original's late cleanup deleted the replacement: %+v", reg.Squads)
	}
	if err := f.m.setPhase(original, PhaseRunning, ""); err == nil {
		t.Fatal("a stale reservation promoted another squad's row")
	}
}
