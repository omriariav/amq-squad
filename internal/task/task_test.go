package task

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func newStore(t *testing.T) Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustAdd(t *testing.T, s Store, in AddInput) Task {
	t.Helper()
	if in.Class == "" {
		in.Class = "implement"
	}
	got, err := s.Add(in, t0)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestOpenRequiresCleanAbsoluteRoot(t *testing.T) {
	for _, root := range []string{"", "rel/root", "/a/../b"} {
		if _, err := Open(root); err == nil {
			t.Errorf("Open(%q) accepted", root)
		}
	}
	s, _ := Open("/r")
	if s.Dir != filepath.Join("/r", "extensions", "amq-squad", "tasks") {
		t.Fatalf("dir %s", s.Dir)
	}
}

func TestAddValidates(t *testing.T) {
	s := newStore(t)
	if _, err := s.Add(AddInput{Title: "x"}, t0); err == nil || !strings.Contains(err.Error(), "class") {
		t.Fatalf("a task without a class was accepted: %v", err)
	}
	if _, err := s.Add(AddInput{Title: "x", Class: "deploy"}, t0); err == nil {
		t.Fatal("a class outside the closed list was accepted")
	}
	if _, err := s.Add(AddInput{Title: " ", Class: "plan"}, t0); err == nil {
		t.Fatal("an empty title was accepted")
	}
	if _, err := s.Add(AddInput{Title: "x", Class: "plan", DependsOn: []string{"t9"}}, t0); err == nil {
		t.Fatal("a missing dependency was accepted")
	}
	if _, err := s.Add(AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"../etc"}}, t0); err == nil {
		t.Fatal("a declared file outside the repository was accepted")
	}
	a := mustAdd(t, s, AddInput{Title: "a", DeclaredFiles: []string{"./src/x.go", "docs/", "src/x.go"}})
	b := mustAdd(t, s, AddInput{Title: "b", Class: "review", DependsOn: []string{a.ID, a.ID}})
	if a.ID != "t1" || b.ID != "t2" || a.State != StatePending || a.Schema != Schema {
		t.Fatalf("ids/state: %+v %+v", a, b)
	}
	if strings.Join(a.DeclaredFiles, ",") != "docs/,src/x.go" || len(b.DependsOn) != 1 {
		t.Fatalf("normalization: %v %v", a.DeclaredFiles, b.DependsOn)
	}
	info, err := os.Stat(s.Dir)
	if err != nil || info.Mode().Perm() != 0o700 && runtime.GOOS != "windows" {
		t.Fatalf("store dir mode %v %v", info.Mode(), err)
	}
}

func TestLeaseLifecycle(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a"})
	pool := &Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	g, err := s.Grant(a.ID, "impl-1", pool, time.Minute, t0)
	if err != nil {
		t.Fatal(err)
	}
	if g.State != StateInProgress || g.Lease.Holder != "impl-1" || g.Lease.ClaimedAt != nil || g.Attempt != 1 || g.Pool.Key() != "claude:sonnet:medium" {
		t.Fatalf("grant: %+v", g)
	}
	if _, _, err := s.Renew("impl-1", time.Minute, 0, t0.Add(time.Second)); !errors.Is(err, ErrNoLease) {
		t.Fatalf("an unclaimed lease was renewed: %v", err)
	}
	if _, err := s.Claim(a.ID, "impl-2", nil, time.Minute, t0); err == nil {
		t.Fatal("another seat claimed the lease")
	}
	c, err := s.Claim("", "impl-1", nil, time.Minute, t0.Add(2*time.Second))
	if err != nil || c.Lease.ClaimedAt == nil || !c.Lease.ExpiresAt.Equal(t0.Add(62*time.Second)) {
		t.Fatalf("claim: %+v %v", c.Lease, err)
	}
	if _, renewed, err := s.Renew("impl-1", time.Minute, 10*time.Second, t0.Add(5*time.Second)); err != nil || renewed {
		t.Fatalf("a renewal inside the minimum interval wrote: %v %v", renewed, err)
	}
	r, renewed, err := s.Renew("impl-1", time.Minute, 10*time.Second, t0.Add(30*time.Second))
	if err != nil || !renewed || r.Lease.Renewals != 1 || !r.Lease.ExpiresAt.Equal(t0.Add(90*time.Second)) {
		t.Fatalf("renew: %+v %v %v", r.Lease, renewed, err)
	}
	harnessPass(t, s, a.ID, t0.Add(39*time.Second))
	d, err := s.Done(a.ID, "impl-1", "go test ok", "abc", t0.Add(40*time.Second))
	if err != nil || d.State != StateCompleted || d.Lease != nil || d.Evidence != "go test ok" || len(d.Attempts) != 1 || d.Attempts[0].Outcome != OutcomeCompleted {
		t.Fatalf("done without declared files should complete: %+v %v", d, err)
	}
	if _, err := s.Done(a.ID, "impl-1", "", "", t0); err == nil {
		t.Fatal("done on a completed task passed")
	}
}

func TestDoneWithDeclaredFilesWaitsForTheCommitStep(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a", DeclaredFiles: []string{"src/"}})
	b := mustAdd(t, s, AddInput{Title: "b", DeclaredFiles: []string{"src/x.go"}})
	if _, err := s.Claim(a.ID, "impl-1", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Grant(b.ID, "impl-2", nil, time.Minute, t0); err == nil || !strings.Contains(err.Error(), WaitFileLock) {
		t.Fatalf("an overlapping file set was leased: %v", err)
	}
	d, err := s.Done(a.ID, "impl-1", "e", "", t0.Add(time.Second))
	if err != nil || d.State != StateInProgress || d.Report == nil {
		t.Fatalf("done report: %+v %v", d, err)
	}
	if exp, _ := s.ExpireDue(t0.Add(time.Hour)); len(exp) != 0 {
		t.Fatal("a lease with a pending done report expired")
	}
	r, err := s.RefuseCommit(a.ID, "outside_declared_files", "README.md", RouteLead, t0.Add(2*time.Second))
	if err != nil || r.State != StateBlocked || r.Report == nil || r.Lease != nil {
		t.Fatalf("refuse: %+v %v", r, err)
	}
	list, _ := s.List()
	if HeldBy("impl-1", list) == nil {
		t.Fatal("the reporting seat is not reserved after a refusal")
	}
	if _, err := s.Grant(b.ID, "impl-2", nil, time.Minute, t0); err == nil {
		t.Fatal("a blocked task released its file lock")
	}
	w, err := s.Widen(a.ID, "lead", "README is part of it", []string{"README.md"}, time.Minute, t0.Add(3*time.Second))
	if err != nil || w.State != StateInProgress || w.Lease.Holder != "impl-1" || w.Lease.ClaimedAt == nil || len(w.ScopeChanges) != 1 {
		t.Fatalf("widen: %+v %v", w, err)
	}
	harnessPass(t, s, a.ID, t0.Add(4*time.Second))
	c, err := s.Complete(a.ID, t0.Add(4*time.Second))
	if err != nil || c.State != StateCompleted || c.ClosedAt == nil {
		t.Fatalf("complete: %+v %v", c, err)
	}
	if _, err := s.Grant(b.ID, "impl-2", nil, time.Minute, t0); err != nil {
		t.Fatalf("file lock not released on completion: %v", err)
	}
}

func TestDependenciesGateTheLease(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a"})
	b := mustAdd(t, s, AddInput{Title: "b", DependsOn: []string{a.ID}})
	if _, err := s.Grant(b.ID, "h", nil, 0, t0); err == nil || !strings.Contains(err.Error(), WaitDependencies) {
		t.Fatalf("dependency not enforced: %v", err)
	}
	if got, err := s.Claim("", "h", nil, 0, t0); err != nil || got.ID != a.ID {
		t.Fatalf("pull should take the ready task: %+v %v", got, err)
	}
	if _, err := s.Claim("", "h", nil, 0, t0); err == nil {
		t.Fatal("a seat claimed a second lease")
	}
	harnessPass(t, s, a.ID, t0)
	if _, err := s.Done(a.ID, "h", "", "", t0); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Claim("", "h", nil, 0, t0); err != nil || got.ID != b.ID {
		t.Fatalf("the dependent was not released: %+v %v", got, err)
	}
}

func TestExpireDueReturnsUnclaimedAndExpiredToThePool(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a"})
	b := mustAdd(t, s, AddInput{Title: "b"})
	if _, err := s.Grant(a.ID, "never-claims", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(b.ID, "goes-quiet", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if exp, _ := s.ExpireDue(t0.Add(59 * time.Second)); len(exp) != 0 {
		t.Fatalf("expired early: %v", exp)
	}
	exp, err := s.ExpireDue(t0.Add(time.Minute))
	if err != nil || len(exp) != 2 {
		t.Fatalf("expire: %v %v", exp, err)
	}
	if exp[0].State != StatePending || exp[0].Attempts[0].Outcome != OutcomeUnclaimed || exp[1].Attempts[0].Outcome != OutcomeExpired {
		t.Fatalf("outcomes: %+v", exp)
	}
	if got, err := s.Grant(a.ID, "impl-2", nil, time.Minute, t0.Add(time.Minute)); err != nil || got.Attempt != 2 {
		t.Fatalf("re-grant: %+v %v", got, err)
	}
}

func TestFailBlockReleaseResetCancel(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a"})
	if _, err := s.Claim(a.ID, "h", nil, 0, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Fail(a.ID, "other", "x", t0); err == nil {
		t.Fatal("a non-holder failed the attempt")
	}
	f, err := s.Fail(a.ID, "h", "tests red", t0)
	if err != nil || f.State != StatePending || f.Attempts[0].Outcome != OutcomeFailed {
		t.Fatalf("fail: %+v %v", f, err)
	}
	if _, err := s.Claim(a.ID, "h", nil, 0, t0); err != nil {
		t.Fatal(err)
	}
	bl, err := s.Block(a.ID, "h", "seat_blocked", "needs a decision", "", t0)
	if err != nil || bl.State != StateBlocked || bl.BlockRoute != RouteLead || bl.Lease != nil {
		t.Fatalf("block: %+v %v", bl, err)
	}
	pool := &Pool{Runtime: "codex", Model: "gpt-5.4", Effort: "medium"}
	rs, err := s.Reset(a.ID, pool, t0)
	if err != nil || rs.State != StatePending || rs.Pool.Runtime != "codex" || rs.BlockReason != "" {
		t.Fatalf("reset: %+v %v", rs, err)
	}
	if _, err := s.Grant(a.ID, "h", nil, 0, t0); err != nil {
		t.Fatal(err)
	}
	rl, err := s.Release(a.ID, "lead", "reassign", t0)
	if err != nil || rl.State != StatePending || rl.Attempts[2].Outcome != OutcomeReleased {
		t.Fatalf("release: %+v %v", rl, err)
	}
	prio := 5
	if st, err := s.Set(a.ID, &prio, nil, t0); err != nil || st.Priority != 5 {
		t.Fatalf("set: %+v %v", st, err)
	}
	c, err := s.Cancel(a.ID, "lead", "superseded", t0)
	if err != nil || c.State != StateCancelled || c.ClosedAt == nil || !strings.Contains(c.CancelReason, "superseded") {
		t.Fatalf("cancel: %+v %v", c, err)
	}
	if _, err := s.Cancel(a.ID, "lead", "again", t0); err == nil {
		t.Fatal("a terminal task was cancelled again")
	}
}

func TestDispatchOrderAndPoolFilter(t *testing.T) {
	s := newStore(t)
	claude := &Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	codex := &Pool{Runtime: "codex", Model: "gpt-5.4", Effort: "medium"}
	mustAdd(t, s, AddInput{Title: "low", Pool: claude})
	mustAdd(t, s, AddInput{Title: "codex", Pool: codex, Priority: 9})
	mustAdd(t, s, AddInput{Title: "high", Pool: claude, Priority: 3})
	got, err := s.Claim("", "c1", claude, 0, t0)
	if err != nil || got.Title != "high" {
		t.Fatalf("priority order: %+v %v", got, err)
	}
	if got, _ := s.Claim("", "c2", claude, 0, t0); got.Title != "low" {
		t.Fatalf("pool filter: %+v", got)
	}
}

// The task's rule: the pool changes only until the first attempt is
// granted. A task never granted is re-pinned freely; after a grant the
// re-pin is refused with the code, and a task back in pending after an
// unclaimed expiry keeps its attempt count and is refused too.
func TestSetRepinsOnlyBeforeTheFirstGrant(t *testing.T) {
	s := newStore(t)
	initial := &Pool{Runtime: "claude", Model: "haiku", Effort: "low"}
	repinned := &Pool{Runtime: "codex", Model: "gpt-5.6-terra", Effort: "medium"}
	other := &Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	a := mustAdd(t, s, AddInput{Title: "a", Pool: initial})
	got, err := s.Set(a.ID, nil, repinned, t0)
	if err != nil || got.Pool == nil || *got.Pool != *repinned {
		t.Fatalf("re-pin before the first grant: %+v %v", got, err)
	}
	refused := func(when string) {
		t.Helper()
		_, err := s.Set(a.ID, nil, other, t0)
		if !errors.Is(err, ErrRePinAfterAttempt) || !strings.Contains(err.Error(), "task reset --pool") {
			t.Fatalf("re-pin %s: %v", when, err)
		}
		if stored, err := s.Get(a.ID); err != nil || stored.Pool == nil || *stored.Pool != *repinned {
			t.Fatalf("refused re-pin %s changed the task: %+v %v", when, stored, err)
		}
	}
	// Granted, not claimed: the grant is the boundary, not the claim.
	if _, err := s.Grant(a.ID, "h", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	refused("after the first grant")
	// The unclaimed lease expires: the task is pending again, attempt 1 stands.
	expired, err := s.ExpireDue(t0.Add(time.Hour))
	if err != nil || len(expired) != 1 || expired[0].State != StatePending || expired[0].Attempt != 1 ||
		expired[0].Attempts[0].Outcome != OutcomeUnclaimed {
		t.Fatalf("unclaimed expiry: %+v %v", expired, err)
	}
	refused("after an unclaimed expiry")
}

func TestSetWaitWritesOnlyOnChange(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a"})
	if ch, err := s.SetWait(a.ID, WaitSeatCap, t0); err != nil || !ch {
		t.Fatalf("first set: %v %v", ch, err)
	}
	if ch, _ := s.SetWait(a.ID, WaitSeatCap, t0); ch {
		t.Fatal("an unchanged wait reason wrote")
	}
}

func TestFilesOverlap(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"src/x.go", "src/x.go", true},
		{"src/x.go", "src/y.go", false},
		{"src/", "src/a/b.go", true},
		{"src/**", "src/a/b.go", true},
		{"src/*.go", "src/x.go", true},
		{"src/*.go", "src/a/x.go", false},
		{"src/*.go", "docs/*.md", false},
		{"*.md", "README.md", true},
		{"**", "anything/at/all", true},
		{"a/**/z.go", "a/b", true},
		{"internal/task/", "internal/taskx/y.go", false},
		{"cmd/[ab].go", "cmd/a.go", true},
		{"cmd/a?.go", "cmd/b*.go", true}, // both globs: conservative overlap
	}
	for _, c := range cases {
		if got := FilesOverlap([]string{c.a}, []string{c.b}); got != c.want {
			t.Errorf("%s vs %s: %v, want %v", c.a, c.b, got, c.want)
		}
		if got := FilesOverlap([]string{c.b}, []string{c.a}); got != c.want {
			t.Errorf("%s vs %s (swapped): %v, want %v", c.b, c.a, got, c.want)
		}
	}
	if FilesOverlap(nil, []string{"**"}) {
		t.Fatal("an empty set overlaps")
	}
}

func TestCommittedJournalReplaysAndSnapshotOverlays(t *testing.T) {
	s := newStore(t)
	a := mustAdd(t, s, AddInput{Title: "a"})
	fault = func(phase string) error {
		if phase == phaseAfterCommit {
			return errors.New("crash")
		}
		return nil
	}
	_, err := s.Grant(a.ID, "h", nil, time.Minute, t0)
	fault = nil
	var ce *CommittedError
	if !errors.As(err, &ce) {
		t.Fatalf("want a committed error, got %v", err)
	}
	// The task file still holds the before-image; the lock-free snapshot
	// overlays the journal; a locked read replays it.
	snap, err := Snapshot(s.Dir)
	if err != nil || snap[0].State != StateInProgress {
		t.Fatalf("snapshot did not overlay the journal: %+v %v", snap, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, journalName)); err != nil {
		t.Fatal("the journal was removed by a read-only snapshot")
	}
	got, err := s.Get(a.ID)
	if err != nil || got.State != StateInProgress {
		t.Fatalf("locked read did not replay: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, journalName)); !os.IsNotExist(err) {
		t.Fatal("the journal survived the replay")
	}

	fault = func(phase string) error {
		if phase == phaseBeforeCommit {
			return errors.New("crash")
		}
		return nil
	}
	_, err = s.Release(a.ID, "lead", "x", t0)
	fault = nil
	if err == nil || errors.As(err, &ce) {
		t.Fatalf("a crash before the commit point must fail uncommitted: %v", err)
	}
	if got, _ := s.Get(a.ID); got.State != StateInProgress {
		t.Fatalf("an uncommitted transaction became visible: %+v", got)
	}
}

// Acceptance 8: eight processes claim from one store; no task gets two
// lease holders and every task is taken exactly once.
func TestConcurrentClaimsEightProcesses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the store lock is a no-op on Windows (internal/flock)")
	}
	s := newStore(t)
	const n = 40
	for i := 0; i < n; i++ {
		mustAdd(t, s, AddInput{Title: "t" + strconv.Itoa(i), Class: "script"})
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var cmds []*exec.Cmd
	var outs []*strings.Builder
	for p := 0; p < 8; p++ {
		cmd := exec.Command(exe, "-test.run=^TestClaimWorkerProcess$")
		cmd.Env = append(os.Environ(), "TASK_CLAIM_WORKER="+s.Dir, "TASK_CLAIM_HANDLE=seat-"+strconv.Itoa(p))
		out := &strings.Builder{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds, outs = append(cmds, cmd), append(outs, out)
	}
	seen := map[string]string{}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("worker %d: %v\n%s", i, err, outs[i])
		}
		for _, line := range strings.Split(outs[i].String(), "\n") {
			if id, ok := strings.CutPrefix(line, "CLAIMED "); ok {
				if prev, dup := seen[id]; dup {
					t.Fatalf("task %s claimed by seat-%d and %s", id, i, prev)
				}
				seen[id] = "seat-" + strconv.Itoa(i)
			}
		}
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	var holders []string
	for _, tk := range list {
		if tk.State != StateCompleted || tk.Attempt != 1 || len(tk.Attempts) != 1 {
			t.Fatalf("task %s: state %s attempt %d attempts %d", tk.ID, tk.State, tk.Attempt, len(tk.Attempts))
		}
		holders = append(holders, tk.Attempts[0].Holder)
	}
	sort.Strings(holders)
	if len(seen) != n {
		t.Fatalf("claimed %d of %d tasks", len(seen), n)
	}
	t.Logf("40 tasks over 8 processes; holders %v", countHolders(holders))
}

func countHolders(h []string) map[string]int {
	out := map[string]int{}
	for _, x := range h {
		out[x]++
	}
	return out
}

// TestClaimWorkerProcess is the child of the concurrency test: it claims and
// completes tasks until none is left.
func TestClaimWorkerProcess(t *testing.T) {
	dir := os.Getenv("TASK_CLAIM_WORKER")
	if dir == "" {
		t.Skip("child process only")
	}
	s, handle := Store{Dir: dir}, os.Getenv("TASK_CLAIM_HANDLE")
	for {
		now := time.Now().UTC()
		got, err := s.Claim("", handle, nil, time.Minute, now)
		if errors.Is(err, ErrNotFound) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fmt.Printf("CLAIMED %s\n", got.ID)
		harnessPass(t, s, got.ID, now)
		if _, err := s.Done(got.ID, handle, "", "", now); err != nil {
			t.Fatal(err)
		}
	}
}
