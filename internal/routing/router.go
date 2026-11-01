package routing

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
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
	"github.com/omriariav/amq-squad/v3/internal/status"
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

// Usage is one attempt's evidence: nil fields are unknown. Invalid says why
// the usage is unknown when a counter was not a count (negative, fractional,
// out of range): the row reports it.
type Usage struct {
	TokensIn, TokensOut *int64
	CostUSD             *float64
	Invalid             string
}

// checked is the usage a row may carry (phase 5 re-pass, P2-4): a negative
// or out-of-range token count, or a cost that is negative or not finite, is
// no evidence, so unknown usage, and the reason is kept.
func (u Usage) checked() Usage {
	bad := func(n *int64) bool { return n != nil && (*n < 0 || *n > status.MaxTokenCount) }
	switch {
	case u.Invalid != "":
	case bad(u.TokensIn) || bad(u.TokensOut):
		u.Invalid = "a token count is negative or out of range"
	case u.CostUSD != nil && !validCost(*u.CostUSD):
		u.Invalid = "the cost is negative or not finite"
	default:
		return u
	}
	return Usage{Invalid: u.Invalid}
}

func validCost(c float64) bool { return c >= 0 && !math.IsInf(c, 0) && !math.IsNaN(c) }

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
	// Seats is the scheduler's seat view (Scheduler.Occupancy), what a
	// concurrency cap counts (phase 5 re-pass, ruling 2): every live seat,
	// pending seat request and closing seat, each once. nil: the holders of
	// the leased tasks stand in (tests, routing explain).
	Seats func() []scheduler.SeatView
	// LeadPools are the pools of the lead's own seat: it occupies a
	// concurrency pool it shares (a lead on the Ollama GPU), once.
	LeadPools []task.Pool
	// RosterPools are the pools of the roster's seats (brief 31-1): each is
	// admitted without a registry entry, and one the table places on no
	// tier is admitted at the class's own tier (a pin to it, or a candidate
	// after the tier's chain). The registry stays optional cap and cost
	// metadata for them. A relaunch that reloads squad.yaml replaces them
	// through SetRosterPools (brief 31-2 PR 2); every read is under the
	// router's lock.
	RosterPools []task.Pool
}

// SetRosterPools replaces the roster pools under the router's lock (brief
// 31-2 PR 2): a relaunch that reloads squad.yaml swaps them at the drain's
// end with the roster and the slots' squad, so a seat added to squad.yaml
// has its own pool admitted from that point, and not before.
func (r *Router) SetRosterPools(pools []task.Pool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.o.RosterPools = slices.Clone(pools)
}

// RosterPools are the roster pools the router admits now (a copy).
func (r *Router) RosterPools() []task.Pool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.o.RosterPools)
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
	// Seq and Prev chain the decision log like the outcome log (chain.go).
	Seq  int    `json:"seq"`
	Prev string `json:"prev"`
	Tag  string `json:"tag,omitempty"` // HMAC under the session seed, as on outcome rows
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
	o         Options
	mu        sync.Mutex
	decisions map[string]*Decision // task/attempt
	logged    map[string]bool      // task/attempt with an outcome row
	smolDone  map[string]string    // task -> "task/attempt" of its completed attempt when it ran on smol
	spent     map[string]float64   // UTC day -> known cost
	hist      map[string]record    // task/attempt -> what its outcome row recorded
	key       []byte               // HMAC key of rows and decisions: the session seed
	// unverified are the classes the start found in the squad's verifier gate
	// with no command in the owner grant (v3-5-verifier rule 4): a task of
	// one is refused dispatch, grants_invalid, never routed to a seat.
	unverified []string
	// The chains (chain.go), the head record (head.go) and the breaks found
	// so far; problems persist until acknowledged.
	head     chainHead // the outcome log
	decHead  chainHead // the decision log
	headRec  headRecord
	seedDev  uint64
	seedIno  uint64
	problems []Problem
	tornTail int  // bytes after the outcome log's last newline at the last read
	loaded   bool // the log was read at least once (a later read can find rows gone)
	// The history_tampered hold (tamper.go): when each task was held and for
	// which breaks, when it was seen blocked, what was acknowledged, and the
	// breaks already seen.
	tamperBlockedAt map[string]time.Time
	holdProblems    map[string][]Problem
	observedAt      map[string]time.Time
	acked           map[string]bool
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
		if TierRank(tier) >= TierRank(tableTier) || (r.mayRouteDown(class) && !escalated) {
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

// mayRouteDown: a class runs below its tier only when its verifier is
// mechanical and its command is configured in the grant (phase 5 re-pass,
// ruling on ruling 1): a verifier nobody runs is no verifier, so the class
// routes at its tier and never below.
func (r *Router) mayRouteDown(class string) bool {
	return r.o.Table.MayRouteDown(class) && r.verifierConfigured(class)
}

// verifierConfigured: the grant names a verifier command for the class.
func (r *Router) verifierConfigured(class string) bool { return r.o.Grants.VerifierFor(class) != "" }

// pinTier resolves a lead pin to the lowest tier the class may run on whose
// chain lists the pinned entry; "" and the reason when there is none.
func (r *Router) pinTier(class, tableTier string, escalated bool, pin task.Pool) (string, string) {
	if r.rosterOffTable(pin) {
		return tableTier, ""
	}
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
	// A rooted router checks its history on every pass, which needs the
	// task store (Codex pass 5, (d)): without Tasks it would skip every check.
	if o.Root != "" && o.Tasks == nil {
		return nil, fmt.Errorf("routing: a router with a session root needs Options.Tasks (task.Snapshot of the store)")
	}
	var seedDev, seedIno uint64
	if o.Seed == "" {
		// A random session seed from crypto/rand, persisted under the AMQ
		// root, so the draw is not predictable from the root and the task id
		// but stays the same across a restart (Codex pass 1, limit b).
		seed, dev, ino, err := SessionSeedID(o.Root)
		if err != nil {
			return nil, err
		}
		o.Seed, seedDev, seedIno = seed, dev, ino
	}
	if o.Usage == nil && o.Root != "" {
		o.Usage = LedgerUsage(o.Root, o.Table.Prices)
	}
	r := &Router{o: o, decisions: map[string]*Decision{}, logged: map[string]bool{}, smolDone: map[string]string{},
		spent: map[string]float64{}, hist: map[string]record{}, key: SeedKey(o.Seed), seedDev: seedDev, seedIno: seedIno,
		tamperBlockedAt: map[string]time.Time{}, holdProblems: map[string][]Problem{}, observedAt: map[string]time.Time{},
		acked: map[string]bool{}, knownProblems: map[string]bool{}}
	// The verifier gate, a start check beside the head's: a gated class with
	// no command never reaches a seat.
	r.unverified = o.Grants.UnconfiguredGate()
	if o.Root == "" {
		return r, nil
	}
	// The head record first (Codex pass 5, (a), (e)): how far each chain
	// must reach, and which seed file signs them.
	h, found, err := checkHead(o.Root, r.key, seedDev, seedIno)
	if err != nil {
		return nil, err
	}
	r.headRec = h
	r.problems = append(r.problems, h.Open...) // open breaks persist across restarts (new 2)
	if err := r.reload(); err != nil {
		return nil, err
	}
	lines, decProblems, decHead, decTail, err := verifyDecisions(DecisionPath(o.Root), r.key)
	if err != nil {
		return nil, err
	}
	if decTail > 0 {
		decProblems = append(decProblems, Problem{ID: fmt.Sprintf("decision_torn_tail:%d", decTail), Log: "decision",
			What: fmt.Sprintf("the decision log ends in %d bytes after its last newline", decTail)})
	}
	decProblems = append(decProblems, shortfall("decision", decHead, h.DecisionSeq, h.DecisionTag)...)
	if decHead.Seq < h.DecisionSeq {
		decHead = chainHead{Seq: h.DecisionSeq, Tag: h.DecisionTag}
	}
	r.decHead = decHead
	r.problems = mergeProblems(r.problems, decProblems)
	for _, l := range lines {
		if l.tamper != nil {
			r.applyTamper(*l.tamper)
			continue
		}
		r.decisions[key(l.decision.Task, l.decision.Attempt)] = l.decision
	}
	if !found {
		// A fresh session: the head starts here.
		if err := r.saveHead(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Tampered names the open breaks in the history (history_tampered): rows,
// decisions or lines that were removed, replaced, reordered, duplicated or
// do not verify, until a clearance acknowledges them, and a torn tail of the
// outcome log with its byte length. The scheduler process surfaces them to
// the lead; the weekly report counts them.
func (r *Router) Tampered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, p := range r.openProblems() {
		out = append(out, p.ID)
	}
	if r.tornTail > 0 {
		out = append(out, fmt.Sprintf("torn_tail:%d", r.tornTail))
	}
	return out
}

// Problems are the open breaks with what is known of each.
func (r *Router) Problems() []Problem {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.openProblems()
}

func (r *Router) note(row Row) {
	r.logged[key(row.Task, row.Attempt)] = true
	r.hist[key(row.Task, row.Attempt)] = record{UsedTier: row.UsedTier, Policy: row.Policy, Propensity: row.Propensity, Skipped: slices.Clone(row.Skipped)}
	if row.Outcome == task.OutcomeCompleted && row.UsedTier == TierSmol {
		r.smolDone[row.Task] = key(row.Task, row.Attempt)
	}
	if row.CostUSD != nil && validCost(*row.CostUSD) { // a cost never subtracts from the day's spend
		r.spent[row.EndedAt.UTC().Format(time.DateOnly)] += *row.CostUSD
	}
}

// failedOutcome is the weekly report's failed row: any attempt that ended
// failed, expired, refused by the commit step or blocked. The router's own
// count of failed attempts is failure (refusal.go), which classifies the
// refusal's code.
func failedOutcome(outcome string) bool {
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
	if _, err := r.durable(list, now); err != nil {
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
	if slices.Contains(r.unverified, t.Class) {
		return nil, scheduler.Route{Block: task.ReasonGrantsInvalid, Detail: (&task.VerifierUnconfiguredError{Class: t.Class}).Error()}
	}
	failures := 0
	used := map[task.Pool]bool{}
	for _, a := range t.Attempts {
		if failure(a) {
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
		if base == "" && r.rosterOffTable(entry) {
			base = spec.Tier
		}
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
	chain := slices.Clone(tbl.Tiers[tier].Chain)
	for _, p := range r.o.RosterPools {
		if r.rosterOffTable(p) && !slices.Contains(chain, p) {
			chain = append(chain, p) // a roster seat's own pool the table does not place
		}
	}
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
		if failure(a) && a.Pool != nil && !slices.Contains(chain, *a.Pool) {
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
	if slices.Contains(r.o.RosterPools, e) {
		// a roster seat's own pool needs no registry entry and no
		// verification (brief 31-1); an entry, when present, still caps it
		if !inReg {
			return ""
		}
	} else {
		if !inReg {
			return SkipNotInRegistry
		}
		if !reg.Verified {
			return SkipUnverified
		}
		if reg.Probation && n == 1 {
			return SkipProbation
		}
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

// inUse counts the seats that occupy the entry's concurrency pool (phase 5
// re-pass, ruling 2): the scheduler's seat view, each seat once (busy,
// pending launch, closing: a seat whose task expired holds the GPU until
// its process is gone), the holder of every leased task the view does not
// name, and the lead's own seat when it shares the pool. Tasks are never
// what is counted: one seat is one occupant. An idle seat of the entry's
// own pool is capacity for the pending task, which the scheduler dispatches
// to it, not a consumer of the cap (narrow pass, R2 (a)); an idle seat of
// another pool in the same concurrency pool still holds its share.
func (r *Router) inUse(reg registry.Entry, list []task.Task) int {
	own := task.Pool{Runtime: reg.Runtime, Model: reg.Model, Effort: reg.Effort}
	shares := func(p task.Pool) bool {
		if p == own {
			return true
		}
		other, ok := r.concurrencyEntry(p)
		return ok && reg.ConcurrencyPool != "" && other.ConcurrencyPool == reg.ConcurrencyPool
	}
	seen := map[string]bool{}
	count := func(occupant string, p task.Pool) {
		if !seen[occupant] && shares(p) {
			seen[occupant] = true
		}
	}
	if r.o.Seats != nil {
		for _, s := range r.o.Seats() {
			idle := s.Closing == "" && s.RequestID == "" && task.HeldBy(s.Handle, list) == nil
			if idle && s.Pool == own {
				continue
			}
			count("seat:"+s.Handle, s.Pool)
		}
	}
	for _, t := range list {
		if t.State != task.StateInProgress || t.Pool == nil {
			continue
		}
		holder := "task:" + t.ID
		if t.Lease != nil {
			holder = "seat:" + t.Lease.Holder
		}
		count(holder, *t.Pool)
	}
	for _, p := range r.o.LeadPools {
		count("lead", p)
	}
	return len(seen)
}

// concurrencyEntry is the registry entry that places a pool in a
// concurrency pool: the exact entry, else one of the same runtime and model
// (a lead whose effort the registry does not list).
func (r *Router) concurrencyEntry(p task.Pool) (registry.Entry, bool) {
	if e, ok := r.o.Registry.Lookup(p.Runtime, p.Model, p.Effort); ok {
		return e, true
	}
	for _, e := range r.o.Registry.Entries {
		if e.Runtime == p.Runtime && e.Model == p.Model {
			return e, true
		}
	}
	return registry.Entry{}, false
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
// rosterOffTable reports whether the pool is a roster seat's own pool that
// the table places on no tier (brief 31-1).
func (r *Router) rosterOffTable(p task.Pool) bool {
	return slices.Contains(r.o.RosterPools, p) && tierOf(r.o.Table, p) == ""
}

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
		if r.mayRouteDown(t.Class) && !escalated {
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
	holds, err := r.durable(list, r.o.Now())
	if err != nil {
		return err
	}
	if len(holds) > 0 {
		return &HoldsCreated{Problems: holds} // rows of tasks not held were still appended
	}
	return nil
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
					// The head moves with every append (Codex pass 5, (a)).
					if err := r.saveHead(); err != nil {
						return err
					}
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
		u := r.o.Usage(t, a).checked()
		row.TokensIn, row.TokensOut, row.CostUSD, row.UsageInvalid = u.TokensIn, u.TokensOut, u.CostUSD, u.Invalid
	}
	verifier := r.o.Table.Classes[t.Class].Verifier
	if !r.o.Grants.VerifierRequired(t.Class) {
		verifier = VerifierNone // not in the verifier gate: the result is none, never pass
	}
	row.VerifierResult = verifierResult(verifier, a)
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

// verifierResult is the row's verifier result. A pass is only ever the
// harness's recorded pass verdict on the attempt (phase 5 re-pass, ruling
// 1): a completion with no verdict (a store from before the gate) is
// not_run, never pass.
func verifierResult(verifier string, a task.Attempt) string {
	if !Mechanical(verifier) {
		return VerifierNoCheck
	}
	if a.Verdict != nil {
		switch a.Verdict.Result {
		case task.VerdictPass:
			return VerifierPass
		case task.VerdictInvalid:
			return VerifierInvalid // never a pass from an unverified archive
		}
		return VerifierFail
	}
	if a.Outcome == task.OutcomeBlocked && a.Detail == task.BlockVerdictMissing {
		return VerifierNotRun // the harness never ran the verifier
	}
	switch a.Outcome {
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
