//go:build unix

package fsanchor

import (
	"os"
	"path/filepath"
	"testing"
)

// A populated directory renamed into place after the failed unlink and before
// the child is opened is not emptied: the recursive remover checks the
// directory it opened is the entry at the name.
func TestStageRemoveAllRefusesAReplacedDirectory(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mk := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mk(filepath.Join(base, "target", "x"), "x")
	mk(filepath.Join(base, "valuable", "precious"), "keep")
	d, err := OpenAnchored(base, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	removeBeforeOpen = func() {
		os.Rename(filepath.Join(base, "target"), filepath.Join(base, "aside"))
		os.Rename(filepath.Join(base, "valuable"), filepath.Join(base, "target"))
	}
	defer func() { removeBeforeOpen = nil }()
	if err := d.StageRemoveAll("target"); err == nil {
		t.Error("a replaced directory should be refused")
	}
	if _, err := os.Stat(filepath.Join(base, "target", "precious")); err != nil {
		t.Error("the replacement was emptied")
	}
}
