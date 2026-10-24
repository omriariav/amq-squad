package grants

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

const fullOwner = `# owner grants
schema: 1
mode: auto
max_seats: 10
cost_per_day_usd: 40.5
permitted_runtimes: [claude, codex, pi]
privacy_class: vendor
default_branch: trunk
protected_paths:
  - conf/*.yaml   # globs
  - "~/vault/"
merge:
  - repository: omriariav/amq-noc
    base_branch: main
    ci_required: true
    size_cap_lines: 400
  - repository: omriariav/workspace-cli
    base_branch: main
    size_cap_lines: 200
fleet:
  max_squads: 4
  cost_per_day_usd: 100
  permitted_repositories:
    - omriariav/amq-noc
`

func TestParseOwnerSchema1(t *testing.T) {
	g, err := ParseOwner(fullOwner, "g")
	if err != nil {
		t.Fatal(err)
	}
	want := Grants{
		Mode: ModeAuto, MaxSeats: 10, CostPerDayUSD: 40.5, PermittedRuntimes: []string{"claude", "codex", "pi"},
		PrivacyClass: PrivacyVendor, DefaultBranch: "trunk", ProtectedPaths: []string{"conf/*.yaml", "~/vault/"},
		Merge: []Merge{
			{Repository: "omriariav/amq-noc", BaseBranch: "main", CIRequired: true, SizeCapLines: 400},
			{Repository: "omriariav/workspace-cli", BaseBranch: "main", CIRequired: true, SizeCapLines: 200},
		},
		Fleet: Fleet{MaxSquads: 4, CostPerDayUSD: 100, PermittedRepositories: []string{"omriariav/amq-noc"}},
	}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("got  %+v\nwant %+v", g, want)
	}
	if d, err := ParseOwner("", "g"); err != nil || !reflect.DeepEqual(d, Defaults()) {
		t.Fatalf("empty file = %+v, %v", d, err)
	}
	if d := Defaults(); d.MaxSeats != 12 || d.Mode != ModeAuto || d.PermittedRuntimes != nil {
		t.Fatalf("defaults = %+v", d)
	}
}

func TestParseOwnerRejects(t *testing.T) {
	for _, bad := range []string{
		"bogus: 1\n",
		"schema: 2\n",
		"max_seats: 0\n",
		"max_seats: twelve\n",
		"max_seats: 3\nmax_seats: 4\n",
		"mode: yolo\n",
		"privacy_class: cloud\n",
		"cost_per_day_usd: -1\n",
		"permitted_runtimes: claude\n",
		"permitted_runtimes: [Claude Code]\n",
		"default_branch: a b\n",
		"protected_paths: x\n",
		"protected_paths:\n  - managed:CLAUDE.md\n",
		"  max_seats: 3\n",
		"- x\n",
		"max_seats\n",
		"merge:\n  - repository: a/b\n    base_branch: main\n",                                              // no size cap
		"merge:\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 9\n    force_push: true\n", // fixed limit, not a field
		"merge:\n  - repository: nope\n    base_branch: main\n    size_cap_lines: 9\n",
		"merge:\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 9\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 9\n",
		"fleet:\n  max_squads: 2\n  extra: 1\n",
		"fleet: 3\n",
		"\tmax_seats: 3\n",
	} {
		if _, err := ParseOwner(bad, "g"); err == nil {
			t.Errorf("ParseOwner(%q) accepted", bad)
		}
	}
}

func TestNarrowNeverWidens(t *testing.T) {
	owner, err := ParseOwner(fullOwner, "g")
	if err != nil {
		t.Fatal(err)
	}
	wide := Narrowing{Mode: ModeAuto, MaxSeats: 50, CostPerDayUSD: 500, PermittedRuntimes: []string{"grok", "pi"}, RuntimesSet: true, PrivacyClass: PrivacyVendor, MergeSizeCapLines: 9000}
	got, ignored := Narrow(owner, wide)
	if got.MaxSeats != 10 || got.CostPerDayUSD != 40.5 || !reflect.DeepEqual(got.PermittedRuntimes, []string{"pi"}) || got.PrivacyClass != PrivacyVendor || got.Merge[0].SizeCapLines != 400 {
		t.Fatalf("widened: %+v", got)
	}
	if strings.Join(ignored, ";") != "max_seats 50 above 10;cost_per_day_usd 500 above 40.5;runtime grok" {
		t.Fatalf("ignored = %q", ignored)
	}
	narrow := Narrowing{Mode: ModeManual, MaxSeats: 2, CostPerDayUSD: 5, PrivacyClass: PrivacyLocal, ProtectedPaths: []string{"docs/"}, MergeRepositories: []string{"OMRIARIAV/amq-noc"}, MergeSizeCapLines: 50}
	got, ignored = Narrow(owner, narrow)
	if got.Mode != ModeManual || got.MaxSeats != 2 || got.CostPerDayUSD != 5 || got.PrivacyClass != PrivacyLocal || len(ignored) != 0 ||
		!reflect.DeepEqual(got.ProtectedPaths, []string{"conf/*.yaml", "~/vault/", "docs/"}) ||
		len(got.Merge) != 1 || got.Merge[0].SizeCapLines != 50 {
		t.Fatalf("narrowed = %+v %q", got, ignored)
	}
	if owner.Merge[0].SizeCapLines != 400 || len(owner.ProtectedPaths) != 2 {
		t.Fatalf("Narrow mutated its input: %+v", owner)
	}
	// manual cannot be widened back to auto
	m := owner
	m.Mode = ModeManual
	if got, ignored := Narrow(m, Narrowing{Mode: ModeAuto}); got.Mode != ModeManual || len(ignored) != 1 {
		t.Fatalf("auto widened manual: %+v %q", got, ignored)
	}
	// local cannot be widened back to vendor; "any runtime" narrows to a list
	m.PrivacyClass = PrivacyLocal
	if got, _ := Narrow(Defaults(), Narrowing{PermittedRuntimes: []string{"claude"}, RuntimesSet: true}); !reflect.DeepEqual(got.PermittedRuntimes, []string{"claude"}) {
		t.Fatalf("runtimes = %v", got.PermittedRuntimes)
	}
	if got, _ := Narrow(m, Narrowing{PrivacyClass: PrivacyVendor}); got.PrivacyClass != PrivacyLocal {
		t.Fatalf("privacy widened: %+v", got)
	}
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadSources(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "home", ".amq-squad", "grants.yaml")
	repo := filepath.Join(dir, "repo")
	r, err := LoadFrom(owner, repo)
	if err != nil || r.Source != "default" || r.MaxSeats != 12 || r.Insecure {
		t.Fatalf("no files = %+v, %v", r, err)
	}
	writeFile(t, owner, "max_seats: 20\nprotected_paths:\n  - a/\n", 0o600)
	writeFile(t, filepath.Join(repo, "squad.yaml"), "name: x\nseats:\n  - cto\n  - qa\nmax_seats: 30\nprotected_paths: [docs/]\nlead:\n  runtime: claude\n", 0o644)
	r, err = LoadFrom(owner, repo)
	if err != nil || r.MaxSeats != 20 || r.Source != owner+"+"+filepath.Join(repo, "squad.yaml") ||
		!reflect.DeepEqual(r.ProtectedPaths, []string{"a/", "docs/"}) || len(r.Ignored) != 1 {
		t.Fatalf("owner+squad = %+v, %v", r, err)
	}
	// a squad.yaml grant key with a bad value is an error, not dropped
	writeFile(t, filepath.Join(repo, "squad.yaml"), "max_seats: lots\n", 0o644)
	if _, err := LoadFrom(owner, repo); err == nil {
		t.Fatal("bad squad.yaml max_seats accepted")
	}
	// an unknown owner key is an error
	writeFile(t, owner, "max_seat: 3\n", 0o600)
	if _, err := LoadFrom(owner, ""); err == nil {
		t.Fatal("unknown owner key accepted")
	}
}

func TestInsecureOwnerFailsClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permissions")
	}
	dir := t.TempDir()
	owner := filepath.Join(dir, ".amq-squad", "grants.yaml")
	writeFile(t, owner, "max_seats: 20\nprotected_paths:\n  - secret/\nmerge:\n  - repository: a/b\n    base_branch: main\n    size_cap_lines: 9\n", 0o666)
	r, err := LoadFrom(owner, "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Insecure || r.ReasonCode != ReasonInsecure || r.MaxSeats != 0 || r.Mode != ModeManual || len(r.Merge) != 0 ||
		!reflect.DeepEqual(r.ProtectedPaths, []string{"secret/"}) || r.SeatRequest(1) || !strings.Contains(r.InsecureWhy, "writable") {
		t.Fatalf("insecure = %+v", r)
	}
	// a writable directory makes a 0600 file untrusted too
	if err := os.Chmod(owner, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(owner), 0o777); err != nil {
		t.Fatal(err)
	}
	if r, _ := LoadFrom(owner, ""); !r.Insecure {
		t.Fatalf("writable dir trusted: %+v", r)
	}
	if err := os.Chmod(filepath.Dir(owner), 0o700); err != nil {
		t.Fatal(err)
	}
	if r, _ := LoadFrom(owner, ""); r.Insecure || r.MaxSeats != 20 || !r.SeatRequest(20) || r.SeatRequest(21) {
		t.Fatalf("secure = %+v", r)
	}
	// a symlinked owner file is untrusted
	link := filepath.Join(dir, ".amq-squad", "link.yaml")
	if err := os.Symlink(owner, link); err != nil {
		t.Fatal(err)
	}
	if r, _ := LoadFrom(link, ""); !r.Insecure {
		t.Fatalf("symlink trusted: %+v", r)
	}
	// a squad.yaml cannot lift the insecure zero cap
	repo := filepath.Join(dir, "repo")
	writeFile(t, owner, "max_seats: 20\n", 0o666)
	writeFile(t, filepath.Join(repo, "squad.yaml"), "max_seats: 5\n", 0o644)
	if r, _ := LoadFrom(owner, repo); r.MaxSeats != 0 || !r.Insecure {
		t.Fatalf("squad.yaml lifted the insecure cap: %+v", r)
	}
}

func TestImplicitProtectedSet(t *testing.T) {
	want := []string{".amq-squad/team-rules.md", "managed:CLAUDE.md", "managed:AGENTS.md", ".claude/settings*.json", "squad.yaml"}
	for _, w := range want {
		found := false
		for _, e := range Defaults().ProtectedEntries() {
			found = found || e == w
		}
		if !found {
			t.Errorf("implicit set lacks %s", w)
		}
	}
	g := Grants{PermittedRuntimes: []string{"pi"}, PrivacyClass: PrivacyLocal}
	if g.RuntimePermitted("claude") || !g.RuntimePermitted("pi") || g.PrivacyPermitted(PrivacyVendor) || !g.PrivacyPermitted(PrivacyLocal) {
		t.Fatalf("permits wrong: %+v", g)
	}
	if !Defaults().RuntimePermitted("anything") || !Defaults().PrivacyPermitted(PrivacyVendor) {
		t.Fatal("defaults admit any runtime and vendor privacy")
	}
}
