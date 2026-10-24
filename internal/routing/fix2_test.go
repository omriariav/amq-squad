package routing

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex pass 2 on 2f1a46d2: one test row per item.

// N1: a retry checks the recorded starting tier against the class rules: a
// plan task once dispatched by hand to a smol entry (or to an entry in no
// chain) is refused with pin_unresolved, not retried there as lead_pin.
func TestFix2HistoryIsCheckedNotTrusted(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	for _, pool := range []string{"claude:haiku:low", "claude:sonnet:high"} {
		rn := f.runner()
		p := newTask("t1", "plan")
		// dispatched by hand (task claim --pool): no router decision
		p.Attempt = 1
		p.Attempts = []task.Attempt{{N: 1, Holder: "s", Pool: pl(pool), GrantedAt: t0, EndedAt: t0.Add(time.Minute), Outcome: task.OutcomeUnclaimed}}
		rn.cur = []task.Task{p}
		if err := rn.r.Flush(); err != nil {
			t.Fatal(err)
		}
		if len(rn.rows) != 1 || rn.rows[0].Policy != PolicyLeadPin {
			t.Fatalf("hand dispatch row: %+v", rn.rows)
		}
		if _, route := rn.r.Decide(p, rn.cur); route.Block != BlockPinUnresolved || route.Pool != nil {
			t.Fatalf("plan retried on %s's recorded tier: %+v", pool, route)
		}
	}
	// A valid record still carries over: implement explored down to smol.
	rn := f.runner()
	tk := newTask("t2", "implement")
	tk.Pool = pl("claude:haiku:low")
	rn.attempt(&tk, task.OutcomeUnclaimed)
	if d, route := rn.r.Decide(tk, rn.cur); route.Pool == nil || d.UsedTier != TierSmol {
		t.Fatalf("valid record: %+v %+v", d, route)
	}
}

// N2: the starting tier is the maximum of the recorded one and the current
// escalation: a task that becomes wider than three files after its first
// attempt starts on plan, and a pin below it is dropped.
func TestFix2EscalationIsAFloor(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	for _, pin := range []string{"", "claude:sonnet:medium"} {
		rn := f.runner()
		tk := newTask("t1", "implement", "a.go")
		if pin != "" {
			tk.Pool = pl(pin)
		}
		first := rn.attempt(&tk, task.OutcomeUnclaimed)
		if first.UsedTier != TierTask {
			t.Fatalf("first: %+v", first)
		}
		tk.DeclaredFiles = []string{"a.go", "b.go", "c.go", "d.go"} // widened past three files
		retry, route := rn.r.Decide(tk, rn.cur)
		if route.Pool == nil || retry.UsedTier != TierPlan || retry.Entry.Key() != "claude:opus:high" {
			t.Fatalf("pin %q: retry after escalation %+v %+v", pin, retry, route)
		}
	}
}

// P1-4 residuals: the log is re-verified before every dispatch (a deleted or
// truncated log has its rows written again; a tail that does not parse
// stops routing), and a failed decision write stops every route, not only
// the one whose decision changed.
func TestFix2LogDurabilityIsReverified(t *testing.T) {
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
	a, b := newTask("ta", "plan"), newTask("tb", "plan")
	if route := r.Route(b); route.Pool == nil {
		t.Fatalf("first route: %+v", route)
	}
	rows := func() int { n, _, _ := ReadRows(OutcomePath(root)); return len(n) }
	// Truncated: the row is written again before the next dispatch.
	if err := os.Truncate(OutcomePath(root), 0); err != nil {
		t.Fatal(err)
	}
	if route := r.Route(b); route.Pool == nil || rows() != 1 {
		t.Fatalf("after truncation: %+v, %d rows", route, rows())
	}
	// Deleted: the same.
	if err := os.Remove(OutcomePath(root)); err != nil {
		t.Fatal(err)
	}
	if route := r.Route(b); route.Pool == nil || rows() != 1 {
		t.Fatalf("after deletion: %+v, %d rows", route, rows())
	}
	// A tail that does not parse (not one of the router's rows) stops routing.
	fh, _ := os.OpenFile(OutcomePath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString("{\"schema\":1,\"task\":\"x\",\"att")
	fh.Close()
	if route := r.Route(b); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("torn foreign tail: %+v", route)
	}
	fh, _ = os.OpenFile(OutcomePath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString("garbage\n") // now terminated, but the last line is no row
	fh.Close()
	if route := r.Route(b); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("a last line that is no row: %+v", route)
	}
	// Repair the log for the decision part.
	if err := os.Remove(OutcomePath(root)); err != nil {
		t.Fatal(err)
	}
	if route := r.Route(b); route.Pool == nil {
		t.Fatalf("repaired: %+v", route)
	}
	// A failed decision write stops every route, also b's unchanged one.
	if err := os.Remove(DecisionPath(root)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(DecisionPath(root), 0o700); err != nil {
		t.Fatal(err)
	}
	if route := r.Route(a); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("a's decision unwritable: %+v", route)
	}
	if route := r.Route(b); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("b dispatched past a pending decision: %+v", route)
	}
	if err := os.Remove(DecisionPath(root)); err != nil {
		t.Fatal(err)
	}
	if route := r.Route(b); route.Pool == nil {
		t.Fatalf("after the decision log is writable: %+v", route)
	}
	var decided []string
	readLines(DecisionPath(root), func(line []byte) { decided = append(decided, string(line)) })
	if len(decided) != 1 {
		t.Fatalf("pending decision not written once: %d lines", len(decided))
	}
}

// P1-2 residuals: a declared tree or glob is walked without following links
// (a link beneath the literal prefix that leaves the declared tree makes the
// task local-only), and the root's symlinked ancestors are resolved once.
func TestFix2DeclaredTreesAreWalked(t *testing.T) {
	real := t.TempDir() // on macOS under /var, itself a symlink to /private/var
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Join(real, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("internal/secrets")
	mk("public/sub")
	mk("docs/b")
	if err := os.Symlink("../internal/secrets", filepath.Join(real, "public", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("./b", filepath.Join(real, "docs", "a")); err != nil {
		t.Fatal(err)
	}
	resolved := ResolveRoot(real)
	if err := os.Symlink(filepath.Join(resolved, "docs", "b"), filepath.Join(real, "docs", "abs")); err != nil {
		t.Fatal(err)
	}
	via := filepath.Join(t.TempDir(), "repo-link") // the root itself reached through a link
	if err := os.Symlink(real, via); err != nil {
		t.Fatal(err)
	}
	tbl, err := Apply(Default(), "schema: 1\nprivacy_paths:\n  - glob: internal/secrets/\n    class: local\n", "repo.yaml", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{real, via} {
		for _, c := range []struct{ file, want string }{
			{"public/", grants.PrivacyLocal},      // a tree with a link that leaves it
			{"public/*.go", grants.PrivacyLocal},  // a glob under the same prefix
			{"public/**", grants.PrivacyLocal},    // and every depth
			{"public/sub/", grants.PrivacyVendor}, // a tree without links
			{"docs/", grants.PrivacyVendor},       // links that stay inside the tree, relative and absolute
			{"internal/secrets/k", grants.PrivacyLocal},
		} {
			if got, why := tbl.RequiredPrivacyAt(root, []string{c.file}); got != c.want {
				t.Errorf("root %s: %s: %s (%s), want %s", root, c.file, got, why, c.want)
			}
		}
	}
}

// Limit b: at a later start the seed must be there, 32 raw bytes, mode
// 0600; otherwise the start is routing_invalid and the seed is never
// regenerated.
func TestFix2SeedIsCheckedNotRegenerated(t *testing.T) {
	root := t.TempDir()
	r, err := New(Options{Table: Default(), Registry: fixtureRegistry(), Root: root, Now: func() time.Time { return t0 }})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(SeedPath(root))
	if len(raw) != SeedBytes || r.o.Seed == "" {
		t.Fatalf("seed %d bytes", len(raw))
	}
	if r.Route(newTask("t1", "plan")).Pool == nil { // the session has routed: decisions exist
		t.Fatal("no route")
	}
	restart := func() error { _, err := New(Options{Table: Default(), Root: root}); return err }
	if err := restart(); err != nil {
		t.Fatalf("restart with the seed: %v", err)
	}
	for _, c := range []struct {
		name  string
		setup func()
	}{
		{"missing", func() { os.Remove(SeedPath(root)) }},
		{"mode 0644", func() { os.WriteFile(SeedPath(root), raw, 0o600); os.Chmod(SeedPath(root), 0o644) }},
		{"31 bytes", func() { os.Remove(SeedPath(root)); os.WriteFile(SeedPath(root), raw[:31], 0o600) }},
		{"hex text", func() { os.Remove(SeedPath(root)); os.WriteFile(SeedPath(root), []byte(r.o.Seed), 0o600) }},
	} {
		c.setup()
		if err := restart(); !IsInvalid(err) {
			t.Fatalf("%s: %v", c.name, err)
		}
		if c.name == "missing" {
			if _, err := os.Stat(SeedPath(root)); !os.IsNotExist(err) {
				t.Fatal("a missing seed was regenerated")
			}
		}
	}
}
