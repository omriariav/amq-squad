package gatestore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 3.3.x item 1: Store.Raise into a root that does not exist yet, with no
// identity in the spec, creates the root and only then takes its identity,
// so the record carries the identity a verifier reads from the same root; a
// spec it refuses creates no root.
func TestRaiseIntoAFreshRootStampsItsIdentity(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fresh")
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	bad := spec("g-bad")
	bad.Window = time.Second
	if _, _, err := s.Raise(bad, testIdent, testNow, fileSender{t.TempDir()}); err == nil {
		t.Fatal("a one-second window was accepted")
	}
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a refused raise created the root: %v", err)
	}
	rec, _, err := s.Raise(spec("g-fresh"), testIdent, testNow, fileSender{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if want := SquadID(root); rec.Squad != want {
		t.Fatalf("the record's squad identity is %q; the root's is %q", rec.Squad, want)
	}
}
