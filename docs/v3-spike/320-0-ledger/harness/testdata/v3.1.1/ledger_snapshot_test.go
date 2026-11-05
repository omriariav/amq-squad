package seatcommit

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotSeq152Seal(t *testing.T) {
	snapshot := os.Getenv("AMQ_LEDGER_SNAPSHOT")
	if snapshot == "" {
		t.Skip("AMQ_LEDGER_SNAPSHOT is required")
	}
	root := t.TempDir()
	for _, rel := range []string{"routing/seed", "commits/reviewer.json", "routing/heads/commits.reviewer.json"} {
		b, err := os.ReadFile(filepath.Join(snapshot, rel))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(root, "extensions", "amq-squad", rel)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	seed, _, _, err := sealchain.ReadSeed(filepath.Join(root, "extensions", "amq-squad", "routing", "seed"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "extensions", "amq-squad", "commits", "reviewer.json"))
	if err != nil {
		t.Fatal(err)
	}
	before := sha256.Sum256(b)
	var ledger Ledger
	if err := json.Unmarshal(b, &ledger); err != nil {
		t.Fatal(err)
	}
	link := ledger.Seal
	ledger.Seal = nil
	if link == nil || !link.Verify(sealchain.Key(seed), ledgerLabel, ledger) {
		t.Fatal("seq-152 ledger body seal rejected")
	}
	after, err := os.ReadFile(filepath.Join(root, "extensions", "amq-squad", "commits", "reviewer.json"))
	if err != nil || sha256.Sum256(after) != before {
		t.Fatal("seq-152 ledger body changed during verification")
	}
}
