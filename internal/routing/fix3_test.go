package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex pass 3 on 9fb1e131: the router no longer trusts its own recorded
// history as fact. One test row per ruling.

// fix3Router is a router on a real session root whose task list the test
// sets; clock is the router's clock.
type fix3Router struct {
	t     *testing.T
	root  string
	r     *Router
	cur   []task.Task
	clock time.Time
}

func newFix3(t *testing.T, seed string) *fix3Router {
	h := &fix3Router{t: t, root: t.TempDir(), clock: t0}
	h.r = h.open(seed)
	return h
}

func (h *fix3Router) open(seed string) *Router {
	f := newFixture(h.t).apply("schema: 1\nexploration_share: 0.05\n")
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: h.root, Seed: seed, Now: func() time.Time { return h.clock },
		Tasks: func() ([]task.Task, error) { return h.cur, nil }})
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *fix3Router) key() []byte { return h.r.key }

func ended(id, class, pool, outcome string) task.Task {
	tk := newTask(id, class)
	tk.State, tk.Attempt = task.StateCompleted, 1
	if outcome != task.OutcomeCompleted {
		tk.State = task.StatePending
	}
	tk.Attempts = []task.Attempt{{N: 1, Holder: "s1", Pool: pl(pool), GrantedAt: t0, EndedAt: t0, Outcome: outcome}}
	return tk
}

func logLines(t *testing.T, root string) [][]byte {
	raw, err := os.ReadFile(OutcomePath(root))
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
}

// Ruling 1: every row carries an HMAC under the session seed; a row whose
// tag fails, after a hand edit of the same size and even with its mtime put
// back, is treated as absent and reported (history_tampered), and the
// weekly report leaves it out.
func TestFix3TamperedRowsAreAbsentAndReported(t *testing.T) {
	for _, keepMtime := range []bool{false, true} {
		h := newFix3(t, "")
		h.cur = []task.Task{ended("t1", "extract", "claude:haiku:low", task.OutcomeCompleted)}
		if route := h.r.Route(newTask("t9", "plan")); route.Pool == nil {
			t.Fatalf("first route: %+v", route)
		}
		var row Row
		if json.Unmarshal(logLines(t, h.root)[0], &row); !row.Verify(h.key()) {
			t.Fatalf("written row does not verify: %+v", row)
		}
		path := OutcomePath(h.root)
		st, _ := os.Stat(path)
		raw, _ := os.ReadFile(path)
		edited := bytes.Replace(raw, []byte(`"holder":"s1"`), []byte(`"holder":"s2"`), 1) // same size
		if len(edited) != len(raw) || bytes.Equal(edited, raw) {
			t.Fatal("bad edit")
		}
		os.WriteFile(path, edited, 0o600)
		if keepMtime {
			os.Chtimes(path, st.ModTime(), st.ModTime())
			h.clock = h.clock.Add(2 * FullCheckInterval) // only the periodic full read sees it
		}
		if route := h.r.Route(newTask("t9", "plan")); route.Pool == nil {
			t.Fatalf("route after the edit: %+v", route)
		}
		if got := h.r.Tampered(); !slices.Contains(got, "row t1/1") {
			t.Fatalf("keep mtime %v: tampered = %v", keepMtime, got)
		}
		lines := logLines(t, h.root)
		if len(lines) != 2 {
			t.Fatalf("keep mtime %v: %d lines, want the edited row and a fresh one", keepMtime, len(lines))
		}
		rows, _, _ := ReadRows(path)
		good, bad, verified, err := VerifySessionRows(h.root, rows)
		if err != nil || !verified || len(good) != 1 || len(bad) != 1 || bad[0].Holder != "s2" {
			t.Fatalf("report rows: %d good %d bad verified %v err %v", len(good), len(bad), verified, err)
		}
		rep := Weekly(good, t0.Add(time.Hour), Default())
		rep.HistoryTampered = len(bad)
		var out bytes.Buffer
		rep.Print(&out)
		if !strings.Contains(out.String(), HistoryTampered+": 1 row(s) left out") {
			t.Fatalf("report:\n%s", out.String())
		}
	}
	// A decision whose tag fails is absent at the next start and reported.
	h := newFix3(t, "")
	h.r.Route(newTask("t2", "plan"))
	raw, _ := os.ReadFile(DecisionPath(h.root))
	os.WriteFile(DecisionPath(h.root), bytes.Replace(raw, []byte(`"policy":"table"`), []byte(`"policy":"xable"`), 1), 0o600)
	r2 := h.open("")
	if got := r2.Tampered(); !slices.Contains(got, "decision t2/1") || r2.decisions[key("t2", 1)] != nil {
		t.Fatalf("decision tampered = %v", got)
	}
}

// Ruling 2: the policy is validated on read against the closed list, and
// lead_pin is written only when a lead pin exists: an attempt that ran on
// another pool than the router decided is manual, propensity 0.
func TestFix3PolicyIsAClosedListAndLeadPinIsReal(t *testing.T) {
	k := SeedKey(strings.Repeat("ab", 32))
	for _, p := range Policies {
		if !(Row{Schema: 1, Task: "t", Policy: p}).Sign(k).Verify(k) {
			t.Fatalf("policy %s refused", p)
		}
	}
	if (Row{Schema: 1, Task: "t", Policy: "bogus"}).Sign(k).Verify(k) {
		t.Fatal("a signed row with a policy off the list verified")
	}
	if (Decision{Task: "t", Attempt: 1, Policy: "bogus"}).sign(k).verify(k) {
		t.Fatal("a signed decision with a policy off the list verified")
	}
	rn := newFixture(t).apply("schema: 1\nexploration_share: 0\n").runner()
	tk := newTask("t1", "implement")
	rn.cur = []task.Task{tk}
	if route := rn.r.Route(tk); route.Pool == nil || route.Pool.Key() != "claude:sonnet:medium" {
		t.Fatalf("route: %+v", route)
	}
	// the lead dispatched it to another pool
	tk.Attempt = 1
	tk.Attempts = []task.Attempt{{N: 1, Holder: "s", Pool: pl("codex:gpt-5.6-terra:medium"), GrantedAt: t0, EndedAt: t0, Outcome: task.OutcomeUnclaimed}}
	rn.cur = []task.Task{tk}
	rn.r.Flush()
	if len(rn.rows) != 1 || rn.rows[0].Policy != PolicyManual || rn.rows[0].Propensity != 0 || rn.rows[0].UsedTier != TierTask {
		t.Fatalf("mismatched dispatch row: %+v", rn.rows)
	}
}

// writeSigned appends a correctly signed row: a record a writer with the
// seed could produce, which the router must still check against the table.
func writeSigned(t *testing.T, root string, key []byte, row Row) {
	row.Schema = RowSchema
	if err := AppendRow(root, row.Sign(key)); err != nil {
		t.Fatal(err)
	}
}

// Ruling 3: the class validation and the floor apply to the tier of the
// entry the first attempt actually ran on, not to the recorded tier string.
func TestFix3ActualTierNotTheRecordedString(t *testing.T) {
	h := newFix3(t, "seed-fix3")
	// A plan task whose record claims plan but whose entry is haiku (smol).
	p := ended("t1", "plan", "claude:haiku:low", task.OutcomeUnclaimed)
	writeSigned(t, h.root, h.key(), Row{Task: "t1", Attempt: 1, Class: "plan", TableTier: TierPlan, UsedTier: TierPlan,
		Entry: "claude:haiku:low", Policy: PolicyLeadPin, Propensity: 1, Outcome: task.OutcomeUnclaimed, GrantedAt: t0, EndedAt: t0})
	// An implement task whose record claims smol but whose entry is sonnet (task).
	im := ended("t2", "implement", "claude:sonnet:medium", task.OutcomeUnclaimed)
	writeSigned(t, h.root, h.key(), Row{Task: "t2", Attempt: 1, Class: "implement", TableTier: TierTask, UsedTier: TierSmol,
		Entry: "claude:sonnet:medium", Policy: PolicyTable, Propensity: 0.95, Outcome: task.OutcomeUnclaimed, GrantedAt: t0, EndedAt: t0})
	r := h.open("seed-fix3")
	if _, route := r.Decide(p, nil); route.Block != BlockPinUnresolved {
		t.Fatalf("plan on a smol entry recorded as plan: %+v", route)
	}
	if d, route := r.Decide(im, nil); route.Pool == nil || d.UsedTier != TierTask || d.Entry.Key() != "claude:sonnet:medium" {
		t.Fatalf("implement recorded smol on a task entry: %+v %+v", d, route)
	}
	// The floor applies to the actual tier: widened past three files, the
	// task-entry attempt retries on plan.
	im.DeclaredFiles = []string{"a", "b", "c", "d"}
	if d, _ := r.Decide(im, nil); d.UsedTier != TierPlan {
		t.Fatalf("floor on the actual tier: %+v", d)
	}
}

// Ruling 4: the tail is checked before any flush appends (a torn tail is
// never covered by a new row), and a row written again after the log lost
// it keeps the policy of its decision, never a new one.
func TestFix3TailBeforeFlushAndRecoveryKeepsPolicy(t *testing.T) {
	id := idWithPolicy("seed-r4", 0.05, true)
	h := newFix3(t, "seed-r4")
	tk := newTask(id, "implement")
	h.cur = []task.Task{tk}
	if route := h.r.Route(tk); route.Pool == nil {
		t.Fatalf("route: %+v", route)
	}
	tk.State, tk.Attempt = task.StateCompleted, 1
	tk.Attempts = []task.Attempt{{N: 1, Holder: "s1", Pool: h.r.decisions[key(id, 1)].Entry, GrantedAt: t0, EndedAt: t0, Outcome: task.OutcomeCompleted}}
	h.cur = []task.Task{tk}
	if err := h.r.Flush(); err != nil {
		t.Fatal(err)
	}
	// Truncated: the row is written again with the decision's policy.
	os.Truncate(OutcomePath(h.root), 0)
	if route := h.r.Route(newTask("t9", "plan")); route.Pool == nil {
		t.Fatalf("after truncation: %+v", route)
	}
	var row Row
	json.Unmarshal(logLines(t, h.root)[0], &row)
	if row.Policy != PolicyExplore || row.Propensity != 0.05 || !row.Verify(h.key()) {
		t.Fatalf("recovered row: %+v", row)
	}
	// A torn tail with a row still to write: routing stops, nothing is appended.
	t2 := ended("t2", "extract", "claude:haiku:low", task.OutcomeCompleted)
	h.cur = []task.Task{tk, t2}
	fh, _ := os.OpenFile(OutcomePath(h.root), os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString(`{"schema":1,"task":"t2","att`)
	fh.Close()
	before, _ := os.ReadFile(OutcomePath(h.root))
	if route := h.r.Route(newTask("t9", "plan")); route.Pool != nil || route.Wait != task.WaitOutcomeLogUnwritable {
		t.Fatalf("torn tail: %+v", route)
	}
	if after, _ := os.ReadFile(OutcomePath(h.root)); !bytes.Equal(before, after) {
		t.Fatalf("the flush appended past a torn tail:\n%s", after)
	}
}

// Ruling 5: the seed is read through a no-follow open: a symlink to a valid
// seed file is refused.
func TestFix3SeedIsNotReadThroughALink(t *testing.T) {
	h := newFix3(t, "")
	raw, _ := os.ReadFile(SeedPath(h.root))
	other := filepath.Join(t.TempDir(), "seed")
	if err := os.WriteFile(other, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(SeedPath(h.root))
	if err := os.Symlink(other, SeedPath(h.root)); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Options{Table: Default(), Root: h.root}); !IsInvalid(err) || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("seed through a link: %v", err)
	}
}

// Ruling 6: a declared tree that cannot be checked (over MaxTreeEntries) is
// a refusal with its reason, not a silent local-only.
func TestFix3UncheckedTreeIsARefusal(t *testing.T) {
	repo := t.TempDir()
	for i := 0; i < 10; i++ {
		p := filepath.Join(repo, "big", fmt.Sprintf("f%d.go", i))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	prev := MaxTreeEntries
	MaxTreeEntries = 5
	t.Cleanup(func() { MaxTreeEntries = prev })
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\nprivacy_paths:\n  - glob: secrets/\n    class: local\n")
	tk := newTask("t1", "implement", "big/")
	tk.ProjectPath = repo
	_, route := f.router().Decide(tk, nil)
	if route.Block != BlockTreeUnchecked || route.Pool != nil || !strings.Contains(route.Detail, "more than 5 entries") {
		t.Fatalf("route = %+v", route)
	}
	if c, _ := f.tbl.RequiredPrivacyAt(repo, []string{"big/"}); c != "vendor" {
		t.Fatalf("an unchecked tree was made silently local: %s", c)
	}
	tk.DeclaredFiles = []string{"big/f1.go"} // narrower: checked
	if _, route := f.router().Decide(tk, nil); route.Pool == nil {
		t.Fatalf("narrowed: %+v", route)
	}
}
