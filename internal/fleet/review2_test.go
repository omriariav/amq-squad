package fleet

import (
	"context"
	"errors"
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
	if len(reg.Squads) != 1 || reg.Squads[0].Phase != PhaseLaunching || reg.Squads[0].Reservation == "" || reg.Squads[0].Incarnation == "" || reg.Squads[0].Expires == "" {
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

// P1 2: a mayor killed between reservation and launch leaves a reserved
// row; it holds its slot until it expires, and then the registry releases
// it under the lock at the next admission.
func TestExpiredReservationIsReleasedUnderTheLock(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/dead"))
	f.m.Now = func() time.Time { return at }
	dead := f.request(t, "dead", false).Squad
	dead.Repository = "owner/dead"
	dead.Phase, dead.Reservation, dead.Incarnation = PhaseReserved, "0123456789abcdef01234567", "fedcba9876543210fedcba98"
	dead.Expires = at.Add(5 * time.Minute).Format(time.RFC3339)
	if err := Update(f.m.RegistryPath, func(r *Registry) error { r.Squads = append(r.Squads, dead); return nil }); err != nil {
		t.Fatal(err)
	}
	app := f.request(t, "app", false)
	if r := mustStart(t, f.m, app); r.Outcome != OutcomeWaiting || r.Reason != ReasonMaxSquads {
		t.Fatalf("an unexpired orphan reservation must hold its slot: %+v", r)
	}
	f.m.Now = func() time.Time { return at.Add(6 * time.Minute) }
	if res, err := f.m.Retry(context.Background()); err != nil || res["app"].Outcome != OutcomeStarted {
		t.Fatalf("after the orphan expired: %+v %v", res, err)
	}
	if got := f.registered(t); len(got) != 1 || got[0] != "app" {
		t.Fatalf("registry = %v, want the orphan released", got)
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
	replacement.Expires = at.Add(time.Hour).Format(time.RFC3339)
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
