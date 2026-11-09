package schedproc

import "testing"

// A seat without a commit ledger keeps the pre-0e no-op preparation path.
// The read-only compatibility probe must not turn absence into a CleanSeat
// call, which would require a staged launch record and strand fake or newly
// launched seats in seat_cleaning.
func TestPrepareSeatWithoutLedgerRemainsANoop(t *testing.T) {
	c := &CleanSeats{Root: t.TempDir(), Git: "git"}
	if err := c.PrepareSeat("impl-1"); err != nil {
		t.Fatalf("prepare absent ledger: %v", err)
	}
}
