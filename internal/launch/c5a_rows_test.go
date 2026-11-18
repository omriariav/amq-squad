package launch

import (
	"os"
	"path/filepath"
	"testing"
)

// C5a PR 1: ReadStaged and ReadExec return only the named seat's own record,
// at the named seat's own path.
func TestC5aReadStagedIsTheNamedSeatsOwn(t *testing.T) {
	root := t.TempDir()
	for _, h := range []string{"cto", "qa"} {
		if err := os.MkdirAll(filepath.Join(StagedDir(root, h), "exec"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// qa's staged and exec records name cto
	for _, p := range []string{filepath.Join(StagedDir(root, "qa"), FileName), filepath.Join(StagedDir(root, "qa"), "exec", FileName)} {
		if err := WriteFile(p, Record{Handle: "cto", Role: "lead"}); err != nil {
			t.Fatal(err)
		}
	}
	if rec, err := ReadStaged(root, "qa"); err == nil {
		t.Errorf("qa's staged record naming cto was read as qa's: %+v", rec)
	}
	if rec, err := ReadExec(root, "qa"); err == nil {
		t.Errorf("qa's exec record naming cto was read as qa's: %+v", rec)
	}
	if err := WriteFile(filepath.Join(StagedDir(root, "cto"), FileName), Record{Handle: "cto", Role: "lead"}); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"", ".", "..", "../launch/cto", "x/../cto", `x\cto`} {
		if rec, err := ReadStaged(filepath.Join(root, "extensions", "amq-squad", "launch", "qa"), h); err == nil {
			t.Errorf("handle %q read %+v", h, rec)
		}
		if rec, err := ReadStaged(root, h); err == nil {
			t.Errorf("handle %q read %+v", h, rec)
		}
	}
	// a record a seat wrote in its own agent directory naming a handle that
	// walks there from the staged directory
	walk := "../../../agents/cto"
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(root, "agents", "cto", FileName), filepath.Join(root, "agents", "cto", "exec", FileName)} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := WriteFile(p, Record{Handle: walk, Role: "lead"}); err != nil {
			t.Fatal(err)
		}
	}
	if rec, err := ReadStaged(root, walk); err == nil {
		t.Errorf("ReadStaged(%q) read the seat's own record: %+v", walk, rec)
	}
	if rec, err := ReadExec(root, walk); err == nil {
		t.Errorf("ReadExec(%q) read the seat's own record: %+v", walk, rec)
	}
	if rec, err := ReadStaged(root, "cto"); err != nil || rec.Handle != "cto" {
		t.Errorf("cto's own staged record: %+v %v", rec, err)
	}
}

// C5a PR 1 (Codex pass 5, test strength): a handle with a backslash is not
// a seat handle, even where a record naming it sits at its path.
func TestC5aABackslashHandleIsNoSeatHandle(t *testing.T) {
	root := t.TempDir()
	h := `x\cto`
	dir := StagedDir(root, h)
	if err := os.MkdirAll(filepath.Join(dir, "exec"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{filepath.Join(dir, FileName), filepath.Join(dir, "exec", FileName)} {
		if err := WriteFile(p, Record{Handle: h, Role: "lead"}); err != nil {
			t.Fatal(err)
		}
	}
	if rec, err := ReadStaged(root, h); err == nil {
		t.Errorf("ReadStaged(%q) read %+v", h, rec)
	}
	if rec, err := ReadExec(root, h); err == nil {
		t.Errorf("ReadExec(%q) read %+v", h, rec)
	}
}

// C5a PR 1 (Codex pass 5, test strength): the scan through an alias of the
// base root still finds a session whose staged records name the canonical
// root (the root is compared resolved as well as cleaned).
func TestC5aTheScanThroughABaseRootAliasKeepsTheSession(t *testing.T) {
	tmp, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(tmp, "proj", ".agent-mail")
	root := filepath.Join(base, "s1")
	if err := os.MkdirAll(StagedDir(root, "cto"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(filepath.Join(StagedDir(root, "cto"), FileName), Record{Handle: "cto", Root: root, Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(tmp, "alias-mail")
	if err := os.Symlink(base, alias); err != nil {
		t.Fatal(err)
	}
	entries, err := ScanEntriesInRoot("", alias)
	if err != nil || len(entries) != 1 || entries[0].Record.Handle != "cto" {
		t.Errorf("scan through %s: %+v %v, want cto's record", alias, entries, err)
	}
}
