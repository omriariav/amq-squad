package cli

import (
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

func TestRunThreadsStampedVersionIntoLedgerWriters(t *testing.T) {
	oldSeat, oldSeal := seatcommit.BinaryVersion, sealchain.BinaryVersion
	t.Cleanup(func() {
		seatcommit.BinaryVersion = oldSeat
		sealchain.BinaryVersion = oldSeal
	})
	const stamped = "v3.2.0-rc.2"
	_ = Run([]string{"not-a-command"}, stamped)
	if seatcommit.BinaryVersion != stamped || sealchain.WriterVersion() != stamped {
		t.Fatalf("seatcommit=%q sealchain=%q; want %q", seatcommit.BinaryVersion, sealchain.WriterVersion(), stamped)
	}
}
