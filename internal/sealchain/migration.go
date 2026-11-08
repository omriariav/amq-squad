package sealchain

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
)

// LayoutMigrationDecisionKind is the scheduler decision written before a
// legacy head is completed as a raw-layout head.
const LayoutMigrationDecisionKind = "ledger_layout_migrated"

type layoutMigrationDecision struct {
	At         time.Time `json:"at"`
	Kind       string    `json:"kind"`
	LedgerKind string    `json:"ledger_kind"`
	Seat       string    `json:"seat,omitempty"`
	Seq        int       `json:"seq"`
	Reason     string    `json:"reason"`
}

// RecordLayoutMigration appends one durable migration decision unless the
// decision log already carries the same ledger kind, seat, and sequence. The
// caller holds that ledger's writer lock, so a read-check-append is serialized
// for one dedupe key. The append is synced before the caller completes the
// head, making a retry after a crash find the record instead of duplicating it.
func RecordLayoutMigration(path, ledgerKind, seat string, seq int, reason string, now time.Time) (bool, error) {
	if path == "" || ledgerKind == "" || seq <= 0 {
		return false, errors.New("layout migration decision path, ledger kind, and sequence are required")
	}
	if f, err := os.Open(path); err == nil {
		s := bufio.NewScanner(f)
		for s.Scan() {
			var d layoutMigrationDecision
			if json.Unmarshal(s.Bytes(), &d) == nil && d.Kind == LayoutMigrationDecisionKind && d.LedgerKind == ledgerKind && d.Seat == seat && d.Seq == seq {
				_ = f.Close()
				return false, nil
			}
		}
		err = errors.Join(s.Err(), f.Close())
		if err != nil {
			return false, err
		}
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, err
	}
	d := layoutMigrationDecision{At: now.UTC(), Kind: LayoutMigrationDecisionKind, LedgerKind: ledgerKind, Seat: seat, Seq: seq, Reason: reason}
	raw, err := json.Marshal(d)
	if err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	_, werr := f.Write(append(raw, '\n'))
	err = errors.Join(werr, f.Sync(), f.Close())
	if err != nil {
		return false, err
	}
	if err := atomicfile.SyncDir(filepath.Dir(path)); err != nil {
		return false, err
	}
	return true, nil
}
