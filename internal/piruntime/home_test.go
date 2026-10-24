package piruntime

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// tempHome is a test HOME by its real path. On macOS t.TempDir() is under
// /var, a symlink to /private/var, and the fsanchor anchor is opened from /
// one component at a time with O_NOFOLLOW (4-3 second pass), so a HOME
// reached through a symlink is refused like any other symlinked component.
func tempHome(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// A symlinked HOME (or a symlinked ancestor above it) holding a valid
// runtime is refused: the anchor itself is opened component by component
// from / without following a symlink (the fsanchor fix of the 4-3 second
// pass, applied to item 3).
func TestSymlinkedHomeRefused(t *testing.T) {
	e := fixture(t)
	real := tempHome(t)
	if _, _, err := Ensure(real, e); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tempHome(t), "home")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	_, _, err := Ensure(link, e)
	if err == nil {
		t.Fatal("a runtime behind a symlinked HOME was accepted")
	}
	var rej *Reject
	if !errors.As(err, &rej) {
		t.Logf("refused before the runtime directory: %v", err)
	}
	if _, err := Verify(link, e); err == nil {
		t.Fatal("doctor's verify accepted a symlinked HOME")
	}
}
