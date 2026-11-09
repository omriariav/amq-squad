package sealchain_test

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

func TestLayoutMigrationDedupeTerminatesWholeTailForSchedulerReader(t *testing.T) {
	root := t.TempDir()
	path := schedproc.DecisionsPath(root)
	appended, err := sealchain.RecordLayoutMigration(path, "commits", "qa", 9, "raw layout", time.Unix(7, 0))
	if err != nil || !appended {
		t.Fatalf("initial append = %t, %v", appended, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.TrimSuffix(raw, []byte{'\n'}), 0o600); err != nil {
		t.Fatal(err)
	}

	appended, err = sealchain.RecordLayoutMigration(path, "commits", "qa", 9, "raw layout", time.Unix(8, 0))
	if err != nil || appended {
		t.Fatalf("dedupe repair = %t, %v", appended, err)
	}
	raw, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasSuffix(raw, []byte{'\n'}) || bytes.Count(raw, []byte(`"kind":"ledger_layout_migrated"`)) != 1 {
		t.Fatalf("repaired migration log = %q", raw)
	}
	lines, _, err := schedproc.ReadDecisionsFrom(root, 0)
	if err != nil || len(lines) != 1 || lines[0].Kind != sealchain.LayoutMigrationDecisionKind {
		t.Fatalf("scheduler decisions = %+v, %v", lines, err)
	}
}
