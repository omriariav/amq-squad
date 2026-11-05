package schedproc

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// 3.2.0-0c-A, the A4 row's scheduler half: the prepare's read of a tampered
// ledger is still seat_prepare_failed (no lease), and its reason now names
// the quarantine the refusing read made, whose copy is the refused body byte
// for byte and whose sidecar names the prepare. The root is t.TempDir
// (rule 18).
func TestPrepareNamesTheQuarantineOfATamperedLedger(t *testing.T) {
	c := &Committer{Step: func(context.Context, string, task.Task) seatcommit.Result { return seatcommit.Result{OK: true} },
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := newLiveRig(t, c)
	r.s.Preparer = &CleanSeats{Root: r.root}
	tk, err := r.store.Add(task.AddInput{Title: "t", Class: "summarize", Pool: &sonnet}, r.now)
	if err != nil {
		t.Fatal(err)
	}
	// The fake launcher names the seat of the task t1 seat-t1. A sealed
	// ledger for it, then a hand edit of its base (the tag fails).
	handle := "seat-" + tk.ID
	base := strings.Repeat("a", 40)
	if _, err := seatcommit.RecordBase(r.root, handle, filepath.Join(r.root, "wt"), "seat/"+handle, base, r.now); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(seatcommit.LedgerDir(r.root), handle+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(base), []byte(strings.Repeat("0", 40)), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("the base is not in the ledger")
	}
	if err := os.WriteFile(p, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile(sealchain.HeadPath(r.root, "commits."+handle))
	if err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(seatcommit.QuarantineDir(r.root), "commits."+handle+".seq1")
	reason := ""
	for i := 0; i < 4 && reason == ""; i++ {
		d, err := r.tick(func() {})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range d {
			if x.Kind == "seat_prepare_failed" {
				reason = x.Reason
			}
		}
	}
	if reason == "" {
		t.Fatal("no seat_prepare_failed decision")
	}
	if !strings.Contains(reason, "ledger_tampered") || !strings.Contains(reason, "quarantined at "+slot) {
		t.Fatalf("seat_prepare_failed's reason does not name the quarantine %s: %q", slot, reason)
	}
	if got, err := os.ReadFile(filepath.Join(slot, "body.json")); err != nil || !bytes.Equal(got, tampered) {
		t.Fatalf("the quarantine body is not the refused body: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(slot, "head.json")); err != nil || !bytes.Equal(got, head) {
		t.Fatalf("the quarantine head is not the head: %v", err)
	}
	if side, err := os.ReadFile(filepath.Join(slot, "quarantine.txt")); err != nil || !strings.Contains(string(side), "writer: scheduler prepare\n") {
		t.Fatalf("the sidecar does not name the prepare: %v\n%s", err, side)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, tampered) {
		t.Fatal("the prepare moved the document")
	}
	if got, _ := r.store.Get(tk.ID); got.Lease != nil {
		t.Fatal("the task was leased on a tampered ledger")
	}
}
