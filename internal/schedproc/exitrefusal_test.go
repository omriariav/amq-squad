package schedproc

import (
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// P3 of the Buzz-signed clear's re-pass: an exit check that refuses with
// anything the exit step does not name (ledger_tampered above all) is a block
// that quarantines the seat, never OK.
func TestExitCheckRefusalIsABlockNotOK(t *testing.T) {
	c := &Committer{ExitStep: func(handle, taskID, why string) seatcommit.Result {
		return seatcommit.Result{ReasonCode: seatcommit.ReasonLedgerTampered, Route: task.RouteOwner, Detail: "commit ledger s1: edited"}
	}}
	r := c.CheckExit("s1", "t1", "gone")
	if r.OK || !r.Quarantine || r.ReasonCode != seatcommit.ReasonLedgerTampered {
		t.Fatalf("a tampered ledger at the exit check: %+v", r)
	}
	ok := &Committer{ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	if r := ok.CheckExit("s1", "t1", "gone"); !r.OK || r.Quarantine {
		t.Fatalf("a clean exit: %+v", r)
	}
}
