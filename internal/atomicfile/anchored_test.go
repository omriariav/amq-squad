//go:build unix

package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// WriteAt (4-1 rebase): the write goes to the directory the descriptor
// holds, even after the path was swapped for a symlink; a symlink at the
// target name is replaced, never followed; the temp leaves nothing behind;
// a name with a slash is refused.
func TestWriteAtStaysOnTheHeldDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "d")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	attacker := t.TempDir()
	if err := os.Rename(dir, dir+".held"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(attacker, dir); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("owner"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir+".held", "models.json")); err != nil {
		t.Fatal(err)
	}
	if err := WriteAt(fd, dir, "models.json", []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir+".held", "models.json")); string(b) != "{}" {
		t.Fatalf("held dir got %q", b)
	}
	if b, _ := os.ReadFile(victim); string(b) != "owner" {
		t.Fatalf("a symlink at the target was followed: victim %q", b)
	}
	if ents, _ := os.ReadDir(attacker); len(ents) != 0 {
		t.Fatal("the write followed the swapped path")
	}
	ents, _ := os.ReadDir(dir + ".held")
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), TempPrefix) {
			t.Fatalf("temp left behind: %s", e.Name())
		}
	}
	if fi, _ := os.Lstat(filepath.Join(dir+".held", "models.json")); fi.Mode().Perm() != 0o600 || !fi.Mode().IsRegular() {
		t.Fatalf("mode %v", fi.Mode())
	}
	if err := WriteAt(fd, dir, "a/b", []byte("x"), 0o600); err == nil {
		t.Fatal("a name with a slash was accepted")
	}
}
