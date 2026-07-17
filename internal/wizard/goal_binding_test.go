package wizard

import (
	"strings"
	"testing"
)

func TestResolveGoalBindingExplicit(t *testing.T) {
	s := Spec{Backend: BackendRunStart, Profile: "release", Session: "v2-22-0", Goal: "Ship the milestone"}
	if err := s.ResolveGoalBinding(); err != nil {
		t.Fatal(err)
	}
	if !s.GoalBindingVerified || s.GoalBindingDerived || s.GoalBindingSource != GoalBindingSourceExplicit || !strings.HasPrefix(s.GoalBindingDigest, "sha256:") {
		t.Fatalf("unexpected binding: %+v", s)
	}
}

func TestResolveGoalBindingDerivesFromAcceptedBriefWithoutRewritingIt(t *testing.T) {
	s := Spec{Backend: BackendRunStart, Profile: "release", Session: "v2-22-0", BriefPath: "/repo/.amq-squad/briefs/release/v2-22-0.md", BriefGoal: "Deliver v2.22.0"}
	beforePath, beforeGoal := s.BriefPath, s.BriefGoal
	if err := s.ResolveGoalBinding(); err != nil {
		t.Fatal(err)
	}
	if !s.GoalBindingVerified || !s.GoalBindingDerived || s.GoalBindingSource != GoalBindingSourceAcceptedBrief {
		t.Fatalf("unexpected binding: %+v", s)
	}
	if !strings.Contains(s.Goal, "release/v2-22-0") || s.BriefPath != beforePath || s.BriefGoal != beforeGoal {
		t.Fatalf("derived goal changed accepted brief evidence: %+v", s)
	}
}

func TestResolveGoalBindingRejectsBlankOrStubBrief(t *testing.T) {
	for _, goal := range []string{"", "TODO: one-sentence description of what this workstream ships."} {
		s := Spec{Backend: BackendRunStart, Profile: "release", Session: "v2-22-0", BriefPath: "/repo/brief.md", BriefGoal: goal}
		if err := s.ResolveGoalBinding(); err == nil || !strings.Contains(err.Error(), "goal binding is required") {
			t.Fatalf("goal=%q err=%v", goal, err)
		}
		if s.GoalBindingVerified || s.Goal != "" {
			t.Fatalf("failed binding mutated goal: %+v", s)
		}
	}
}

func TestResolveGoalBindingBacktrackedExplicitGoalRecomputesSourceAndDigest(t *testing.T) {
	s := Spec{Backend: BackendRunStart, Profile: "release", Session: "v2-22-0", BriefPath: "/repo/brief.md", BriefGoal: "Deliver v2.22.0"}
	if err := s.ResolveGoalBinding(); err != nil {
		t.Fatal(err)
	}
	derivedDigest := s.GoalBindingDigest
	s.Goal = "Use the operator's revised goal"
	if err := s.ResolveGoalBinding(); err != nil {
		t.Fatal(err)
	}
	if s.GoalBindingDerived || s.GoalBindingSource != GoalBindingSourceExplicit || s.GoalBindingText != s.Goal || s.GoalBindingDigest == derivedDigest {
		t.Fatalf("stale derived binding survived explicit edit: %+v", s)
	}
}

func TestGoalBindingReviewExposesDeliveryValidationStatus(t *testing.T) {
	s := Spec{Backend: BackendRunStart, Profile: "release", Session: "s", Goal: "ship"}
	if err := s.ResolveGoalBinding(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"source=operator_goal", "namespace=release/s", "delivery=pending-dry-run", "status=ready-for-preview"} {
		if !strings.Contains(s.GoalBindingReview(), want) {
			t.Fatalf("review missing %q: %s", want, s.GoalBindingReview())
		}
	}
}
