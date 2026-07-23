package wizard

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
)

func TestWorkClassPoliciesAreCompleteAndConsistent(t *testing.T) {
	for _, wc := range WorkClasses() {
		policy, ok := WorkClassPolicyFor(wc)
		if !ok {
			t.Fatalf("WorkClasses() lists %q but WorkClassPolicyFor has no entry", wc)
		}
		if policy.Label == "" || policy.Tier == "" || policy.Effort == "" || policy.Rationale == "" {
			t.Fatalf("work class %q has an incomplete policy: %+v", wc, policy)
		}
		switch policy.Tier {
		case agentcatalog.TierFast, agentcatalog.TierBalanced, agentcatalog.TierFrontier:
		default:
			t.Fatalf("work class %q has an unknown tier %q", wc, policy.Tier)
		}
	}
}

func TestRecommendModelEffortWellSpecifiedLeafDefaultsEconomical(t *testing.T) {
	cat := agentcatalog.Builtins()
	rec := RecommendModelEffort("claude", WorkClassWellSpecifiedLeaf, TaskProperties{}, cat)
	if rec.Model != "sonnet" {
		t.Fatalf("model = %q, want the balanced-tier sonnet", rec.Model)
	}
	if rec.Effort != "low" {
		t.Fatalf("effort = %q, want low", rec.Effort)
	}
	if rec.PolicySource != RecommendationSourcePolicy {
		t.Fatalf("policy source = %q, want %q", rec.PolicySource, RecommendationSourcePolicy)
	}
	if rec.Rationale == "" {
		t.Fatal("expected a non-empty rationale")
	}
}

func TestRecommendModelEffortArchitectureDefaultsFrontier(t *testing.T) {
	cat := agentcatalog.Builtins()
	rec := RecommendModelEffort("claude", WorkClassGoalDecomposition, TaskProperties{}, cat)
	if rec.Model != "fable" && rec.Model != "opus" {
		t.Fatalf("model = %q, want a frontier-tier model", rec.Model)
	}
	if rec.Effort != "high" {
		t.Fatalf("effort = %q, want high", rec.Effort)
	}
}

func TestRecommendModelEffortMechanicalDefaultsFastAndMinimal(t *testing.T) {
	cat := agentcatalog.Builtins()
	rec := RecommendModelEffort("claude", WorkClassMechanical, TaskProperties{}, cat)
	if rec.Model != "haiku" {
		t.Fatalf("model = %q, want the fast-tier haiku", rec.Model)
	}
	if rec.Effort != "minimal" {
		t.Fatalf("effort = %q, want minimal", rec.Effort)
	}
}

func TestRecommendModelEffortEscalatesWellSpecifiedLeafWhenAmbiguous(t *testing.T) {
	cat := agentcatalog.Builtins()
	baseline := RecommendModelEffort("claude", WorkClassWellSpecifiedLeaf, TaskProperties{}, cat)
	escalated := RecommendModelEffort("claude", WorkClassWellSpecifiedLeaf, TaskProperties{Ambiguous: true}, cat)
	if baseline.Model == escalated.Model && baseline.Effort == escalated.Effort {
		t.Fatalf("ambiguity flag did not change the recommendation: baseline=%+v escalated=%+v", baseline, escalated)
	}
	if escalated.Model != "fable" && escalated.Model != "opus" {
		t.Fatalf("escalated model = %q, want frontier tier", escalated.Model)
	}
	if escalated.Effort != "high" {
		t.Fatalf("escalated effort = %q, want high", escalated.Effort)
	}
	if !strings.Contains(escalated.Rationale, "ambiguous") {
		t.Fatalf("rationale should mention the ambiguity escalation: %q", escalated.Rationale)
	}
	if escalated.Confidence != "medium" {
		t.Fatalf("escalated confidence = %q, want medium (an escalation is a judgment call)", escalated.Confidence)
	}
}

func TestRecommendModelEffortHighBlastRadiusEscalatesEvenMechanical(t *testing.T) {
	cat := agentcatalog.Builtins()
	rec := RecommendModelEffort("claude", WorkClassMechanical, TaskProperties{HighBlastRadius: true}, cat)
	if rec.Model != "fable" && rec.Model != "opus" {
		t.Fatalf("model = %q, want frontier tier despite the mechanical work class", rec.Model)
	}
	if rec.Effort != "high" {
		t.Fatalf("effort = %q, want high", rec.Effort)
	}
}

func TestRecommendModelEffortLowTestabilityRaisesEffortOnly(t *testing.T) {
	cat := agentcatalog.Builtins()
	baseline := RecommendModelEffort("claude", WorkClassWellSpecifiedLeaf, TaskProperties{}, cat)
	rec := RecommendModelEffort("claude", WorkClassWellSpecifiedLeaf, TaskProperties{LowTestability: true}, cat)
	if rec.Model != baseline.Model {
		t.Fatalf("low testability should not change the model tier: baseline=%q got=%q", baseline.Model, rec.Model)
	}
	if rec.Effort != "high" {
		t.Fatalf("effort = %q, want high", rec.Effort)
	}
}

func TestRecommendModelEffortDecorrelatedReviewAvoidsPeerModel(t *testing.T) {
	cat := agentcatalog.Builtins()
	rec := RecommendModelEffort("codex", WorkClassSecurityReview, TaskProperties{
		NeedsDecorrelatedReview: true,
		PeerModel:               "gpt-5.6-sol",
	}, cat)
	if rec.Model == "gpt-5.6-sol" {
		t.Fatalf("decorrelated review recommendation must avoid the peer model, got %q", rec.Model)
	}
}

func TestRecommendModelEffortUnknownWorkClassIsLowConfidenceNotError(t *testing.T) {
	cat := agentcatalog.Builtins()
	rec := RecommendModelEffort("claude", WorkClass("not_a_real_class"), TaskProperties{}, cat)
	if rec.Confidence != "low" {
		t.Fatalf("confidence = %q, want low for an unrecognized work class", rec.Confidence)
	}
	if rec.Model != "" {
		t.Fatalf("unknown work class should not fabricate a model, got %q", rec.Model)
	}
}

// TestRecommendModelEffortMetadataLessCatalogStillRecommends is #496's
// explicit backward-compatibility requirement applied to routing: a catalog
// with no capability-tier data at all must still produce a usable (if
// lower-precision) recommendation, never an empty one.
func TestRecommendModelEffortMetadataLessCatalogStillRecommends(t *testing.T) {
	cat := agentcatalog.Catalog{Binaries: map[string]agentcatalog.Binary{
		"claude": {Models: []agentcatalog.Entry{
			{Value: "house-model", Label: "House model", Enabled: true},
		}},
	}}
	rec := RecommendModelEffort("claude", WorkClassGoalDecomposition, TaskProperties{}, cat)
	if rec.Model != "house-model" {
		t.Fatalf("model = %q, want the only available model even without tier metadata", rec.Model)
	}
	if rec.Effort != "high" {
		t.Fatalf("effort = %q, want high (effort comes from the policy, not the catalog)", rec.Effort)
	}
}

func TestDefaultWorkClassForRoleKnownAndUnknown(t *testing.T) {
	if got := DefaultWorkClassForRole("cto"); got != WorkClassGoalDecomposition {
		t.Fatalf("cto = %q, want %q", got, WorkClassGoalDecomposition)
	}
	if got := DefaultWorkClassForRole("QA"); got != WorkClassSecurityReview {
		t.Fatalf("QA (case-insensitive) = %q, want %q", got, WorkClassSecurityReview)
	}
	if got := DefaultWorkClassForRole("some-custom-role"); got != WorkClassWellSpecifiedLeaf {
		t.Fatalf("unknown role = %q, want the conservative default %q", got, WorkClassWellSpecifiedLeaf)
	}
}

func TestRecommendLeadModeMultiWorkerSuggestsPlanner(t *testing.T) {
	mode, rationale := RecommendLeadMode(3)
	if mode != "planner" {
		t.Fatalf("mode = %q, want planner for a multi-worker roster", mode)
	}
	if rationale == "" {
		t.Fatal("expected a non-empty rationale")
	}
}

func TestRecommendLeadModeSingleMemberSuggestsBuilder(t *testing.T) {
	mode, _ := RecommendLeadMode(1)
	if mode != "builder" {
		t.Fatalf("mode = %q, want builder for a single-member roster", mode)
	}
}

// TestRecommendationOutcomeDetectsOverride models #496's requirement that an
// operator override is visible/auditable: a worked comparison across the
// recommendation path, the override path, and an escalation after a quality
// miss (item 6's minimal dogfood-comparison shape).
func TestRecommendationOutcomeDetectsOverride(t *testing.T) {
	cat := agentcatalog.Builtins()
	recommended := RecommendModelEffort("claude", WorkClassWellSpecifiedLeaf, TaskProperties{}, cat)

	followedPolicy := RecommendationOutcome{Recommendation: recommended, Used: recommended, Accepted: true}
	if followedPolicy.Overridden() {
		t.Fatalf("following the recommendation must not read as overridden: %+v", followedPolicy)
	}

	operatorOverride := RecommendationOutcome{
		Recommendation: recommended,
		Used:           Recommendation{Binary: "claude", Model: "opus", Effort: "high"},
		Accepted:       true,
	}
	if !operatorOverride.Overridden() {
		t.Fatalf("a different used model/effort must read as overridden: %+v", operatorOverride)
	}

	// Escalation after a quality miss: the recommendation was followed, work
	// was rejected, and a second attempt at a higher tier succeeds.
	firstAttempt := RecommendationOutcome{Recommendation: recommended, Used: recommended, Accepted: false, ReviewFindings: 2}
	escalated := RecommendModelEffort("claude", WorkClassWellSpecifiedLeaf, TaskProperties{Ambiguous: true}, cat)
	secondAttempt := RecommendationOutcome{Recommendation: escalated, Used: escalated, Accepted: true, Retries: 1}
	if firstAttempt.Accepted {
		t.Fatal("fixture setup error: first attempt should be the rejected one")
	}
	if !secondAttempt.Accepted || secondAttempt.Retries != 1 {
		t.Fatalf("escalated second attempt should be accepted with a recorded retry: %+v", secondAttempt)
	}
}
