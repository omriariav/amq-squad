package fleet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Codex re-pass of d02dd2fc. One row per item; each fails with its fix
// reverted (docs/v3-spike/4-3-evidence/c9-revert-check.txt).

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Identity 1: a mapped path that is a symlink to the checkout does not
// identify it (the mapping is opened no-follow and matched by dev and
// inode).
func TestMappedSymlinkPathIsUnidentified(t *testing.T) {
	dir := canonicalTemp(t)
	app := filepath.Join(dir, "app")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "mapped")
	if err := os.Symlink(app, link); err != nil {
		t.Fatal(err)
	}
	g := grants.Fleet{RepositoryPaths: []grants.RepositoryPath{{Repository: "owner/app", Path: link}}}
	if id, _, err := RepositoryIdentity(app, g); err == nil {
		t.Fatalf("a symlinked mapped path identified the checkout as %q", id)
	}
	g.RepositoryPaths[0].Path = app
	if id, _, err := RepositoryIdentity(app, g); err != nil || id != "owner/app" {
		t.Fatalf("the checkout itself mapped: %q %v", id, err)
	}
}

// Identity 2: a symlinked .amq-squad directory never imports another
// checkout's identity file.
func TestSymlinkedIdentityDirIsRefused(t *testing.T) {
	dir := canonicalTemp(t)
	other, app := filepath.Join(dir, "other"), filepath.Join(dir, "app")
	writeFile(t, filepath.Join(other, IdentityFile), "owner/other\n")
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, ".amq-squad"), filepath.Join(app, ".amq-squad")); err != nil {
		t.Fatal(err)
	}
	if id, _, err := RepositoryIdentity(app, grants.Fleet{}); err == nil {
		t.Fatalf("a symlinked .amq-squad lent the identity %q", id)
	}
}

// Identity 3: a linked worktree's identity is its main checkout's; the
// worktree's own identity file does not count, and an unidentified main
// checkout leaves the worktree unidentified.
func TestWorktreeIdentityIsItsMainCheckouts(t *testing.T) {
	dir := canonicalTemp(t)
	main, wt := filepath.Join(dir, "main"), filepath.Join(dir, "wt")
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	writeFile(t, filepath.Join(gitdir, "commondir"), "../..\n")
	writeFile(t, filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	writeFile(t, filepath.Join(wt, IdentityFile), "owner/wt\n")
	if id, _, err := RepositoryIdentity(wt, grants.Fleet{}); err == nil {
		t.Fatalf("a worktree identified itself as %q with its main checkout unidentified", id)
	}
	writeFile(t, filepath.Join(main, IdentityFile), "owner/main\n")
	id, canon, err := RepositoryIdentity(wt, grants.Fleet{})
	if err != nil || id != "owner/main" || canon != wt {
		t.Fatalf("worktree identity = %q %q %v, want owner/main at %s", id, canon, err, wt)
	}
	g := grants.Fleet{RepositoryPaths: []grants.RepositoryPath{{Repository: "owner/main", Path: wt}}}
	if err := os.Remove(filepath.Join(main, IdentityFile)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := RepositoryIdentity(wt, g); err == nil {
		t.Fatal("a mapping of the worktree path, not its main checkout, identified it")
	}
}

type gatedLauncher struct {
	entered chan struct{}
	release chan struct{}
	mu      sync.Mutex
	closed  []LaunchRequest
}

func (l *gatedLauncher) Launch(context.Context, LaunchRequest) error {
	close(l.entered)
	<-l.release
	return nil
}

func (l *gatedLauncher) Close(_ context.Context, req LaunchRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closed = append(l.closed, req)
	return nil
}

// Reservation: reclaiming a stalled reservation fences it; the launch that
// finishes afterwards closes its seats through the launcher, rolls the
// squad files back, and the row is deleted with launch_fenced. No second
// squad keeps the slot alongside it.
func TestFencedLateLaunchIsClosed(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	alive := func(int, time.Time) Liveness { return LivenessAlive }
	gl := &gatedLauncher{entered: make(chan struct{}), release: make(chan struct{})}
	w := &fakeWriter{}
	slow := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: w, Launcher: gl, Now: c.now, IncarnationAlive: alive, Session: true}
	done := make(chan Result, 1)
	go func() {
		r, err := slow.Start(context.Background(), f.request(t, "app", false))
		if err != nil {
			t.Error(err)
		}
		done <- r
	}()
	<-gl.entered
	c.add(PhaseWindow) // the waiting_seats phase stalls
	other := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: &fakeWriter{}, Launcher: &fakeLauncher{}, Now: c.now, IncarnationAlive: alive, Session: true}
	if r := mustStart(t, other, f.request(t, "web", false)); r.Outcome != OutcomeStarted {
		t.Fatalf("the reclaimed slot: %+v", r)
	}
	reg, _ := Read(f.m.RegistryPath)
	if i := indexOf(reg, "app"); i < 0 || reg.Squads[i].Phase != PhaseFenced || reg.Squads[i].FencedReason != ReleasePhaseStalled {
		t.Fatalf("the stalled reservation is not fenced: %+v", reg.Squads)
	}
	close(gl.release)
	r := <-done
	if r.Outcome != OutcomeRefused || r.Reason != ReleaseLaunchFenced {
		t.Fatalf("the late launch: %+v", r)
	}
	if len(gl.closed) != 1 || len(w.rolledBack) != 1 {
		t.Fatalf("closed %d seats rosters, rolled back %d", len(gl.closed), len(w.rolledBack))
	}
	reg, _ = Read(f.m.RegistryPath)
	if indexOf(reg, "app") >= 0 || indexOf(reg, "web") < 0 {
		t.Fatalf("registry after the close: %+v", reg.Squads)
	}
	if n := len(reg.Released); n == 0 || reg.Released[n-1].Reason != ReleaseLaunchFenced {
		t.Fatalf("released = %+v", reg.Released)
	}
}

func indexOf(r Registry, name string) int {
	for i, s := range r.Squads {
		if s.Name == name {
			return i
		}
	}
	return -1
}

// Liveness 1: admission reads the clock under the registry lock, so a
// start that waited for the lock stamps the time it was admitted.
func TestAdmissionReadsTheClockUnderTheLock(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	mustStart(t, f.m, f.request(t, "app", false)) // the fleet is full
	called := make(chan struct{}, 64)
	m := &Mayor{Grant: f.m.Grant, RegistryPath: f.m.RegistryPath, Squads: &fakeWriter{}, Launcher: &fakeLauncher{},
		Now: func() time.Time { called <- struct{}{}; return c.now() }, Session: true}
	locked, unlock := make(chan struct{}), make(chan struct{})
	go func() {
		_ = Update(f.m.RegistryPath, func(*Registry) error { close(locked); <-unlock; return nil })
	}()
	<-locked
	web := f.request(t, "web", false)
	done := make(chan Result, 1)
	go func() {
		r, _ := m.Start(context.Background(), web)
		done <- r
	}()
	<-called                           // the start is under way and has read the clock once
	time.Sleep(200 * time.Millisecond) // it now waits for the lock
	c.add(time.Hour)
	close(unlock)
	if r := <-done; r.Outcome != OutcomeWaiting {
		t.Fatalf("start: %+v", r)
	}
	reg, _ := Read(f.m.RegistryPath)
	want := c.now().UTC().Format(time.RFC3339)
	if len(reg.Waiting) != 1 || reg.Waiting[0].Since != want {
		t.Fatalf("waiting since %+v, want %s (the clock under the lock)", reg.Waiting, want)
	}
}

// Liveness 2: a foreign pid whose start time is near, not equal to, the
// recorded one is a reused pid: gone.
func TestReusedPidNearTheStartTimeIsGone(t *testing.T) {
	const foreign = 4245
	start := time.Date(2026, 10, 24, 11, 0, 0, 0, time.UTC)
	prevAlive, prevStart := pidAlive, pidStartTime
	t.Cleanup(func() { pidAlive, pidStartTime = prevAlive, prevStart })
	pidAlive = func(pid int) bool { return pid == foreign || prevAlive(pid) }
	pidStartTime = func(pid int) (time.Time, bool) {
		if pid == foreign {
			return start.Add(500 * time.Millisecond), true
		}
		return prevStart(pid)
	}
	m := &Mayor{}
	if got := m.incarnationAlive(foreign, start); got != LivenessGone {
		t.Fatalf("a pid started 500 ms after the record is %v, want gone", got)
	}
	pidStartTime = func(int) (time.Time, bool) { return start, true }
	if got := m.incarnationAlive(foreign, start); got != LivenessAlive {
		t.Fatalf("the exact start time is %v, want alive", got)
	}
}

// Adapters c: the launch runs under the registered root; a session that is
// not the root's name is refused.
func TestStartRunsUnderTheRegisteredRoot(t *testing.T) {
	f := newMayor(t, fleetGrant(2, "owner/app"))
	req := f.request(t, "app", false)
	req.Squad.Session = "elsewhere"
	if r := mustStart(t, f.m, req); r.Outcome != OutcomeRefused || r.Reason != ReasonSession {
		t.Fatalf("a session that is not the root's: %+v", r)
	}
	req.Squad.Session = ""
	if r := mustStart(t, f.m, req); r.Outcome != OutcomeStarted {
		t.Fatalf("start: %+v", r)
	}
	lr := f.launcher.launched[0]
	if lr.Root != req.Squad.Root || lr.Session != filepath.Base(req.Squad.Root) {
		t.Fatalf("launch request %+v, want root %s session %s", lr, req.Squad.Root, filepath.Base(req.Squad.Root))
	}
}

// Adapters a and b at the mayor: a launch that fails with nothing live
// rolls the squad files back; decisions pending is a refusal, never a
// start, and its reservation is released.
type failLauncher struct{ err error }

func (l failLauncher) Launch(context.Context, LaunchRequest) error { return l.err }
func (failLauncher) Close(context.Context, LaunchRequest) error    { return nil }

func TestFailedLaunchRollsBackAndPendingIsRefused(t *testing.T) {
	f := newMayor(t, fleetGrant(2, "owner/app", "owner/web"))
	f.m.Launcher = failLauncher{err: errors.New("amq: not on PATH")}
	if r := mustStart(t, f.m, f.request(t, "app", false)); r.Outcome != OutcomeRefused || r.Reason != ReasonLaunchFailure {
		t.Fatalf("failed launch: %+v", r)
	}
	if len(f.writer.rolledBack) != 1 || len(f.registered(t)) != 0 {
		t.Fatalf("rolled back %v, registry %v", f.writer.rolledBack, f.registered(t))
	}
	f.m.Launcher = failLauncher{err: &DecisionsPendingError{Actions: []string{"a1: trust_folder (approve|deny)"}}}
	r := mustStart(t, f.m, f.request(t, "web", false))
	if r.Outcome != OutcomeRefused || r.Reason != ReasonDecisionsPending || !strings.Contains(r.Detail, "a1: trust_folder") {
		t.Fatalf("decisions pending: %+v", r)
	}
	if len(f.registered(t)) != 0 || len(f.writer.rolledBack) != 2 {
		t.Fatalf("pending left a row or files: %v %v", f.registered(t), f.writer.rolledBack)
	}
}
