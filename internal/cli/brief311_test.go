package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Brief 31-1, row 11 (the scheduler's half): a scheduler whose squad's
// run.lock another process holds (ErrRunning) is a refusal with the exit
// the lead's supervisor never restarts, so it is attempted once, not five
// times as a crash (TestSupervisorDoesNotRestartARefusal is the
// supervisor's half).
func TestSchedulerErrRunningIsARefusalAttemptedOnce(t *testing.T) {
	_, root, proj := schedulerFixture(t)
	if err := os.MkdirAll(schedproc.Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	lock, ok, err := flock.TryExclusive(filepath.Join(schedproc.Dir(root), "run.lock"), true)
	if err != nil || !ok {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	defer lock.Close()
	_, _, err = captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
	})
	if ExitCode(err) != ExitSchedulerRefused || !strings.Contains(err.Error(), schedproc.ErrRunning.Error()) {
		t.Fatalf("a second scheduler: %v (exit %d)", err, ExitCode(err))
	}
}

// Brief 31-1, the start stamp: scheduler run records AMQ_SQUAD_START (set
// by `lead --team`, by `up` in brief 31-1's second PR) in its launch record
// as the start of the squad's launch; without it the record names none.
func TestSchedulerRecordsTheLaunchStart(t *testing.T) {
	_, root, proj := schedulerFixture(t)
	t.Setenv(leadStartEnv, "2026-10-31T19:24:30.5Z")
	ctx, cancel := context.WithCancel(context.Background())
	prev := schedulerStarted
	schedulerStarted = func(*scheduler.Scheduler) { cancel() }
	t.Cleanup(func() { schedulerStarted = prev })
	if _, _, err := captureOutput(t, func() error {
		return runSchedulerRun(ctx, []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := schedproc.ReadLaunchRecord(root)
	if err != nil || rec.Start == nil || rec.Start.Format("15:04:05.0") != "19:24:30.5" || rec.Lead != "lead" {
		t.Fatalf("launch record %+v %v", rec, err)
	}
}

// Brief 31-1, row 8: the checked-in squad.yaml (no worktree keys) is the
// squad of scheduler run: every seat but its lead is in the roster, each in
// a worktree derived under <repo>/.worktrees/<handle>, none at the
// repository root, and the lead's handle is squad.yaml's. (It refused with
// "has no seat that can start" before.)
func TestTheCheckedInSquadYAMLLaunchesWithDerivedWorktrees(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	raw, err := os.ReadFile(filepath.Join("..", "..", "squad.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	mustWrite(t, filepath.Join(proj, "squad.yaml"), string(raw))
	q, lead, err := squadOfProject(proj)
	if err != nil {
		t.Fatal(err)
	}
	q.Wrapper, q.AMQPath = "/bin/amq-squad", "/bin/amq"
	if q, err = q.Normalize(); err != nil {
		t.Fatal(err)
	}
	roster, _ := (&schedproc.Slots{Squad: q}).Roster(lead)
	if lead != "cto" || len(roster) != len(q.Seats)-1 || len(roster) == 0 {
		t.Fatalf("lead %q, roster %+v of %d seats", lead, roster, len(q.Seats))
	}
	for _, rs := range roster {
		if want := filepath.Join(proj, ".worktrees", rs.Handle); rs.Worktree != want || rs.Worktree == proj {
			t.Fatalf("seat %s worktree %s, want %s", rs.Handle, rs.Worktree, want)
		}
	}
}
