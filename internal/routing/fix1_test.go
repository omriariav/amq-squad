package routing

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex pass 1 on d0b0b39e: one test row per item.

// P1-2: privacy globs match the canonical repository path: a symlink alias,
// a case variant on a folding filesystem, or a path through a link that
// leaves the repository never reaches a vendor.
func TestFix1PrivacyMatchesTheCanonicalPath(t *testing.T) {
	repo := t.TempDir()
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "internal", "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(repo, "internal", "secrets"), filepath.Join(repo, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(repo, "out")); err != nil {
		t.Fatal(err)
	}
	tbl, err := Apply(Default(), "schema: 1\nprivacy_paths:\n  - glob: internal/secrets/\n    class: local\n  - glob: vendor-keys/\n    class: local\n", "repo.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	fold := CaseInsensitive(repo)
	cases := []struct {
		file, want string
	}{
		{"internal/secrets/vault.go", grants.PrivacyLocal},
		{"alias/vault.go", grants.PrivacyLocal}, // a symlink alias of the local tree
		{"alias/", grants.PrivacyLocal},         // the alias as a tree
		{"out/x.go", grants.PrivacyLocal},       // through a link out of the repository
		{"cmd/main.go", grants.PrivacyVendor},   // a plain path
		{"internal/secretsx/a.go", grants.PrivacyVendor},
	}
	if fold {
		cases = append(cases,
			struct{ file, want string }{"Internal/Secrets/vault.go", grants.PrivacyLocal}, // case variant of an existing tree
			struct{ file, want string }{"INTERNAL/SECRETS/new.go", grants.PrivacyLocal},   // new file under a case variant
			struct{ file, want string }{"ALIAS/vault.go", grants.PrivacyLocal},            // the alias spelled in another case
			struct{ file, want string }{"Vendor-Keys/a.pem", grants.PrivacyLocal},         // a rule on a tree that does not exist yet
		)
	}
	t.Logf("case-insensitive filesystem under %s: %v", repo, fold)
	for _, c := range cases {
		if got, why := tbl.RequiredPrivacyAt(repo, []string{c.file}); got != c.want {
			t.Errorf("%s: %s (%s), want %s", c.file, got, why, c.want)
		}
	}
	// Through the router: the alias waits, never a vendor.
	f := newFixture(t)
	f.tbl = tbl
	tk := newTask("t1", "implement", "alias/vault.go")
	tk.ProjectPath = repo
	if _, route := f.router().Decide(tk, nil); route.Pool != nil || route.Wait != task.WaitPrivacyClass {
		t.Fatalf("alias route: %+v", route)
	}
	// Sensitive paths use the same canonical match.
	st, _ := Apply(Default(), "schema: 1\nsensitive_paths: [internal/secrets/]\n", "repo.yaml", true)
	if !st.SensitiveAt(repo, []string{"alias/vault.go"}) || st.SensitiveAt(repo, []string{"cmd/main.go"}) {
		t.Fatal("sensitive canonical match")
	}
}

// P1-4: an outcome row or a decision that is not durable stops every
// dispatch (wait_reason outcome_log_unwritable) until it is.
func TestFix1UnwritableLogStopsDispatch(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	done := newTask("t1", "extract")
	done.State, done.Attempt = task.StateCompleted, 1
	done.Attempts = []task.Attempt{{N: 1, Pool: pl("claude:haiku:low"), GrantedAt: t0, EndedAt: t0, Outcome: task.OutcomeCompleted}}
	cur := []task.Task{done}
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: root, Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return cur, nil }})
	if err != nil {
		t.Fatal(err)
	}
	// The outcome log path is a directory: the row cannot be written.
	if err := os.MkdirAll(OutcomePath(root), 0o700); err != nil {
		t.Fatal(err)
	}
	pending := newTask("t2", "plan")
	if route := r.Route(pending); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("unwritable outcome log: %+v", route)
	}
	if err := os.Remove(OutcomePath(root)); err != nil {
		t.Fatal(err)
	}
	if route := r.Route(pending); route.Pool == nil {
		t.Fatalf("after the log is writable: %+v", route)
	}
	if rows, _, _ := ReadRows(OutcomePath(root)); len(rows) != 1 || rows[0].Task != "t1" {
		t.Fatalf("rows %+v", rows)
	}
	// The decision log path is a directory: the dispatch decision is not durable.
	if err := os.Remove(DecisionPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(DecisionPath(root), 0o700); err != nil {
		t.Fatal(err)
	}
	other := newTask("t3", "plan")
	if route := r.Route(other); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("unwritable decision log: %+v", route)
	}
}

// P2-5: an attempt's usage needs both counters; a payload with one is
// unknown usage (incomplete evidence), never a zero for the other.
func TestFix1UsageNeedsBothCounters(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, "extensions", "amq-squad", "ledger", "s1.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	line := func(usage string) string {
		return fmt.Sprintf(`{"schema":1,"seq":1,"at":%q,"handle":"s1","payload":{"usage":%s}}`+"\n", t0.Add(time.Second).Format(time.RFC3339Nano), usage)
	}
	a := task.Attempt{N: 1, Holder: "s1", Pool: pl("claude:sonnet:medium"), GrantedAt: t0, EndedAt: t0.Add(time.Minute)}
	usage := LedgerUsage(root, nil)
	for _, c := range []struct {
		name, body string
		known      bool
	}{
		{"both", line(`{"input_tokens":10,"output_tokens":2}`), true},
		{"input only", line(`{"input_tokens":10}`), false},
		{"output only", line(`{"output_tokens":2}`), false},
		{"both then one", line(`{"input_tokens":10,"output_tokens":2}`) + line(`{"input_tokens":5}`), false},
	} {
		if err := os.WriteFile(p, []byte(c.body), 0o600); err != nil {
			t.Fatal(err)
		}
		u := usage(task.Task{}, a)
		if (u.TokensIn != nil) != c.known || (u.TokensOut != nil) != c.known {
			t.Errorf("%s: tokens %v %v, known want %v", c.name, u.TokensIn, u.TokensOut, c.known)
		}
		if row := (Row{TokensIn: u.TokensIn, TokensOut: u.TokensOut}); row.EvidenceComplete() != c.known {
			t.Errorf("%s: evidence complete %v", c.name, row.EvidenceComplete())
		}
	}
}

// Limit b: the exploration seed is random per session, persisted under the
// AMQ root, read back after a restart, and private.
func TestFix1SessionSeed(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	sa, err := SessionSeed(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, _ := SessionSeed(b)
	again, _ := SessionSeed(a)
	if len(sa) != 64 || sa == sb || sa != again {
		t.Fatalf("seeds %q %q %q", sa, sb, again)
	}
	if st, err := os.Stat(SeedPath(a)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("seed file mode %v (%v)", st.Mode(), err)
	}
	r, err := New(Options{Table: Default(), Root: a, Tasks: noTasks})
	if err != nil || r.o.Seed != sa {
		t.Fatalf("router seed %q, want the session seed (%v)", r.o.Seed, err)
	}
	if m1, _ := SessionSeed(""); m1 == sa || len(m1) != 64 {
		t.Fatalf("in-memory seed %q", m1)
	}
	if err := os.WriteFile(SeedPath(b), []byte("abcd\n"), 0o600); err != nil { // hex, but not 32 bytes
		t.Fatal(err)
	}
	if _, err := New(Options{Table: Default(), Root: b, Tasks: noTasks}); err == nil {
		t.Fatal("a damaged seed file was accepted")
	}
}

// Ruling 2: the start step refuses a table that does not load with
// routing_invalid; the process never falls back to the default router.
func TestFix1OpenRefusesAnInvalidTable(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RepoPath(repo), []byte("schema: 1\nprivacy_paths:\n  - glob: s/\n    class: local\n    override.class: vendor\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Open(StartOptions{Home: home, RepoRoot: repo, Root: t.TempDir(), Tasks: noTasks})
	if r != nil || !IsInvalid(err) || !strings.HasPrefix(err.Error(), ReasonInvalid+": ") {
		t.Fatalf("invalid table: router %v err %v", r, err)
	}
	if err := os.WriteFile(RepoPath(repo), []byte("schema: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := Open(StartOptions{Home: home, RepoRoot: repo, Root: t.TempDir(), Tasks: noTasks}); err != nil || r == nil || r.o.RepoRoot != repo {
		t.Fatalf("valid table: %v %v", r, err)
	}
	if _, err := New(Options{Table: Table{}}); !IsInvalid(err) {
		t.Fatalf("New on a broken table: %v", err)
	}
}
