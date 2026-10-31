package cli

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
	f := &upFake{t: t, proj: proj, root: root, panes: map[string]bool{}, sessions: map[string]bool{}}
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
