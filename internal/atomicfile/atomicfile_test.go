package atomicfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<absent>"
	}
	return string(b)
}

// Codex P1-1: a failed rename restores every file the transaction already
// replaced, and removes the ones it created.
func TestCommitRollsBackCompletedRenames(t *testing.T) {
	dir := t.TempDir()
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	if err := os.WriteFile(a, []byte("old a"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c, []byte("old c"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := 0
	rename = func(from, to string) error {
		if calls++; calls == 3 {
			return errors.New("injected rename failure")
		}
		return os.Rename(from, to)
	}
	t.Cleanup(func() { rename = os.Rename })
	var tx Tx
	for _, p := range []string{a, b, c} {
		if err := tx.Stage(p, []byte("new "+filepath.Base(p)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(nil); err == nil {
		t.Fatal("commit succeeded")
	}
	if read(t, a) != "old a" || read(t, b) != "<absent>" || read(t, c) != "old c" {
		t.Fatalf("after rollback a=%q b=%q c=%q", read(t, a), read(t, b), read(t, c))
	}
	if info, _ := os.Stat(a); info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode = %v", info.Mode().Perm())
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*.tmp-*")); len(left) != 0 {
		t.Fatalf("temps left behind: %v", left)
	}
}

// A failing precondition renames nothing.
func TestCommitCheckRunsBeforeAnyRename(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	var tx Tx
	if err := tx.Stage(a, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(func() error { return errors.New("changed since the plan") }); err == nil || read(t, a) != "<absent>" {
		t.Fatalf("commit = %v, a = %q", err, read(t, a))
	}
}

// Codex P1-2: the temp name is random and created exclusively, so a planted
// symlink at a predictable name is never followed.
func TestWriteNeverFollowsAPlantedTempName(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "grants.yaml")
	if err := os.WriteFile(victim, []byte("owner"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "squad.yaml")
	for _, name := range []string{target + ".tmp", target + ".1.tmp", filepath.Join(dir, ".squad.yaml.tmp-")} {
		if err := os.Symlink(victim, name); err != nil {
			t.Fatal(err)
		}
	}
	if err := Write(target, []byte("schema: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if read(t, victim) != "owner" || read(t, target) != "schema: 1\n" {
		t.Fatalf("victim %q, target %q", read(t, victim), read(t, target))
	}
}
