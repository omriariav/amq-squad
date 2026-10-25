//go:build unix

package migrate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// P1-1 (rule 18): a credential-shaped KEY in a v2 file is refused like a
// value, naming the location and never the text; so is a credential-shaped
// project path. Nothing is printed or written.
func TestMigrateRefusesACredentialInAKeyOrAPath(t *testing.T) {
	secret := "gh" + "p_" + strings.Repeat("a1B2c3", 6)
	f := newFixture(t)
	f.profileWith(func(s string) string {
		return strings.Replace(s, `"trust": "sandboxed",`, `"trust": "sandboxed", "`+secret+`": 1,`, 1)
	})
	before := snapshot(t, f.project, f.home)
	_, err := Build(f.opts())
	r := refusal(t, err, CodeCredential)
	if len(r.Fields) != 1 || r.Fields[0] != ".amq-squad/team.json: a key name under top level" || strings.Contains(err.Error(), secret) {
		t.Fatalf("fields %q, error %v", r.Fields, err)
	}
	if ch := changed(before, snapshot(t, f.project, f.home)); len(ch) > 0 {
		t.Fatalf("a refusal changed %v", ch)
	}
	// a key in launch.local.json
	f2 := newFixture(t)
	f2.put(filepath.Join(f2.project, ".amq-squad", "launch.local.json"), `{"env": {"`+secret+`": "x"}}`, 0o600)
	_, err = Build(f2.opts())
	if r := refusal(t, err, CodeCredential); strings.Contains(err.Error(), secret) || len(r.Fields) != 1 {
		t.Fatalf("launch.local.json key: %v", err)
	}
	// the project path
	f3 := newFixtureNamed(t, secret)
	_, err = Build(f3.opts())
	if r := refusal(t, err, CodeCredential); strings.Contains(err.Error(), secret) || len(r.Fields) != 1 || r.Fields[0] != "the profile path" {
		t.Fatalf("project path: %v", err)
	}
	_, err = BuildRemove(f3.opts())
	refusal(t, err, CodeCredential)
	// the name of an input file under the project
	f4 := newFixture(t)
	f4.put(filepath.Join(f4.project, ".amq-squad", "briefs", secret+".md"), "# brief\n", 0o644)
	_, err = Build(f4.opts())
	if r := refusal(t, err, CodeCredential); strings.Contains(err.Error(), secret) || len(r.Fields) != 1 || r.Fields[0] != "the name of an input file" {
		t.Fatalf("input file name: %v", err)
	}
	// the profile path argument, before anything is read
	_, err = Build(Options{Profile: filepath.Join(t.TempDir(), secret, ".amq-squad", "team.json")})
	if r := refusal(t, err, CodeCredential); strings.Contains(err.Error(), secret) || r.Fields[0] != "the profile path" {
		t.Fatalf("profile path: %v", err)
	}
}

// P1-2 (U6): --remove-v2 removes only through the descriptor walk from the
// project root. A symlink at a directory on the way is migrate_refused_symlink
// and nothing is removed: the file outside the project survives, whether the
// symlink is there when the removal is planned or appears after.
func TestMigrateRemoveRefusesASymlinkOnTheWay(t *testing.T) {
	setup := func(t *testing.T) (*fixture, string) {
		f := newFixture(t)
		p := mustBuild(t, f)
		if _, err := p.Apply(); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(filepath.Dir(f.project), "outside")
		f.put(filepath.Join(outside, "extensions", roleLayer, "role.md"), f.testdata("role-legal.md"), 0o644)
		return f, outside
	}
	swap := func(t *testing.T, f *fixture, outside string) {
		counsel := filepath.Join(f.project, ".agent-mail", "feature-x", "agents", "counsel")
		if err := os.RemoveAll(counsel); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, counsel); err != nil {
			t.Fatal(err)
		}
	}
	survives := func(t *testing.T, f *fixture, outside string) {
		if _, err := os.Stat(filepath.Join(outside, "extensions", roleLayer, "role.md")); err != nil {
			t.Fatalf("the file outside the project was removed: %v", err)
		}
		if _, err := os.Stat(f.profile); err != nil {
			t.Fatal("the profile was removed by a refused removal")
		}
	}
	t.Run("symlink present at plan time", func(t *testing.T) {
		f, outside := setup(t)
		swap(t, f, outside)
		_, err := BuildRemove(f.opts())
		r := refusal(t, err, CodeSymlink)
		if len(r.Fields) != 1 || !strings.HasSuffix(r.Fields[0], "agents/counsel") {
			t.Fatalf("fields %q", r.Fields)
		}
		survives(t, f, outside)
	})
	t.Run("symlink appears after the plan", func(t *testing.T) {
		f, outside := setup(t)
		rp, err := BuildRemove(f.opts())
		if err != nil {
			t.Fatal(err)
		}
		swap(t, f, outside)
		if removed, err := rp.Apply(); err == nil || len(removed) != 0 {
			t.Fatalf("Apply through a symlink: %v %v", removed, err)
		}
		survives(t, f, outside)
	})
	t.Run("the descriptor walk refuses on its own", func(t *testing.T) {
		f, outside := setup(t)
		swap(t, f, outside)
		target := filepath.Join(f.project, ".agent-mail", "feature-x", "agents", "counsel", "extensions", roleLayer, "role.md")
		r, err := openRoot(f.project)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		rel, _ := r.rel(target)
		refusal(t, r.remove(rel), CodeSymlink)
		survives(t, f, outside)
	})
}

// P2-1: a named profile's agent role.md files sit one level deeper
// (.agent-mail/<profile>/<session>/agents/<agent>, main's namespace.AMQRoot):
// they are staged and scanned.
func TestMigrateNamedProfileFindsItsAgentRoleFiles(t *testing.T) {
	f := newFixture(t)
	named := filepath.Join(f.project, ".amq-squad", "teams", "alt.json")
	f.put(named, f.testdata("team.json"), 0o644)
	f.profile = named
	deep := filepath.Join(f.project, ".agent-mail", "alt", "feature-x", "agents", "counsel", "extensions", roleLayer, "role.md")
	f.put(deep, "# Alt counsel\n", 0o644)
	p := mustBuild(t, f)
	var staged bool
	for _, w := range p.Writes {
		staged = staged || strings.HasSuffix(w.Path, filepath.Join("roles", "legal.md")) && string(w.Content) == "# Alt counsel\n"
	}
	if !staged {
		t.Fatalf("the named profile's role.md is not staged: %v", p.Writes)
	}
	secret := "gh" + "p_" + strings.Repeat("a1B2c3", 6)
	f.put(deep, "# Alt counsel\nkey "+secret+"\n", 0o644)
	_, err := Build(f.opts())
	refusal(t, err, CodeCredential)
}

// P2-2: the prompt-skipping list holds (flag, value) pairs matched on
// normalized argv tokens, `--flag value` and `--flag=value` alike, besides
// the substrings.
func TestMigrateRefusesPromptSkippingFlagValuePairs(t *testing.T) {
	withArgs := func(f *fixture, args []string) {
		quoted, _ := json.Marshal(args)
		f.profileWith(func(s string) string {
			return strings.Replace(s, `"tool_profile": "full",`, `"tool_profile": "full", "codex_args": `+string(quoted)+`,`, 1)
		})
	}
	cases := [][]string{
		{"--ask-for-approval", "never"}, {"--ask-for-approval=never"}, {"--ask-for-approval", "on-failure"},
		{"-a", "never"}, {"--approval-policy", "never"}, {"--approval-policy=never"},
		{"--sandbox", "danger-full-access"}, {"--sandbox=danger-full-access"},
		{"--permission-mode", "bypassPermissions"}, {"--permission-mode=dontAsk"},
		{"--dangerously-skip-permissions"}, {"--yolo"}, {"--full-auto"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newFixture(t)
			withArgs(f, args)
			_, err := Build(f.opts())
			if r := refusal(t, err, CodeSafetyDefault); !contains(r.Fields, "members[dev].codex_args") {
				t.Fatalf("fields %q", r.Fields)
			}
		})
	}
	for _, args := range [][]string{{"--ask-for-approval", "on-request"}, {"-a", "on-request"}, {"--sandbox", "workspace-write"}, {"--search"}, {"--model", "never"}} {
		f := newFixture(t)
		withArgs(f, args)
		p, err := Build(f.opts())
		if err != nil {
			t.Fatalf("%v is refused: %v", args, err)
		}
		var found bool
		for _, u := range p.Unmapped {
			found = found || u.Key == "members[dev].codex_args"
		}
		if !found {
			t.Fatalf("%v is not reported as unmapped", args)
		}
	}
}
