package routing

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Skip reason codes. The four checks of 6.2 are verified (not_in_registry,
// unverified, runtime_not_permitted), privacy_class, concurrency_cap and
// cost_cap; the escalation and family rules add theirs.
const (
	SkipUsedBefore          = "used_before"     // a failed attempt ran on it: a retry never repeats an entry
	SkipProbation           = "probation"       // never first in a chain
	SkipInputOver32k        = "input_over_32k"  // input above 32k tokens skips Ollama
	SkipInputOver100k       = "input_over_100k" // input above 100k tokens skips Haiku
	SkipReviewerFamily      = "reviewer_family" // the reviewer family differs from the implementer family
	SkipRuntimeNotPermitted = "runtime_not_permitted"
	SkipNotInRegistry       = "not_in_registry"
	SkipUnverified          = "unverified"
	SkipPrivacyClass        = task.WaitPrivacyClass
	SkipConcurrencyCap      = task.WaitConcurrencyCap
	SkipCostCap             = task.WaitCostCap
)

// Token thresholds of the escalation rules (6.2).
const (
	OllamaTokenLimit = 32_000
	HaikuTokenLimit  = 100_000
)

// DefaultMaxAttempts matches scheduler.DefaultRouter: three failed attempts
// block the task with retries_exhausted.
const DefaultMaxAttempts = 3

// Usage is one attempt's evidence: nil fields are unknown.
type Usage struct {
	TokensIn, TokensOut *int64
	CostUSD             *float64
}

// Options configure a Router.
type Options struct {
	Table    Table
	Registry registry.Registry
	Grants   grants.Grants
	// Root is the AMQ session root: the outcome log and the decisions live
	// under it. "" keeps everything in memory (tests).
	Root string
	// Tasks is a lock-free read of the whole store (task.Snapshot(store.Dir)): concurrency
	// counts, the implementer of a review and the rows of ended attempts.
	Tasks func() ([]task.Task, error)
	// RepoRoot is the repository the path rules resolve against when a task
	// carries no project_path.
	RepoRoot string
	// Seed makes the exploration draw reproducible (tests). Default: the
	// session seed, random and persisted under Root (SessionSeed).
	Seed string
	// InputTokens estimates a task's input (default EstimateInputTokens).
	InputTokens func(task.Task) int
	// Usage is an ended attempt's tokens and cost (default: unknown).
	Usage       func(task.Task, task.Attempt) Usage
	Now         func() time.Time
	MaxAttempts int
	// OnRow observes every outcome row after it is written (nil: none).
	OnRow func(Row)
}

// Decision is the router's choice for one attempt of one task.
type Decision struct {
	Task             string     `json:"task"`
	Attempt          int        `json:"attempt"`
	Class            string     `json:"class"`
	Features         Features   `json:"features"`
	TableTier        string     `json:"table_tier"`
	UsedTier         string     `json:"used_tier"`
	Entry            *task.Pool `json:"entry"`
	Skipped          []Skip     `json:"skipped"`
	Policy           string     `json:"policy"`
	Propensity       float64    `json:"propensity"`
	ExplorationShare float64    `json:"exploration_share"`
	WaitReasons      []string   `json:"wait_reasons"`
	At               time.Time  `json:"at"`
	Tag              string     `json:"tag,omitempty"` // HMAC under the session seed, as on outcome rows
}

func (d Decision) sign(key []byte) Decision {
	d.Tag = ""
	d.Tag = hmacTag(key, "amq-squad routing decision v1", d)
	return d
}

func (d Decision) verify(key []byte) bool {
	return d.Tag != "" && d.sign(key).Tag == d.Tag && slices.Contains(Policies, d.Policy)
}

// Router is the scheduler's router of plan 6 (scheduler.Router).
type Router struct {
	o           Options
	mu          sync.Mutex
	decisions   map[string]*Decision // task/attempt
	logged      map[string]bool      // task/attempt with an outcome row
	smolDone    map[string]string    // task -> "task/attempt" of its completed attempt when it ran on smol
	spent       map[string]float64   // UTC day -> known cost
	hist        map[string]record    // task/attempt -> what its outcome row recorded
	key         []byte               // HMAC key of rows and decisions: the session seed
	decTampered []string             // decisions whose tag failed when the router started
	// The outcome log's chain (chain.go) and its breaks at the last full read.
	head     chainHead
	problems []Problem
	loaded   bool // the log was read at least once (a later read can find rows gone)
	// The history_tampered hold (tamper.go): when each task was held, and
	// the breaks already seen.
	tamperBlockedAt map[string]time.Time
	knownProblems   map[string]bool
	logStat         logStamp    // the outcome log as last read or written
	checkedAt       time.Time   // when the log was last read in full
	pendingDec      []*Decision // decisions not yet durable, oldest first
}

// record is what an attempt's outcome row says about its routing decision.
type record struct {
	UsedTier   string
	Policy     string
	Propensity float64
	Skipped    []Skip
}

// BlockPinUnresolved blocks a task whose lead pin is no entry of a tier its
// class may run on (Codex pass 1, P1-1): a plan or review task pinned to an
// entry outside its chains would run with no mechanical verifier.
const BlockPinUnresolved = "pin_unresolved"

// firstRecord is the task's first attempt as recorded: its outcome row, else
// its persisted decision.
func (r *Router) firstRecord(id string) (record, bool) {
	if h, ok := r.hist[key(id, 1)]; ok {
		return h, true
	}
	if d := r.decisions[key(id, 1)]; d != nil && d.Entry != nil {
		return record{UsedTier: d.UsedTier, Policy: d.Policy, Propensity: d.Propensity, Skipped: d.Skipped}, true
	}
	return record{}, false
}

// recordedSkips is attempt n's recorded skipped list (row, else decision).
func (r *Router) recordedSkips(id string, n int) []Skip {
	if h, ok := r.hist[key(id, n)]; ok {
		return slices.Clone(h.Skipped)
	}
	if d := r.decisions[key(id, n)]; d != nil {
		return slices.Clone(d.Skipped)
	}
	return nil
}

// classTiers are the tiers a class may run on: review only on advisor; the
// others on their table tier and up the ladder, and below it only with a
// mechanical verifier and no implement escalation.
func (r *Router) classTiers(class, tableTier string, escalated bool) []string {
	if class == "review" {
		return []string{TierAdvisor}
	}
	var out []string
	for _, tier := range []string{TierSmol, TierTask, TierPlan} {
		if TierRank(tier) >= TierRank(tableTier) || (r.o.Table.MayRouteDown(class) && !escalated) {
			out = append(out, tier)
		}
	}
	return out
}

// entryTier is the tier of entry among allowed: the recorded tier when it is
// allowed and lists the entry, else the lowest allowed tier that lists it,
// else "".
func entryTier(t Table, allowed []string, recorded string, entry task.Pool) string {
	if slices.Contains(allowed, recorded) && slices.Contains(t.Tiers[recorded].Chain, entry) {
		return recorded
	}
	for _, tier := range allowed {
		if slices.Contains(t.Tiers[tier].Chain, entry) {
			return tier
		}
	}
	return ""
}

// pinTier resolves a lead pin to the lowest tier the class may run on whose
// chain lists the pinned entry; "" and the reason when there is none.
func (r *Router) pinTier(class, tableTier string, escalated bool, pin task.Pool) (string, string) {
	allowed := r.classTiers(class, tableTier, escalated)
	for _, tier := range allowed {
		if slices.Contains(r.o.Table.Tiers[tier].Chain, pin) {
			return tier, ""
		}
	}
	if tierOf(r.o.Table, pin) == "" {
		return "", "the entry is in no chain of the table"
	}
	return "", "the entry is only on tiers the class may not run on (" + strings.Join(allowed, ", ") + " allowed)"
}

var _ scheduler.Router = (*Router)(nil)

func key(id string, n int) string { return id + "/" + strconv.Itoa(n) }

// New builds a router. With a Root it reads back the outcome log and the
// decisions file, so a restarted scheduler neither logs an attempt twice nor
// loses a decision.
func New(o Options) (*Router, error) {
	if err := o.Table.Validate(); err != nil {
		return nil, &InvalidError{Err: err}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.InputTokens == nil {
		o.InputTokens = EstimateInputTokens
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = DefaultMaxAttempts
	}
	if o.Seed == "" {
		// A random session seed from crypto/rand, persisted under the AMQ
		// root, so the draw is not predictable from the root and the task id
		// but stays the same across a restart (Codex pass 1, limit b).
		seed, err := SessionSeed(o.Root)
		if err != nil {
			return nil, err
		}
		o.Seed = seed
	}
	if o.Usage == nil && o.Root != "" {
		o.Usage = LedgerUsage(o.Root, o.Table.Prices)
	}
	r := &Router{o: o, decisions: map[string]*Decision{}, logged: map[string]bool{}, smolDone: map[string]string{},
		spent: map[string]float64{}, hist: map[string]record{}, key: SeedKey(o.Seed),
		tamperBlockedAt: map[string]time.Time{}, knownProblems: map[string]bool{}}
	if o.Root == "" {
		return r, nil
	}
	if err := r.reload(); err != nil {
		return nil, err
	}
	err := readLines(DecisionPath(o.Root), func(line []byte) {
		if r.loadTamperLine(line) {
			return
		}
		var d Decision
		if json.Unmarshal(line, &d) != nil || d.Task == "" || d.Attempt <= 0 {
			return
		}
		if !d.verify(r.key) {
			r.decTampered = append(r.decTampered, "decision "+key(d.Task, d.Attempt))
			return
		}
		r.decisions[key(d.Task, d.Attempt)] = &d
	})
	return r, err
}

// Tampered names the outcome rows and decisions whose tag did not verify at
// the last read (history_tampered): they are treated as absent. The
// scheduler process surfaces them to the lead; the weekly report counts
// them.
func (r *Router) Tampered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, p := range r.problems {
		out = append(out, p.ID)
	}
	return append(out, r.decTampered...)
}

func (r *Router) note(row Row) {
	r.logged[key(row.Task, row.Attempt)] = true
	r.hist[key(row.Task, row.Attempt)] = record{UsedTier: row.UsedTier, Policy: row.Policy, Propensity: row.Propensity, Skipped: slices.Clone(row.Skipped)}
	if row.Outcome == task.OutcomeCompleted && row.UsedTier == TierSmol {
		r.smolDone[row.Task] = key(row.Task, row.Attempt)
	}
	if row.CostUSD != nil {
		r.spent[row.EndedAt.UTC().Format(time.DateOnly)] += *row.CostUSD
	}
}

// failure reports whether an attempt outcome counts as a failed attempt of
// the work: a retry moves one tier up. Unclaimed and released attempts are
// seat or lead events and retry on the same tier.
func failure(outcome string) bool {
	switch outcome {
	case task.OutcomeFailed, task.OutcomeExpired, task.OutcomeCommitRefused, task.OutcomeBlocked:
		return true
	}
	return false
}

// Route picks the pool for a pending task's next attempt.
func (r *Router) Route(t task.Task) scheduler.Route {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.o.Now()
	var list []task.Task
	if r.o.Tasks != nil {
		var err error
		if list, err = r.o.Tasks(); err != nil {
			return scheduler.Route{Wait: task.WaitNoRoute, Detail: "routing: read the task store: " + err.Error()}
		}
	}
	// Nothing dispatches until the log is durable (Codex pass 1 and 2,
	// P1-4): the outcome log re-verified by size and tail, every ended
	// attempt's row written, every pending decision written. The spend the
	// budget stop reads and the record a retry reads come from it.
	if err := r.durable(list, now); err != nil {
		return scheduler.Route{Wait: task.WaitOutcomeLogUnwritable, Detail: "routing: " + err.Error()}
	}
	if hold, held := r.holdTampered(t, now); held {
		return hold
	}
	d, route := r.decide(t, list, now)
	if d != nil {
		if err := r.remember(d, t, route); err != nil {
			return scheduler.Route{Wait: task.WaitOutcomeLogUnwritable, Detail: "routing: decision log: " + err.Error()}
		}
	}
	return route
}

// Decide is Route without side effects on the log: the decision it would
// take. Tests and `routing explain` use it.
func (r *Router) Decide(t task.Task, list []task.Task) (Decision, scheduler.Route) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, route := r.decide(t, list, r.o.Now())
	if d == nil {
		return Decision{}, route
	}
	return *d, route
}

func (r *Router) decide(t task.Task, list []task.Task, now time.Time) (*Decision, scheduler.Route) {
	tbl := r.o.Table
	spec, ok := tbl.Classes[t.Class]
	if !ok {
		return nil, scheduler.Route{Block: "class_unknown", Detail: fmt.Sprintf("class %q is not in the routing table", t.Class)}
	}
	failures := 0
	used := map[task.Pool]bool{}
	for _, a := range t.Attempts {
		if failure(a.Outcome) {
			failures++
			if a.Pool != nil {
				used[*a.Pool] = true
			}
		}
	}
	if failures >= r.o.MaxAttempts {
		return nil, scheduler.Route{Block: "retries_exhausted", Detail: fmt.Sprintf("%d failed attempts", failures)}
	}
	n := t.Attempt + 1
	f := r.features(t, list)
	if f.Unchecked != "" {
		return nil, scheduler.Route{Block: BlockTreeUnchecked, Detail: "routing: " + f.Unchecked}
	}
	tableTier, escalated := spec.Tier, false
	if t.Class == "implement" && (f.DeclaredFiles > ImplementFileLimit || f.Sensitive || f.NoTests) {
		tableTier, escalated = TierPlan, true
	}
	d := &Decision{Task: t.ID, Attempt: n, Class: t.Class, Features: f, TableTier: tableTier,
		ExplorationShare: tbl.ExplorationShare, At: now}
	// The starting tier is fixed once per task: the first attempt's recorded
	// decision when there is one (its policy and propensity carry over), else
	// a lead pin (the pin's own tier), else the exploration draw. A retry
	// moves one tier up from it per failed attempt and never below it; the
	// tier is fixed per attempt: nothing below switches it.
	var base string
	var pin *task.Pool
	first, haveFirst := r.firstRecord(t.ID)
	switch {
	case t.Attempt > 0 && len(t.Attempts) > 0 && t.Attempts[0].Pool != nil:
		// History is checked, never trusted (Codex passes 2 and 3, N1 and
		// ruling 3): the starting tier is the tier of the entry the first
		// attempt actually ran on, as the table places it among the tiers
		// the class may run on by its own rules (minimum tier, mechanical
		// verifier); the recorded tier string only breaks a tie between
		// tiers that list the same entry. An entry on no such tier refuses
		// the task. The policy and propensity carry over from the record
		// (manual, propensity 0, when there is none).
		entry := *t.Attempts[0].Pool
		recorded := ""
		d.Policy, d.Propensity = PolicyManual, 0
		if haveFirst {
			d.Policy, d.Propensity, recorded = first.Policy, first.Propensity, first.UsedTier
		}
		base = entryTier(tbl, r.classTiers(t.Class, spec.Tier, false), recorded, entry)
		if base == "" {
			return d, scheduler.Route{Block: BlockPinUnresolved, Detail: fmt.Sprintf("routing: the first attempt of %s ran on %s, which is on no tier class %s may run on", t.ID, entry.Key(), t.Class)}
		}
		if d.Policy == PolicyLeadPin {
			pin = &entry
		}
		// The current escalation is a floor history never lowers (Codex
		// pass 2, N2), applied to that actual tier: a task that became
		// sensitive, wider than three files or test-less starts on plan.
		if escalated && TierRank(base) < TierRank(tableTier) {
			base, pin = tableTier, nil
		}
	case t.Attempt == 0 && t.Pool != nil:
		pinTier, why := r.pinTier(t.Class, tableTier, escalated, *t.Pool)
		if pinTier == "" {
			return d, scheduler.Route{Block: BlockPinUnresolved, Detail: fmt.Sprintf("routing: pin %s for class %s: %s", t.Pool.Key(), t.Class, why)}
		}
		p := *t.Pool
		base, pin, d.Policy, d.Propensity = pinTier, &p, PolicyLeadPin, 1
	default:
		d.Policy, d.Propensity, base = r.explore(t, tableTier, escalated)
	}
	tier := base
	for i := 0; i < failures; i++ {
		tier = up(tier)
	}
	chain := tbl.Tiers[tier].Chain
	if pin != nil && failures == 0 {
		chain = []task.Pool{*pin} // the pinned entry, still checked
	}
	d.UsedTier = tier
	impl := ""
	if t.Class == "review" {
		impl = implementerFamily(t, list)
		d.Features.ImplementerFamily = impl
	}
	required := f.Privacy
	if r.o.Grants.PrivacyClass == grants.PrivacyLocal {
		required = grants.PrivacyLocal
	}
	// A retry starts from the previous attempt's recorded skipped entries
	// (its outcome row, else its persisted decision; never recomputed from
	// the conditions of now, so a cap that has since freed does not erase a
	// skip) and names the entries that already failed, so the skipped list
	// grows with every attempt even when the retry runs on another tier.
	if k := len(t.Attempts); k > 0 {
		d.Skipped = append(d.Skipped, r.recordedSkips(t.ID, t.Attempts[k-1].N)...)
	}
	for _, a := range t.Attempts {
		if failure(a.Outcome) && a.Pool != nil && !slices.Contains(chain, *a.Pool) {
			d.Skipped = addSkip(d.Skipped, Skip{Entry: a.Pool.Key(), Reason: SkipUsedBefore})
		}
	}
	allUsed := true
	var here []Skip // this chain's skips: they name the wait reason
	for _, e := range chain {
		reason := r.check(e, n, f, impl, required, used, list, tier, now)
		if reason != SkipUsedBefore {
			allUsed = false
		}
		if reason != "" {
			d.Skipped = addSkip(d.Skipped, Skip{Entry: e.Key(), Reason: reason})
			here = append(here, Skip{Entry: e.Key(), Reason: reason})
			continue
		}
		pick := e
		d.Entry = &pick
		return d, scheduler.Route{Pool: &pick, Detail: "routing: " + d.UsedTier + " " + d.Policy}
	}
	if allUsed && len(chain) > 0 {
		return d, scheduler.Route{Block: "retries_exhausted", Detail: "every entry of tier " + tier + " ran a failed attempt"}
	}
	return d, scheduler.Route{Wait: waitReason(here), Detail: "routing: " + skipsText(here)}
}

// check runs the rules on one chain entry; "" means it passes.
func (r *Router) check(e task.Pool, n int, f Features, impl, required string, used map[task.Pool]bool,
	list []task.Task, tier string, now time.Time) string {
	if used[e] {
		return SkipUsedBefore
	}
	reg, inReg := r.o.Registry.Lookup(e.Runtime, e.Model, e.Effort)
	if Family(e) == "ollama" && f.InputTokens > OllamaTokenLimit {
		return SkipInputOver32k
	}
	if strings.Contains(e.Model, "haiku") && f.InputTokens > HaikuTokenLimit {
		return SkipInputOver100k
	}
	if impl != "" && Family(e) == impl {
		return SkipReviewerFamily
	}
	// Privacy before anything else that can fail, so a vendor entry for a
	// local-only task always says privacy_class.
	privacy := grants.PrivacyVendor
	if inReg && reg.PrivacyClass != "" {
		privacy = reg.PrivacyClass
	} else if Family(e) == "ollama" {
		privacy = grants.PrivacyLocal
	}
	if required == grants.PrivacyLocal && privacy != grants.PrivacyLocal {
		return SkipPrivacyClass
	}
	if !r.o.Grants.RuntimePermitted(e.Runtime) {
		return SkipRuntimeNotPermitted
	}
	if !inReg {
		return SkipNotInRegistry
	}
	if !reg.Verified {
		return SkipUnverified
	}
	if reg.Probation && n == 1 {
		return SkipProbation
	}
	if reg.ConcurrencyCap > 0 && r.inUse(reg, list) >= reg.ConcurrencyCap {
		return SkipConcurrencyCap
	}
	if max := r.o.Table.Tiers[tier].MaxCostIndex; max > 0 && reg.CostIndex > max {
		return SkipCostCap
	}
	// The budget stop applies to an entry with an owner price; an entry with
	// none has no known cost (lead ruling: its rows carry cost_usd null).
	if _, priced := r.o.Table.Prices[e]; priced {
		if b := r.o.Grants.CostPerDayUSD; b > 0 && r.spent[now.UTC().Format(time.DateOnly)] >= b {
			return SkipCostCap
		}
	}
	return ""
}

// inUse counts the leased tasks whose pool shares the entry's concurrency pool.
func (r *Router) inUse(reg registry.Entry, list []task.Task) int {
	n := 0
	for _, t := range list {
		if t.State != task.StateInProgress || t.Pool == nil {
			continue
		}
		other, ok := r.o.Registry.Lookup(t.Pool.Runtime, t.Pool.Model, t.Pool.Effort)
		switch {
		case ok && reg.ConcurrencyPool != "" && other.ConcurrencyPool == reg.ConcurrencyPool:
			n++
		case *t.Pool == (task.Pool{Runtime: reg.Runtime, Model: reg.Model, Effort: reg.Effort}):
			n++
		}
	}
	return n
}

// addSkip records a skip; an entry skipped before keeps one row with the
// newest reason.
func addSkip(skips []Skip, s Skip) []Skip {
	for i := range skips {
		if skips[i].Entry == s.Entry {
			skips[i].Reason = s.Reason
			return skips
		}
	}
	return append(skips, s)
}

// waitReason names why no entry passed: a cap that will lift first, then the
// privacy class, then the reviewer family, else no route.
func waitReason(skips []Skip) string {
	has := func(reason string) bool {
		return slices.ContainsFunc(skips, func(s Skip) bool { return s.Reason == reason })
	}
	for _, r := range []string{SkipConcurrencyCap, SkipCostCap, SkipPrivacyClass, SkipReviewerFamily} {
		if has(r) {
			return r
		}
	}
	return task.WaitNoRoute
}

func skipsText(skips []Skip) string {
	if len(skips) == 0 {
		return "empty chain"
	}
	parts := make([]string, len(skips))
	for i, s := range skips {
		parts[i] = s.Entry + " " + s.Reason
	}
	return strings.Join(parts, "; ")
}

// tierOf is the lowest tier whose chain lists the pool, "" when none does.
func tierOf(t Table, pl task.Pool) string {
	for _, tier := range Tiers {
		if slices.Contains(t.Tiers[tier].Chain, pl) {
			return tier
		}
	}
	return ""
}

// explore draws the task's policy once: a deterministic draw on (seed, task
// id), so a restart or a second tick gives the same answer. Exploration is
// never on plan or review and moves one tier away from the table: down when
// the verifier is mechanical and no escalation rule fired, else up.
func (r *Router) explore(t task.Task, tableTier string, escalated bool) (policy string, propensity float64, tier string) {
	share := r.o.Table.ExplorationShare
	away := ""
	if t.Class != "plan" && t.Class != "review" && share > 0 {
		if r.o.Table.MayRouteDown(t.Class) && !escalated {
			away = down(tableTier)
		}
		if away == "" && up(tableTier) != tableTier {
			away = up(tableTier)
		}
	}
	if away == "" {
		return PolicyTable, 1, tableTier
	}
	if Draw(r.o.Seed, t.ID) < share {
		return PolicyExplore, share, away
	}
	return PolicyTable, 1 - share, tableTier
}

// Draw is the uniform [0, 1) exploration draw for a task.
func Draw(seed, id string) float64 {
	h := sha256.Sum256([]byte(seed + "\x00" + id))
	return float64(binary.BigEndian.Uint64(h[:8])>>11) / float64(uint64(1)<<53)
}

// implementerFamily is the family of the implement task a review depends on:
// the pool of its last attempt, else its own pool. "" when none is known.
func implementerFamily(t task.Task, list []task.Task) string {
	for i := len(t.DependsOn) - 1; i >= 0; i-- {
		for _, o := range list {
			if o.ID != t.DependsOn[i] || o.Class != "implement" {
				continue
			}
			if n := len(o.Attempts); n > 0 && o.Attempts[n-1].Pool != nil {
				return Family(*o.Attempts[n-1].Pool)
			}
			if o.Pool != nil {
				return Family(*o.Pool)
			}
		}
	}
	return ""
}

func (r *Router) features(t task.Task, list []task.Task) Features {
	tbl := r.o.Table
	// Path rules match canonical repository paths: the task's project path,
	// else the router's repository root.
	root := t.ProjectPath
	if root == "" {
		root = r.o.RepoRoot
	}
	f := Features{DeclaredFiles: len(t.DeclaredFiles), SizeBand: SizeBand(len(t.DeclaredFiles)),
		InputTokens: r.o.InputTokens(t), Sensitive: tbl.SensitiveAt(root, t.DeclaredFiles), NoTests: tbl.NoTests}
	f.Privacy, f.PrivacyGlob = tbl.RequiredPrivacyAt(root, t.DeclaredFiles)
	f.Unchecked = tbl.Unchecked(root, t.DeclaredFiles)
	return f
}

// EstimateInputTokens estimates a task's input: the title and description,
// plus the bytes of each declared file that is a plain path (no glob) under
// the task's project path, at four bytes a token.
func EstimateInputTokens(t task.Task) int {
	n := len(t.Title) + len(t.Description)
	if t.ProjectPath != "" {
		for _, f := range t.DeclaredFiles {
			if strings.ContainsAny(f, "*?[") || strings.HasSuffix(f, "/") {
				continue
			}
			if st, err := os.Stat(filepath.Join(t.ProjectPath, filepath.FromSlash(f))); err == nil && st.Mode().IsRegular() {
				n += int(st.Size())
			}
		}
	}
	return n / 4
}

// remember keeps the decision for the attempt it routes and records the wait
// reasons seen before the dispatch. The first chosen entry, and any change of
// it, is written to the decisions file.
func (r *Router) remember(d *Decision, t task.Task, route scheduler.Route) error {
	k := key(d.Task, d.Attempt)
	prev := r.decisions[k]
	if prev != nil {
		d.WaitReasons = prev.WaitReasons
		d.At = prev.At
	}
	for _, w := range []string{t.WaitReason, route.Wait} {
		if w != "" && !slices.Contains(d.WaitReasons, w) {
			d.WaitReasons = append(d.WaitReasons, w)
		}
	}
	changed := prev == nil || (d.Entry != nil && (prev.Entry == nil || *prev.Entry != *d.Entry))
	r.decisions[k] = d
	if r.o.Root != "" && d.Entry != nil && changed {
		// Queued, then written: a decision that cannot be written stays
		// pending and stops every route until it is durable.
		cp := *d
		r.pendingDec = append(r.pendingDec, &cp)
		return r.writePendingDecisions()
	}
	return nil
}

// Flush writes the outcome row of every ended attempt that has none yet.
// Route calls it on every call; the scheduler process calls it once per
// tick too, so the last attempt of a task that never routes again (it
// completed or was cancelled) is logged.
//
// It runs the same checks as Route (Codex pass 4, P1-4): the log is verified
// and its tail checked before anything is appended, so a torn tail makes
// Flush return the stop error and write nothing.
func (r *Router) Flush() error {
	if r.o.Tasks == nil {
		return nil
	}
	list, err := r.o.Tasks()
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.durable(list, r.o.Now())
}

func (r *Router) flush(list []task.Task, now time.Time) error {
	for _, t := range list {
		if r.held(t.ID) {
			continue // held for history_tampered: nothing is logged for it until the lead clears it
		}
		start := t.CreatedAt
		for _, a := range t.Attempts {
			k := key(t.ID, a.N)
			if !r.logged[k] {
				row := r.row(t, a, start, list, now)
				if r.o.Root != "" {
					// Chained: the next place and the last good row's tag,
					// inside the signed bytes (chain.go).
					row.Seq, row.Prev = r.head.Seq+1, r.head.Tag
					row = row.Sign(r.key)
					if err := AppendRow(r.o.Root, row); err != nil {
						return err // stop: the chain stays contiguous
					}
					r.head = chainHead{Seq: row.Seq, Tag: row.Tag}
					r.logStat, _ = stampOf(OutcomePath(r.o.Root))
				}
				// The decision is kept: a row written again after the log
				// lost it takes its policy from the decision, never a new one
				// (Codex pass 3, ruling 4).
				r.note(row)
				if r.o.OnRow != nil {
					r.o.OnRow(row)
				}
			}
			start = a.EndedAt
		}
	}
	return nil
}

func (r *Router) row(t task.Task, a task.Attempt, pendingSince time.Time, list []task.Task, now time.Time) Row {
	row := Row{Schema: RowSchema, At: now, Task: t.ID, Attempt: a.N, Class: t.Class, Outcome: a.Outcome,
		Holder: a.Holder, GrantedAt: a.GrantedAt, EndedAt: a.EndedAt,
		Skipped: []Skip{}, WaitReasons: []string{}, ConsumedSmolOutputs: []string{}}
	if a.Pool != nil {
		row.Entry, row.Runtime = a.Pool.Key(), a.Pool.Runtime
		if reg, ok := r.o.Registry.Lookup(a.Pool.Runtime, a.Pool.Model, a.Pool.Effort); ok {
			row.CostIndex = reg.CostIndex
		}
	}
	if d := r.decisions[key(t.ID, a.N)]; d != nil {
		row.Features, row.TableTier, row.UsedTier = d.Features, d.TableTier, d.UsedTier
		row.Skipped = append(row.Skipped, d.Skipped...)
		row.WaitReasons = append(row.WaitReasons, d.WaitReasons...)
		row.Policy, row.Propensity, row.ExplorationShare = d.Policy, d.Propensity, d.ExplorationShare
		if d.Entry == nil || a.Pool == nil || *d.Entry != *a.Pool {
			// dispatched by hand (task claim, dispatch) to another pool: not
			// the router's decision and not a lead pin (Codex pass 3, ruling 2)
			row.Policy, row.Propensity = PolicyManual, 0
			row.UsedTier = ""
			if a.Pool != nil {
				row.UsedTier = tierOf(r.o.Table, *a.Pool)
			}
		}
	} else {
		// No decision: the attempt was dispatched outside the router (a lead
		// claim or dispatch), or its decision is lost. Never lead_pin: that is
		// written only when a lead pin exists (Codex pass 3, ruling 2).
		row.Features = r.features(t, list)
		row.TableTier = r.o.Table.Classes[t.Class].Tier
		if a.Pool != nil {
			row.UsedTier = tierOf(r.o.Table, *a.Pool)
		}
		row.Policy, row.Propensity, row.ExplorationShare = PolicyManual, 0, r.o.Table.ExplorationShare
	}
	if !a.GrantedAt.IsZero() && !a.EndedAt.IsZero() {
		row.WallMS = a.EndedAt.Sub(a.GrantedAt).Milliseconds()
	}
	if !pendingSince.IsZero() && a.GrantedAt.After(pendingSince) {
		row.WaitMS = a.GrantedAt.Sub(pendingSince).Milliseconds()
	}
	if r.o.Usage != nil {
		u := r.o.Usage(t, a)
		row.TokensIn, row.TokensOut, row.CostUSD = u.TokensIn, u.TokensOut, u.CostUSD
	}
	row.VerifierResult = verifierResult(r.o.Table.Classes[t.Class].Verifier, a.Outcome)
	if t.Class == "review" && a.Outcome == task.OutcomeCompleted {
		row.ReviewFindings = findings(t.Evidence)
	}
	for _, dep := range t.DependsOn {
		if k := r.smolDone[dep]; k != "" {
			row.ConsumedSmolOutputs = append(row.ConsumedSmolOutputs, k)
		}
	}
	return row
}

func verifierResult(verifier, outcome string) string {
	if !Mechanical(verifier) {
		return VerifierNoCheck
	}
	switch outcome {
	case task.OutcomeCompleted:
		return VerifierPass
	case task.OutcomeFailed, task.OutcomeCommitRefused, task.OutcomeBlocked:
		return VerifierFail
	}
	return VerifierNotRun
}

// findings reads the review's finding count from its completion evidence:
// "findings=N" or "findings: N". nil when the evidence names none.
func findings(evidence string) *int {
	low := strings.ToLower(evidence)
	for _, sep := range []string{"findings=", "findings: ", "findings:"} {
		i := strings.Index(low, sep)
		if i < 0 {
			continue
		}
		rest := low[i+len(sep):]
		j := 0
		for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
			j++
		}
		if n, err := strconv.Atoi(rest[:j]); err == nil {
			return &n
		}
	}
	return nil
}
