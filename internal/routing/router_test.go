package routing

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func ready(runtime, provider, model, effort, privacy, pool string, cap int) registry.Entry {
	return registry.Entry{Runtime: runtime, Provider: provider, Model: model, Effort: effort, Verified: true,
		State: registry.StateReady, PrivacyClass: privacy, ConcurrencyPool: pool, ConcurrencyCap: cap, CostTier: "low"}
}

// fixtureRegistry: every vendor entry of the default chains is verified, the
// local qwen3.6:27b low is verified (5 of 5), 35b-a3b is unprobed, and the pi
// + Anthropic entries do not exist (4-1 writes them).
func fixtureRegistry() registry.Registry {
	local := ready("pi", "ollama", "ollama/qwen3.6:27b", "low", "local", "ollama-gpu", 1)
	local.CostTier = "free"
	unprobed := local
	unprobed.Model, unprobed.Verified, unprobed.State = "ollama/qwen3.6:35b-a3b", false, registry.StateUnprobed
	high := local
	high.Effort, high.Verified, high.State, high.Probation = "high", false, registry.StateProbeFailed, true
	return registry.Registry{Schema: 1, Entries: []registry.Entry{
		ready("claude", "anthropic", "haiku", "low", "vendor", "claude-subscription", 6),
		ready("claude", "anthropic", "sonnet", "medium", "vendor", "claude-subscription", 6),
		ready("claude", "anthropic", "opus", "high", "vendor", "claude-subscription", 6),
		ready("claude", "anthropic", "opus", "xhigh", "vendor", "claude-subscription", 6),
		ready("codex", "openai", "gpt-5.6-luna", "low", "vendor", "codex-chatgpt", 4),
		ready("codex", "openai", "gpt-5.6-terra", "medium", "vendor", "codex-chatgpt", 4),
		ready("codex", "openai", "gpt-5.6-sol", "high", "vendor", "codex-chatgpt", 4),
		ready("codex", "openai", "gpt-5.6-sol", "xhigh", "vendor", "codex-chatgpt", 4),
		local, unprobed, high,
	}}
}

type fixture struct {
	t     *testing.T
	tbl   Table
	reg   registry.Registry
	g     grants.Grants
	list  []task.Task
	input int
}

func newFixture(t *testing.T) *fixture {
	return &fixture{t: t, tbl: Default(), reg: fixtureRegistry(), g: grants.Defaults()}
}

func (f *fixture) apply(text string) *fixture {
	tbl, err := Apply(f.tbl, text, "routing.yaml", true)
	if err != nil {
		f.t.Fatal(err)
	}
	f.tbl = tbl
	return f
}

func (f *fixture) router() *Router {
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "fixture", Now: func() time.Time { return t0 },
		Tasks:       func() ([]task.Task, error) { return f.list, nil },
		InputTokens: func(task.Task) int { return f.input }})
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fixture) decide(tk task.Task) Decision {
	d, route := f.router().Decide(tk, f.list)
	if route.Pool != nil && d.Entry == nil {
		f.t.Fatalf("route %+v without an entry", route)
	}
	return d
}

func newTask(id, class string, files ...string) task.Task {
	return task.Task{Schema: 1, ID: id, Title: id, Class: class, State: task.StatePending, DeclaredFiles: files, CreatedAt: t0}
}

func pl(s string) *task.Pool {
	p, err := task.ParsePool(s)
	if err != nil {
		panic(err)
	}
	return &p
}

func failed(pool string, n int) task.Attempt {
	return task.Attempt{N: n, Holder: "s", Pool: pl(pool), GrantedAt: t0, EndedAt: t0.Add(time.Minute), Outcome: task.OutcomeFailed}
}

// idsWithPolicy finds a task id whose exploration draw gives the policy.
func idWithPolicy(seed string, share float64, explore bool) string {
	for i := 0; ; i++ {
		id := "t" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)
		if (Draw(seed, id) < share) == explore {
			return id
		}
	}
}

// Acceptance 1: a table test per class. With every draw an exploration
// (share 1), a class with a mechanical verifier routes one tier down, and
// summarize, plan and review never route below their tier.
func TestAcceptance1ClassTable(t *testing.T) {
	cases := []struct {
		class, override, wantTier, wantPolicy string
	}{
		{"extract", "", TierTask, PolicyExplore},       // smol has no tier below: one tier away is up
		{"extract", "task", TierSmol, PolicyExplore},   // schema verifier: routes down
		{"script", "", TierTask, PolicyExplore},        // exit code verifier at the bottom tier
		{"script", "task", TierSmol, PolicyExplore},    // script routes down
		{"implement", "", TierSmol, PolicyExplore},     // tests verifier: routes down
		{"summarize", "", TierTask, PolicyExplore},     // no verifier: never below smol, explores up
		{"summarize", "task", TierPlan, PolicyExplore}, // never below its tier: up, not down
		{"plan", "", TierPlan, PolicyTable},            // never explored, never down
		{"review", "", TierAdvisor, PolicyTable},       // never explored, never down
	}
	for _, c := range cases {
		f := newFixture(t).apply("schema: 1\nexploration_share: 1\n")
		if c.override != "" {
			f.apply("schema: 1\nclasses:\n  " + c.class + ":\n    tier: " + c.override + "\n")
		}
		d := f.decide(newTask("t1", c.class))
		if d.UsedTier != c.wantTier || d.Policy != c.wantPolicy {
			t.Errorf("%s (tier %q): used %s policy %s, want %s %s", c.class, c.override, d.UsedTier, d.Policy, c.wantTier, c.wantPolicy)
		}
		if TierRank(d.UsedTier) < TierRank(d.TableTier) && !Default().MayRouteDown(c.class) {
			t.Errorf("%s routed below its tier: %s < %s", c.class, d.UsedTier, d.TableTier)
		}
	}
	// With exploration off, every class runs on its table tier.
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	for class, want := range map[string]string{"extract": TierSmol, "summarize": TierSmol, "script": TierSmol,
		"implement": TierTask, "plan": TierPlan, "review": TierAdvisor} {
		if d := f.decide(newTask("t1", class)); d.UsedTier != want || d.Policy != PolicyTable || d.Propensity != 1 {
			t.Errorf("%s: %s %s %v", class, d.UsedTier, d.Policy, d.Propensity)
		}
	}
}

// Acceptance 1: a review of a Claude implementation picks a non-Anthropic
// family, and a review of an OpenAI implementation picks Claude.
func TestAcceptance1ReviewerFamilyDiffers(t *testing.T) {
	for impl, want := range map[string]string{
		"claude:sonnet:medium":       "codex:gpt-5.6-sol:xhigh",
		"codex:gpt-5.6-terra:medium": "claude:opus:xhigh",
	} {
		f := newFixture(t)
		done := newTask("t1", "implement", "x.go")
		done.State, done.Attempts = task.StateCompleted, []task.Attempt{{N: 1, Pool: pl(impl), Outcome: task.OutcomeCompleted}}
		f.list = []task.Task{done}
		rv := newTask("t2", "review")
		rv.DependsOn = []string{"t1"}
		d := f.decide(rv)
		if d.Entry == nil || d.Entry.Key() != want || Family(*d.Entry) == Family(*pl(impl)) {
			t.Fatalf("review of %s: %+v", impl, d)
		}
		if d.Features.ImplementerFamily != Family(*pl(impl)) {
			t.Fatalf("implementer family = %q", d.Features.ImplementerFamily)
		}
	}
	// Only the implementer's family passes: the review waits, never same-family.
	f := newFixture(t)
	f.reg.Entries = slices.DeleteFunc(f.reg.Entries, func(e registry.Entry) bool { return e.Runtime == "codex" })
	done := newTask("t1", "implement", "x.go")
	done.State, done.Attempts = task.StateCompleted, []task.Attempt{{N: 1, Pool: pl("claude:sonnet:medium"), Outcome: task.OutcomeCompleted}}
	f.list = []task.Task{done}
	rv := newTask("t2", "review")
	rv.DependsOn = []string{"t1"}
	_, route := f.router().Decide(rv, f.list)
	if route.Pool != nil || route.Wait != task.WaitReviewerFamily {
		t.Fatalf("same-family-only review: %+v", route)
	}
}

// Acceptance 1 (6.1 implement rule): more than three files, a sensitive path
// or no tests escalate implement to plan; plan never routes down.
func TestImplementEscalatesToPlan(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\nsensitive_paths: [internal/auth/]\n")
	if d := f.decide(newTask("t1", "implement", "a", "b", "c")); d.TableTier != TierTask {
		t.Fatalf("3 files: %s", d.TableTier)
	}
	if d := f.decide(newTask("t1", "implement", "a", "b", "c", "d")); d.TableTier != TierPlan || d.Entry.Key() != "claude:opus:high" {
		t.Fatalf("4 files: %+v", d)
	}
	if d := f.decide(newTask("t1", "implement", "internal/auth/token.go")); d.TableTier != TierPlan || !d.Features.Sensitive {
		t.Fatalf("sensitive: %+v", d)
	}
	g := newFixture(t).apply("schema: 1\nno_tests: true\nexploration_share: 1\n")
	if d := g.decide(newTask("t1", "implement", "a")); d.TableTier != TierPlan || d.UsedTier != TierPlan {
		t.Fatalf("no tests, explored: %+v", d) // an escalated implement is not explored down
	}
}

// Acceptance 2: a task touching a local-only path never selects a vendor
// entry, even when no local entry passes.
func TestAcceptance2LocalOnlyPathNeverGoesToAVendor(t *testing.T) {
	const repo = "schema: 1\nexploration_share: 0\nprivacy_paths:\n  - glob: internal/secrets/\n    class: local\n"
	// No local entry on the task tier: the task waits on privacy_class.
	f := newFixture(t).apply(repo)
	tk := newTask("t1", "implement", "internal/secrets/vault.go")
	d, route := f.router().Decide(tk, nil)
	if route.Pool != nil || route.Wait != task.WaitPrivacyClass {
		t.Fatalf("route = %+v", route)
	}
	for _, s := range d.Skipped {
		if pool := pl(s.Entry); Family(*pool) != "ollama" && s.Reason != SkipPrivacyClass {
			t.Errorf("vendor entry %s skipped for %s, want privacy_class", s.Entry, s.Reason)
		}
	}
	// The verified local smol entry is taken; when it is at its cap, the
	// task waits and still no vendor entry is taken.
	smol := newTask("t2", "script", "internal/secrets/rotate.sh")
	if d := f.decide(smol); d.Entry == nil || d.Entry.Key() != "pi:ollama/qwen3.6:27b:low" {
		t.Fatalf("local smol: %+v", d)
	}
	busy := newTask("t3", "script")
	busy.State, busy.Pool = task.StateInProgress, pl("pi:ollama/qwen3.6:27b:low")
	f.list = []task.Task{busy}
	_, route = f.router().Decide(smol, f.list)
	if route.Pool != nil || route.Wait != task.WaitConcurrencyCap {
		t.Fatalf("local at cap: %+v", route)
	}
	// A glob that is not under the rule stays free to use vendors.
	if d := f.decide(newTask("t4", "implement", "cmd/main.go")); d.Entry == nil || d.Entry.Key() != "claude:sonnet:medium" {
		t.Fatalf("plain path: %+v", d)
	}
	// A local grant makes every task local-only.
	g := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	g.g.PrivacyClass = grants.PrivacyLocal
	if _, route := g.router().Decide(newTask("t5", "plan"), nil); route.Pool != nil || route.Wait != task.WaitPrivacyClass {
		t.Fatalf("local grant: %+v", route)
	}
}

// Acceptance 3: 40k tokens skips Ollama, 120k skips Haiku, a retry moves one
// tier up and the skipped entry list grows.
func TestAcceptance3Escalation(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	f.input = 10_000
	if d := f.decide(newTask("t1", "extract")); d.Entry.Key() != "pi:ollama/qwen3.6:27b:low" {
		t.Fatalf("10k: %s", d.Entry.Key())
	}
	f.input = 40_000
	d := f.decide(newTask("t1", "extract"))
	if d.Entry.Key() != "claude:haiku:low" || !hasSkip(d, "pi:ollama/qwen3.6:27b:low", SkipInputOver32k) {
		t.Fatalf("40k: %+v", d)
	}
	f.input = 120_000
	d = f.decide(newTask("t1", "extract"))
	if d.Entry.Key() != "codex:gpt-5.6-luna:low" || !hasSkip(d, "claude:haiku:low", SkipInputOver100k) || !hasSkip(d, "pi:ollama/qwen3.6:27b:low", SkipInputOver32k) {
		t.Fatalf("120k: %+v", d)
	}
	// Retries: one tier up per failed attempt, never the same entry again;
	// each retry starts from the previous attempt's recorded skips.
	f.input = 1_000
	rn := f.runner()
	tk := newTask("t1", "extract")
	first := rn.attempt(&tk, task.OutcomeFailed)
	second := rn.attempt(&tk, task.OutcomeFailed)
	if second.UsedTier != TierTask || second.Entry.Key() != "claude:sonnet:medium" || len(second.Skipped) <= len(first.Skipped) ||
		!hasSkip(second, first.Entry.Key(), SkipUsedBefore) {
		t.Fatalf("retry 1: first %+v second %+v", first, second)
	}
	third, _ := rn.r.Decide(tk, rn.cur)
	if third.UsedTier != TierPlan || len(third.Skipped) <= len(second.Skipped) || !hasSkip(third, second.Entry.Key(), SkipUsedBefore) {
		t.Fatalf("retry 2: %+v", third)
	}
	// On the top tier a retry takes the next entry, never the failed one.
	p := newTask("t2", "plan")
	p.Attempt, p.Attempts = 1, []task.Attempt{failed("claude:opus:high", 1)}
	if d := f.decide(p); d.UsedTier != TierPlan || d.Entry.Key() != "codex:gpt-5.6-sol:high" || !hasSkip(d, "claude:opus:high", SkipUsedBefore) {
		t.Fatalf("plan retry: %+v", d)
	}
	p.Attempt, p.Attempts = 2, append(p.Attempts, failed("codex:gpt-5.6-sol:high", 2))
	if _, route := f.router().Decide(p, nil); route.Block != "retries_exhausted" {
		t.Fatalf("plan chain used up: %+v", route)
	}
	// Unclaimed and released attempts are not failures: same tier, same entry.
	u := newTask("t3", "extract")
	u.Attempt, u.Attempts = 1, []task.Attempt{{N: 1, Pool: pl("pi:ollama/qwen3.6:27b:low"), Outcome: task.OutcomeUnclaimed}}
	if d := f.decide(u); d.UsedTier != TierSmol || d.Entry.Key() != "pi:ollama/qwen3.6:27b:low" {
		t.Fatalf("after unclaimed: %+v", d)
	}
}

// The tier is fixed per attempt: the same task routes the same way on every
// tick and after a restart (the draw is a function of seed and task id).
func TestTierFixedAcrossTicksAndRestarts(t *testing.T) {
	f := newFixture(t)
	id := idWithPolicy("fixture", 0.05, true)
	a := f.decide(newTask(id, "implement"))
	b := f.decide(newTask(id, "implement"))
	if a.Policy != PolicyExplore || a.UsedTier != b.UsedTier || a.Entry.Key() != b.Entry.Key() || a.Propensity != 0.05 {
		t.Fatalf("a %+v b %+v", a, b)
	}
}

func TestFourChecks(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	// verified: an unverified entry is skipped; not in the registry too.
	d := f.decide(newTask("t1", "extract"))
	if !hasSkip(d, "pi:ollama/qwen3.6:35b-a3b:low", SkipUnverified) {
		t.Fatalf("unverified: %+v", d.Skipped)
	}
	f.input = 50_000
	if d := f.decide(newTask("t1", "extract")); !hasSkip(d, "pi:anthropic/haiku:low", SkipNotInRegistry) {
		t.Fatalf("not in registry: %+v", d.Skipped)
	}
	f.input = 0
	// grant: a runtime the grant does not permit.
	f.g.PermittedRuntimes = []string{"codex"}
	if d := f.decide(newTask("t1", "implement")); d.Entry.Key() != "codex:gpt-5.6-terra:medium" || !hasSkip(d, "claude:sonnet:medium", SkipRuntimeNotPermitted) {
		t.Fatalf("permitted runtimes: %+v", d)
	}
	f.g.PermittedRuntimes = nil
	// concurrency: six Claude seats busy.
	for i := 0; i < 6; i++ {
		b := newTask("b", "implement")
		b.State, b.Pool = task.StateInProgress, pl("claude:haiku:low")
		f.list = append(f.list, b)
	}
	if d := f.decide(newTask("t1", "implement")); d.Entry.Key() != "codex:gpt-5.6-terra:medium" || !hasSkip(d, "claude:sonnet:medium", SkipConcurrencyCap) {
		t.Fatalf("concurrency: %+v", d)
	}
	f.list = nil
	// cost cap from cost_index.
	for i := range f.reg.Entries {
		if f.reg.Entries[i].Model == "opus" {
			f.reg.Entries[i].CostIndex = 5
		}
	}
	f.apply("schema: 1\ntiers:\n  plan:\n    max_cost_index: 3\n")
	if d := f.decide(newTask("t1", "plan")); d.Entry.Key() != "codex:gpt-5.6-sol:high" || !hasSkip(d, "claude:opus:high", SkipCostCap) {
		t.Fatalf("cost_index cap: %+v", d)
	}
}

// The grant's per-day budget is the stop: a spent day leaves only free
// (local) entries, and a task with none waits on cost_cap.
func TestGrantDailyBudgetStops(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	tbl, err := Apply(f.tbl, "schema: 1\nprices:\n  - entry: claude:opus:high\n    input_usd_per_mtok: 15\n    output_usd_per_mtok: 75\n"+
		"  - entry: codex:gpt-5.6-sol:high\n    input_usd_per_mtok: 10\n    output_usd_per_mtok: 40\n", "user.yaml", false)
	if err != nil {
		t.Fatal(err)
	}
	f.tbl = tbl
	f.g.CostPerDayUSD = 2
	r := f.router()
	cost := 2.5
	r.note(Row{Task: "t9", Attempt: 1, CostUSD: &cost, EndedAt: t0.Add(-time.Hour)})
	if d, route := r.Decide(newTask("t1", "plan"), nil); route.Pool != nil || route.Wait != task.WaitCostCap || !hasSkip(d, "claude:opus:high", SkipCostCap) {
		t.Fatalf("over budget: %+v", route)
	}
	if d, _ := r.Decide(newTask("t2", "extract"), nil); d.Entry == nil || d.Entry.Key() != "pi:ollama/qwen3.6:27b:low" {
		t.Fatalf("free local entry under a spent budget: %+v", d)
	}
	// An entry with no owner price is not stopped by the budget (its cost is unknown).
	if d, _ := r.Decide(newTask("t3", "implement"), nil); d.Entry == nil || d.Entry.Key() != "claude:sonnet:medium" {
		t.Fatalf("unpriced entry under a spent budget: %+v", d)
	}
	r2 := f.router()
	r2.note(Row{Task: "t9", Attempt: 1, CostUSD: &cost, EndedAt: t0.Add(-24 * time.Hour)})
	if _, route := r2.Decide(newTask("t1", "plan"), nil); route.Pool == nil {
		t.Fatalf("yesterday's spend stopped today: %+v", route)
	}
}

// A lead pin governs the first attempt but never routes plan or review down.
func TestLeadPin(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	tk := newTask("t1", "implement")
	tk.Pool = pl("codex:gpt-5.6-terra:medium")
	if d := f.decide(tk); d.Policy != PolicyLeadPin || d.Entry.Key() != "codex:gpt-5.6-terra:medium" {
		t.Fatalf("pin: %+v", d)
	}
	// A pin must resolve to a tier the class may run on (Codex pass 1, P1-1).
	for _, c := range []struct{ class, pin, why string }{
		{"plan", "claude:haiku:low", "tiers the class may not run on"},         // below plan
		{"plan", "claude:sonnet:high", "in no chain"},                          // in no chain of the table
		{"review", "codex:gpt-5.6-sol:high", "tiers the class may not run on"}, // plan tier, not advisor
	} {
		p := newTask("t2", c.class)
		p.Pool = pl(c.pin)
		if _, route := f.router().Decide(p, nil); route.Block != BlockPinUnresolved || route.Pool != nil || !strings.Contains(route.Detail, c.why) {
			t.Fatalf("%s pinned to %s: %+v", c.class, c.pin, route)
		}
	}
	// A review pinned to an advisor entry runs there.
	rv := newTask("t4", "review")
	rv.Pool = pl("codex:gpt-5.6-sol:xhigh")
	if d := f.decide(rv); d.Policy != PolicyLeadPin || d.UsedTier != TierAdvisor || d.Entry.Key() != "codex:gpt-5.6-sol:xhigh" {
		t.Fatalf("review pinned to advisor: %+v", d)
	}
	// A pin is still checked: a local-only task pinned to a vendor waits.
	g := newFixture(t).apply("schema: 1\nprivacy_paths:\n  - glob: s/\n    class: local\n")
	s := newTask("t3", "implement", "s/x.go")
	s.Pool = pl("claude:sonnet:medium")
	if _, route := g.router().Decide(s, nil); route.Pool != nil || route.Wait != task.WaitPrivacyClass {
		t.Fatalf("pinned vendor on a local path: %+v", route)
	}
}

// A lead pin fixes the starting tier: retries move one tier up from the
// pinned tier and never below it (ruling 4; Codex pass 1, limit a).
func TestLeadPinFixesTheStartingTier(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	cases := []struct {
		pin, firstTier, retryTier, retryEntry string
	}{
		{"claude:opus:high", TierPlan, TierPlan, "codex:gpt-5.6-sol:high"}, // implement pinned up to plan stays on plan
		{"claude:sonnet:medium", TierTask, TierPlan, "claude:opus:high"},   // from the pin's tier, one up
		{"claude:haiku:low", TierSmol, TierTask, "claude:sonnet:medium"},   // a mechanical class may be pinned down
	}
	for _, c := range cases {
		rn := f.runner()
		tk := newTask("t1", "implement")
		tk.Pool = pl(c.pin)
		first := rn.attempt(&tk, task.OutcomeFailed)
		if first.Policy != PolicyLeadPin || first.UsedTier != c.firstTier || first.Entry.Key() != c.pin {
			t.Fatalf("pin %s first: %+v", c.pin, first)
		}
		retry, _ := rn.r.Decide(tk, rn.cur)
		if retry.UsedTier != c.retryTier || retry.Entry == nil || retry.Entry.Key() != c.retryEntry || retry.Policy != PolicyLeadPin {
			t.Fatalf("pin %s retry: %+v", c.pin, retry)
		}
	}
	// An unclaimed pinned attempt retries the pin itself.
	rn := f.runner()
	tk := newTask("t1", "implement")
	tk.Pool = pl("codex:gpt-5.6-terra:medium")
	rn.attempt(&tk, task.OutcomeUnclaimed)
	if d, _ := rn.r.Decide(tk, rn.cur); d.Entry == nil || d.Entry.Key() != "codex:gpt-5.6-terra:medium" {
		t.Fatalf("pinned retry after unclaimed: %+v", d)
	}
}

// A retry reads the previous attempt's recorded skips, not the conditions
// of now: a cap that has freed since does not erase the skip (Codex pass 1,
// P2-7).
func TestRetryKeepsTheRecordedSkips(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\n")
	rn := f.runner()
	busy := newTask("b1", "script")
	busy.State, busy.Pool = task.StateInProgress, pl("pi:ollama/qwen3.6:27b:low")
	tk := newTask("t1", "extract")
	first := rn.attempt(&tk, task.OutcomeFailed, busy)
	if !hasSkip(first, "pi:ollama/qwen3.6:27b:low", SkipConcurrencyCap) || first.Entry.Key() != "claude:haiku:low" {
		t.Fatalf("first: %+v", first)
	}
	retry, _ := rn.r.Decide(tk, []task.Task{tk}) // the local slot is free now
	if !hasSkip(retry, "pi:ollama/qwen3.6:27b:low", SkipConcurrencyCap) || !hasSkip(retry, "claude:haiku:low", SkipUsedBefore) {
		t.Fatalf("retry lost the recorded skip: %+v", retry.Skipped)
	}
}

// runner routes a task through Route and Flush, so retries read the rows.
type runner struct {
	t    *testing.T
	r    *Router
	cur  []task.Task
	rows []Row
}

func (f *fixture) runner() *runner {
	rn := &runner{t: f.t}
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Seed: "fixture", Now: func() time.Time { return t0 },
		Tasks:       func() ([]task.Task, error) { return rn.cur, nil },
		InputTokens: func(task.Task) int { return f.input },
		OnRow:       func(row Row) { rn.rows = append(rn.rows, row) }})
	if err != nil {
		f.t.Fatal(err)
	}
	rn.r = r
	return rn
}

// attempt routes tk once, ends the attempt with outcome and flushes its row;
// it returns the decision the attempt ran on.
func (rn *runner) attempt(tk *task.Task, outcome string, others ...task.Task) Decision {
	rn.t.Helper()
	rn.cur = append([]task.Task{*tk}, others...)
	route := rn.r.Route(*tk)
	if route.Pool == nil {
		rn.t.Fatalf("%s attempt %d: no route %+v", tk.ID, tk.Attempt+1, route)
	}
	d := *rn.r.decisions[key(tk.ID, tk.Attempt+1)]
	tk.Attempt++
	tk.Attempts = append(tk.Attempts, task.Attempt{N: tk.Attempt, Holder: "s", Pool: route.Pool, GrantedAt: t0, EndedAt: t0.Add(time.Minute), Outcome: outcome})
	rn.cur = []task.Task{*tk}
	if err := rn.r.Flush(); err != nil {
		rn.t.Fatal(err)
	}
	return d
}

func TestRetriesExhaustedAfterMaxAttempts(t *testing.T) {
	f := newFixture(t)
	tk := newTask("t1", "extract")
	tk.Attempt = 3
	tk.Attempts = []task.Attempt{failed("claude:haiku:low", 1), failed("claude:sonnet:medium", 2), failed("claude:opus:high", 3)}
	if _, route := f.router().Decide(tk, nil); route.Block != "retries_exhausted" {
		t.Fatalf("route = %+v", route)
	}
}

// A probation entry is never first: the first attempt skips it, a later
// attempt may take it.
func TestProbationNeverFirst(t *testing.T) {
	f := newFixture(t).apply("schema: 1\nexploration_share: 0\ntiers:\n  task:\n    chain: [\"pi:ollama/qwen3.6:27b:high\"]\n")
	for i := range f.reg.Entries {
		if e := &f.reg.Entries[i]; e.Model == "ollama/qwen3.6:27b" && e.Effort == "high" {
			e.Verified, e.State = true, registry.StateReady
		}
	}
	tk := newTask("t1", "implement")
	d, route := f.router().Decide(tk, nil)
	if route.Pool != nil || !hasSkip(d, "pi:ollama/qwen3.6:27b:high", SkipProbation) {
		t.Fatalf("first attempt: %+v %+v", d, route)
	}
	tk.Attempt, tk.Attempts = 1, []task.Attempt{{N: 1, Pool: pl("pi:ollama/qwen3.6:27b:high"), Outcome: task.OutcomeUnclaimed}} // a lead dispatch to the entry; the router may take it from attempt 2
	if d := f.decide(tk); d.Entry == nil || d.Entry.Key() != "pi:ollama/qwen3.6:27b:high" {
		t.Fatalf("second attempt: %+v", d)
	}
}

func hasSkip(d Decision, entry, reason string) bool {
	return slices.Contains(d.Skipped, Skip{Entry: entry, Reason: reason})
}
