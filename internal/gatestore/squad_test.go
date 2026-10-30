package gatestore

import (
	"os"
	"path/filepath"
	"testing"
)

// 4-5 re-pass (amendment 28 a): the squad identity is the canonical root,
// so a symlinked path to one store gives the same identity, and a directory
// replaced at the same path gives another.
func TestSquadIDIsCanonical(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	a, _ := Open(root)
	b, _ := Open(link)
	if a.Squad() != b.Squad() || SquadID(root) != SquadID(link) {
		t.Fatalf("two paths to one store: %s %s", a.Squad(), b.Squad())
	}
	before := SquadID(root)
	if err := os.Remove(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	// the computed-once value stays the store's; a fresh look sees a new directory
	if SquadID(root) == before && fileIDSupported() {
		t.Fatal("a directory replaced at the same path kept the identity")
	}
}

func fileIDSupported() bool {
	info, err := os.Stat(os.TempDir())
	return err == nil && fileID(info) != ""
}
