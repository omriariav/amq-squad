package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// sixFourFields are the outcome row fields plan 6.4 names, as JSON keys.
var sixFourFields = []string{"task", "attempt", "class", "features", "table_tier", "used_tier", "entry", "skipped",
	"policy", "propensity", "tokens_in", "tokens_out", "cost_usd", "wall_ms", "wait_ms", "outcome", "verifier_result",
	"review_findings", "consumed_smol_outputs", "exploration_share"}

// exploreRun routes n tasks (classes in rotation) one attempt each and
// returns the rows.
func exploreRun(t *testing.T, share float64, n int) []Row {
	f := newFixture(t)
	f.tbl.ExplorationShare = share
	var cur []task.Task
	var rows []Row
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "acceptance-4", Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return cur, nil }, OnRow: func(row Row) { rows = append(rows, row) }})
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		cur = nil
		tk := newTask(fmt.Sprintf("t%d", i), task.Classes[i%len(task.Classes)])
		route := r.Route(tk)
		if route.Pool == nil {
			t.Fatalf("%s %s: no route %+v", tk.ID, tk.Class, route)
		}
		tk.State, tk.Attempt, tk.Pool = task.StateCompleted, 1, route.Pool
		tk.Attempts = []task.Attempt{{N: 1, Holder: "s", Pool: route.Pool, GrantedAt: t0, EndedAt: t0.Add(time.Minute), Outcome: task.OutcomeCompleted}}
		cur = []task.Task{tk}
		if err := r.Flush(); err != nil {
			t.Fatal(err)
		}
	}
	if len(rows) != n {
		t.Fatalf("%d rows for %d attempts", len(rows), n)
	}
	return rows
}

// Acceptance 4: over 10,000 seeded draws the share is 0.05 within 0.01,
// never on plan or review, and every row records the policy and the
// propensity.
func TestAcceptance4Exploration(t *testing.T) {
	rows := exploreRun(t, 0.05, 15_000) // 10,000 eligible draws, 5,000 plan and review
	eligible, explored := 0, 0
	for _, row := range rows {
		switch {
		case row.Class == "plan" || row.Class == "review":
			if row.Policy != PolicyTable || row.Propensity != 1 || row.UsedTier != row.TableTier {
				t.Fatalf("%s %s: policy %s propensity %v tier %s", row.Task, row.Class, row.Policy, row.Propensity, row.UsedTier)
			}
		default:
			eligible++
			switch row.Policy {
			case PolicyExplore:
				explored++
				if row.Propensity != 0.05 || row.UsedTier == row.TableTier {
					t.Fatalf("explore row %+v", row)
				}
			case PolicyTable:
				if row.Propensity != 0.95 || row.UsedTier != row.TableTier {
					t.Fatalf("table row %+v", row)
				}
			default:
				t.Fatalf("row %s policy %q", row.Task, row.Policy)
			}
		}
		if row.ExplorationShare != 0.05 {
			t.Fatalf("row %s records share %v", row.Task, row.ExplorationShare)
		}
	}
	if eligible != 10_000 {
		t.Fatalf("%d eligible draws", eligible)
	}
	got := float64(explored) / float64(eligible)
	t.Logf("exploration share over %d eligible draws: %d explored = %.4f", eligible, explored, got)
	if math.Abs(got-0.05) > 0.01 {
		t.Fatalf("share = %.4f, want 0.05 within 0.01", got)
	}
	// Zero disables.
	for _, row := range exploreRun(t, 0, 2_000) {
		if row.Policy != PolicyTable || row.Propensity != 1 {
			t.Fatalf("share 0: %+v", row)
		}
	}
}

func TestDrawIsUniform(t *testing.T) {
	const n = 100_000
	var buckets [10]int
	for i := 0; i < n; i++ {
		u := Draw("uniform", fmt.Sprintf("t%d", i))
		if u < 0 || u >= 1 {
			t.Fatalf("draw %v", u)
		}
		buckets[int(u*10)]++
	}
	for i, c := range buckets {
		if c < n/10*9/10 || c > n/10*11/10 {
			t.Fatalf("bucket %d has %d of %d", i, c, n)
		}
	}
}

type seatLauncher struct{ requests []scheduler.SeatRequest }

func (l *seatLauncher) RequestSeat(r scheduler.SeatRequest) error {
	l.requests = append(l.requests, r)
	return nil
}
func (l *seatLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (l *seatLauncher) CloseSeat(string, string) error                { return nil }

// Acceptance 5: every attempt of a seeded run has a row with every 6.4 field.
// The run uses the real store, the real scheduler with this Router, seats
// that join on request, one failed attempt that retries one tier up, a
// review of an implementation, and ledger evidence for the token counts.
func TestAcceptance5EveryAttemptHasARow(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := t0
	clock := func() time.Time { return now }
	add := func(title, class string, deps ...string) {
		if _, err := store.Add(task.AddInput{Title: title, Class: class, DependsOn: deps}, now); err != nil {
			t.Fatal(err)
		}
	}
	add("pull fields", "extract")     // t1
	add("summarize log", "summarize") // t2
	add("run linter", "script")       // t3 (its first attempt fails)
	add("fix parser", "implement")    // t4
	add("plan cutover", "plan")       // t5
	add("review parser", "review", "t4")
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	// The owner's price for one entry (user routing.yaml); the router's
	// default usage reads the hook ledger under Root.
	if f.tbl, err = Apply(f.tbl, "schema: 1\nprices:\n  - entry: claude:sonnet:medium\n    input_usd_per_mtok: 3\n    output_usd_per_mtok: 15\n", "user.yaml", false); err != nil {
		t.Fatal(err)
	}
	for i := range f.reg.Entries {
		if f.reg.Entries[i].Model == "sonnet" {
			f.reg.Entries[i].CostIndex = 2
		}
	}
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: root, Now: clock,
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }})
	if err != nil {
		t.Fatal(err)
	}
	l := &seatLauncher{}
	s, err := scheduler.New(scheduler.Options{Store: store, Now: clock, Launcher: l, Router: r, Config: scheduler.Config{MaxSeats: 12}})
	if err != nil {
		t.Fatal(err)
	}
	joined := 0
	failedOnce := false
	ledger := func(handle string) {
		line := fmt.Sprintf(`{"schema":1,"seq":1,"at":%q,"handle":%q,"payload":{"usage":{"input_tokens":1000,"output_tokens":200}}}`+"\n",
			now.Format(time.RFC3339Nano), handle)
		p := filepath.Join(root, "extensions", "amq-squad", "ledger", handle+".jsonl")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		fh, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		fh.WriteString(line)
		fh.Close()
	}
	for step := 0; step < 40; step++ {
		now = now.Add(time.Minute)
		for ; joined < len(l.requests); joined++ {
			q := l.requests[joined]
			s.Post(scheduler.Event{Kind: scheduler.SeatUp, At: now, Handle: fmt.Sprintf("seat%d", joined+1), Pool: q.Pool, RequestID: q.ID})
		}
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		list, err := store.List()
		if err != nil {
			t.Fatal(err)
		}
		done := 0
		for _, tk := range list {
			if tk.State == task.StateCompleted {
				done++
			}
			if tk.State != task.StateInProgress || tk.Lease == nil {
				continue
			}
			h := tk.Lease.Holder
			now = now.Add(10 * time.Second)
			if _, err := store.Claim(tk.ID, h, nil, task.DefaultLeaseDuration, now); err != nil {
				t.Fatal(err)
			}
			now = now.Add(30 * time.Second)
			ledger(h)
			if tk.ID == "t3" && !failedOnce {
				failedOnce = true
				if _, err := store.Fail(tk.ID, h, "lint exit 1", now); err != nil {
					t.Fatal(err)
				}
				continue
			}
			ev := "ok"
			if tk.Class == "review" {
				ev = "findings=2"
			}
			harnessPass(t, store, tk.ID, now)
			if _, err := store.Done(tk.ID, h, ev, "", now); err != nil {
				t.Fatal(err)
			}
		}
		if done == len(list) {
			break
		}
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	list, _ := store.List()
	attempts := 0
	for _, tk := range list {
		if tk.State != task.StateCompleted {
			t.Fatalf("%s is %s (%s)", tk.ID, tk.State, tk.WaitReason)
		}
		attempts += len(tk.Attempts)
	}
	raw, err := os.ReadFile(OutcomePath(root))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if attempts != 7 || len(lines) != attempts {
		t.Fatalf("%d attempts, %d rows:\n%s", attempts, len(lines), raw)
	}
	seen := map[string]bool{}
	for _, line := range lines {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatal(err)
		}
		for _, k := range sixFourFields {
			if _, ok := m[k]; !ok {
				t.Fatalf("row without %s: %s", k, line)
			}
		}
		var row Row
		json.Unmarshal(line, &row)
		k := key(row.Task, row.Attempt)
		if seen[k] {
			t.Fatalf("attempt %s logged twice", k)
		}
		seen[k] = true
		if row.Entry == "" || row.UsedTier == "" || row.Policy == "" || row.TokensIn == nil || *row.TokensIn != 1000 {
			t.Fatalf("row %s", line)
		}
	}
	rows, _, _ := ReadRows(OutcomePath(root))
	byKey := map[string]Row{}
	for _, row := range rows {
		byKey[key(row.Task, row.Attempt)] = row
	}
	if f1, f2 := byKey["t3/1"], byKey["t3/2"]; f1.Outcome != task.OutcomeFailed || f1.VerifierResult != VerifierFail ||
		f2.UsedTier != TierTask || !slices.Contains(f2.Skipped, Skip{Entry: f1.Entry, Reason: SkipUsedBefore}) || f2.VerifierResult != VerifierPass {
		t.Fatalf("retry rows: %+v / %+v", f1, f2)
	}
	if rv := byKey["t6/1"]; rv.ReviewFindings == nil || *rv.ReviewFindings != 2 || rv.Features.ImplementerFamily != "anthropic" || !strings.HasPrefix(rv.Entry, "codex:") {
		t.Fatalf("review row: %+v", rv)
	}
	if im := byKey["t4/1"]; im.CostUSD == nil || math.Abs(*im.CostUSD-0.006) > 1e-9 || im.WaitMS <= 0 || im.WallMS <= 0 || im.CostIndex != 2 {
		t.Fatalf("implement row cost %v wait %d wall %d", im.CostUSD, im.WaitMS, im.WallMS)
	}
	if sm := byKey["t2/1"]; sm.CostUSD != nil || sm.VerifierResult != VerifierNoCheck {
		t.Fatalf("summarize row: %+v", sm)
	}
	// A restarted router logs nothing twice.
	r2, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: root, Now: clock,
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }})
	if err != nil {
		t.Fatal(err)
	}
	if err := r2.Flush(); err != nil {
		t.Fatal(err)
	}
	if again, _, _ := ReadRows(OutcomePath(root)); len(again) != attempts {
		t.Fatalf("restart wrote %d rows, want %d", len(again), attempts)
	}
}

// Consumed smol outputs: a task that depends on a task completed on smol
// names it in its row.
func TestConsumedSmolOutputs(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	var cur []task.Task
	var rows []Row
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return cur, nil }, OnRow: func(row Row) { rows = append(rows, row) }})
	if err != nil {
		t.Fatal(err)
	}
	ex := newTask("t1", "extract")
	route := r.Route(ex)
	ex.State, ex.Attempt = task.StateCompleted, 1
	ex.Attempts = []task.Attempt{{N: 1, Pool: route.Pool, GrantedAt: t0, EndedAt: t0, Outcome: task.OutcomeCompleted}}
	cur = []task.Task{ex}
	r.Flush()
	rv := newTask("t2", "review")
	rv.DependsOn = []string{"t1"}
	route = r.Route(rv)
	rv.State, rv.Attempt, rv.Evidence = task.StateCompleted, 1, "review: findings: 3"
	rv.Attempts = []task.Attempt{{N: 1, Pool: route.Pool, GrantedAt: t0, EndedAt: t0, Outcome: task.OutcomeCompleted}}
	cur = []task.Task{ex, rv}
	r.Flush()
	if len(rows) != 2 || !slices.Equal(rows[1].ConsumedSmolOutputs, []string{"t1/1"}) || *rows[1].ReviewFindings != 3 {
		t.Fatalf("rows %+v", rows)
	}
}

// A decision survives a scheduler restart: the row of an attempt dispatched
// before the restart keeps its policy, propensity and skipped entries.
func TestDecisionSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	f := newFixture(t)
	id := idWithPolicy("seed", 0.05, true)
	tk := newTask(id, "implement")
	var cur []task.Task
	opts := Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: root, Seed: "seed", Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return cur, nil }}
	r1, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	route := r1.Route(tk)
	var rows []Row
	opts.OnRow = func(row Row) { rows = append(rows, row) }
	r2, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	tk.State, tk.Attempt = task.StateCompleted, 1
	tk.Attempts = []task.Attempt{{N: 1, Pool: route.Pool, GrantedAt: t0, EndedAt: t0, Outcome: task.OutcomeCompleted}}
	cur = []task.Task{tk}
	if err := r2.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Policy != PolicyExplore || rows[0].Propensity != 0.05 || rows[0].UsedTier != TierSmol || len(rows[0].Skipped) == 0 {
		t.Fatalf("rows after restart: %+v", rows)
	}
}

// TestOutcomeAppendChild is one writer process of the concurrency test.
func TestOutcomeAppendChild(t *testing.T) {
	tag := os.Getenv("ROUTING_APPEND_CHILD_TAG")
	if tag == "" {
		t.Skip("helper process")
	}
	// Each row is far above PIPE_BUF, so only the flock keeps lines whole.
	row := Row{Task: "t-" + tag, Attempt: 1, Class: "script", Skipped: []Skip{{Entry: tag, Reason: strings.Repeat("x", 512<<10)}}}
	if err := AppendRow(os.Getenv("ROUTING_APPEND_CHILD_ROOT"), row); err != nil {
		t.Fatal(err)
	}
}

// Acceptance 5: a concurrent append as in 2a-3: eight writer processes,
// every line whole.
func TestAcceptance5ConcurrentAppendKeepsLinesWhole(t *testing.T) {
	root := t.TempDir()
	const writers = 8
	var cmds []*exec.Cmd
	var outs []*bytes.Buffer
	for i := 0; i < writers; i++ {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOutcomeAppendChild$", "-test.count=1")
		cmd.Env = append(os.Environ(), fmt.Sprintf("ROUTING_APPEND_CHILD_TAG=w%d", i), "ROUTING_APPEND_CHILD_ROOT="+root)
		out := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		cmds, outs = append(cmds, cmd), append(outs, out)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("writer %d: %v\n%s", i, err, outs[i])
		}
	}
	rows, skipped, err := ReadRows(OutcomePath(root))
	if err != nil || skipped != 0 || len(rows) != writers {
		t.Fatalf("%d rows, %d unparseable, err %v", len(rows), skipped, err)
	}
	tags := map[string]bool{}
	for _, row := range rows {
		if len(row.Skipped) != 1 || len(row.Skipped[0].Reason) != 512<<10 || row.Task != "t-"+row.Skipped[0].Entry {
			t.Fatalf("torn row for %s", row.Task)
		}
		tags[row.Task] = true
	}
	if len(tags) != writers {
		t.Fatalf("tags %v", tags)
	}
	st, err := os.Stat(OutcomePath(root))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("outcome log mode %v (%v)", st.Mode(), err)
	}
}

// A torn tail (a crash mid-write) is terminated, skipped by readers, and the
// next row stays whole.
func TestOutcomeTornTail(t *testing.T) {
	root := t.TempDir()
	if err := AppendRow(root, Row{Task: "t1", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	fh, _ := os.OpenFile(OutcomePath(root), os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString(`{"schema":1,"task":"t2","att`)
	fh.Close()
	if err := AppendRow(root, Row{Task: "t3", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	rows, skipped, err := ReadRows(OutcomePath(root))
	if err != nil || skipped != 1 || len(rows) != 2 || rows[1].Task != "t3" {
		t.Fatalf("rows %+v skipped %d err %v", rows, skipped, err)
	}
}
