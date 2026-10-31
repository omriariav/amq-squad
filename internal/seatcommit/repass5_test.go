package seatcommit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Phase 5 re-pass of 2b-2 B, P1-1: a managed (or protected) path must stay
// a regular file in the snapshot: CLAUDE.md replaced by a symlink whose
// target text is the managed block's own text, or a mode change there, is
// protected_path, though the block's shape matches.
func TestManagedPathMustStayARegularFile(t *testing.T) {
	f := newFixture(t)
	p := filepath.Join(f.wt, "CLAUDE.md")
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(claudeMD, p); err != nil { // the link's target text is the file's content
		t.Fatal(err)
	}
	if res := f.run(f.reported("CLAUDE.md")); res.OK || res.ReasonCode != ReasonProtected || !strings.Contains(res.Detail, "not a regular file") {
		t.Fatalf("a symlinked managed file: %+v", res)
	}
	g := newFixture(t)
	if err := os.Chmod(filepath.Join(g.wt, "CLAUDE.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if res := g.run(g.reported("CLAUDE.md")); res.OK || res.ReasonCode != ReasonProtected {
		t.Fatalf("a mode change of a managed file: %+v", res)
	}
}

// Phase 5 re-pass of 2b-2 B, P2-3 and P2-6: with --open-pr a gh failure
// after a successful push defers the completion (pr_pending) instead of
// completing the task unbound; the retry opens the PR with the diff the
// step checked when it committed, not an empty diff against the commit.
func TestPRFailureDefersAndTheRetryKeepsTheCheckedDiff(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// pending\n")
	tk := f.reported("cmd/")
	gh, _, calls := fakeGh(t, f)
	// gh answers the binding check before the commit (no open PR), then fails.
	broken, once := filepath.Join(f.dir, "gh-broken"), filepath.Join(f.dir, "gh-once")
	write(t, broken, "#!/bin/sh\nif [ ! -e "+once+" ]; then touch "+once+"; echo '[]'; exit 0; fi\necho 'gh: HTTP 502' >&2\nexit 1\n")
	os.Chmod(broken, 0o755)
	o := f.opts(tk)
	o.OpenPR, o.Gh = true, broken
	f.storeBinding(&o)
	first := Run(o)
	if first.OK || !first.Deferred || first.ReasonCode != ReasonPRPending || !first.Pushed || first.Commit == "" || first.DiffDigest == "" {
		t.Fatalf("a gh failure after the push: %+v", first)
	}
	o.Gh = gh
	again := Run(o)
	raw, _ := os.ReadFile(calls)
	if !again.OK || again.PRNumber != 42 || again.DiffDigest != first.DiffDigest || again.Lines != first.Lines || len(again.Changes) != len(first.Changes) {
		t.Fatalf("the retry: %+v (first diff %s)", again, first.DiffDigest)
	}
	if !strings.Contains(string(raw), "Amq-Squad-Diff: "+first.DiffDigest) {
		t.Fatalf("the PR body does not carry the checked diff %s:\n%s", first.DiffDigest, raw)
	}
}

// Phase 5 re-pass of 2b-2 B, P2-5: a discarded tree is anchored under
// refs/amq-squad/evidence/<task>/<n> in the seat repository, so git gc keeps
// it, until its retention expiry deletes the ref.
func TestKeptTreesAreAnchoredUntilTheirRetention(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "README.md"), "out of scope\n")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if res.ReasonCode != ReasonOutside {
		t.Fatalf("refusal: %+v", res)
	}
	f.store.RefuseCommit(tk.ID, res.ReasonCode, res.Detail, res.Route, f.now)
	f.store.Reset(tk.ID, nil, f.now)
	if d, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.trust(), f.now); err != nil || !d {
		t.Fatalf("clean: %v %v", d, err)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || len(l.Discarded) != 1 || l.Discarded[0].Ref == "" || l.Discarded[0].RetainUntil == nil {
		t.Fatalf("ledger evidence: %+v %v", l.Discarded, err)
	}
	ref, tree := l.Discarded[0].Ref, l.Discarded[0].Tree
	if got := sh(t, f.main, "rev-parse", ref); got != tree {
		t.Fatalf("%s is %s, the kept tree is %s", ref, got, tree)
	}
	sh(t, f.main, "-c", "gc.reflogExpire=now", "-c", "gc.reflogExpireUnreachable=now", "gc", "-q", "--prune=now")
	sh(t, f.main, "cat-file", "-e", tree) // still there after gc
	// The retention expiry deletes the ref; the record stays.
	rp, err := worktreePaths(f.wt)
	if err != nil {
		t.Fatal(err)
	}
	expireEvidence(seatRepo(gitBin(t), harnessEnv(os.Environ()), rp), &l, f.now.Add(EvidenceRetention+time.Hour))
	if l.Discarded[0].Ref != "" || l.Discarded[0].Tree != tree {
		t.Fatalf("after the expiry: %+v", l.Discarded[0])
	}
}

// Phase 5 re-pass of 2b-2 B, P2-7: the declared set's digest is over its
// canonical JSON, and an entry with a newline is refused at the store.
func TestDeclaredSetDigestIsCanonical(t *testing.T) {
	if DeclaredDigest([]string{"a", "b"}) == DeclaredDigest([]string{"a\nb"}) {
		t.Fatal(`["a","b"] and ["a\nb"] share a digest`)
	}
	if _, err := task.CleanDeclaredFiles([]string{"cmd/\nREADME.md"}); err == nil {
		t.Fatal("a declared entry with a newline was accepted")
	}
}

// Phase 5 re-pass of 2b-2 B, the ruling: the commits ledger is sealed under
// the session seed with a signed head; a hand edit, an older copy put back,
// or a deleted head is ledger_tampered, a block routed to the owner.
func TestSealedCommitsLedgerDetectsASameUIDEdit(t *testing.T) {
	cases := []struct {
		what   string
		tamper func(f *fixture, p string, orig []byte)
	}{
		{"a hand-edited base", func(f *fixture, p string, orig []byte) {
			os.WriteFile(p, []byte(strings.Replace(string(orig), f.base, strings.Repeat("0", len(f.base)), 1)), 0o600)
		}},
		{"an older copy put back", func(f *fixture, p string, orig []byte) {
			l, err := ReadLedger(f.root, f.handle)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeLedger(f.root, l); err != nil { // a later sealed version
				t.Fatal(err)
			}
			os.WriteFile(p, orig, 0o600)
		}},
		{"a deleted head", func(f *fixture, p string, orig []byte) {
			os.Remove(sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
		}},
	}
	for i, c := range cases {
		f := newFixture(t)
		p := ledgerPath(f.root, f.handle)
		orig, _ := os.ReadFile(p)
		if _, err := ReadLedger(f.root, f.handle); err != nil {
			t.Fatalf("a sealed ledger as written: %v", err)
		}
		c.tamper(f, p, orig)
		write(t, filepath.Join(f.wt, "cmd", "root.go"), fmt.Sprintf("package cmd\n// %d\n", i))
		res := f.run(f.reported("cmd/"))
		if res.OK || res.ReasonCode != ReasonLedgerTampered || res.Route != task.RouteOwner {
			t.Errorf("%s: %+v", c.what, res)
		}
	}
}
