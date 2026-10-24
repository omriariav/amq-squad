package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// AC1 (#643 class): a fresh project whose path the operator spelled with the
// wrong on-disk case must launch on the first try. The v2.28 contract is that
// the project path is canonicalized ONCE (EvalSymlinks + on-disk case) and every
// later component consumes that canonical value instead of re-deriving its own
// spelling. #643 was two components deriving the same path differently and then
// comparing the results.

// v228MiscasedProject creates a real directory with a mixed-case leaf and
// returns (realSpelling, lowercasedSpelling). It skips when the filesystem is
// case sensitive, because the defect class only exists on a case-insensitive one.
func v228MiscasedProject(t *testing.T) (string, string) {
	t.Helper()
	parent := t.TempDir()
	real := filepath.Join(parent, "Project")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	miscased := filepath.Join(parent, "project")
	if _, err := os.Stat(miscased); err != nil {
		t.Skipf("case-insensitive filesystem required for the #643 repro: %v", err)
	}
	return real, miscased
}
