package schedproc

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier, item 5 of the third round: the deferral limit's refusal is a
// decided result, retained until applied: a transient store failure at the
// limit retries the refusal at the next tick and starts no new step.
func TestDeferralLimitRefusalIsRetained(t *testing.T) {
	var steps atomic.Int32
	c := &Committer{Inline: time.Second,
		Step: func(context.Context, string, task.Task) seatcommit.Result {
			steps.Add(1)
			return seatcommit.Result{Deferred: true, ReasonCode: seatcommit.ReasonVerifierInterrupted, Detail: "interrupted"}
		},
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := newLiveRig(t, c) // MaxCommitDeferrals 3
	a, _ := r.reported("a")
	for i := 0; i < 2; i++ {
		if _, err := r.tick(func() {}); err != nil {
			t.Fatal(err)
		}
	}
	dir := task.DirFor(r.root)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := r.tick(func() {}) // the third deferral: the limit, the refusal fails to apply
	os.Chmod(dir, 0o700)
	if err == nil {
		t.Skip("the store accepted a write into a read-only directory (running as root)")
	}
	if steps.Load() != 3 {
		t.Fatalf("the limit tick did not reach the step (%d steps)", steps.Load())
	}
	if _, err := r.tick(func() {}); err != nil {
		t.Fatal(err)
	}
	got, _ := r.store.Get(a.ID)
	if got.State != task.StateBlocked || got.BlockReason != seatcommit.ReasonVerifierInterrupted || steps.Load() != 3 {
		t.Fatalf("after the retried refusal: %s %s, %d steps", got.State, got.BlockReason, steps.Load())
	}
}
