package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func temps(t *testing.T, dir string) []string {
	t.Helper()
	got, _ := filepath.Glob(filepath.Join(dir, TempPrefix+"*"))
	return got
}

// Review pass 3, item 5: the rollback fsyncs the directory it changed.
func TestRollbackFsyncsTheDirectory(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	calls, synced := 0, 0
	rename = func(from, to string) error {
		if calls++; calls == 2 {
			return errors.New("injected rename failure")
		}
		return os.Rename(from, to)
	}
	syncDir = func(d string) error { synced++; return SyncDir(d) }
	t.Cleanup(func() { rename, syncDir = os.Rename, SyncDir })
	var tx Tx
	for _, p := range []string{a, b} {
		if err := tx.Stage(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(nil); err == nil {
		t.Fatal("commit succeeded")
	}
	// one fsync after the first rename, one after the rollback removed it
	if synced != 2 || read(t, a) != "<absent>" {
		t.Fatalf("fsyncs %d, a %q", synced, read(t, a))
	}
}

// Review pass 3, item 6: no temp survives a failure after staging or a
// failed restore.
func TestFailuresLeaveNoTemps(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	var tx Tx
	if err := tx.Stage(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(dir, []byte("x"), 0o644); err == nil { // reading a directory fails
		t.Fatal("staging over a directory succeeded")
	}
	if left := temps(t, dir); len(left) != 0 {
		t.Fatalf("a failed stage left temps: %v", left)
	}

	// a failed restore: the second rename fails and so does every restore
	if err := os.WriteFile(a, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "b")
	calls := 0
	rename = func(from, to string) error {
		if calls++; calls >= 2 {
			return errors.New("injected rename failure")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { rename = os.Rename })
	tx = Tx{}
	for _, p := range []string{a, b} {
		if err := tx.Stage(p, []byte("new"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := tx.Commit(nil)
	if err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("commit = %v, want the rollback failure reported", err)
	}
	if left := temps(t, dir); len(left) != 0 {
		t.Fatalf("a failed restore left temps: %v", left)
	}
}

// Review pass 3, item 7: every temp carries the shared prefix the gate
// store's sweep matches, in the target directory.
func TestTempNamesUseTheSharedPrefix(t *testing.T) {
	dir := t.TempDir()
	var tx Tx
	if err := tx.Stage(filepath.Join(dir, "squad.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	name := filepath.Base(tx.files[0].tmp)
	if filepath.Dir(tx.files[0].tmp) != dir || !strings.HasPrefix(name, TempPrefix+"squad.yaml-") || TempPrefix != ".tmp-" {
		t.Fatalf("temp %q", tx.files[0].tmp)
	}
}
