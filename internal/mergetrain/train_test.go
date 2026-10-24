package mergetrain

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/grants"
)

const fixtureRepo = "owner/fixture"

// ciOnlyRuleset is the S2 ruleset shape as GET rules/branches returns it.
const ciOnlyRuleset = `[{"type":"deletion","ruleset_id":7},{"type":"non_fast_forward","ruleset_id":7},
{"type":"pull_request","ruleset_id":7,"parameters":{"required_approving_review_count":0}},
{"type":"required_status_checks","ruleset_id":7,"parameters":{"required_status_checks":[{"context":"ci","integration_id":15368}]}}]`

type fixture struct {
	t     *testing.T
	dir   string
	bare  string
	work  string
	state string
	gh    *GH
	store gatestore.Store
	train *Train
	req   Request
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitOut(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines10(prefix string) string {
	var b strings.Builder
	for i := 1; i <= 10; i++ {
		b.WriteString(prefix + " line " + string(rune('0'+i%10)) + "\n")
	}
	return b.String()
}

// branch makes a PR branch from main with the given file contents.
func (f *fixture) branch(name, base string, files map[string]string) {
	f.t.Helper()
	mustGit(f.t, f.work, "checkout", "-q", "-B", name, "origin/"+base)
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		writeFile(f.t, filepath.Join(f.work, k), files[k])
	}
	mustGit(f.t, f.work, "add", "-A")
	mustGit(f.t, f.work, "commit", "-q", "-m", name)
	mustGit(f.t, f.work, "push", "-q", "origin", name)
}

func (f *fixture) head(ref string) string {
	return mustGit(f.t, f.bare, "rev-parse", "refs/heads/"+ref)
}

func (f *fixture) edit(fn func(*fakeState)) {
	s := loadFake(f.state)
	fn(s)
	s.save(f.state)
}

func (f *fixture) calls() string {
	raw, _ := os.ReadFile(filepath.Join(f.dir, "calls.log"))
	return string(raw)
}

func gateIdentity() gatestore.Identity {
	return gatestore.Identity{OwnerPubKey: strings.Repeat("a", 64), IssuerKeyID: "ed25519-sha256:" + strings.Repeat("b", 64)}
}

// newFixture: a bare repository with main and five PR branches; PR 4 shares
// shared.txt with PR 2 (other lines, so its rebase succeeds and changes its
// tree), the rest touch distinct files. Draft PRs, a CI-only ruleset, a
// merge grant with a 400-line cap.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{t: t, dir: dir, bare: filepath.Join(dir, "bare.git"), work: filepath.Join(dir, "work"), state: filepath.Join(dir, "state.json")}
	mustGit(t, dir, "init", "-q", "--bare", "-b", "main", f.bare)
	mustGit(t, dir, "clone", "-q", f.bare, f.work)
	for _, p := range []string{"pkg/a.go", "pkg/b.go", "pkg/c.go", "pkg/d.go"} {
		writeFile(t, filepath.Join(f.work, p), "package pkg\n")
	}
	writeFile(t, filepath.Join(f.work, "shared.txt"), lines10("base"))
	writeFile(t, filepath.Join(f.work, ".amq-squad/team-rules.md"), "rules\n")
	mustGit(t, f.work, "checkout", "-q", "-b", "main")
	mustGit(t, f.work, "add", "-A")
	mustGit(t, f.work, "commit", "-q", "-m", "base")
	mustGit(t, f.work, "push", "-q", "origin", "main")
	mustGit(t, f.work, "push", "-q", "origin", "main:dev")
	shared2 := strings.Replace(lines10("base"), "base line 1\n", "pr2 line 1\n", 1)
	shared4 := strings.Replace(lines10("base"), "base line 0\n", "pr4 line 0\n", 1)
	f.branch("pr1", "main", map[string]string{"pkg/a.go": "package pkg\n\nfunc A() {}\n"})
	f.branch("pr2", "main", map[string]string{"pkg/b.go": "package pkg\n\nfunc B() {}\n", "shared.txt": shared2})
	f.branch("pr3", "main", map[string]string{"pkg/c.go": "package pkg\n\nfunc C() {}\n"})
	f.branch("pr4", "main", map[string]string{"pkg/d.go": "package pkg\n\nfunc D() {}\n", "shared.txt": shared4})
	f.branch("pr5", "main", map[string]string{"pkg/e_test.go": "package pkg\n"})
	st := &fakeState{Bare: f.bare, Repo: fixtureRepo, Rules: json.RawMessage(ciOnlyRuleset), CIName: "ci", CIApp: 15368, PRs: map[string]*fakePR{}}
	for i := 1; i <= 5; i++ {
		st.PRs[string(rune('0'+i))] = &fakePR{Head: "pr" + string(rune('0'+i)), Base: "main", Draft: true, State: "OPEN"}
	}
	st.save(f.state)
	f.gh = &GH{Bin: os.Args[0], Env: append(os.Environ(), "FAKE_GH_STATE="+f.state)}
	store, err := gatestore.Open(filepath.Join(dir, "root"))
	if err != nil {
		t.Fatal(err)
	}
	f.store = store
	g := grants.Defaults()
	g.Merge = []grants.Merge{{Repository: fixtureRepo, BaseBranch: "main", CIRequired: true, SizeCapLines: 400, SizeCapFiles: 5}}
	f.train = &Train{Forge: f.gh, Grants: grants.Resolved{Grants: g, Source: "test"}, Audit: AuditFor(filepath.Join(dir, "root")),
		Escalate: StoreEscalator{Store: store, Identity: gateIdentity()}, Lead: "lead", CIWait: time.Second, Poll: time.Millisecond}
	f.req = Request{Schema: 1, Repository: fixtureRepo, Base: "main", PRs: []Item{
		{Number: 1, Task: "T1", DeclaredFiles: []string{"pkg/a.go"}},
		{Number: 2, Task: "T2", DeclaredFiles: []string{"pkg/b.go", "shared.txt"}},
		{Number: 3, Task: "T3", DeclaredFiles: []string{"pkg/c.go"}},
		{Number: 4, Task: "T4", DeclaredFiles: []string{"pkg/d.go", "shared.txt"}},
		{Number: 5, Task: "T5", DeclaredFiles: []string{"pkg/"}},
	}}
	return f
}
