//go:build unix

package uninstall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// P1-a: the seat set is this root's own (launch records, task store), never a
// ledger's: an edited ledger naming another squad's seat deletes nothing.
func TestAnEditedLedgerNamingAnotherSquadsSeatDeletesNothing(t *testing.T) {
	f := newFixture(t)
	head := git(t, f.proj, "rev-parse", "HEAD")
	other := "refs/amq-squad/evidence/other-seat/x/1"
	git(t, f.proj, "update-ref", other, head)
	writeJSON(t, filepath.Join(f.x, "commits", "other-seat.json"), seatcommit.Ledger{Schema: 1, Handle: "other-seat", Worktree: f.wt, Branch: "b",
		Discarded: []seatcommit.LedgerDiscard{{TaskID: "x", EvidenceRef: seatcommit.EvidenceRef{Ref: other}}}})
	// a sealed entry (genuine seal) for a seat that has no launch record and no
	// task here: only the seat set keeps its ref
	stranger := filepath.Join(f.proj, ".claude", "worktrees", "stranger")
	git(t, f.proj, "worktree", "add", "-q", "-b", "stranger", stranger)
	writeJSON(t, filepath.Join(f.x, "launch", "stranger", launch.FileName), launch.Record{Schema: 1, Handle: "stranger", Worktree: stranger, Branch: "stranger"})
	if _, err := seatcommit.RecordBase(f.root, "stranger", stranger, "stranger", head, time.Now()); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(stranger, "work.txt"), "w\n", 0o644)
	if ok, err := seatcommit.CleanSeat(f.root, "stranger", "git", nil, time.Now()); err != nil || !ok {
		t.Fatalf("clean seat: %v %v", ok, err)
	}
	sl, err := seatcommit.ReadLedger(f.root, "stranger")
	if err != nil || len(sl.EvidenceRefs()) != 1 {
		t.Fatalf("the stranger's sealed ledger: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(f.x, "launch", "stranger")); err != nil {
		t.Fatal(err)
	}
	// and a ref of one of this squad's own seats that no sealed entry records
	unrecorded := "refs/amq-squad/evidence/seat-a/zzz/1"
	git(t, f.proj, "update-ref", unrecorded, head)
	res, err := Run(f.all())
	if err != nil {
		t.Fatal(err)
	}
	if !refExists(t, f.proj, other) {
		t.Error("another squad's seat ref was deleted on an edited ledger's word")
	}
	if !refExists(t, f.proj, sl.EvidenceRefs()[0]) {
		t.Error("the ref of a seat this root does not know was deleted on a sealed ledger's word")
	}
	if !refExists(t, f.proj, unrecorded) {
		t.Error("a ref no sealed ledger entry records was deleted")
	}
	if refExists(t, f.proj, f.ref) {
		t.Error("the recorded ref should be deleted")
	}
	if out := render(res); !strings.Contains(out, other+" ") || !strings.Contains(out, "evidence_ref_foreign") {
		t.Errorf("the foreign seat's ref is not listed as left:\n%s", out)
	}
	if out := render(res); !strings.Contains(out, "evidence_ref_unrecorded") || !strings.Contains(out, unrecorded) {
		t.Errorf("the unrecorded ref is not listed:\n%s", out)
	}
}

// P1-a: a ref recorded only in an entry that fails its seal is left.
func TestARefRecordedOnlyInATamperedEntryIsLeft(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.x, "commits", "seat-a.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"seat/a"`) {
		t.Fatal("the ledger has no branch to edit")
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(raw), `"seat/a"`, `"seat/b"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Run(f.all())
	if err != nil {
		t.Fatal(err)
	}
	if !refExists(t, f.proj, f.ref) {
		t.Error("a ref recorded only by a tampered ledger entry was deleted")
	}
	if out := render(res); !strings.Contains(out, "evidence_ref_unrecorded") || !strings.Contains(out, "seal") {
		t.Errorf("the tampered entry is not reported:\n%s", out)
	}
}

// P1-b: run.lock is not in the removal set; it is unlinked last, after every
// other group, so a scheduler cannot lock a new inode mid-run.
func TestTheSchedulerLockIsUnlinkedLastNeverMidRun(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3000; i++ {
		write(t, filepath.Join(f.x, "tasks", fmt.Sprintf("f%05d.dat", i)), "x", 0o600)
	}
	lock := filepath.Join(f.x, "scheduler", "run.lock")
	tasks := filepath.Join(f.x, "tasks")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var early bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			gone := !exists(lock) // observed first: so tasks existed at that moment too
			if gone && exists(tasks) {
				early = true
				return
			}
		}
	}()
	_, err := Run(f.all())
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if early {
		t.Error("run.lock was unlinked while other groups were still being removed")
	}
	if exists(lock) {
		t.Error("run.lock was not unlinked at the end")
	}
}

// P1-c: a live seat whose start-stamp probe fails is busy, naming the pid; so
// is a stray file where a seat directory is expected.
func TestALiveSeatWithAFailingProbeRefuses(t *testing.T) {
	f := newFixture(t)
	writeJSON(t, filepath.Join(f.x, "launch", "seat-a", "proof.json"), launch.ProofEntry{Handle: "seat-a", PID: os.Getpid(), Start: "x"})
	o := f.all()
	o.StartToken = func(int) (string, bool) { return "", false }
	_, err := Run(o)
	e := wantCode(t, err, CodeBusy)
	if !strings.Contains(e.Detail, fmt.Sprintf("pid %d", os.Getpid())) {
		t.Errorf("the refusal does not name the pid: %s", e.Detail)
	}
}

func TestAStrayFileWhereASeatDirectoryIsExpectedRefuses(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.x, "launch", "stray"), "x", 0o600)
	_, err := Run(f.all())
	e := wantCode(t, err, CodeBusy)
	if !strings.Contains(e.Detail, "stray") {
		t.Errorf("the refusal does not name the file: %s", e.Detail)
	}
}

// P1-d: the directory that was verified is the one that goes. Renamed aside
// and replaced after the check, the replacement is left and nothing is removed
// at the name.
func TestAWorktreeRenamedAsideAndReplacedIsNotRemoved(t *testing.T) {
	f := newFixture(t)
	o := f.all()
	var once sync.Once
	o.Now = func() time.Time {
		// busy runs after the plan is made and the worktree verified, before removal
		once.Do(func() {
			if err := os.Rename(f.wt, f.wt+".aside"); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(f.wt, "precious"), "keep\n", 0o600)
		})
		return time.Now()
	}
	res, err := Run(o)
	wantCode(t, err, CodePartial)
	if !exists(filepath.Join(f.wt, "precious")) {
		t.Error("the replacement directory was removed")
	}
	if !strings.Contains(strings.Join(res.Failures, "\n"), CodeWorktreeReplaced) {
		t.Errorf("worktree_replaced is not reported:\n%s", render(res))
	}
}

// P2: every candidate appears with the reason it is not removed.
func TestEveryRejectedWorktreeCandidateIsListedWithAReason(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(f.outside, "elsewhere")
	write(t, filepath.Join(outside, "file"), "x", 0o600)
	notDir := filepath.Join(f.proj, ".claude", "worktrees", "afile")
	write(t, notDir, "x", 0o600)
	for h, w := range map[string]string{"seat-p": outside, "seat-q": notDir, "seat-r": "relative/path"} {
		writeJSON(t, filepath.Join(f.x, "launch", h, launch.FileName), launch.Record{Schema: 1, Handle: h, Worktree: w, Branch: "b"})
	}
	writeJSON(t, filepath.Join(f.x, "tasks", "t3.json"), map[string]any{"id": "t3", "project_path": f.proj, "attempts": []map[string]any{{"holder": "seat-q"}}})
	res, err := Run(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	out := render(res)
	for _, want := range []string{outside + ": not removed", notDir + ": not removed, it exists and is not a directory", "relative/path: not removed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report lacks %q:\n%s", want, out)
		}
	}
}

// P1-d: the gitdir the opened directory's .git names must be, by device and
// inode, the project's own .git/worktrees entry (not merely a path that reads
// alike).
func TestTheGitdirMustBeTheProjectsOwnEntryByInode(t *testing.T) {
	f := newFixture(t)
	other := realDir(t)
	git(t, other, "init", "-q", "-b", "main")
	write(t, filepath.Join(other, "README"), "x\n", 0o644)
	git(t, other, "add", "README")
	git(t, other, "commit", "-q", "-m", "init")
	git(t, other, "worktree", "add", "-q", "-b", "seat-a-other", filepath.Join(other, ".claude", "worktrees", "seat-a"))
	imposter := filepath.Join(f.proj, ".claude", "worktrees", "imposter")
	write(t, filepath.Join(imposter, ".git"), "gitdir: "+filepath.Join(other, ".git", "worktrees", "seat-a")+"\n", 0o600)
	parent, err := openDir(f.proj, []string{".claude", "worktrees"})
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if wd, err := verifyWorktreeDir(f.proj, parent, "imposter", imposter); err == nil {
		wd.Close()
		t.Error("a directory whose .git names another project's gitdir was verified")
	}
	if wd, err := verifyWorktreeDir(f.proj, parent, "seat-a", f.wt); err != nil {
		t.Errorf("the genuine worktree was not verified: %v", err)
	} else {
		wd.Close()
	}
}
