package seatcommit

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// 3.2.0-2b-1's cancel rows, driven through the scheduler's exit processing
// over this package's real archive and clean (rule 18: the fake seat is this
// test binary, re-run as the writer helper; temp HOME; nothing under a real
// session root).

// TestCancelledSeatWriterHelper is the fake seat process of R1: it writes
// live.txt until it is stopped (SIGTERM, as the launcher's close sends).
func TestCancelledSeatWriterHelper(t *testing.T) {
	target := os.Getenv("AMQ_TEST_CANCEL_WRITER")
	if target == "" {
		return
	}
	for n := 1; ; n++ {
		if err := os.WriteFile(target, []byte(fmt.Sprintf("write-%06d\n", n)), 0o644); err != nil {
			os.Exit(2)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// cancellingProcessLauncher closes the fake seat the way the AMQ launcher
// closes a provider (a signal to the process, its termination confirmed
// before the close returns) and keeps the file's bytes at the stop.
type cancellingProcessLauncher struct {
	cmd     *exec.Cmd
	target  string
	stopped []byte
	closed  []string
}

func (*cancellingProcessLauncher) RequestSeat(scheduler.SeatRequest) error       { return nil }
func (*cancellingProcessLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (l *cancellingProcessLauncher) CloseSeat(handle, reason string) error {
	l.closed = append(l.closed, handle+":"+reason)
	if l.cmd == nil || l.cmd.Process == nil {
		return nil
	}
	_ = l.cmd.Process.Signal(syscall.SIGTERM)
	_ = l.cmd.Wait()
	l.cmd = nil
	l.stopped, _ = os.ReadFile(l.target)
	return nil
}

type commitExitChecker struct{ f *fixture }

func (c commitExitChecker) CheckExit(handle, taskID, why string) scheduler.ExitResult {
	tk, err := c.f.store.Get(taskID)
	if err != nil {
		return scheduler.ExitResult{Deferred: true, Detail: err.Error()}
	}
	o := c.f.opts(tk)
	o.Handle = handle
	r := ExitCheck(o, why)
	return scheduler.ExitResult{OK: r.OK, Deferred: r.Deferred, Refused: !r.OK && !r.Deferred, ReasonCode: r.ReasonCode, Detail: r.Detail}
}

// commitAttemptCleaner is schedproc.CleanSeats' CleanAttempt over this
// package: the archive named by task and attempt, then the clean.
type commitAttemptCleaner struct {
	f     *fixture
	calls int
}

func (*commitAttemptCleaner) PrepareSeat(string) error { return nil }
func (c *commitAttemptCleaner) CleanAttempt(handle, taskID string, attempt int, outcome string) error {
	c.calls++
	if _, err := ArchiveAttemptReason(c.f.root, handle, gitBin(c.f.t), nil, taskID, attempt, outcome, c.f.now); err != nil {
		return err
	}
	_, err := CleanSeat(c.f.root, handle, gitBin(c.f.t), nil, c.f.now)
	return err
}

// cancelScheduler is a scheduler over the fixture's store with the seat up,
// its first tick taken so the scheduler saw the seat's lease held.
func cancelScheduler(t *testing.T, f *fixture, launcher scheduler.SeatLauncher, cleaner scheduler.SeatPreparer, incarnation string) *scheduler.Scheduler {
	t.Helper()
	s, err := scheduler.New(scheduler.Options{Store: f.store, Now: func() time.Time { return f.now.Add(2 * time.Second) }, Launcher: launcher, Exits: commitExitChecker{f: f}, Preparer: cleaner, Config: scheduler.Config{IdleTimeout: time.Hour}, Incarnation: incarnation})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp(f.handle, task.Pool{Runtime: "test", Model: "writer"}, f.now)
	if d, err := s.Tick(); err != nil || decisionHas(d, "seat_closing") || decisionHas(d, "seat_cleaned") {
		t.Fatalf("the observing tick: %+v, %v", d, err)
	}
	return s
}

// R1: a cancel while the seat is mid-write. The fake seat writes live.txt
// every 20 ms; the cancel lands; the scheduler closes the seat first (its
// termination confirmed), then archives and cleans on the next tick: no
// file remains in the worktree, and the archived bytes are the bytes at
// the stop, never a write made during the archive. Reverted (the clean run
// before the stop: runExits no longer waits for the closing seat), the
// writer writes again after the clean and live.txt remains.
func TestCancelledLiveWriterStopsBeforeItsFinalTreeIsArchivedAndCleaned(t *testing.T) {
	f := newFixture(t)
	tk := f.leased("live.txt")
	target := filepath.Join(f.wt, "live.txt")
	cmd := exec.Command(os.Args[0], "-test.run=^TestCancelledSeatWriterHelper$")
	cmd.Env = append(os.Environ(), "AMQ_TEST_CANCEL_WRITER="+target)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if body, err := os.ReadFile(target); err == nil && len(body) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("writer did not create live.txt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	launcher := &cancellingProcessLauncher{cmd: cmd, target: target}
	cleaner := &commitAttemptCleaner{f: f}
	s := cancelScheduler(t, f, launcher, cleaner, "canceltest")
	if _, err := f.store.Cancel(tk.ID, "lead", "stop the writer", f.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	first, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if len(launcher.stopped) == 0 || cleaner.calls != 0 || !decisionHas(first, "seat_closing") || !decisionHas(first, "seat_closed") || len(launcher.closed) != 1 || launcher.closed[0] != f.handle+":task_cancelled" {
		t.Fatalf("stop pass: stopped=%q cleaner calls=%d closed=%v decisions=%+v", launcher.stopped, cleaner.calls, launcher.closed, first)
	}
	stopped := append([]byte(nil), launcher.stopped...)
	second, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if cleaner.calls != 1 || !decisionHas(second, "seat_cleaned") {
		t.Fatalf("clean pass: cleaner calls=%d decisions=%+v", cleaner.calls, second)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("live.txt remains after clean: %v", err)
	}
	if got := sh(t, f.wt, "status", "--porcelain"); got != "" {
		t.Fatalf("the worktree is not clean after the clean: %s", got)
	}
	ledger, err := ReadLedger(f.root, f.handle)
	if err != nil || len(ledger.Discarded) != 1 || ledger.Discarded[0].TaskID != tk.ID || ledger.Discarded[0].Attempt != 1 || ledger.Discarded[0].Reason != task.OutcomeCancelled {
		t.Fatalf("discard ledger = %+v, %v", ledger.Discarded, err)
	}
	archived := sh(t, f.wt, "cat-file", "-p", ledger.Discarded[0].Tree+":live.txt") + "\n"
	if archived != string(stopped) {
		t.Fatalf("archived writer bytes = %q, stopped bytes = %q", archived, stopped)
	}
	got, err := f.store.Get(tk.ID)
	if err != nil || got.Attempts[0].CleanedAt == nil {
		t.Fatalf("cancelled attempt was not durably marked cleaned: %+v, %v", got.Attempts[0], err)
	}
}

// R7: a cancel under a tampered ledger (0c-A's ErrTampered at the exit
// check) quarantines and refuses, once, and leaves the worktree untouched
// with no discard: cleaning never writes past a tamper. The refusal holds
// the seat (Codex's D): the next tick re-checks silently.
func TestCancelledAttemptWithTamperedLedgerIsQuarantinedWithoutTouchingWorktree(t *testing.T) {
	f := newFixture(t)
	tk := f.leased("keep.txt")
	target := filepath.Join(f.wt, "keep.txt")
	want := []byte("uncommitted work must survive\n")
	if err := os.WriteFile(target, want, 0o644); err != nil {
		t.Fatal(err)
	}
	launcher := &cancellingProcessLauncher{}
	cleaner := &commitAttemptCleaner{f: f}
	s := cancelScheduler(t, f, launcher, cleaner, "tampertest")
	if _, err := f.store.Cancel(tk.ID, "lead", "stop", f.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	tamperBase(t, f, f.base, '0')
	if d, err := s.Tick(); err != nil || !decisionHas(d, "seat_closed") {
		t.Fatalf("the close pass: %+v, %v", d, err)
	}
	decisions, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if cleaner.calls != 0 || !decisionHas(decisions, "exit_refused") || decisionHas(decisions, "seat_cleaned") {
		t.Fatalf("tampered cancellation was not refused before cleanup: calls=%d decisions=%+v", cleaner.calls, decisions)
	}
	again, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if cleaner.calls != 0 || decisionHas(again, "exit_refused") || decisionHas(again, "seat_cleaned") {
		t.Fatalf("the held refusal was repeated or cleaned: calls=%d decisions=%+v", cleaner.calls, again)
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(want) {
		t.Fatalf("worktree changed after refusal: got=%q err=%v", got, err)
	}
	raw, err := os.ReadFile(ledgerPath(f.root, f.handle))
	if err != nil {
		t.Fatal(err)
	}
	var ledger Ledger
	if err := json.Unmarshal(raw, &ledger); err != nil || len(ledger.Discarded) != 0 {
		t.Fatalf("refusal wrote a discard: %+v, %v", ledger.Discarded, err)
	}
	entries, err := os.ReadDir(QuarantineDir(f.root))
	if err != nil || len(entries) != 1 {
		t.Fatalf("quarantine entries = %d, %v", len(entries), err)
	}
	if tk, _ := f.store.Get(tk.ID); tk.Attempts[0].CleanedAt != nil {
		t.Fatalf("a refused cancellation was recorded cleaned: %+v", tk.Attempts[0])
	}
}

// R6, the untracked-only half (item 5): a worktree on a cancelled task's
// branch with only untracked files is archived, moved to the seat branch
// by the clean itself (never refused), and the seat is granted again. The
// shape is t86's (2026-11-05 18:08Z): the ledger's lease record is gone
// (a relaunch recorded the base afresh) while the worktree stayed on the
// cancelled task's branch, so the branch is one the ledger does not name.
// Reverted (the old refusal of a branch the lease does not name, put
// back), the clean refuses and the file stays.
func TestUntrackedOnlyCancellationIsArchivedCleanedAndSeatCanBeGranted(t *testing.T) {
	f := newFixture(t)
	tk := f.leased("scratch.txt")
	if got := sh(t, f.wt, "symbolic-ref", "--short", "HEAD"); got != TaskBranch(f.handle, tk.ID) {
		t.Fatalf("the worktree is not on the task branch: %s", got)
	}
	target := filepath.Join(f.wt, "scratch.txt")
	want := "untracked cancellation evidence\n"
	if err := os.WriteFile(target, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Cancel(tk.ID, "lead", "stop", f.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || l.Lease == nil {
		t.Fatalf("the lease record before the relaunch: %+v, %v", l.Lease, err)
	}
	l.Lease = nil
	if err := writeLedger(f.root, l, "test: the relaunch's fresh record"); err != nil {
		t.Fatal(err)
	}
	if archived, err := ArchiveAttemptReason(f.root, f.handle, gitBin(t), nil, tk.ID, 1, task.OutcomeCancelled, f.now.Add(2*time.Second)); err != nil || !archived {
		t.Fatalf("archive untracked-only attempt = %v, %v", archived, err)
	}
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("scratch.txt remains after clean: %v", err)
	}
	if branch := sh(t, f.wt, "symbolic-ref", "--short", "HEAD"); branch != f.branch {
		t.Fatalf("worktree branch = %q, want %q", branch, f.branch)
	}
	if got := sh(t, f.wt, "rev-parse", "--verify", "refs/heads/"+TaskBranch(f.handle, tk.ID)); got == "" {
		t.Fatal("the clean deleted the task branch")
	}
	ledger, err := ReadLedger(f.root, f.handle)
	if err != nil || len(ledger.Discarded) != 1 {
		t.Fatalf("discard ledger = %+v, %v", ledger.Discarded, err)
	}
	if got := sh(t, f.wt, "cat-file", "-p", ledger.Discarded[0].Tree+":scratch.txt") + "\n"; got != want {
		t.Fatalf("archived scratch.txt = %q", got)
	}
	pool := task.Pool{Runtime: "test", Model: "worker"}
	next, err := f.store.Add(task.AddInput{Title: "next", Class: "script", Pool: &pool}, f.now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	s, err := scheduler.New(scheduler.Options{Store: f.store, Now: func() time.Time { return f.now.Add(3 * time.Second) }, Launcher: &cancellingProcessLauncher{}, Config: scheduler.Config{IdleTimeout: time.Hour}, Incarnation: "regrant"})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp(f.handle, pool, f.now.Add(3*time.Second))
	decisions, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	granted, err := f.store.Get(next.ID)
	if err != nil || granted.Lease == nil || granted.Lease.Holder != f.handle || !decisionHas(decisions, "dispatched") {
		t.Fatalf("next grant = %+v, decisions=%+v err=%v", granted.Lease, decisions, err)
	}
}

// R6, the unrecorded-commit half, through the scheduler: a cancelled task
// branch with a commit neither pushed nor on the ledger stays put (the
// clean refuses seat_head_detached_unmerged), the reason is logged once
// (seat_clean_deferred on the first tick, silent on the next), the
// cleaner's marker is released, and the attempt is not recorded cleaned.
func TestUnrecordedCommitOnCancelledBranchDefersOnceAndReleasesTheMarker(t *testing.T) {
	f := newFixture(t)
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// local commit\n")
	sh(t, f.wt, "add", "-A")
	sh(t, f.wt, "commit", "-q", "-m", "seat local commit")
	head := sh(t, f.wt, "rev-parse", "HEAD")
	launcher := &cancellingProcessLauncher{}
	cleaner := &commitAttemptCleaner{f: f}
	s := cancelScheduler(t, f, launcher, cleaner, "unrecorded")
	if _, err := f.store.Cancel(tk.ID, "lead", "stop", f.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Tick(); err != nil || !decisionHas(d, "seat_closed") {
		t.Fatalf("the close pass: %+v, %v", d, err)
	}
	first, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	deferred := func(ds []scheduler.Decision) int {
		n := 0
		for _, d := range ds {
			if d.Kind == "seat_clean_deferred" && strings.Contains(d.Reason, RefusalSeatHeadDetachedUnmerged) {
				n++
			}
		}
		return n
	}
	if cleaner.calls != 1 || deferred(first) != 1 || decisionHas(first, "seat_cleaned") {
		t.Fatalf("first clean pass: calls=%d decisions=%+v", cleaner.calls, first)
	}
	second, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if cleaner.calls != 2 || deferred(second) != 0 || decisionHas(second, "seat_cleaned") {
		t.Fatalf("second clean pass said it again or cleaned: calls=%d decisions=%+v", cleaner.calls, second)
	}
	if got := sh(t, f.wt, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the refused clean moved HEAD from %s to %s", head, got)
	}
	if _, err := os.Stat(filepath.Join(f.store.Dir, "reserved", f.handle)); !os.IsNotExist(err) {
		t.Fatalf("the refused clean left its marker: %v", err)
	}
	if got, _ := f.store.Get(tk.ID); got.Attempts[0].CleanedAt != nil {
		t.Fatalf("a refused clean was recorded: %+v", got.Attempts[0])
	}
}

// Codex's G on #898, the foreign-branch move: a detached HEAD. At a commit
// the resting commit holds (rev-list rest..HEAD empty), the clean moves the
// worktree back to the seat branch; at a local commit the ledger did not
// record, the clean refuses with RefusalSeatHeadDetachedUnmerged, naming the
// detached HEAD, and HEAD stays.
func TestCleanSeatMovesADetachedHeadAtARecordedCommitAndRefusesAnUnrecordedOne(t *testing.T) {
	f := newFixture(t)
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now); err != nil {
		t.Fatal(err)
	}
	rest := sh(t, f.wt, "rev-parse", "HEAD")
	sh(t, f.wt, "checkout", "-q", "--detach", rest)
	if raw, err := os.ReadFile(filepath.Join(sh(t, f.wt, "rev-parse", "--git-dir"), "HEAD")); err != nil || strings.HasPrefix(string(raw), "ref:") {
		t.Fatalf("HEAD is not detached: %q, %v", raw, err)
	}
	if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now); err != nil {
		t.Fatalf("a detached HEAD at the resting commit: %v", err)
	}
	if got := sh(t, f.wt, "symbolic-ref", "--short", "HEAD"); got != f.branch {
		t.Fatalf("the clean left HEAD at %q, want %s", got, f.branch)
	}
	sh(t, f.wt, "checkout", "-q", "--detach", rest)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// detached local commit\n")
	sh(t, f.wt, "add", "-A")
	sh(t, f.wt, "commit", "-q", "-m", "detached local commit")
	head := sh(t, f.wt, "rev-parse", "HEAD")
	_, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now)
	if err == nil || !strings.Contains(err.Error(), RefusalSeatHeadDetachedUnmerged) || !strings.Contains(err.Error(), "(detached HEAD)") {
		t.Fatalf("a detached HEAD at an unrecorded commit: %v", err)
	}
	if got := sh(t, f.wt, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the refused clean moved HEAD from %s to %s", head, got)
	}
}

func decisionHas(decisions []scheduler.Decision, kind string) bool {
	for _, d := range decisions {
		if d.Kind == kind {
			return true
		}
	}
	return false
}
