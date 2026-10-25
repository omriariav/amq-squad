package routing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Phase 5 Codex re-pass on the routing package: one test row per item.

// Ruling 1: an implement task pinned to smol, claimed and reported done with
// empty evidence, does not complete as verified. The store blocks it with
// verdict_missing, its row says the verifier did not run (never pass), and
// the attempt counts as failed: the retry runs one tier up. A completion
// with no recorded verdict is never pass either.
func TestRepassVerdictMissingIsNeverAPassAndEscalates(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var rows []Row
	f := newFixture(t)
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "repass", Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }, OnRow: func(row Row) { rows = append(rows, row) }})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := store.Add(task.AddInput{Title: "smol implement", Class: "implement", Pool: pl("claude:haiku:low")}, t0)
	if err != nil {
		t.Fatal(err)
	}
	route := r.Route(tk)
	if route.Pool == nil || route.Pool.Key() != "claude:haiku:low" {
		t.Fatalf("pinned route: %+v", route)
	}
	if _, err := store.Claim(tk.ID, "seat-1", route.Pool, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	done, err := store.Done(tk.ID, "seat-1", "", "", t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if done.State != task.StateBlocked || done.BlockReason != task.BlockVerdictMissing {
		t.Fatalf("an implement done report with no verdict completed: %s %s", done.State, done.BlockReason)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].VerifierResult == VerifierPass || rows[0].VerifierResult != VerifierNotRun || rows[0].Outcome != task.OutcomeBlocked {
		t.Fatalf("row: %+v", rows)
	}
	if _, err := store.Reset(tk.ID, nil, t0.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	again, _ := store.Get(tk.ID)
	if d, route := r.Decide(again, nil); route.Pool == nil || d.UsedTier != TierTask {
		t.Fatalf("the retry after a missing verdict did not escalate: tier %s route %+v", d.UsedTier, route)
	}
	if got := verifierResult(VerifierTests, task.Attempt{Outcome: task.OutcomeCompleted}); got != VerifierNotRun {
		t.Fatalf("a completion with no verdict is %q", got)
	}
	pass := task.Attempt{Outcome: task.OutcomeCompleted, Verdict: &task.Verdict{Verifier: VerifierTests, Result: task.VerdictPass}}
	if got := verifierResult(VerifierTests, pass); got != VerifierPass {
		t.Fatalf("a recorded pass is %q", got)
	}
}

// Ruling 2: a concurrency cap counts the scheduler's seat view, each seat
// once, not tasks: a seat still closing after its task expired holds the
// Ollama GPU, so does the lead's own seat on it, and a seat holding a
// leased task is one occupant, not two.
func TestRepassConcurrencyCountsSeatsNotTasks(t *testing.T) {
	local := "pi:ollama/qwen3.6:27b:low"
	f := newFixture(t)
	f.input = 10_000
	var view []scheduler.SeatView
	var lead []task.Pool
	router := func() *Router {
		r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "fixture", Now: func() time.Time { return t0 },
			Tasks: func() ([]task.Task, error) { return f.list, nil }, InputTokens: func(task.Task) int { return f.input },
			Seats: func() []scheduler.SeatView { return view }, LeadPools: lead})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	decide := func() Decision {
		d, _ := router().Decide(newTask("t1", "script"), f.list)
		return d
	}
	if d := decide(); d.Entry == nil || d.Entry.Key() != local {
		t.Fatalf("free GPU: %+v", d)
	}
	// The expired task went back to pending; its seat is still closing.
	view = []scheduler.SeatView{{Handle: "seat-old", Pool: *pl(local), Closing: "lease_expired"}}
	if d := decide(); d.Entry.Key() == local || !hasSkip(d, local, SkipConcurrencyCap) {
		t.Fatalf("a closing seat occupied nothing: %+v", d)
	}
	// The lead's own seat on the same GPU (a thinking level the registry
	// does not list: the model places it in the pool).
	view, lead = nil, []task.Pool{*pl("pi:ollama/qwen3.6:27b:medium")}
	if d := decide(); d.Entry.Key() == local || !hasSkip(d, local, SkipConcurrencyCap) {
		t.Fatalf("the lead's seat occupied nothing: %+v", d)
	}
	// A seat holding a leased task is one occupant: under a cap of 2 a
	// second seat still fits.
	lead = nil
	for i := range f.reg.Entries {
		if e := &f.reg.Entries[i]; e.ConcurrencyPool == "ollama-gpu" {
			e.ConcurrencyCap = 2
		}
	}
	busy := newTask("b1", "script")
	busy.State, busy.Pool, busy.Lease = task.StateInProgress, pl(local), &task.Lease{Holder: "seat-1"}
	f.list = []task.Task{busy}
	view = []scheduler.SeatView{{Handle: "seat-1", Pool: *pl(local)}}
	if d := decide(); d.Entry == nil || d.Entry.Key() != local {
		t.Fatalf("one seat with its task counted twice: %+v", d)
	}
}

// P2-3: the counters are decoded by name, so a usage object with nested
// objects (input_tokens_details) is read, and the OpenAI-style aliases
// prompt_tokens and completion_tokens are the input and output counters,
// in a usage object or flat on the payload; the attempt's usage and cost
// are known.
func TestRepassUsageCountersByNameNestedFlatAndAliases(t *testing.T) {
	root := t.TempDir()
	payloads := []string{
		`{"usage":{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":10},"output_tokens_details":{"reasoning_tokens":5}}}`,
		`{"message":{"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":0}}}}`,
		`{"prompt_tokens":10,"completion_tokens":2,"tool_input":{"input":"x"}}`,
	}
	writeLedger(t, root, "s1", payloads)
	pool := pl("claude:sonnet:medium")
	u := LedgerUsage(root, map[task.Pool]Price{*pool: {InputPerMTok: 3, OutputPerMTok: 15}})(task.Task{},
		task.Attempt{Holder: "s1", Pool: pool, GrantedAt: t0, EndedAt: t0.Add(time.Hour)})
	if u.TokensIn == nil || *u.TokensIn != 1110 || u.TokensOut == nil || *u.TokensOut != 222 || u.CostUSD == nil || u.Invalid != "" {
		t.Fatalf("usage: in %v out %v cost %v invalid %q", deref(u.TokensIn), deref(u.TokensOut), u.CostUSD, u.Invalid)
	}
}

// P2-4: a negative, fractional or out-of-range counter is unknown usage,
// reported on the row and in the weekly report, and never subtracts from
// the day's spend; a usage source's negative cost is refused the same way.
func TestRepassInvalidCountersAreUnknownUsageAndReported(t *testing.T) {
	for _, bad := range []string{`-500`, `12.5`, `1e15`} {
		root := t.TempDir()
		writeLedger(t, root, "s1", []string{`{"usage":{"input_tokens":1000,"output_tokens":200}}`,
			`{"usage":{"input_tokens":` + bad + `,"output_tokens":1}}`})
		pool := pl("claude:sonnet:medium")
		u := LedgerUsage(root, map[task.Pool]Price{*pool: {InputPerMTok: 3, OutputPerMTok: 15}})(task.Task{},
			task.Attempt{Holder: "s1", Pool: pool, GrantedAt: t0, EndedAt: t0.Add(time.Hour)})
		if u.TokensIn != nil || u.CostUSD != nil || u.Invalid == "" {
			t.Fatalf("counter %s: in %v cost %v invalid %q", bad, deref(u.TokensIn), u.CostUSD, u.Invalid)
		}
	}
	f := newFixture(t)
	neg := -5.0
	var rows []Row
	tk := newTask("t1", "plan")
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "repass", Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return []task.Task{tk}, nil }, OnRow: func(row Row) { rows = append(rows, row) },
		Usage: func(task.Task, task.Attempt) Usage {
			in, out := int64(10), int64(-1)
			return Usage{TokensIn: &in, TokensOut: &out, CostUSD: &neg}
		}})
	if err != nil {
		t.Fatal(err)
	}
	tk.Attempt, tk.Attempts = 1, []task.Attempt{{N: 1, Holder: "s", Pool: pl("claude:opus:high"), GrantedAt: t0, EndedAt: t0.Add(time.Minute), Outcome: task.OutcomeFailed}}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CostUSD != nil || rows[0].TokensOut != nil || rows[0].UsageInvalid == "" {
		t.Fatalf("row: %+v", rows)
	}
	if spent := r.spent[t0.UTC().Format(time.DateOnly)]; spent != 0 {
		t.Fatalf("the day's spend is %v", spent)
	}
	rows[0].EndedAt = ReportNow.Add(-time.Hour)
	if rep := Weekly(rows, ReportNow, Default()); rep.UsageInvalid != 1 {
		t.Fatalf("report usage_invalid = %d", rep.UsageInvalid)
	}
}

// P2-5: a task's cost is attributed from its whole history: an excluded
// first attempt (a runtime with incomplete evidence) or a first attempt in
// an earlier week never makes the second attempt the task's first. The
// task's cost is unknown when a required attempt is excluded, and it is
// never booked at the retry's tier.
func TestRepassTaskHistoriesKeepTheFirstAttempt(t *testing.T) {
	now := ReportNow
	mk := func(id string, n int, entry, tier, outcome string, cost *float64, tokens bool, ended time.Time) Row {
		r := Row{Schema: RowSchema, Task: id, Attempt: n, Class: "implement", Features: Features{DeclaredFiles: 1}, UsedTier: tier,
			Entry: entry, Runtime: strings.SplitN(entry, ":", 2)[0], Outcome: outcome, CostUSD: cost, EndedAt: ended}
		if tokens {
			r.TokensIn, r.TokensOut = i64(100), i64(10)
		}
		return r
	}
	day := now.Add(-24 * time.Hour)
	taskBucket := func(rep Report) *BucketStats {
		for i := range rep.Buckets {
			if b := rep.Buckets[i]; b.Class == "implement" && b.Tier == TierTask {
				return &rep.Buckets[i]
			}
		}
		return nil
	}
	// Excluded: the pi runtime has a row without tokens (t9), so t1's smol
	// attempt on pi is left out; the completion on task is t1's second
	// attempt. t8 is a clean smol task, so the smol bucket has a cost.
	excluded := []Row{
		mk("t1", 1, "pi:ollama/qwen3.6:27b:low", TierSmol, task.OutcomeFailed, f64(0.001), true, day),
		mk("t1", 2, "claude:sonnet:medium", TierTask, task.OutcomeCompleted, f64(0.01), true, day.Add(time.Hour)),
		mk("t9", 1, "pi:ollama/qwen3.6:27b:low", TierSmol, task.OutcomeFailed, nil, false, day),
		mk("t8", 1, "claude:haiku:low", TierSmol, task.OutcomeCompleted, f64(0.002), true, day),
	}
	rep := Weekly(excluded, now, Default())
	if b := taskBucket(rep); b == nil || b.CostUSD != nil {
		t.Fatalf("excluded first attempt: the retry's bucket books the task's cost: %+v", b)
	}
	for _, b := range rep.Buckets {
		if b.Class == "implement" && b.Tier == TierSmol && (b.CostUSD == nil || *b.CostUSD != 0.002) {
			t.Fatalf("excluded first attempt: the task's cost is not unknown: %+v", b)
		}
	}
	// Across the weekly boundary: the failed first attempt ended last week.
	boundary := []Row{
		mk("t2", 1, "claude:haiku:low", TierSmol, task.OutcomeFailed, f64(0.001), true, now.Add(-8*24*time.Hour)),
		mk("t2", 2, "claude:sonnet:medium", TierTask, task.OutcomeCompleted, f64(0.01), true, day),
	}
	rep = Weekly(boundary, now, Default())
	if b := taskBucket(rep); b == nil || b.CostUSD != nil {
		t.Fatalf("first attempt last week: the retry's bucket books the task's cost: %+v", b)
	}
	// ... and the first attempt's bucket, with no row in this week, reports
	// the task's whole cost (narrow pass, P2-5).
	if !slices.ContainsFunc(rep.Buckets, func(b BucketStats) bool {
		return b.Tier == TierSmol && b.CostUSD != nil && *b.CostUSD > 0.0109 && *b.CostUSD < 0.0111
	}) {
		t.Fatalf("first attempt last week: the smol cost is not reported: %+v", rep.Buckets)
	}
	// A first attempt with no row at all (the history starts at attempt 2).
	if b := taskBucket(Weekly(boundary[1:], now, Default())); b == nil || b.CostUSD != nil {
		t.Fatalf("missing first attempt: %+v", b)
	}
	// The whole history in the window: the cost, redo included, is the
	// first attempt's bucket's.
	inWeek := []Row{
		mk("t3", 1, "claude:haiku:low", TierSmol, task.OutcomeFailed, f64(0.001), true, day),
		mk("t3", 2, "claude:sonnet:medium", TierTask, task.OutcomeCompleted, f64(0.01), true, day.Add(time.Hour)),
	}
	rep = Weekly(inWeek, now, Default())
	for _, b := range rep.Buckets {
		if b.Tier == TierSmol && (b.CostUSD == nil || *b.CostUSD < 0.0109 || *b.CostUSD > 0.0111 || b.Redone != 1) {
			t.Fatalf("smol bucket: %+v", b)
		}
	}
}

// P2-6: an unpriced entry (known tokens, no cost) on a runtime that has an
// incomplete row elsewhere is still named.
func TestRepassUnpricedEntriesBeforeTheRuntimeFilter(t *testing.T) {
	at := ReportNow.Add(-time.Hour)
	priced := Row{Schema: RowSchema, Task: "t1", Attempt: 1, Class: "script", UsedTier: TierSmol, Entry: "pi:ollama/qwen3.6:27b:low", Runtime: "pi",
		TokensIn: i64(100), TokensOut: i64(10), Outcome: task.OutcomeCompleted, EndedAt: at}
	incomplete := Row{Schema: RowSchema, Task: "t2", Attempt: 1, Class: "script", UsedTier: TierSmol, Entry: "pi:ollama/qwen3.6:35b-a3b:low", Runtime: "pi",
		Outcome: task.OutcomeFailed, EndedAt: at}
	rep := Weekly([]Row{priced, incomplete}, ReportNow, Default())
	if len(rep.Excluded) != 1 || len(rep.Unpriced) != 1 || rep.Unpriced[0] != "pi:ollama/qwen3.6:27b:low" {
		t.Fatalf("excluded %+v unpriced %v", rep.Excluded, rep.Unpriced)
	}
}

// P2-7: the report runs the start's head and seed-identity checks: a log
// cut to a valid prefix with head.json removed, and a seed copied onto
// another inode, are routing_invalid, as at the router's start.
func TestRepassReportRunsTheStartChecks(t *testing.T) {
	h, lines := threeRows(t)
	if _, _, verified, err := VerifySessionLog(h.root); err != nil || !verified {
		t.Fatalf("intact session: %v %v", verified, err)
	}
	writeLines(t, h.root, lines[:1])
	if err := os.Remove(HeadPath(h.root)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := VerifySessionLog(h.root); !IsInvalid(err) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("cut log with its head removed: %v", err)
	}
	h2 := newFix3(t, "")
	h2.r.Route(newTask("t1", "plan"))
	raw, _ := os.ReadFile(SeedPath(h2.root))
	os.Remove(SeedPath(h2.root))
	if err := os.WriteFile(SeedPath(h2.root), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := VerifySessionLog(h2.root); !IsInvalid(err) || !strings.Contains(err.Error(), "device and inode") {
		t.Fatalf("replaced seed: %v", err)
	}
}

func writeLedger(t *testing.T, root, holder string, payloads []string) {
	t.Helper()
	dir := filepath.Join(root, "extensions", "amq-squad", "ledger")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for i, p := range payloads {
		line, _ := json.Marshal(map[string]any{"schema": 1, "seq": i + 1, "at": t0.Add(time.Duration(i+1) * time.Second).Format(time.RFC3339Nano),
			"handle": holder, "payload": json.RawMessage(p)})
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, holder+".jsonl"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// Ruling on ruling 1: a class whose verifier command the grant does not
// configure routes at its tier and never below (an exploration draw goes up,
// a pin below the tier is refused), completes on the done report, and its
// row's verifier_result is none, never pass. With a verifier configured the
// rows above stand (TestRepassVerdictMissingIsNeverAPassAndEscalates).
func TestRepassUnconfiguredVerifierRoutesAtTierAndCompletes(t *testing.T) {
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	f.g.Verifiers = nil
	store.Verifiers = func(tk task.Task) (string, bool, error) {
		return f.g.VerifierFor(tk.Class), f.g.VerifierRequired(tk.Class), nil
	}
	var rows []Row
	mk := func(share float64) *Router {
		f.tbl.ExplorationShare = share
		r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "repass", Now: func() time.Time { return t0 },
			Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }, OnRow: func(row Row) { rows = append(rows, row) }})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if d, _ := mk(1).Decide(newTask("x1", "implement"), nil); d.UsedTier != TierPlan || d.Policy != PolicyExplore {
		t.Fatalf("an explored implement with no verifier: %s %s, want plan explore", d.UsedTier, d.Policy)
	}
	pinned := newTask("x2", "script")
	pinned.Pool = pl("claude:sonnet:medium")
	if _, route := mk(0).Decide(pinned, nil); route.Pool == nil {
		t.Fatalf("a script pinned at task tier: %+v", route)
	}
	smol := newTask("x3", "implement")
	smol.Pool = pl("claude:haiku:low")
	if _, route := mk(0).Decide(smol, nil); route.Block != BlockPinUnresolved {
		t.Fatalf("an implement with no verifier pinned below its tier: %+v", route)
	}
	r := mk(0)
	tk, err := store.Add(task.AddInput{Title: "implement", Class: "implement"}, t0)
	if err != nil {
		t.Fatal(err)
	}
	route := r.Route(tk)
	if route.Pool == nil || route.Pool.Key() != "claude:sonnet:medium" {
		t.Fatalf("at-tier route: %+v", route)
	}
	if _, err := store.Claim(tk.ID, "seat-1", route.Pool, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	done, err := store.Done(tk.ID, "seat-1", "", "", t0.Add(time.Second))
	if err != nil || done.State != task.StateCompleted {
		t.Fatalf("done with no verifier configured: %s %s %v", done.State, done.BlockReason, err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].VerifierResult != VerifierNoCheck || rows[0].Outcome != task.OutcomeCompleted {
		t.Fatalf("row: %+v", rows)
	}
}

// Narrow pass R2 (a) and the seat-view wiring, through a real scheduler
// whose router counts the scheduler's own Occupancy (the local entry's cap
// is 1): a seat still closing after its claimed lease expired holds the GPU,
// so the pending task's retry goes to the next entry; an idle seat of the
// local pool is capacity, so a new task is dispatched to it.
func TestRepassSchedulerSeatViewHoldsTheCap(t *testing.T) {
	local := "pi:ollama/qwen3.6:27b:low"
	run := func(t *testing.T) (task.Store, *scheduler.Scheduler, *recordingLauncher, *time.Time) {
		store, err := task.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		now := t0
		f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
		var s *scheduler.Scheduler
		r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "repass", Now: func() time.Time { return now },
			Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }, InputTokens: func(task.Task) int { return 10 },
			Seats: func() []scheduler.SeatView { return s.Occupancy() }})
		if err != nil {
			t.Fatal(err)
		}
		l := &recordingLauncher{}
		s, err = scheduler.New(scheduler.Options{Store: store, Now: func() time.Time { return now }, Launcher: l, Router: r,
			Config: scheduler.Config{MaxSeats: 4, LeaseDuration: time.Minute}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Add(task.AddInput{Title: "one", Class: "script"}, now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		if len(l.requests) != 1 || l.requests[0].Pool.Key() != local {
			t.Fatalf("first request: %+v", l.requests)
		}
		s.Post(scheduler.Event{Kind: scheduler.SeatUp, At: now, Handle: "seat-a", Pool: l.requests[0].Pool, RequestID: l.requests[0].ID})
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.Get("t1"); got.State != task.StateInProgress || got.Lease.Holder != "seat-a" {
			t.Fatalf("t1 not dispatched to seat-a: %s %+v", got.State, got.Lease)
		}
		return store, s, l, &now
	}
	t.Run("closing seat", func(t *testing.T) {
		store, s, l, now := run(t)
		if _, err := store.Claim("t1", "seat-a", nil, time.Minute, *now); err != nil {
			t.Fatal(err)
		}
		l.closeErr = errors.New("still running") // its process is still closing
		*now = now.Add(2 * time.Minute)          // the claimed lease expires; seat-a closes
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		// t1's retry moves a tier up; a new smol task t2 would take the GPU,
		// which the closing seat still holds
		if _, err := store.Add(task.AddInput{Title: "two", Class: "script"}, *now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		var t2 *scheduler.SeatRequest
		for i := range l.requests {
			if l.requests[i].TaskID == "t2" {
				t2 = &l.requests[i]
			}
		}
		if t2 == nil || t2.Pool.Key() == local {
			t.Fatalf("a new task went to the GPU a closing seat holds: %+v", l.requests)
		}
	})
	t.Run("idle seat", func(t *testing.T) {
		store, s, l, now := run(t)
		harnessPass(t, store, "t1", *now)
		if _, err := store.Done("t1", "seat-a", "", "", *now); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Add(task.AddInput{Title: "two", Class: "script"}, *now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		if got, _ := store.Get("t2"); got.State != task.StateInProgress || got.Lease.Holder != "seat-a" || len(l.requests) != 1 {
			t.Fatalf("an idle seat of the pool was not capacity: t2 %s %+v requests %+v", got.State, got.Lease, l.requests)
		}
	})
}

// Narrow pass, coverage: a signed outcome row with a negative cost, read
// back at a restart, never lowers the day's spend.
func TestRepassNegativeCostRowNeverLowersTheSpend(t *testing.T) {
	h := newFix3(t, "")
	h.cur = []task.Task{ended("t1", "plan", "claude:opus:high", task.OutcomeCompleted)}
	if err := h.r.Flush(); err != nil {
		t.Fatal(err)
	}
	day := t0.UTC().Format(time.DateOnly)
	neg := -5.0
	row := Row{Schema: RowSchema, At: t0, Task: "t2", Attempt: 1, Class: "plan", Outcome: task.OutcomeFailed, Policy: PolicyManual,
		Skipped: []Skip{}, WaitReasons: []string{}, ConsumedSmolOutputs: []string{}, CostUSD: &neg, EndedAt: t0,
		Seq: h.r.head.Seq + 1, Prev: h.r.head.Tag}
	if err := AppendRow(h.root, row.Sign(h.key())); err != nil {
		t.Fatal(err)
	}
	r := h.open("")
	if got := r.spent[day]; got < 0 {
		t.Fatalf("the day's spend after a negative-cost row: %v", got)
	}
	if ids := r.Tampered(); len(ids) != 0 {
		t.Fatalf("the signed row did not verify: %v", ids)
	}
}
