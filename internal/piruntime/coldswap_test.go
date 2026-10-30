package piruntime

import (
	"os"
	"path/filepath"
	"testing"
)

// Review 2, item 3: a symlink swapped in at ~/.amq-squad/runtime after the
// cold install opened the base does not redirect a single staging write
// (lock, staging directory, files, rename): they all go to the held
// descriptor, so the attacker's directory stays empty, and the final
// verification refuses the symlinked base.
func TestColdInstallIgnoresSwappedBase(t *testing.T) {
	e := fixture(t)
	home := tempHome(t)
	attacker := tempHome(t)
	base := DefaultBase(home)
	t.Cleanup(func() { coldInstallHook = nil })
	coldInstallHook = func() {
		if err := os.Rename(base, base+".moved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(attacker, base); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := Ensure(home, e)
	if err == nil {
		t.Fatal("a cold install through a swapped symlinked base was accepted")
	}
	ents, _ := os.ReadDir(attacker)
	if len(ents) > 0 {
		var names []string
		for _, d := range ents {
			names = append(names, d.Name())
		}
		t.Fatalf("staging writes followed the swapped symlink into %s: %v", attacker, names)
	}
	// The writes went to the directory that was opened (now base.moved).
	if _, err := os.Stat(filepath.Join(base+".moved", e.Manifest.DirName(), ManifestName)); err != nil {
		t.Fatalf("the install did not land in the held directory: %v", err)
	}
}
