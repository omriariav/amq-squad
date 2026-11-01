package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// Brief 31-1's second PR, row 20 (item 7): `scheduler run` on a repository
// whose session root is absent no longer refuses "create the session
// first": it creates the root and the session the way `up` does (the one
// shared function, through the fake amq), logs session_created, and the
// scheduler starts.
func TestSchedulerRunOnAnAbsentSessionRootCreatesIt(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	writeSquadProject(t, proj, fixtureSquadYAML, "implementer")
	mustMkdir(t, filepath.Join(proj, "wt-impl-1"))
	root := filepath.Join(proj, ".agent-mail", "s1")
	f := &upFake{t: t, proj: proj, root: root, panes: map[string]string{}, sessions: map[string]bool{}}
	fake := &cliFakeLauncher{}
	prev := []any{squadRun, schedulerAMQLauncher, schedulerSendRun, schedulerStarted, schedulerOnTick}
	t.Cleanup(func() {
		squadRun = prev[0].(squadRunner)
		schedulerAMQLauncher = prev[1].(func(*schedproc.Slots, string, seatlaunch.Options) scheduler.SeatLauncher)
		schedulerSendRun = prev[2].(func(context.Context, string, []string, []string) ([]byte, error))
		schedulerStarted = prev[3].(func(*scheduler.Scheduler))
		schedulerOnTick = prev[4].(func([]scheduler.Decision))
	})
	squadRun = f.run
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher {
		fake.sl = sl
		return fake
	}
	schedulerSendRun = func(context.Context, string, []string, []string) ([]byte, error) { return []byte("{}"), nil }
	schedulerStarted = func(s *scheduler.Scheduler) { fake.mu.Lock(); fake.s = s; fake.mu.Unlock() }
	ticks := make(chan []scheduler.Decision, 100)
	schedulerOnTick = func(ds []scheduler.Decision) { ticks <- ds }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var stdout string
	go func() {
		out, _, err := captureOutput(t, func() error {
			return runSchedulerRun(ctx, []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
		})
		stdout = out
		done <- err
	}()
	waitDecision(t, ticks, "roster_launched")
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("scheduler run on an absent session root: %v", err)
	}
	if strings.Contains(stdout, "create the session first") || !strings.Contains(stdout, "created the session") {
		t.Fatalf("stdout:\n%s", stdout)
	}
	if !slices.ContainsFunc(f.calls, func(c string) bool {
		return strings.HasPrefix(c, "amq session create s1 --root "+filepath.Join(proj, ".agent-mail"))
	}) {
		t.Fatalf("calls %q", f.calls)
	}
	lines, _ := schedproc.ReadDecisionsTail(root)
	if len(lines) < 2 || lines[0].Kind != KindSessionCreated || !strings.Contains(lines[0].Reason, "scheduler run: ") {
		t.Fatalf("decisions %+v", lines)
	}
}

// Codex re-check on #829: the producer side of the launch budget's marks.
// scheduler run hands AMQ_SQUAD_START to every launch (seatlaunch.Options.
// Start, which writes each seat's marks.json), and its first-message hook
// records a seat's first message in the seat's marks.
func TestSchedulerRunHandsTheStartToLaunchesAndRecordsFirstMessages(t *testing.T) {
	_, root, proj := schedulerFixture(t)
	t.Setenv(leadStartEnv, "2026-11-01T09:00:00.25Z")
	var got time.Time
	prev := []any{schedulerAMQLauncher, schedulerStarted}
	t.Cleanup(func() {
		schedulerAMQLauncher = prev[0].(func(*schedproc.Slots, string, seatlaunch.Options) scheduler.SeatLauncher)
		schedulerStarted = prev[1].(func(*scheduler.Scheduler))
	})
	ctx, cancel := context.WithCancel(context.Background())
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, o seatlaunch.Options) scheduler.SeatLauncher {
		got = o.Start
		return &cliFakeLauncher{sl: sl}
	}
	schedulerStarted = func(*scheduler.Scheduler) { cancel() }
	if _, _, err := captureOutput(t, func() error {
		return runSchedulerRun(ctx, []string{"--project", proj, "--tick", "20ms"}, os.Stdout)
	}); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 1, 9, 0, 0, 250000000, time.UTC); !got.Equal(want) {
		t.Fatalf("the launches' start %v, want %v", got, want)
	}
	// the first-message hook: the seat's outbox message lands in its marks
	mustWrite(t, filepath.Join(launch.StagedDir(root, "impl-1"), launch.FileName), `{"schema":1,"handle":"impl-1","launch_id":"L9"}`)
	created := time.Date(2026, 11, 1, 9, 1, 0, 0, time.UTC)
	msg := filepath.Join(root, "agents", "impl-1", "outbox", "sent", "m1.md")
	mustWrite(t, msg, "---json\n{\"id\":\"m1\",\"created\":\""+created.Format(time.RFC3339Nano)+"\"}\n---\nhello\n")
	_ = os.Chtimes(msg, created, created)
	if at, ok := firstMessageMark(root)("impl-1", created.Add(-time.Minute)); !ok || !at.Equal(created) {
		t.Fatalf("hook %v %v", at, ok)
	}
	if m, err := seatlaunch.ReadSeatMarks(root, "impl-1"); err != nil || m.LaunchID != "L9" || m.FirstMessage == nil || !m.FirstMessage.Equal(created) {
		t.Fatalf("marks %+v %v", m, err)
	}
}
