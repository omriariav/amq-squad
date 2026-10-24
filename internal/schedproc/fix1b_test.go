package schedproc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// Tests of the 2b-2 milestone B fix commit 1 (the first pass on e2a59176).

// P1: admission comes first. A second process for the squad is refused
// before its Admitted hook (the preflight baseline) or its launch record;
// the admitted one records the lead whose AMQ decisions it accepts (R1).
func TestAdmissionComesBeforeTheBaselineAndTheLaunchRecord(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	lock, ok, err := flock.TryExclusive(filepath.Join(Dir(root), "run.lock"), true)
	if err != nil || !ok {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	admitted := 0
	o := Options{Root: root, Launcher: &fakeLauncher{}, Log: &lockedBuf{}, Config: scheduler.Config{Lead: "lead"},
		Admitted: func() error { admitted++; return nil }}
	if err := Run(context.Background(), o); !errors.Is(err, ErrRunning) || admitted != 0 {
		t.Fatalf("the duplicate: %v, admitted %d", err, admitted)
	}
	if _, err := ReadLaunchRecord(root); err == nil {
		t.Fatal("a refused process wrote the launch record")
	}
	lock.Close()
	ctx, cancel := context.WithCancel(context.Background())
	o.Started = func(*scheduler.Scheduler) { cancel() }
	if err := Run(ctx, o); err != nil || admitted != 1 {
		t.Fatalf("the admitted process: %v, admitted %d", err, admitted)
	}
	if rec, err := ReadLaunchRecord(root); err != nil || rec.Lead != "lead" || rec.Root != root {
		t.Fatalf("launch record: %+v %v", rec, err)
	}
}

// R3: a close of the external stand-in does not confirm the termination.
func TestExternalCloseIsUnconfirmed(t *testing.T) {
	q := seatlaunch.Squad{Seats: []seatlaunch.Seat{{Handle: "pi-1", Runtime: "pi"}, {Handle: "c-1", Runtime: "claude"}}}
	l := &Launcher{Slots: &Slots{Squad: q}, ByRuntime: map[string]scheduler.SeatLauncher{"pi": External{}, "claude": &fakeLauncher{}}}
	if !l.CloseUnconfirmed("pi-1") || l.CloseUnconfirmed("c-1") {
		t.Fatal("wrong close confirmation")
	}
}
