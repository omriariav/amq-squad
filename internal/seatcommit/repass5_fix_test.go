package seatcommit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// discardOnce leaves an out-of-scope edit, has it refused, resets the task
// and cleans the seat: one discarded tree with its evidence ref.
func discardOnce(t *testing.T, f *fixture) Ledger {
	t.Helper()
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
	if err != nil || len(l.Discarded) == 0 {
		t.Fatalf("ledger: %+v %v", l, err)
	}
	return l
}

// The fix round's item 22: evidence refs are seat-scoped
// (refs/amq-squad/evidence/<seat>/<task>/<n>), so two seats sharing a
// repository never overwrite each other's, and the sealed ledger records
// each ref with its tree.
func TestEvidenceRefsAreSeatScoped(t *testing.T) {
	f := newFixture(t)
	l := discardOnce(t, f)
	e := l.Discarded[0].EvidenceRef
	if !strings.HasPrefix(e.Ref, "refs/amq-squad/evidence/"+f.handle+"/") || e.RefTree != l.Discarded[0].Tree {
		t.Fatalf("the evidence ref %+v (tree %s)", e, l.Discarded[0].Tree)
	}
	if a, b := evidenceRefName("s1", "t1", 1), evidenceRefName("s2", "t1", 1); a == b {
		t.Fatalf("two seats share %s", a)
	}
}

// The fix round's item 22: a recorded evidence ref that was moved or
// deleted (a same-UID seat can edit its repository's refs) is
// ledger_tampered at the next commit step and at the exit check, routed to
// the owner.
func TestMovedOrDeletedEvidenceRefIsTampered(t *testing.T) {
	for _, tamper := range []string{"moved", "deleted"} {
		f := newFixture(t)
		l := discardOnce(t, f)
		ref := l.Discarded[0].Ref
		if tamper == "moved" {
			sh(t, f.main, "update-ref", ref, f.base+"^{tree}")
		} else {
			sh(t, f.main, "update-ref", "-d", ref)
		}
		write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// "+tamper+"\n")
		res := f.run(f.reported("cmd/"))
		if res.OK || res.ReasonCode != ReasonLedgerTampered || res.Route != task.RouteOwner {
			t.Errorf("%s ref, commit step: %+v", tamper, res)
		}
		if ex := ExitCheck(f.opts(task.Task{ID: "t9"}), "gone"); ex.OK || ex.ReasonCode != ReasonLedgerTampered {
			t.Errorf("%s ref, exit check: %+v", tamper, ex)
		}
	}
}

// The fix round's item 25: a sealed ledger that does not decode, or carries
// another schema, is ledger_tampered for the owner, never a deferral.
func TestUndecodableOrWrongSchemaLedgerIsTampered(t *testing.T) {
	for _, edit := range []func(string) string{
		func(s string) string { return strings.Replace(s, `"schema": 1`, `"schema": 2`, 1) },
		func(s string) string { return s[:len(s)/2] },
	} {
		f := newFixture(t)
		p := ledgerPath(f.root, f.handle)
		raw, _ := os.ReadFile(p)
		os.WriteFile(p, []byte(edit(string(raw))), 0o600)
		write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// schema\n")
		res := f.run(f.reported("cmd/"))
		if res.OK || res.Deferred || res.ReasonCode != ReasonLedgerTampered || res.Route != task.RouteOwner {
			t.Errorf("an edited ledger: %+v", res)
		}
	}
}
