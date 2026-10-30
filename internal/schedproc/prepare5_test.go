package schedproc

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier, the fifth round's (g): a seat whose commit ledger cannot be
// read is not prepared, and so not granted: seat_prepare_failed, no lease
// (a missing ledger, a seat with no recorded base, is still left as it is).
func TestPrepareRefusesALedgerReadError(t *testing.T) {
	c := &Committer{Step: func(context.Context, string, task.Task) seatcommit.Result { return seatcommit.Result{OK: true} },
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := newLiveRig(t, c)
	r.s.Preparer = &CleanSeats{Root: r.root}
	tk, err := r.store.Add(task.AddInput{Title: "t", Class: "summarize", Pool: &sonnet}, r.now)
	if err != nil {
		t.Fatal(err)
	}
	// The fake launcher names the seat of the task t1 seat-t1.
	if err := os.MkdirAll(seatcommit.LedgerDir(r.root), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seatcommit.LedgerDir(r.root), "seat-"+tk.ID+".json"), []byte("not a ledger"), 0o600); err != nil {
		t.Fatal(err)
	}
	failed := false
	for i := 0; i < 4; i++ {
		d, err := r.tick(func() {})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range d {
			failed = failed || x.Kind == "seat_prepare_failed"
		}
	}
	if got, _ := r.store.Get(tk.ID); got.Lease != nil || !failed {
		t.Fatalf("a seat with an unreadable ledger: leased %v, seat_prepare_failed %v", got.Lease != nil, failed)
	}
}
