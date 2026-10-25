//go:build unix

package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// M2: every input read goes through the one root descriptor with O_NOFOLLOW at
// every component, so a symlink on the way to a role.md refuses at the read,
// the layered extensions path included, and so does a symlinked v2 file.
func TestMigrateRefusesASymlinkAtTheReadNotOnlyAtRemoval(t *testing.T) {
	outside := func(f *fixture) string {
		dir := filepath.Join(filepath.Dir(f.project), "outside")
		f.put(filepath.Join(dir, roleLayer, "role.md"), f.testdata("role-legal.md"), 0o644)
		return dir
	}
	t.Run("agents/<handle>/extensions is a symlink", func(t *testing.T) {
		f := newFixture(t)
		ext := filepath.Join(f.project, ".agent-mail", "feature-x", "agents", "counsel", "extensions")
		if err := os.RemoveAll(ext); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside(f), ext); err != nil {
			t.Fatal(err)
		}
		_, err := Build(f.opts())
		r := refusal(t, err, CodeSymlink)
		if len(r.Fields) != 1 || !strings.HasSuffix(r.Fields[0], "counsel/extensions") {
			t.Fatalf("fields %q", r.Fields)
		}
		_, err = BuildRemove(f.opts())
		refusal(t, err, CodeSymlink)
	})
	t.Run("a v2 file is a symlink", func(t *testing.T) {
		f := newFixture(t)
		target := filepath.Join(filepath.Dir(f.project), "elsewhere.json")
		f.put(target, `{"members": []}`, 0o600)
		path := filepath.Join(f.project, ".amq-squad", "launch.local.json")
		os.Remove(path)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		_, err := Build(f.opts())
		refusal(t, err, CodeSymlink)
	})
	t.Run(".amq-squad is a symlink", func(t *testing.T) {
		f := newFixture(t)
		moved := filepath.Join(filepath.Dir(f.project), "moved-amq")
		if err := os.Rename(filepath.Join(f.project, ".amq-squad"), moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, filepath.Join(f.project, ".amq-squad")); err != nil {
			t.Fatal(err)
		}
		_, err := Build(f.opts())
		refusal(t, err, CodeSymlink)
	})
}

// M2: the run keeps the root it opened. Retargeting the project symlink
// between the plan and the apply changes nothing: the apply writes into the
// directory the plan read, and the new target is untouched.
func TestMigrateRetargetingTheRootSymlinkChangesNothing(t *testing.T) {
	f := newFixture(t)
	base := filepath.Dir(f.project)
	link := filepath.Join(base, "link")
	if err := os.Symlink(f.project, link); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(base, "other")
	if err := os.MkdirAll(filepath.Join(other, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Build(Options{Profile: filepath.Join(link, ".amq-squad", "team.json"), OwnerPath: f.owner})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Project != f.project {
		t.Fatalf("the plan's project is %s, want the resolved %s", p.Project, f.project)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, other)
	if _, err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.project, "squad.yaml")); err != nil {
		t.Fatal("the apply did not write into the directory the plan read")
	}
	if c := changed(before, snapshot(t, other)); len(c) > 0 {
		t.Fatalf("the retargeted root was written: %v", c)
	}
}

// M2: when the directory at the root path is no longer the one the descriptor
// holds, the apply refuses root_changed and writes nothing, in either tree.
func TestMigrateApplyRefusesAMovedRoot(t *testing.T) {
	f := newFixture(t)
	p := mustBuild(t, f)
	moved := f.project + ".moved"
	if err := os.Rename(f.project, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.project, 0o755); err != nil {
		t.Fatal(err)
	}
	done, err := p.Apply()
	refusal(t, err, CodeRootChanged)
	if len(done) != 0 {
		t.Fatalf("done %v", done)
	}
	for _, path := range []string{filepath.Join(f.project, "squad.yaml"), filepath.Join(moved, "squad.yaml")} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("%s was written", path)
		}
	}
	// the removal plan refuses the same way
	f2 := newFixture(t)
	pw := mustBuild(t, f2)
	if _, err := pw.Apply(); err != nil {
		t.Fatal(err)
	}
	rp, err := BuildRemove(f2.opts())
	if err != nil {
		t.Fatal(err)
	}
	defer rp.Close()
	moved2 := f2.project + ".moved"
	os.Rename(f2.project, moved2)
	os.MkdirAll(f2.project, 0o755)
	removed, err := rp.Apply()
	refusal(t, err, CodeRootChanged)
	if len(removed) != 0 {
		t.Fatalf("removed %v", removed)
	}
	if _, err := os.Stat(filepath.Join(moved2, ".amq-squad", "team.json")); err != nil {
		t.Fatal("the profile was removed")
	}
}

// M2: a symlink introduced after the plan stops the removal at that file with
// a partial result, not a silent continue: the files before it are removed,
// the refusal is returned, the files after it stay.
func TestMigrateRemovalStopsAtASymlinkIntroducedAfterThePlan(t *testing.T) {
	f := newFixture(t)
	p := mustBuild(t, f)
	if _, err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	rp, err := BuildRemove(f.opts())
	if err != nil {
		t.Fatal(err)
	}
	defer rp.Close()
	outside := filepath.Join(filepath.Dir(f.project), "outside")
	f.put(filepath.Join(outside, "extensions", roleLayer, "role.md"), "# outside\n", 0o644)
	lead := filepath.Join(f.project, ".agent-mail", "feature-x", "agents", "lead")
	if err := os.RemoveAll(lead); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, lead); err != nil {
		t.Fatal(err)
	}
	removed, err := rp.Apply()
	refusal(t, err, CodeSymlink)
	counsel := filepath.Join(f.project, ".agent-mail", "feature-x", "agents", "counsel", "extensions", roleLayer, "role.md")
	if !reflect.DeepEqual(removed, []string{counsel}) {
		t.Fatalf("removed %v, want the one before the symlink", removed)
	}
	if _, err := os.Stat(counsel); err == nil {
		t.Fatal("the reported file is still there")
	}
	if _, err := os.Stat(filepath.Join(outside, "extensions", roleLayer, "role.md")); err != nil {
		t.Fatal("the file outside the project was removed")
	}
	if _, err := os.Stat(f.profile); err != nil {
		t.Fatal("the files after the refusal were removed")
	}
	// and the verb reports it as a partial result: see TestMigrateVerbReportsAPartialRemoval
	if !strings.Contains(PartialReport("--remove-v2", removed, len(rp.Files), err), "stopped at the first refusal after 1 of") {
		t.Fatal("the partial report does not say where it stopped")
	}
}

// The descriptor create never replaces a file: over an existing target it
// fails with os.ErrExist and leaves it as it was.
func TestRootCreateNeverReplacesAFile(t *testing.T) {
	f := newFixture(t)
	r, err := openRoot(f.project)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	f.put(filepath.Join(f.project, "squad.yaml"), "mine\n", 0o644)
	before := snapshot(t, filepath.Join(f.project, "squad.yaml"))
	if err := r.create("squad.yaml", []byte("theirs\n"), 0o644); !errors.Is(err, os.ErrExist) {
		t.Fatalf("create over an existing file: %v", err)
	}
	if c := changed(before, snapshot(t, filepath.Join(f.project, "squad.yaml"))); len(c) > 0 {
		t.Fatalf("create changed %v", c)
	}
	if err := r.create(".amq-squad/roles/new.md", []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(f.project, ".amq-squad", "roles", "new.md")); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("created file: %v %v", err, info)
	}
}
