package sealchain

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
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

var (
	syncMigrationFile = func(f *os.File) error { return f.Sync() }
	syncMigrationDir  = atomicfile.SyncDir
	// afterMigrationDecisionScan is a test seam held under the decision-log
	// lock, after dedupe and before append. Production leaves it nil.
	afterMigrationDecisionScan func()
)

// RecordLayoutMigration appends one durable migration decision unless the
// decision log already carries the same ledger kind, seat, and sequence. The
// decision log's named file lock serializes the dedupe scan, tail check and
// append against every writer. The caller also holds that ledger's writer
// lock. The file and directory are synced before the caller completes the head,
// including on a dedupe hit after an earlier failed sync.
func RecordLayoutMigration(path, ledgerKind, seat string, seq int, reason string, now time.Time) (bool, error) {
	if path == "" || ledgerKind == "" || seq <= 0 {
		return false, errors.New("layout migration decision path, ledger kind, and sequence are required")
	}
	d := layoutMigrationDecision{At: now.UTC(), Kind: LayoutMigrationDecisionKind, LedgerKind: ledgerKind, Seat: seat, Seq: seq, Reason: reason}
	raw, err := json.Marshal(d)
	if err != nil {
		return false, err
	}
	appended := false
	err = flock.WithJSONLFile(path, func(f *os.File) error {
		if _, err := f.Seek(0, 0); err != nil {
			return err
		}
		r := bufio.NewReader(f)
		for {
			line, readErr := r.ReadBytes('\n')
			var prior layoutMigrationDecision
			if json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &prior) == nil && prior.Kind == LayoutMigrationDecisionKind && prior.LedgerKind == ledgerKind && prior.Seat == seat && prior.Seq == seq {
				if !bytes.HasSuffix(line, []byte{'\n'}) {
					if err := flock.TerminateJSONLTail(f); err != nil {
						return err
					}
				}
				return errors.Join(syncMigrationFile(f), syncMigrationDir(filepath.Dir(path)))
			}
			switch {
			case readErr == nil:
			case errors.Is(readErr, io.EOF):
				goto scanned
			default:
				return readErr
			}
		}
	scanned:
		if afterMigrationDecisionScan != nil {
			afterMigrationDecisionScan()
		}
		if err := flock.AppendJSONLLine(f, raw); err != nil {
			return err
		}
		appended = true
		return errors.Join(syncMigrationFile(f), syncMigrationDir(filepath.Dir(path)))
	})
	return appended, err
}
