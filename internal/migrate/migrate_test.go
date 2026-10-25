//go:build unix

package migrate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func mustBuild(t *testing.T, f *fixture) *Plan {
	t.Helper()
	p, err := Build(f.opts())
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return p
}

func refusal(t *testing.T, err error, code string) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) || r.Code != code {
		t.Fatalf("want a %s refusal, got %v", code, err)
	}
	return r
}

func readGolden(t *testing.T, f *fixture) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "expected-squad.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(b), "{{PROJECT}}", f.project)
}

// B4 row 1: a fixture v2 profile (field names from main's loader) maps to a
// golden squad.yaml, byte for byte; the file is written 0644 with --write,
// and it loads against the owner's grant.
func TestMigrateFixtureProfileMapsToTheGoldenSquadYaml(t *testing.T) {
	f := newFixture(t)
	p := mustBuild(t, f)
	if p.SquadText != readGolden(t, f) {
		t.Fatalf("squad.yaml differs from the golden:\n%s", p.SquadText)
	}
	if _, err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.project, "squad.yaml")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != readGolden(t, f) {
		t.Fatalf("written squad.yaml: %v\n%s", err, got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o644 {
		t.Fatalf("squad.yaml mode %v", info.Mode().Perm())
	}
	if _, err := team.LoadSquadWith(f.owner, f.project); err != nil {
		t.Fatalf("the written squad.yaml does not load: %v", err)
	}
}

// B1, B4 row 2: an existing squad.yaml is refused with squad_yaml_exists and
// is never overwritten, at plan time and when it appears after the plan.
func TestMigrateRefusesAnExistingSquadYaml(t *testing.T) {
	f := newFixture(t)
	path := filepath.Join(f.project, "squad.yaml")
	f.put(path, "schema: 1\nseats:\n  - handle: mine\n    runtime: claude\n", 0o644)
	_, err := Build(f.opts())
	refusal(t, err, CodeSquadExists)
	before := snapshot(t, f.project, f.home)
	if _, err := Build(f.opts()); err == nil {
		t.Fatal("Build over an existing squad.yaml succeeded")
	}
	if c := changed(before, snapshot(t, f.project, f.home)); len(c) > 0 {
		t.Fatalf("a refusal changed %v", c)
	}
	// it appears between the plan and the write
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	p := mustBuild(t, f)
	f.put(path, "schema: 1\nseats:\n  - handle: late\n    runtime: claude\n", 0o644)
	_, err = p.Apply()
	refusal(t, err, CodeSquadExists)
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "late") {
		t.Fatalf("squad.yaml was overwritten: %s", b)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".amq-squad", proposedName)); err == nil {
		t.Fatal("the proposal was left behind by a refused write")
	}
}

// B2, B4 row 3: the unmapped report is exact and complete. The fixture's keys
// partition into the ones migrate maps and the ones it reports, and the
// reported list is the list below.
func TestMigrateUnmappedReportIsExactAndComplete(t *testing.T) {
	f := newFixture(t)
	p := mustBuild(t, f)
	var got []string
	for _, u := range p.Unmapped {
		got = append(got, u.Key)
		if strings.TrimSpace(u.Reason) == "" {
			t.Errorf("%s has no reason", u.Key)
		}
	}
	want := []string{
		".agent-mail/feature-x/agents/lead/extensions/io.github.omriariav.amq-squad/role.md",
		".amq-squad/launch.local.json",
		".amq-squad/team-rules.md",
		"binary_args",
		"composition",
		"created_at",
		"max_spawn_depth",
		"members[dev].session",
		"members[dev].spawn_depth",
		"members[lead].claude_args",
		"operator.enabled",
		"operator.handle",
		"operator.kind",
		"operator.notifications",
		"operator.participant",
		"operator.poll_required",
		"trust",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unmapped:\n got %q\nwant %q", got, want)
	}
	// complete: every set key of the profile is mapped or reported
	mapped := map[string]bool{
		"schema": true, "workstream": true, "operator.interaction_mode": true, "members": true, "orchestrated": true, "lead": true,
		"execution_mode": true, "control_root": true, "lead_mode": true, "shared_cwd_exception": true,
		"members.role": true, "members.binary": true, "members.handle": true, "members.session": true, "members.model": true,
		"members.cwd": true, "members.actor_mode": true, "members.tool_profile": true,
	}
	reported := map[string]bool{}
	for _, k := range got {
		reported[k] = true
	}
	var prof map[string]any
	if err := jsonUnmarshal([]byte(f.testdata("team.json")), &prof); err != nil {
		t.Fatal(err)
	}
	for _, k := range sortedKeys(prof) {
		switch v := prof[k].(type) {
		case map[string]any:
			if reported[k] {
				continue
			}
			for _, sub := range sortedKeys(v) {
				if !mapped[k+"."+sub] && !reported[k+"."+sub] {
					t.Errorf("profile key %s.%s is neither mapped nor reported", k, sub)
				}
			}
		default:
			if !mapped[k] && !reported[k] {
				t.Errorf("profile key %s is neither mapped nor reported", k)
			}
		}
	}
	for _, m := range prof["members"].([]any) {
		mm := m.(map[string]any)
		h := mm["handle"].(string)
		for _, k := range sortedKeys(mm) {
			if !mapped["members."+k] && !reported["members["+h+"]."+k] {
				t.Errorf("member %s key %s is neither mapped nor reported", h, k)
			}
		}
	}
	if !sort.StringsAreSorted(got) {
		t.Error("the report is not sorted")
	}
}

// B3, B4 row 4: grants.yaml and registry.yaml are never written (dev, inode,
// mtime and content unchanged after a --write); grants.yaml.proposed is
// written 0600 beside them, parses as an owner file and carries the
// grant-side fields the profile implies. It is never overwritten.
func TestMigrateNeverWritesTheOwnerFilesAndProposesBesideThem(t *testing.T) {
	f := newFixture(t)
	owner := filepath.Join(f.home, ".amq-squad")
	before := snapshot(t, filepath.Join(owner, "grants.yaml"), filepath.Join(owner, "registry.yaml"))
	p := mustBuild(t, f)
	if _, err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, filepath.Join(owner, "grants.yaml"), filepath.Join(owner, "registry.yaml"))
	if c := changed(before, after); len(c) > 0 || len(after) != 2 {
		t.Fatalf("owner files changed: %v", c)
	}
	proposed := filepath.Join(owner, proposedName)
	info, err := os.Stat(proposed)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("grants.yaml.proposed: %v %v", err, info)
	}
	b, _ := os.ReadFile(proposed)
	g, err := grants.ParseOwner(string(b), proposed)
	if err != nil || !reflect.DeepEqual(g.PermittedRuntimes, []string{"claude", "codex"}) {
		t.Fatalf("the proposal: %v %+v\n%s", err, g.PermittedRuntimes, b)
	}
	// a second run neither overwrites the proposal nor the squad.yaml
	f2 := mustBuildOrRefuse(t, f)
	refusal(t, f2, CodeSquadExists)
	os.Remove(filepath.Join(f.project, "squad.yaml"))
	_, err = Build(f.opts())
	refusal(t, err, CodeProposedExist)
	if after2, _ := os.ReadFile(proposed); string(after2) != string(b) {
		t.Fatal("the proposal was overwritten")
	}
}

func mustBuildOrRefuse(t *testing.T, f *fixture) error {
	t.Helper()
	_, err := Build(f.opts())
	return err
}

// B3, B4 row 5: a v2 field that would change a safety default is refused
// with migrate_refused_safety_default naming the field, never dropped, and
// nothing is written.
func TestMigrateRefusesASafetyDefaultNamingTheField(t *testing.T) {
	cases := []struct{ name, field, from, to string }{
		{"launcher_preference", "launcher_preference", `"trust": "sandboxed",`, `"trust": "sandboxed", "launcher_preference": "iterm2",`},
		{"member launcher", "members[dev].launcher", `"tool_profile": "full",`, `"tool_profile": "full", "launcher": "/opt/wrap/run",`},
		{"member launcher_args", "members[dev].launcher_args", `"tool_profile": "full",`, `"tool_profile": "full", "launcher_args": ["--x"],`},
		{"tool allow list", "members[dev].tool_allowlist", `"tool_profile": "full",`, `"tool_profile": "full", "tool_allowlist": ["mcp:github"],`},
		{"permission allow list", "members[lead].permission_allowlist", `"model": "opus",`, `"model": "opus", "permission_allowlist": ["Bash(git status)"],`},
		{"autonomous allow list", "autonomous.allowed_roles", `"trust": "sandboxed",`, `"trust": "sandboxed", "autonomous": {"allowed_roles": ["qa"]},`},
		{"trusted codex", "trust", `"trust": "sandboxed"`, `"trust": "trusted"`},
		{"approve for me", "trust", `"trust": "sandboxed"`, `"trust": "approve-for-me"`},
		{"self operator", "operator.self_operator", `"participant": true,`, `"participant": true, "self_operator": {"lead_role": "cto", "policy_revision": 1, "sessions": {}},`},
		{"self operator mode", "operator.interaction_mode", `"interaction_mode": "lead_pane"`, `"interaction_mode": "self_operator"`},
		{"safe auto goal supervision", "goal_supervision.mode", `"trust": "sandboxed",`, `"trust": "sandboxed", "goal_supervision": {"mode": "safe-auto"},`},
		{"prompt skipping member arg", "members[lead].claude_args", `"/opt/overlay/settings.json"`, `"--dangerously-skip-permissions"`},
		{"prompt skipping binary arg", "binary_args.codex", `["--search"]`, `["--dangerously-bypass-approvals-and-sandbox"]`},
		{"autonomous cap above the default", "autonomous.max_active_agents", `"trust": "sandboxed",`, `"trust": "sandboxed", "autonomous": {"max_active_agents": 40},`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.profileWith(func(s string) string {
				if !strings.Contains(s, c.from) {
					t.Fatalf("fixture lacks %q", c.from)
				}
				return strings.Replace(s, c.from, c.to, 1)
			})
			before := snapshot(t, f.project, f.home)
			_, err := Build(f.opts())
			r := refusal(t, err, CodeSafetyDefault)
			if !contains(r.Fields, c.field) || !strings.Contains(err.Error(), c.field) {
				t.Fatalf("the refusal does not name %s: %v", c.field, err)
			}
			if ch := changed(before, snapshot(t, f.project, f.home)); len(ch) > 0 {
				t.Fatalf("a refusal changed %v", ch)
			}
		})
	}
}

// B8, B4 row 7: a v2 file (launch.local.json, an env block) carrying a value
// of a real credential shape is refused with migrate_refused_credential
// naming the field and never the value; nothing is written, and an env block
// is never copied into squad.yaml or the proposal. The value is built at run
// time.
func TestMigrateRefusesACredentialAndWritesNothing(t *testing.T) {
	secret := "gh" + "p_" + strings.Repeat("a1B2c3", 6)
	f := newFixture(t)
	f.put(filepath.Join(f.project, ".amq-squad", "launch.local.json"), `{"env": {"GH_TOKEN": "`+secret+`", "EDITOR": "vi"}}`, 0o600)
	before := snapshot(t, f.project, f.home)
	_, err := Build(f.opts())
	r := refusal(t, err, CodeCredential)
	if len(r.Fields) != 1 || r.Fields[0] != ".amq-squad/launch.local.json: env.GH_TOKEN" {
		t.Fatalf("fields %q", r.Fields)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(r.Detail, secret) {
		t.Fatal("the refusal carries the value")
	}
	if ch := changed(before, snapshot(t, f.project, f.home)); len(ch) > 0 {
		t.Fatalf("a refusal changed %v", ch)
	}
	// the same shape in the profile and in a role.md is refused too
	f2 := newFixture(t)
	f2.profileWith(func(s string) string { return strings.Replace(s, `"model": "opus"`, `"model": "`+secret+`"`, 1) })
	_, err = Build(f2.opts())
	if r := refusal(t, err, CodeCredential); len(r.Fields) != 1 || r.Fields[0] != ".amq-squad/team.json: members[0].model" {
		t.Fatalf("profile fields %q", r.Fields)
	}
	f3 := newFixture(t)
	f3.put(filepath.Join(f3.project, ".agent-mail", "feature-x", "agents", "counsel", "extensions", roleLayer, "role.md"), "# Legal\nkey "+secret+"\n", 0o644)
	_, err = Build(f3.opts())
	refusal(t, err, CodeCredential)
	// a benign env block is not copied into any output
	f4 := newFixture(t)
	p := mustBuild(t, f4)
	for _, w := range p.Writes {
		if strings.Contains(string(w.Content), "AM_ME") || strings.Contains(string(w.Content), "EDITOR") {
			t.Fatalf("an env block was copied into %s", w.Path)
		}
	}
}

// B1, B6: the v2 files are read-only inputs: dev, inode, mtime and content
// of every v2 file and of the owner's files are unchanged after a plan and
// after a --write.
func TestMigrateReadsTheV2FilesReadOnly(t *testing.T) {
	f := newFixture(t)
	v2 := []string{filepath.Join(f.project, ".amq-squad"), filepath.Join(f.project, ".agent-mail")}
	inputs := func() map[string]entry {
		m := snapshot(t, v2...)
		for p := range m {
			if strings.HasSuffix(p, "roles/legal.md") {
				delete(m, p) // the staged role prompt is an output
			}
		}
		return m
	}
	before := inputs()
	p := mustBuild(t, f)
	if _, err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	after := inputs()
	for path, e := range before {
		if info, ok := after[path]; !ok || info != e {
			// a directory gains an entry (roles/legal.md); only files are inputs
			if e.Sum == "" && ok {
				continue
			}
			t.Errorf("%s changed", path)
		}
	}
	if len(p.Read) != 7 {
		t.Fatalf("read %d files: %v", len(p.Read), p.Read)
	}
}

// B6: a custom role's role.md becomes a staged 3.0 role prompt (written once,
// never over an existing prompt), a catalog role's role.md is reported, and
// migrate prints the amq-squad trust command and runs nothing: no codex
// config appears under HOME.
func TestMigrateStagesRolePromptsAndLeavesTrustToTheOwner(t *testing.T) {
	f := newFixture(t)
	p := mustBuild(t, f)
	if _, err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	staged, err := os.ReadFile(filepath.Join(f.project, ".amq-squad", "roles", "legal.md"))
	if err != nil || string(staged) != f.testdata("role-legal.md") {
		t.Fatalf("staged role prompt: %v %q", err, staged)
	}
	if !strings.Contains(p.Report(true), "amq-squad trust "+f.project) || p.TrustCmd != "amq-squad trust "+f.project {
		t.Fatalf("no trust command in the report:\n%s", p.Report(true))
	}
	for _, path := range []string{filepath.Join(f.home, ".codex"), filepath.Join(f.home, ".amq-squad", "preflight")} {
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("migrate created %s", path)
		}
	}
	// a role.md that differs from an existing staged prompt is reported, not staged over it
	f2 := newFixture(t)
	f2.put(filepath.Join(f2.project, ".amq-squad", "roles", "legal.md"), "# Mine\n", 0o644)
	p2 := mustBuild(t, f2)
	var found bool
	for _, u := range p2.Unmapped {
		found = found || strings.HasSuffix(u.Key, "counsel/extensions/"+roleLayer+"/role.md") && strings.Contains(u.Reason, "differs from the staged")
	}
	if !found {
		t.Fatalf("the differing role.md is not reported: %v", p2.Unmapped)
	}
	if _, err := p2.Apply(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(f2.project, ".amq-squad", "roles", "legal.md")); string(b) != "# Mine\n" {
		t.Fatal("an existing staged prompt was overwritten")
	}
}

// B7, B4: without --write nothing on disk changes: dev, inode, mtime, mode
// and content of every path under the project and HOME, and nothing is
// created (no squad.yaml, no proposal, no staged role).
func TestMigrateDryRunChangesNothingOnDisk(t *testing.T) {
	f := newFixture(t)
	before := snapshot(t, f.project, f.home)
	p := mustBuild(t, f)
	report := p.Report(false)
	if !strings.Contains(report, "dry run") || !strings.Contains(report, "would write") {
		t.Fatalf("report:\n%s", report)
	}
	if c := changed(before, snapshot(t, f.project, f.home)); len(c) > 0 {
		t.Fatalf("a dry run changed %v", c)
	}
}

// B9: nothing is deleted by a migrate. The v2 files stay after --write; the
// report lists the dead ones; removal is the separate flag's, which refuses
// when squad.yaml is absent, removes only the dead files and keeps the briefs,
// the staged roles and team-rules.md.
func TestMigrateDeletesNothingWithoutTheRemoveFlag(t *testing.T) {
	f := newFixture(t)
	v2 := []string{filepath.Join(f.project, ".amq-squad"), filepath.Join(f.project, ".agent-mail")}
	exists := func() map[string]bool {
		out := map[string]bool{}
		for p, e := range snapshot(t, v2...) {
			if e.Sum != "" {
				out[p] = true
			}
		}
		return out
	}
	before := exists()
	// --remove-v2 with no squad.yaml: refused, nothing removed
	_, err := BuildRemove(f.opts())
	refusal(t, err, CodeRemoveNoSquad)
	if !reflect.DeepEqual(before, exists()) {
		t.Fatal("a refused removal deleted files")
	}
	p := mustBuild(t, f)
	if _, err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	after := exists()
	for path := range before {
		if !after[path] {
			t.Errorf("migrate --write deleted %s", path)
		}
	}
	if len(p.Dead) != 4 {
		t.Fatalf("dead list %v", p.Dead)
	}
	// the flag, with squad.yaml in place, removes exactly the dead files
	rp, err := BuildRemove(f.opts())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rp.Files, p.Dead) {
		t.Fatalf("remove list %v != dead list %v", rp.Files, p.Dead)
	}
	if !reflect.DeepEqual(exists(), after) {
		t.Fatal("building the removal deleted files")
	}
	if _, err := rp.Apply(); err != nil {
		t.Fatal(err)
	}
	final := exists()
	for _, dead := range p.Dead {
		if final[dead] {
			t.Errorf("%s was not removed", dead)
		}
	}
	for _, live := range []string{".amq-squad/team-rules.md", ".amq-squad/briefs/feature-x.md", ".amq-squad/roles/contracts.md", ".amq-squad/roles/legal.md"} {
		if !final[filepath.Join(f.project, live)] {
			t.Errorf("%s was removed", live)
		}
	}
	if _, err := os.Stat(filepath.Join(f.project, "squad.yaml")); err != nil {
		t.Fatal("squad.yaml is gone")
	}
}

// B9: --remove-v2 also refuses a squad.yaml that does not load.
func TestMigrateRemoveRefusesASquadYamlThatDoesNotLoad(t *testing.T) {
	f := newFixture(t)
	f.put(filepath.Join(f.project, "squad.yaml"), "schema: 2\n", 0o644)
	_, err := BuildRemove(f.opts())
	r := refusal(t, err, CodeRemoveNoSquad)
	if !strings.Contains(r.Detail, "squad_yaml_schema_unsupported") {
		t.Fatalf("detail %q", r.Detail)
	}
	if _, err := os.Stat(f.profile); err != nil {
		t.Fatal("the profile was removed")
	}
}

// A named profile (.amq-squad/teams/<name>.json) migrates the same way; its
// briefs are listed because 3.0 reads only .amq-squad/briefs/<session>.md.
func TestMigrateNamedProfile(t *testing.T) {
	f := newFixture(t)
	named := filepath.Join(f.project, ".amq-squad", "teams", "alt.json")
	f.put(named, f.testdata("team.json"), 0o644)
	f.put(filepath.Join(f.project, ".amq-squad", "briefs", "alt", "s1.md"), "# alt\n", 0o644)
	f.profile = named
	p := mustBuild(t, f)
	if p.ProfileName != "alt" || p.Project != f.project {
		t.Fatalf("plan %+v", p)
	}
	var found bool
	for _, u := range p.Unmapped {
		found = found || u.Key == ".amq-squad/briefs/alt/s1.md"
	}
	if !found {
		t.Fatalf("the named profile's brief is not reported: %v", p.Unmapped)
	}
	if _, err := Build(Options{Profile: filepath.Join(f.project, "elsewhere.json"), OwnerPath: f.owner}); err == nil {
		t.Fatal("a path outside .amq-squad was accepted")
	}
}
