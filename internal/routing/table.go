// Package routing is the routing of plan section 6 (R20 to R28): the closed
// class list with its verifiers, the tier chains with the four checks
// (verified, privacy class, concurrency cap, cost cap), the escalation rules,
// exploration, the outcome log (one row per attempt, 6.4) and the weekly
// report that proposes table changes for the owner.
//
// The table is machine-independent: a default ships in the binary,
// ~/.amq-squad/routing.yaml overrides it for the user and
// <repo root>/.amq-squad/routing.yaml for a repository, which also maps path
// globs to a required privacy class (6.3). The registry (2b-3) decides what
// is usable on this machine. Router implements scheduler.Router.
package routing

import (
	"fmt"
	"slices"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Schema is the routing.yaml schema version.
const Schema = 1

// Tiers (plan 6.2). Escalation climbs the ladder smol, task, plan; advisor is
// the review tier and has no tier above it.
const (
	TierSmol    = "smol"
	TierTask    = "task"
	TierPlan    = "plan"
	TierAdvisor = "advisor"
)

// Tiers in rank order.
var Tiers = []string{TierSmol, TierTask, TierPlan, TierAdvisor}

// TierRank is the tier's place in Tiers, -1 when unknown.
func TierRank(t string) int { return slices.Index(Tiers, t) }

// up is the tier one step up the escalation ladder; plan and advisor stay.
func up(t string) string {
	switch t {
	case TierSmol:
		return TierTask
	case TierTask:
		return TierPlan
	}
	return t
}

// down is the tier one step down the ladder, "" when there is none.
func down(t string) string {
	switch t {
	case TierTask:
		return TierSmol
	case TierPlan:
		return TierTask
	}
	return ""
}

// Verifiers of plan 6.1. A verifier is mechanical when a program decides it.
const (
	VerifierSchema   = "schema"
	VerifierExitCode = "exit_code"
	VerifierTests    = "tests"
	VerifierNone     = "none"
)

// Mechanical reports whether the verifier is decided by a program.
func Mechanical(v string) bool {
	return v == VerifierSchema || v == VerifierExitCode || v == VerifierTests
}

// ClassSpec is one row of the class table.
type ClassSpec struct {
	Verifier string
	Tier     string
	// RequiredCompanions are declared_files globs a task of this class needs
	// beside its own files: the documentation targets a repository's CI
	// requires (3-2 dry run ruling: workspace-cli's skills test fails a CLI
	// command PR whose task omitted SKILL.md). The lead's widening path adds
	// them by default (Companions); 3-2's per-repository grant carries the
	// repository-specific list. Default empty.
	RequiredCompanions []string
}

// TierSpec is one tier's chain and its cost cap.
type TierSpec struct {
	Chain []task.Pool
	// MaxCostIndex is the highest registry cost_index an entry of this tier
	// may have; 0 means no cap (briefs ruling 2: cost_index for chain
	// selection, the grant's per-day budget as the stop).
	MaxCostIndex float64
}

// Price is an entry's list price in USD per million tokens.
type Price struct{ InputPerMTok, OutputPerMTok float64 }

// PathRule maps a path glob (declared_files syntax) to a required privacy class.
type PathRule struct {
	Glob  string
	Class string // grants.PrivacyLocal or grants.PrivacyVendor
}

// Table is the effective routing table.
type Table struct {
	Schema           int
	ExplorationShare float64
	Classes          map[string]ClassSpec
	Tiers            map[string]TierSpec
	// Prices are the owner's list prices per chain entry, set in the user
	// file only and never fetched (lead ruling 2026-10-24). An entry with no
	// price has a null cost_usd and the per-day budget stop does not apply
	// to it.
	Prices map[task.Pool]Price
	// Repository-only keys.
	PrivacyPaths   []PathRule
	SensitivePaths []string
	NoTests        bool
	// Sources names the files that overrode the default, in order.
	Sources []string
}

// DefaultExplorationShare is the 6.4 default.
const DefaultExplorationShare = 0.05

// ImplementFileLimit is the implement escalation rule of 6.1: more than three
// declared files escalate to plan. The weekly report's size bands use the
// same boundary (briefs ruling 1).
const ImplementFileLimit = 3

func pool(runtime, model, effort string) task.Pool {
	return task.Pool{Runtime: runtime, Model: model, Effort: effort}
}

// Default is the table that ships in the binary: plan 6.1 classes and the 6.2
// default chains, version 1, in the model ids the registry writes. Deviations
// from the plan text (docs/v3-spike/2b4-execution.md): Codex ids are the
// gpt-5.6 family the pinned codex-cli lists (gpt-5.5 is refused on 0.147.0,
// S4); the pi entries name the local models as the pi package (4-1) writes
// them in the registry (ollama/<model>; the lead's fallback model is
// ollama/qwen3.6:27b); the registry has no pi + Anthropic rows, so the
// plan's pi + anthropic haiku and sonnet entries are not in the default
// (rebase onto 4-1, lead ruling: no hardcoded ids of this brief's own); and
// Grok Build waits for its probe.
func Default() Table {
	return Table{
		Schema:           Schema,
		ExplorationShare: DefaultExplorationShare,
		Classes: map[string]ClassSpec{
			"extract":   {Verifier: VerifierSchema, Tier: TierSmol},
			"summarize": {Verifier: VerifierNone, Tier: TierSmol},
			"script":    {Verifier: VerifierExitCode, Tier: TierSmol},
			"implement": {Verifier: VerifierTests, Tier: TierTask},
			"plan":      {Verifier: VerifierNone, Tier: TierPlan},
			"review":    {Verifier: VerifierNone, Tier: TierAdvisor},
		},
		Tiers: map[string]TierSpec{
			TierSmol: {Chain: []task.Pool{
				pool("pi", "ollama/qwen3.6:35b-a3b", "low"),
				pool("pi", "ollama/qwen3.6:27b", "low"),
				pool("claude", "haiku", "low"),
				pool("codex", "gpt-5.6-luna", "low"),
			}},
			TierTask: {Chain: []task.Pool{
				pool("claude", "sonnet", "medium"),
				pool("codex", "gpt-5.6-terra", "medium"),
				pool("pi", "ollama/qwen3.6:27b", "high"), // on probation, never first
			}},
			TierPlan: {Chain: []task.Pool{
				pool("claude", "opus", "high"),
				pool("codex", "gpt-5.6-sol", "high"),
			}},
			// The reviewer's family differs from the implementer's: Codex when
			// the implementer was Claude, Claude when it was OpenAI (Router).
			TierAdvisor: {Chain: []task.Pool{
				pool("codex", "gpt-5.6-sol", "xhigh"),
				pool("claude", "opus", "xhigh"),
			}},
		},
	}
}

// Size is the number of class rows and chain entries: the default table's
// size reported at the end checkpoint.
func (t Table) Size() (classes, entries int) {
	for _, s := range t.Tiers {
		entries += len(s.Chain)
	}
	return len(t.Classes), entries
}

// Clone is a deep copy.
func (t Table) Clone() Table {
	out := t
	out.Classes = map[string]ClassSpec{}
	for k, v := range t.Classes {
		v.RequiredCompanions = slices.Clone(v.RequiredCompanions)
		out.Classes[k] = v
	}
	out.Tiers = map[string]TierSpec{}
	for k, v := range t.Tiers {
		v.Chain = slices.Clone(v.Chain)
		out.Tiers[k] = v
	}
	out.Prices = map[task.Pool]Price{}
	for k, v := range t.Prices {
		out.Prices[k] = v
	}
	out.PrivacyPaths = slices.Clone(t.PrivacyPaths)
	out.SensitivePaths = slices.Clone(t.SensitivePaths)
	out.Sources = slices.Clone(t.Sources)
	return out
}

// MayRouteDown reports whether a class may run below its table tier (6.1:
// only with a mechanical verifier; plan and review never route down).
func (t Table) MayRouteDown(class string) bool {
	if class == "plan" || class == "review" {
		return false
	}
	return Mechanical(t.Classes[class].Verifier)
}

// Validate checks the invariants a table must hold whatever its source.
func (t Table) Validate() error {
	if t.Schema != Schema {
		return fmt.Errorf("routing table schema %d, want %d", t.Schema, Schema)
	}
	if t.ExplorationShare < 0 || t.ExplorationShare > 1 {
		return fmt.Errorf("exploration_share %v outside 0 to 1", t.ExplorationShare)
	}
	def := Default()
	for _, c := range task.Classes {
		s, ok := t.Classes[c]
		if !ok {
			return fmt.Errorf("class %s has no row", c)
		}
		if TierRank(s.Tier) < 0 {
			return fmt.Errorf("class %s: unknown tier %q", c, s.Tier)
		}
		d := def.Classes[c]
		if s.Verifier != d.Verifier {
			return fmt.Errorf("class %s: the verifier is fixed by plan 6.1 (%s)", c, d.Verifier)
		}
		if TierRank(s.Tier) < TierRank(d.Tier) && !def.MayRouteDown(c) {
			return fmt.Errorf("class %s never routes below tier %s (plan 6.1)", c, d.Tier)
		}
		if (c == "review") != (s.Tier == TierAdvisor) {
			return fmt.Errorf("class %s: only review runs on the advisor tier", c)
		}
	}
	for k := range t.Classes {
		if !task.ValidClass(k) {
			return fmt.Errorf("class %q is not in the closed list %s", k, strings.Join(task.Classes, ", "))
		}
	}
	for _, tier := range Tiers {
		s, ok := t.Tiers[tier]
		if !ok || len(s.Chain) == 0 {
			return fmt.Errorf("tier %s has no chain", tier)
		}
		if s.MaxCostIndex < 0 {
			return fmt.Errorf("tier %s: max_cost_index below 0", tier)
		}
		seen := map[task.Pool]bool{}
		for _, e := range s.Chain {
			if seen[e] {
				return fmt.Errorf("tier %s: entry %s repeated", tier, e.Key())
			}
			seen[e] = true
		}
	}
	for k := range t.Tiers {
		if TierRank(k) < 0 {
			return fmt.Errorf("unknown tier %q", k)
		}
	}
	return nil
}

// Companions returns the declared_files globs a task needs that files does
// not already carry: the union of the class's required companions and the
// repository's list (3-2's per-repository grant), each once, in that order.
// What the lead's widening path adds by default (lead ruling 2026-10-24:
// the two lists merge as a union).
func (t Table) Companions(class string, files, repository []string) []string {
	var out []string
	for _, c := range append(slices.Clone(t.Classes[class].RequiredCompanions), repository...) {
		if !slices.Contains(files, c) && !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	return out
}

// SizeBand is the report's size band by declared file count (briefs ruling
// 1): "1", "2-3", "4+". A task with no declared files is in band "1".
func SizeBand(files int) string {
	switch {
	case files <= 1:
		return "1"
	case files <= ImplementFileLimit:
		return "2-3"
	}
	return "4+"
}

// Family is the model vendor of a pool: claude is anthropic, codex openai,
// a pi pool takes the provider prefix of its model ("ollama/...").
func Family(pl task.Pool) string {
	switch pl.Runtime {
	case "claude":
		return "anthropic"
	case "codex":
		return "openai"
	case "grok":
		return "xai"
	case "cursor-agent":
		return "cursor"
	case "pi":
		if prov, _, ok := strings.Cut(pl.Model, "/"); ok {
			return prov
		}
	}
	return pl.Runtime
}
