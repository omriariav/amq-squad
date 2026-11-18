package seatexec

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// C5a PR 1: the role and rules snapshots the provider reads live under the
// staged directory (exec/), and recheck hashes those snapshots: a change to
// either one during the identity proof refuses the exec.
func TestC5aSeatExecRechecksTheExecSnapshots(t *testing.T) {
	for name, want := range map[string]string{launch.RoleFileName: "role_mismatch", rulesSnapshotName: "rules_mismatch"} {
		f := newFixture(t, "claude")
		f.stage()
		err := f.run(Options{BeforeRecheck: func() {
			p := filepath.Join(launch.StagedDir(f.root, "cto"), "exec", name)
			if err := os.Chmod(p, 0o600); err != nil {
				t.Fatal(err)
			}
			write(t, p, "changed during the proof\n", 0o600)
		}})
		if code := refusalCode(err); code != want || f.execPath != "" {
			t.Errorf("exec/%s changed during the proof: %v (code %q, exec %q), want %s", name, err, code, f.execPath, want)
		}
	}
}

// C5a PR 1 (ruling 2): seat-exec still writes the published record and the
// role and rules snapshots in the seat's own agent directory, for a 3.3.0
// binary after a rollback; they carry the exec record's bytes.
func TestC5aSeatExecKeepsTheRollbackCopies(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	execDir := filepath.Join(launch.StagedDir(f.root, "cto"), "exec")
	pubDir := launch.ExtensionDir(filepath.Join(f.root, "agents", "cto"))
	for _, name := range []string{launch.FileName, launch.RoleFileName, rulesSnapshotName} {
		want, err := os.ReadFile(filepath.Join(execDir, name))
		if err != nil {
			t.Fatalf("exec/%s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(pubDir, name))
		if err != nil || string(got) != string(want) {
			t.Errorf("the rollback copy of %s: %v, %d bytes, want the exec copy's %d", name, err, len(got), len(want))
		}
	}
}

// C5a PR 1 (Codex pass 1, test strength): an exec/ that is a symlink (to a
// directory the seat writes) is refused: seat-exec never follows it, so no
// exec record or snapshot lands where the seat can edit what a reader trusts.
func TestC5aSeatExecRefusesASymlinkedExecDirectory(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	target := filepath.Join(f.root, "agents", "cto", "planted")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(launch.StagedDir(f.root, "cto"), "exec")); err != nil {
		t.Fatal(err)
	}
	err := f.run(Options{})
	if refusalCode(err) == "" || f.execPath != "" {
		t.Fatalf("a symlinked exec/ reached exec: %v", err)
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Errorf("seat-exec wrote through the symlinked exec/: %v", ents)
	}
}

// C5a PR 1 (Codex pass 2, test strength): a relaunch of the same handle
// replaces the earlier launch's exec record and 0400 snapshots under exec/
// with its own.
func TestC5aARelaunchReplacesTheExecRecordAndSnapshots(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	execDir := filepath.Join(launch.StagedDir(f.root, "cto"), "exec")
	f.rec.LaunchID = "launch-2"
	f.role = []byte("# Role\n\nYou are the cto seat, relaunched.\n")
	f.rec.RoleSHA256 = sha(f.role)
	rules := "# rules, changed for the relaunch\n"
	write(t, f.rec.RulesPath, rules, 0o600)
	f.rec.RulesSHA256 = sha([]byte(rules))
	f.execPath = ""
	f.stage()
	if err := f.run(Options{}); err != nil || f.execPath == "" {
		t.Fatalf("the relaunch over an earlier exec/: %v (exec %q)", err, f.execPath)
	}
	rec, err := launch.ReadExec(f.root, "cto")
	if err != nil || rec.LaunchID != "launch-2" {
		t.Errorf("exec record after the relaunch: %+v %v", rec, err)
	}
	role := filepath.Join(execDir, launch.RoleFileName)
	b, err := os.ReadFile(role)
	if err != nil || string(b) != string(f.role) {
		t.Errorf("exec/role.md after the relaunch: %q %v", b, err)
	}
	if st, err := os.Stat(role); err != nil || st.Mode().Perm() != 0o400 {
		t.Errorf("exec/role.md mode: %v %v", st, err)
	}
	if b, err := os.ReadFile(filepath.Join(execDir, rulesSnapshotName)); err != nil || string(b) != rules {
		t.Errorf("exec/rules.md after the relaunch: %q %v", b, err)
	}
}
