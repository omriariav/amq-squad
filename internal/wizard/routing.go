package wizard

import (
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
)

// WorkClass is a small, stable classification of the responsibility a member
// or task slice owns (#496). It is data, not prose: skills and the wizard UI
// read this one vocabulary instead of each carrying their own model/effort
// guidance table.
type WorkClass string

const (
	WorkClassGoalDecomposition        WorkClass = "goal_decomposition"
	WorkClassWellSpecifiedLeaf        WorkClass = "well_specified_leaf"
	WorkClassMechanical               WorkClass = "mechanical"
	WorkClassAmbiguousHighBlastRadius WorkClass = "ambiguous_high_blast_radius"
	WorkClassSecurityReview           WorkClass = "security_review"
	WorkClassTaste                    WorkClass = "taste"
)

// WorkClassPolicy is the default routing posture for a WorkClass: a target
// capability tier and effort floor, plus the rationale a recommendation
// cites. Model and effort are deliberately separate dials here: escalating
// one never substitutes for the other.
type WorkClassPolicy struct {
	WorkClass                WorkClass
	Label                    string
	Tier                     string // agentcatalog.Tier{Fast,Balanced,Frontier}
	Effort                   string
	Rationale                string
	PreferDecorrelatedReview bool
}

// workClassPolicies is the canonical posture table from issue #496's
// proposal. Keep it in sync with docs/v2.24.0-model-routing-policy.md; the
// doc explains the "why", this is the "what" a recommendation reads.
var workClassPolicies = map[WorkClass]WorkClassPolicy{
	WorkClassGoalDecomposition: {
		WorkClass: WorkClassGoalDecomposition,
		Label:     "goal decomposition / architecture / cross-cutting decisions",
		Tier:      agentcatalog.TierFrontier, Effort: "high",
		Rationale: "high-leverage decisions compound downstream; a weaker planner tends to increase total worker tokens, retries, and review churn",
	},
	WorkClassWellSpecifiedLeaf: {
		WorkClass: WorkClassWellSpecifiedLeaf,
		Label:     "well-specified implementation leaf",
		Tier:      agentcatalog.TierBalanced, Effort: "low",
		Rationale: "bounded, well-specified work does not need frontier judgment; an economical/balanced model at low effort is the efficient default",
	},
	WorkClassMechanical: {
		WorkClass: WorkClassMechanical,
		Label:     "mechanical edits / generation / bounded test work",
		Tier:      agentcatalog.TierFast, Effort: "minimal",
		Rationale: "deterministic, mechanical work is the cheapest tier by design",
	},
	WorkClassAmbiguousHighBlastRadius: {
		WorkClass: WorkClassAmbiguousHighBlastRadius,
		Label:     "ambiguous or high-blast-radius implementation",
		Tier:      agentcatalog.TierFrontier, Effort: "high",
		Rationale: "ambiguity plus high blast radius raises the cost of a wrong turn enough to justify frontier judgment and effort",
	},
	WorkClassSecurityReview: {
		WorkClass: WorkClassSecurityReview,
		Label:     "security / reconciliation / independent review",
		Tier:      agentcatalog.TierFrontier, Effort: "high",
		Rationale:                "independent review benefits from strong judgment, ideally from a model decorrelated from the implementer",
		PreferDecorrelatedReview: true,
	},
	WorkClassTaste: {
		WorkClass: WorkClassTaste,
		Label:     "UI / API shape / product copy",
		Tier:      agentcatalog.TierBalanced, Effort: "medium",
		Rationale: "taste-sensitive work needs a model chosen for taste as well as intelligence; effort is secondary to fit",
	},
}

// WorkClassPolicyFor returns the canonical policy for wc.
func WorkClassPolicyFor(wc WorkClass) (WorkClassPolicy, bool) {
	p, ok := workClassPolicies[wc]
	return p, ok
}

// WorkClasses returns every known work class in a stable display order.
func WorkClasses() []WorkClass {
	return []WorkClass{
		WorkClassGoalDecomposition,
		WorkClassWellSpecifiedLeaf,
		WorkClassMechanical,
		WorkClassAmbiguousHighBlastRadius,
		WorkClassSecurityReview,
		WorkClassTaste,
	}
}

// defaultRoleWorkClass maps known persona IDs (internal/catalog) to a
// starting work class. This is an advisory heuristic, not authority: a
// custom or unrecognized role, or one the caller knows more about, should
// pass its own WorkClass to RecommendModelEffort rather than rely on this.
var defaultRoleWorkClass = map[string]WorkClass{
	"cpo":          WorkClassGoalDecomposition,
	"cto":          WorkClassGoalDecomposition,
	"lead":         WorkClassGoalDecomposition,
	"senior-dev":   WorkClassWellSpecifiedLeaf,
	"fullstack":    WorkClassWellSpecifiedLeaf,
	"frontend-dev": WorkClassWellSpecifiedLeaf,
	"backend-dev":  WorkClassWellSpecifiedLeaf,
	"mobile-dev":   WorkClassWellSpecifiedLeaf,
	"agent":        WorkClassWellSpecifiedLeaf,
	"junior-dev":   WorkClassMechanical,
	"qa":           WorkClassSecurityReview,
	"pm":           WorkClassTaste,
	"designer":     WorkClassTaste,
	"scribe":       WorkClassTaste,
}

// DefaultWorkClassForRole returns the advisory starting work class for a
// known persona role, or WorkClassWellSpecifiedLeaf (the conservative
// middle default) for a custom/unrecognized one.
func DefaultWorkClassForRole(role string) WorkClass {
	if wc, ok := defaultRoleWorkClass[strings.ToLower(strings.TrimSpace(role))]; ok {
		return wc
	}
	return WorkClassWellSpecifiedLeaf
}

// TaskProperties captures the risk/leverage inputs the routing policy reads
// in addition to the work class itself. Zero values are the conservative
// "not flagged" default -- an unknown task never silently escalates.
type TaskProperties struct {
	Ambiguous               bool
	HighDownstreamFanout    bool
	HighBlastRadius         bool
	LowTestability          bool
	TasteSensitive          bool
	NeedsDecorrelatedReview bool
	// PeerModel names a model already assigned to a correlated actor (e.g.
	// the implementer being reviewed), so a decorrelated-review
	// recommendation can steer away from it. Empty means no peer to avoid.
	PeerModel string
}

// RecommendationSource records where a used value ultimately came from, for
// visibility/auditability in preparation and readiness review.
type RecommendationSource string

const (
	RecommendationSourcePolicy           RecommendationSource = "policy"
	RecommendationSourceProfile          RecommendationSource = "profile"
	RecommendationSourceSavedLaunch      RecommendationSource = "saved_launch"
	RecommendationSourceOperatorOverride RecommendationSource = "operator_override"
)

// Recommendation is the advisory output of the routing policy. It never
// mutates a profile or forces a value; callers decide whether to use it as a
// prompt default and always let the operator override it.
type Recommendation struct {
	Binary       string
	Model        string
	Effort       string
	Rationale    string
	PolicySource RecommendationSource
	// Confidence is a coarse label (low/medium/high), not a fabricated
	// numeric score: the inputs here are qualitative task properties.
	Confidence string
}

var effortRank = map[string]int{
	"":          0,
	"automatic": 0,
	"minimal":   0,
	"low":       1,
	"medium":    2,
	"high":      3,
	"xhigh":     4,
	"max":       5,
}

func effortAtLeast(current, floor string) bool {
	return effortRank[strings.ToLower(strings.TrimSpace(current))] >= effortRank[strings.ToLower(strings.TrimSpace(floor))]
}

// RecommendModelEffort returns an advisory binary/model/effort recommendation
// for a member doing work of class wc, given catalog for concrete model
// selection. It is a pure function: it never touches a team profile, and an
// unknown work class or metadata-less catalog degrades to a low-confidence
// but still usable recommendation rather than an error.
func RecommendModelEffort(binary string, wc WorkClass, props TaskProperties, cat agentcatalog.Catalog) Recommendation {
	policy, ok := WorkClassPolicyFor(wc)
	if !ok {
		return Recommendation{
			Binary: binary, PolicySource: RecommendationSourcePolicy, Confidence: "low",
			Rationale: "unknown work class; no routing policy applies",
		}
	}
	tier := policy.Tier
	effort := policy.Effort
	var rationale strings.Builder
	rationale.WriteString(policy.Rationale)
	confidence := "high"

	escalate := func(reason string) {
		if tier != agentcatalog.TierFrontier {
			tier = agentcatalog.TierFrontier
		}
		if !effortAtLeast(effort, "high") {
			effort = "high"
		}
		rationale.WriteString(". ")
		rationale.WriteString(reason)
		confidence = "medium"
	}
	if props.Ambiguous {
		escalate("escalated to frontier/high effort: this instance is flagged ambiguous")
	}
	if props.HighBlastRadius {
		escalate("escalated to frontier/high effort: this instance is flagged high-blast-radius")
	}
	if props.HighDownstreamFanout && !effortAtLeast(effort, "high") {
		effort = "high"
		rationale.WriteString(". raised effort: high downstream fanout/decision leverage")
	}
	if props.LowTestability && !effortAtLeast(effort, "high") {
		effort = "high"
		rationale.WriteString(". raised effort: weak automated feedback makes mistakes more expensive to catch")
	}
	if (props.NeedsDecorrelatedReview || policy.PreferDecorrelatedReview) && strings.TrimSpace(props.PeerModel) != "" {
		rationale.WriteString(". preferring a model different from the implementer's for a decorrelated review lens")
	}

	model := pickModelForTier(binary, tier, props, cat)
	return Recommendation{
		Binary: binary, Model: model, Effort: effort,
		Rationale: rationale.String(), PolicySource: RecommendationSourcePolicy, Confidence: confidence,
	}
}

// pickModelForTier chooses a concrete model for binary at tier from cat. A
// metadata-less catalog (no CapabilityTier data at all) falls back to
// whatever models are available rather than returning nothing, matching
// #496's backward-compatibility requirement: routing still works, just
// without tier precision.
func pickModelForTier(binary, tier string, props TaskProperties, cat agentcatalog.Catalog) string {
	entries := cat.Entries(binary, agentcatalog.Models)
	if len(entries) == 0 {
		return ""
	}
	candidates := make([]agentcatalog.Entry, 0, len(entries))
	for _, e := range entries {
		if e.CapabilityTier == tier {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		candidates = entries
	}
	peer := strings.TrimSpace(props.PeerModel)
	if peer != "" {
		for _, c := range candidates {
			if !strings.EqualFold(c.Value, peer) {
				return c.Value
			}
		}
		// No in-tier candidate avoids the peer (e.g. it's the only frontier
		// model): broaden to every model for this binary before giving up
		// and returning the peer itself as the last resort.
		for _, c := range entries {
			if !strings.EqualFold(c.Value, peer) {
				return c.Value
			}
		}
	}
	return candidates[0].Value
}

// RecommendLeadMode advises "planner" for a genuinely multi-worker roster and
// "builder" otherwise (#496 item 3). It is a suggested default only: an
// explicit operator choice (--lead-mode, or an existing profile's persisted
// value) always wins and this function is never consulted to override one.
func RecommendLeadMode(memberCount int) (mode, rationale string) {
	if memberCount > 1 {
		return "planner", "multi-worker squads keep high-leverage planning context separate from leaf implementation context (recommendation only; choose builder to override)"
	}
	return "builder", "a single-member roster has no separate worker context to isolate from"
}

// RecommendationOutcome is a minimal evaluation record (#496 item 6): enough
// to compare routing policies by total accepted-work economics rather than
// per-token price alone. This is a starting data shape for dogfood
// comparisons and fixtures, not a telemetry/analytics system.
type RecommendationOutcome struct {
	Recommendation Recommendation
	// Used is what actually launched; it differs from Recommendation exactly
	// when the operator overrode it.
	Used           Recommendation
	Accepted       bool
	Retries        int
	ReviewFindings int
	ElapsedSeconds float64
}

// Overridden reports whether the operator's used binary/model/effort differs
// from the policy's recommendation.
func (o RecommendationOutcome) Overridden() bool {
	return o.Used.Binary != o.Recommendation.Binary || o.Used.Model != o.Recommendation.Model || o.Used.Effort != o.Recommendation.Effort
}
