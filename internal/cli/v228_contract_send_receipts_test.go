package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// AC7: `send` between agents is delivered, and NO receipt artifacts exist
// anywhere under .amq-squad/. Transport acceptance is the confirmation — exit 0
// means accepted. A local receipt is a second owned representation whose only
// job is to certify the first, which the governing rule forbids.
//
// The behavioral half lives in v228_contract_send_receipts_p5a_test.go, bound to
// the P5a send seam and gated behind the v228p5a build tag. This file keeps the
// production-deletion pin, which references no send symbols and so compiles on
// any base. Both are expected-RED until P5a deletes receipt production, in the
// same way AC10 was red until the vocabulary was deleted.

// v228ReceiptArtifactPaths lists everything under .amq-squad/ that looks like a
// delivery receipt: the receipts directory itself plus any stray receipt file.
func v228ReceiptArtifactPaths(t *testing.T, projectDir string) []string {
	t.Helper()
	squadDir := filepath.Join(projectDir, team.DirName)
	var found []string
	err := filepath.Walk(squadDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		rel, relErr := filepath.Rel(squadDir, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		if strings.Contains(strings.ToLower(rel), "receipt") {
			found = append(found, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", squadDir, err)
	}
	return found
}

// TestV228ContractReceiptProductionIsDeleted pins the deletion outcome, the way
// AC10 pins the vocabulary. Same rationale: a behavioral test alone cannot tell
// "no receipt was written this time" from "receipts are gone".
func TestV228ContractReceiptProductionIsDeleted(t *testing.T) {
	requireV228Contract(t)
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"receipt.go", "delivery_receipt.go", "receipt_error.go"} {
		if _, err := os.Stat(filepath.Join(pkgDir, name)); err == nil {
			t.Errorf("%s still exists; step 5 deletes local send receipts and `receipt show`", name)
		}
	}
	// The receipts directory must not be nameable from anywhere in the package.
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(pkgDir, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if strings.Contains(string(data), `"receipts"`) {
			hits = append(hits, name)
		}
	}
	if len(hits) > 0 {
		t.Errorf("the .amq-squad/receipts path is still constructed in: %v", hits)
	}
}
