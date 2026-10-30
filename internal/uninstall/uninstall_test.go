//go:build unix

package uninstall

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
)

func render(r Result) string {
	var b bytes.Buffer
	r.Render(&b)
	return b.String()
}

func wantCode(t *testing.T, err error, code string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	return e
}

// U1: no flag, nothing changes; the report names every group and the
// commands it will not run.
func TestDryRunChangesNothingAndNamesEveryGroup(t *testing.T) {
	f := newFixture(t)
	before := snap(t, f.root, f.home, f.proj, f.outside)
	for _, o := range []Options{f.opts, {Root: f.root, Home: f.home, OwnerFiles: true, Purge: true}} {
		res, err := Run(o)
		if err != nil {
			t.Fatal(err)
		}
		out := render(res)
		for _, want := range []string{
			"session state under the AMQ root", filepath.Join(f.x, "tasks"), filepath.Join(f.x, "scheduler", "decisions.jsonl"), "verifier module cache",
			"per-seat config dirs", filepath.Join(f.x, "launch", "seat-a", "claude-config", "L1"),
			"the core tools dir", filepath.Join(f.x, "tools", "gh"), filepath.Join(f.x, "tools", "claude"),
			"worktrees the core created", f.wt,
			"owner files under ~/.amq-squad", "claude-token", "gate-binding.json", "registry.yaml", "grants.yaml",
			"sealed ledgers", filepath.Join(f.x, "commits"), filepath.Join(f.x, "scheduler", "exits.json"), "outcome log", "hook ledger",
			f.ref, "clear records and the gate store",
			"claude plugin uninstall amq-squad@amq-squad", "worktree prune",
			"dry run",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("the dry run does not name %q:\n%s", want, out)
			}
		}
		if len(res.Removed) > 0 {
			t.Errorf("a dry run removed %v", res.Removed)
		}
		sameSnap(t, before, snap(t, f.root, f.home, f.proj, f.outside), "dry run")
	}
}

// U2: refuses while anything runs, one row each, naming what is live; with
// --write too, and nothing is removed.
func TestRefusesWhileTheSchedulerLockIsHeld(t *testing.T) {
	f := newFixture(t)
	lock, ok, err := flock.TryExclusive(filepath.Join(f.x, "scheduler", "run.lock"), false)
	if err != nil || !ok || lock == nil {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	defer lock.Close()
	f.refused(t, "scheduler")
}

func TestRefusesWhileTheDecisionSocketIsOpen(t *testing.T) {
	f := newFixture(t)
	// the socket node in the launch directory, and the launch record naming a live pid
	write(t, filepath.Join(f.x, "launch", DecisionSocketName), "", 0o600)
	writeJSON(t, filepath.Join(f.x, "scheduler", "launch.json"), map[string]any{"schema": 1, "lead": "lead", "root": f.root, "pid": os.Getpid()})
	f.refused(t, "decision socket")
}

func TestRefusesWhileASeatIsLive(t *testing.T) {
	f := newFixture(t)
	writeJSON(t, filepath.Join(f.x, "launch", "seat-a", "proof.json"), launch.ProofEntry{Handle: "seat-a", PID: os.Getpid(), Start: liveToken(t)})
	f.refused(t, "seat seat-a is live")
}

func TestRefusesWhileAProofWindowIsOpen(t *testing.T) {
	f := newFixture(t)
	writeJSON(t, filepath.Join(f.x, "launch", "seat-a", launch.FileName), launch.Record{Schema: 1, Handle: "seat-a", Worktree: f.wt, Branch: "seat/a",
		Proof: &launch.ProofTicket{Token: "open-ticket", IssuedAt: time.Now()}})
	f.refused(t, "proof window of seat seat-a")
}

func TestRefusesWhileACommitStepIsInTheExitsQueue(t *testing.T) {
	f := newFixture(t)
	f.writeExits(`{"schema":1,"queue":[{"handle":"seat-a","task_id":"t9","why":"commit"}]}`)
	f.refused(t, "commit step of seat seat-a (task t9)")
}

func (f *fixture) refused(t *testing.T, want string) {
	t.Helper()
	before := snap(t, f.root, f.home, f.proj)
	for _, o := range []Options{f.opts, f.all()} {
		_, err := Run(o)
		e := wantCode(t, err, CodeBusy)
		if !strings.Contains(e.Detail, want) {
			t.Errorf("the busy refusal does not name %q: %s", want, e.Detail)
		}
	}
	sameSnap(t, before, snap(t, f.root, f.home, f.proj), "busy refusal")
}

func TestTheDecisionSocketNameIsTheSchedulersOwn(t *testing.T) {
	if DecisionSocketName != schedproc.DecisionSocketName {
		t.Fatal("the socket name drifted from schedproc")
	}
}

// U3: the owner's global agent config is never written; the commands and the
// codex entries are printed.
func TestNeverWritesTheOwnersGlobalAgentConfig(t *testing.T) {
	f := newFixture(t)
	guarded := []string{filepath.Join(f.home, ".claude"), filepath.Join(f.home, ".codex")}
	before := snap(t, guarded...)
	content := map[string][]byte{}
	for p := range before {
		if b, err := os.ReadFile(p); err == nil {
			content[p] = b
		}
	}
	res, err := Run(f.all())
	if err != nil {
		t.Fatal(err)
	}
	sameSnap(t, before, snap(t, guarded...), "global agent config")
	for p, b := range content {
		if now, _ := os.ReadFile(p); !bytes.Equal(now, b) {
			t.Errorf("%s changed", p)
		}
	}
	out := render(res)
	if !strings.Contains(out, "claude plugin uninstall amq-squad@amq-squad") || !strings.Contains(out, "[projects."+quote(f.wt)+"]") {
		t.Errorf("the by-hand commands are missing:\n%s", out)
	}
}

// U4: sealed ledgers, evidence and the gate store are kept by default.
func TestSealedLedgersAndEvidenceAreKeptByDefault(t *testing.T) {
	f := newFixture(t)
	if _, err := Run(f.writeOnly()); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"commits/seat-a.json", "ledger/seat-a.jsonl", "routing/outcomes.jsonl", "routing/seed", "scheduler/exits.json", "gates/g1.question.json", "clears/seat-a.json"} {
		if !exists(filepath.Join(f.x, rel)) {
			t.Errorf("%s was removed without --purge", rel)
		}
	}
	if !refExists(t, f.proj, f.ref) {
		t.Error("the evidence ref was removed without --purge")
	}
	for _, rel := range []string{"tasks", "launch", "tools", "verifiers", "scheduler/run.lock", "scheduler/decisions.jsonl"} {
		if exists(filepath.Join(f.x, rel)) {
			t.Errorf("%s survived --write", rel)
		}
	}
	if exists(f.wt) {
		t.Error("the worktree survived --write")
	}
	for _, name := range []string{"claude-token", "gate-binding.json", "registry.yaml", "grants.yaml"} {
		if !exists(filepath.Join(f.home, ".amq-squad", name)) {
			t.Errorf("%s was removed without --owner-files", name)
		}
	}
}

// U4: --purge removes the evidence refs through the hardened git only: no
// repository hook runs, and no branch or object of the seat repository goes.
func TestPurgeRemovesEvidenceRefsOnlyAndRunsNoRepositoryHook(t *testing.T) {
	f := newFixture(t)
	marker := filepath.Join(f.outside, "hook-ran")
	write(t, filepath.Join(f.proj, ".git", "hooks", "reference-transaction"), "#!/bin/sh\ntouch '"+marker+"'\n", 0o755)
	tree := git(t, f.proj, "rev-parse", "HEAD^{tree}")
	head := git(t, f.proj, "rev-parse", "HEAD")
	if _, err := Run(f.all()); err != nil {
		t.Fatal(err)
	}
	if refExists(t, f.proj, f.ref) {
		t.Error("the evidence ref survived --purge")
	}
	if exists(marker) {
		t.Error("a repository hook ran: the purge did not use the hardened git environment")
	}
	for _, ref := range []string{"refs/heads/main", "refs/heads/seat/a", tree, head} {
		if !refExists(t, f.proj, ref) {
			t.Errorf("%s of the seat repository is gone", ref)
		}
	}
	for _, rel := range []string{"commits", "ledger", "routing", "scheduler/exits.json", "gates", "clears"} {
		if exists(filepath.Join(f.x, rel)) {
			t.Errorf("%s survived --purge", rel)
		}
	}
}

// U5: the secrets go first and unconditionally, overwritten once with zeros;
// their contents are never printed.
func TestSecretsAreZeroedThenUnlinkedFirstAndNeverPrinted(t *testing.T) {
	f := newFixture(t)
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	held := map[string]*os.File{}
	for _, n := range []string{"claude-token", "gate-binding.json"} {
		h, err := os.Open(filepath.Join(f.home, ".amq-squad", n))
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		held[n] = h
	}
	// another group fails; the secrets still go
	if err := os.Chmod(f.x, 0o500); err != nil {
		t.Fatal(err)
	}
	res, err := Run(f.all())
	wantCode(t, err, CodePartial)
	for n, h := range held {
		if exists(filepath.Join(f.home, ".amq-squad", n)) {
			t.Errorf("%s was not unlinked", n)
		}
		b := make([]byte, 4096)
		got, _ := h.ReadAt(b, 0)
		if got != len(map[string]string{"claude-token": tokenBody, "gate-binding.json": bindingBody}[n]) || bytes.Count(b[:got], []byte{0}) != got {
			t.Errorf("%s was not overwritten once with zeros before the unlink", n)
		}
	}
	if len(res.Removed) < 2 || !strings.HasSuffix(res.Removed[0], "claude-token") && !strings.HasSuffix(res.Removed[1], "claude-token") ||
		!strings.HasSuffix(res.Removed[0], "gate-binding.json") && !strings.HasSuffix(res.Removed[1], "gate-binding.json") {
		t.Errorf("the secrets were not removed first: %v", res.Removed)
	}
	out := render(res) + err.Error()
	if strings.Contains(out, tokenBody) || strings.Contains(out, bindingBody) {
		t.Error("a secret's contents were printed")
	}
	if os.Chmod(f.x, 0o700) != nil {
		t.Fatal("chmod")
	}
}

// Without --owner-files the secrets stay.
func TestSecretsStayWithoutOwnerFiles(t *testing.T) {
	f := newFixture(t)
	o := f.writeOnly()
	o.Purge = true
	if _, err := Run(o); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f.home, ".amq-squad", "claude-token")); string(b) != tokenBody {
		t.Error("claude-token changed without --owner-files")
	}
}

// U6: a symlink in place of any directory it expects is refused and nothing
// is removed.
func TestASymlinkWhereADirectoryIsExpectedIsRefusedAndNothingIsRemoved(t *testing.T) {
	cases := map[string]func(f *fixture){
		"worktree dir": func(f *fixture) {
			os.RemoveAll(f.wt)
			mustLink(t, f.outside, f.wt)
		},
		"worktrees dir": func(f *fixture) {
			os.RemoveAll(filepath.Join(f.proj, ".claude", "worktrees"))
			mustLink(t, f.outside, filepath.Join(f.proj, ".claude", "worktrees"))
		},
		"extension dir": func(f *fixture) {
			os.Rename(f.x, f.x+".moved")
			mustLink(t, f.outside, f.x)
		},
		"tools dir": func(f *fixture) {
			os.RemoveAll(filepath.Join(f.x, "tools"))
			mustLink(t, f.outside, filepath.Join(f.x, "tools"))
		},
		"seat config dir": func(f *fixture) {
			p := filepath.Join(f.x, "launch", "seat-a", "claude-config", "L1")
			os.RemoveAll(p)
			mustLink(t, f.outside, p)
		},
		"owner dir": func(f *fixture) {
			os.Rename(filepath.Join(f.home, ".amq-squad"), filepath.Join(f.outside, "moved"))
			mustLink(t, filepath.Join(f.outside, "moved"), filepath.Join(f.home, ".amq-squad"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			mutate(f)
			before := snap(t, f.root, f.home, f.proj, f.outside)
			_, err := Run(f.all())
			wantCode(t, err, CodeSymlink)
			sameSnap(t, before, snap(t, f.root, f.home, f.proj, f.outside), "symlink refusal")
			if !exists(filepath.Join(f.outside, "sentinel")) {
				t.Error("the symlink target was touched")
			}
		})
	}
}

func mustLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// U7: a second run reports nothing to do.
func TestASecondRunHasNothingToDo(t *testing.T) {
	for name, pick := range map[string]func(*fixture) Options{"default": (*fixture).writeOnly, "everything": (*fixture).all} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if _, err := Run(pick(f)); err != nil {
				t.Fatal(err)
			}
			before := snap(t, f.root, f.home, f.proj, f.outside)
			res, err := Run(pick(f))
			if err != nil {
				t.Fatal(err)
			}
			if !res.NothingToDo || len(res.Removed) != 0 || !strings.Contains(render(res), "nothing to do") {
				t.Errorf("the second run did not report nothing to do:\n%s", render(res))
			}
			sameSnap(t, before, snap(t, f.root, f.home, f.proj, f.outside), "second run")
		})
	}
}

// U7: a partial failure leaves a report of what remains and fails; a later
// run finishes the job.
func TestAPartialFailureReportsWhatRemainsAndFails(t *testing.T) {
	f := newFixture(t)
	if os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop root")
	}
	if err := os.Chmod(f.x, 0o500); err != nil {
		t.Fatal(err)
	}
	res, err := Run(f.writeOnly())
	wantCode(t, err, CodePartial)
	if len(res.Remaining) == 0 || !strings.Contains(render(res), "what remains") || !strings.Contains(strings.Join(res.Remaining, "\n"), filepath.Join(f.x, "tasks")) {
		t.Fatalf("the report does not name what remains:\n%s", render(res))
	}
	if !exists(filepath.Join(f.x, "tasks")) || exists(f.wt) {
		t.Error("the failed entries should remain and the others be removed")
	}
	if err := os.Chmod(f.x, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(f.writeOnly()); err != nil {
		t.Fatalf("the retry: %v", err)
	}
	if exists(filepath.Join(f.x, "tasks")) {
		t.Error("the retry left the session state")
	}
	res, err = Run(f.writeOnly())
	if err != nil || !res.NothingToDo {
		t.Fatalf("after the retry: %v nothing=%v", err, res.NothingToDo)
	}
}
