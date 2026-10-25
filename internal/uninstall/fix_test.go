//go:build unix

package uninstall

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// P1-1: a symbolic evidence ref must never take a branch with it.
func TestASymbolicEvidenceRefNeverDeletesABranch(t *testing.T) {
	f := newFixture(t)
	git(t, f.proj, "symbolic-ref", "refs/amq-squad/evidence/seat-a/t2/1", "refs/heads/main")
	main := git(t, f.proj, "rev-parse", "refs/heads/main")
	_, err := Run(f.all())
	wantCode(t, err, CodePartial)
	if git(t, f.proj, "rev-parse", "refs/heads/main") != main {
		t.Fatal("the branch was deleted through a symbolic evidence ref")
	}
	if !refExists(t, f.proj, evidenceRef) {
		t.Error("nothing should be deleted in a repository that holds a symbolic evidence ref")
	}
}

// P1-1: ref names are never taken from a ledger.
func TestALedgerNamingABranchAsEvidenceDeletesNothing(t *testing.T) {
	f := newFixture(t)
	tree := git(t, f.proj, "rev-parse", "HEAD^{tree}")
	writeJSON(t, filepath.Join(f.x, "commits", "seat-a.json"), seatcommit.Ledger{Schema: 1, Handle: "seat-a", Worktree: f.wt, Branch: "seat/a",
		Discarded: []seatcommit.LedgerDiscard{{TaskID: "t1", EvidenceRef: seatcommit.EvidenceRef{Ref: "refs/heads/seat/a", RefTree: tree}}}})
	if _, err := Run(f.all()); err != nil {
		t.Fatal(err)
	}
	if !refExists(t, f.proj, "refs/heads/seat/a") {
		t.Error("a ref named only by a ledger was deleted")
	}
}

// P1-1: a ref replaced between the listing and the deletion is left.
func TestARefReplacedSinceItWasListedIsLeft(t *testing.T) {
	f := newFixture(t)
	_, refs, err := seatcommit.ListEvidenceRefs("git", []string{f.proj})
	if err != nil || len(refs) != 1 {
		t.Fatalf("list: %v %d", err, len(refs))
	}
	head := git(t, f.proj, "rev-parse", "HEAD")
	git(t, f.proj, "update-ref", evidenceRef, head)
	if err := seatcommit.DeleteEvidenceRefs("git", []string{f.proj}, refs); err == nil {
		t.Error("the stale deletion should be reported")
	}
	if git(t, f.proj, "rev-parse", evidenceRef) != head {
		t.Error("a ref replaced since it was listed was deleted")
	}
}

// P1-2: every unreadable input is busy, never skipped as not live.
func TestEveryUnreadableBusyInputRefuses(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
	cases := map[string]func(f *fixture){
		"truncated proof.json": func(f *fixture) {
			write(t, filepath.Join(f.x, "launch", "seat-a", "proof.json"), `{"handle":"seat-a","pi`, 0o600)
		},
		"truncated launch record": func(f *fixture) {
			write(t, filepath.Join(f.x, "launch", "seat-a", launch.FileName), `{"schema":1,"han`, 0o600)
		},
		"exits.json unreadable": func(f *fixture) { os.Chmod(filepath.Join(f.x, "scheduler", "exits.json"), 0) },
		"scheduler dir unreadable": func(f *fixture) {
			os.Chmod(filepath.Join(f.x, "scheduler"), 0)
		},
		"lock file unopenable": func(f *fixture) { os.Chmod(filepath.Join(f.x, "scheduler", "run.lock"), 0) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			mutate(f)
			t.Cleanup(func() {
				os.Chmod(filepath.Join(f.x, "scheduler"), 0o700)
				os.Chmod(filepath.Join(f.x, "scheduler", "exits.json"), 0o600)
				os.Chmod(filepath.Join(f.x, "scheduler", "run.lock"), 0o600)
			})
			for _, o := range []Options{f.opts, f.all()} {
				_, err := Run(o)
				wantCode(t, err, CodeBusy)
			}
		})
	}
}

// P1-3: a staged record cannot aim the removal at another project.
func TestAStagedRecordCannotAimTheRemovalAtAnotherProject(t *testing.T) {
	f := newFixture(t)
	// another project, with a real linked worktree of its own: git vouches for
	// it, only the task store does not
	other := realDir(t)
	git(t, other, "init", "-q", "-b", "main")
	write(t, filepath.Join(other, "README"), "x\n", 0o644)
	git(t, other, "add", "README")
	git(t, other, "commit", "-q", "-m", "init")
	valuable := filepath.Join(other, ".claude", "worktrees", "valuable")
	git(t, other, "worktree", "add", "-q", "-b", "v", valuable)
	write(t, filepath.Join(valuable, "file"), "precious\n", 0o600)
	// inside the right project, but git does not list it as a worktree
	plain := filepath.Join(f.proj, ".claude", "worktrees", "plain")
	write(t, filepath.Join(plain, "file"), "precious\n", 0o600)
	writeJSON(t, filepath.Join(f.x, "launch", "seat-x", launch.FileName), launch.Record{Schema: 1, Handle: "seat-x", Worktree: valuable, Branch: "b"})
	writeJSON(t, filepath.Join(f.x, "launch", "seat-y", launch.FileName), launch.Record{Schema: 1, Handle: "seat-y", Worktree: plain, Branch: "b"})
	writeJSON(t, filepath.Join(f.x, "tasks", "t2.json"), map[string]any{"id": "t2", "project_path": f.proj, "attempts": []map[string]any{{"holder": "seat-y"}}})
	res, err := Run(f.all())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{valuable, plain} {
		if !exists(filepath.Join(p, "file")) {
			t.Errorf("%s was removed on a staged record's word", p)
		}
	}
	if out := render(res); !strings.Contains(out, "not removed") || !strings.Contains(out, valuable) {
		t.Errorf("the report does not list the unremoved worktree:\n%s", out)
	}
	if exists(f.wt) {
		t.Error("the squad's own worktree should still be removed")
	}
}

// P2-1: a failed group stops the removal of the records that name it; the
// retry finishes.
func TestAFailedEvidenceGroupKeepsTheRecordsAndTheRetryFinishes(t *testing.T) {
	f := newFixture(t)
	git(t, f.proj, "symbolic-ref", "refs/amq-squad/evidence/seat-a/t2/1", "refs/heads/main")
	res, err := Run(f.all())
	wantCode(t, err, CodePartial)
	for _, p := range []string{filepath.Join(f.x, "commits", "seat-a.json"), filepath.Join(f.x, "launch"), filepath.Join(f.x, "tasks"), f.wt} {
		if !exists(p) {
			t.Errorf("%s was removed although the evidence group failed", p)
		}
	}
	if !strings.Contains(strings.Join(res.Remaining, "\n"), filepath.Join(f.x, "commits")) {
		t.Errorf("the held-back ledgers are not in the report:\n%s", render(res))
	}
	git(t, f.proj, "update-ref", "--no-deref", "-d", "refs/amq-squad/evidence/seat-a/t2/1")
	if _, err := Run(f.all()); err != nil {
		t.Fatalf("the retry: %v", err)
	}
	if exists(filepath.Join(f.x, "commits")) || exists(f.wt) || refExists(t, f.proj, evidenceRef) {
		t.Error("the retry did not finish")
	}
	if res, err := Run(f.all()); err != nil || !res.NothingToDo {
		t.Errorf("after the retry: %v", err)
	}
}

// P2-2: a symlink at a nested expected directory refuses the whole run.
func TestASymlinkedNestedDirectoryIsRefused(t *testing.T) {
	f := newFixture(t)
	cache := filepath.Join(f.x, "verifiers", "gomodcache")
	if err := os.Rename(cache, filepath.Join(f.outside, "moved")); err != nil {
		t.Fatal(err)
	}
	mustLink(t, f.outside, cache)
	before := snap(t, f.root, f.home, f.proj)
	_, err := Run(f.all())
	wantCode(t, err, CodeSymlink)
	sameSnap(t, before, snap(t, f.root, f.home, f.proj), "nested symlink refusal")
}

// P2-3: a hard-linked secret is not zeroed (the other name would be).
func TestAHardLinkedSecretIsUnlinkedWithoutZeroing(t *testing.T) {
	f := newFixture(t)
	other := filepath.Join(f.outside, "other-name")
	if err := os.Link(filepath.Join(f.home, ".amq-squad", "claude-token"), other); err != nil {
		t.Skip("no hard links here")
	}
	res, err := Run(f.all())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(other); string(b) != tokenBody {
		t.Error("the other link's contents were destroyed")
	}
	if exists(filepath.Join(f.home, ".amq-squad", "claude-token")) {
		t.Error("the secret was not unlinked")
	}
	if !strings.Contains(strings.Join(res.Notes, "\n"), "hard link") || !strings.Contains(render(res), "hard link") {
		t.Error("the report does not say why it was not zeroed")
	}
}

// P2-4: a writing run holds the scheduler's own lock; a second uninstall
// and a starting scheduler are refused while it is held.
func TestAWritingRunHoldsTheSchedulersLock(t *testing.T) {
	f := newFixture(t)
	h, why := takeSchedulerLock(f.all())
	if h == nil {
		t.Fatalf("the lock was not taken: %s", why)
	}
	lock := filepath.Join(f.x, "scheduler", "run.lock")
	if l, ok, _ := flock.TryExclusive(lock, true); ok {
		l.Close()
		t.Error("a scheduler could take the lock while an uninstall holds it")
	}
	_, err := Run(f.all())
	wantCode(t, err, CodeBusy)
	h.release()
	if _, err := Run(f.all()); err != nil {
		t.Fatalf("after the first uninstall let go: %v", err)
	}
}

// P2-4: the busy check runs again with the lock held.
func TestTheBusyCheckRunsAgainWithTheLockHeld(t *testing.T) {
	f := newFixture(t)
	var probed bool
	o := f.all()
	o.Now = func() time.Time {
		// busy reads the clock for proof windows: by now the run holds the lock
		if l, ok, _ := flock.TryExclusive(filepath.Join(f.x, "scheduler", "run.lock"), false); ok {
			l.Close()
		} else {
			probed = true
		}
		return time.Now()
	}
	if _, err := Run(o); err != nil {
		t.Fatal(err)
	}
	if !probed {
		t.Error("the busy check did not run with the scheduler lock held")
	}
}

// P2-5: an unreadable target is a failed entry, never "already gone".
func TestAnUnreadableTargetIsAFailedEntry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
	f := newFixture(t)
	reg := filepath.Join(f.home, ".amq-squad", "registry.yaml")
	if err := os.Chmod(reg, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(reg, 0o600) })
	res, err := Run(f.all())
	wantCode(t, err, CodePartial)
	if !strings.Contains(strings.Join(res.Remaining, "\n"), "registry.yaml") || !exists(reg) {
		t.Errorf("the unreadable file is not reported as remaining:\n%s", render(res))
	}
}
