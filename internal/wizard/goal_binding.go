package wizard

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	GoalBindingSourceExplicit      = "operator_goal"
	GoalBindingSourceAcceptedBrief = "accepted_brief"
)

// ResolveGoalBinding turns the reviewed wizard inputs into one executable
// goal contract. A real accepted brief may supply a deterministic directive,
// but a missing or generated-stub brief never makes blank goal input safe.
// The method mutates only the in-memory answer model; it never rewrites the
// accepted brief or another project artifact.
func (s *Spec) ResolveGoalBinding() error {
	if s == nil {
		return fmt.Errorf("goal binding answer model is required")
	}
	if s.Backend == BackendResume || s.Backend == BackendGlobalStart || strings.EqualFold(strings.TrimSpace(s.Scope), "global") {
		return nil
	}
	namespace := strings.TrimSpace(s.Profile) + "/" + strings.TrimSpace(s.Session)
	if strings.Trim(namespace, "/") == "" || strings.TrimSpace(s.Profile) == "" || strings.TrimSpace(s.Session) == "" {
		return fmt.Errorf("goal binding requires an exact profile/session namespace")
	}
	goal := strings.TrimSpace(s.Goal)
	source := GoalBindingSourceExplicit
	derived := false
	derivedGoal := ""
	if strings.TrimSpace(s.BriefPath) != "" && acceptedBriefGoal(s.BriefGoal) {
		derivedGoal = fmt.Sprintf("Execute the accepted brief for namespace %s at %s.", namespace, strings.TrimSpace(s.BriefPath))
	}
	if goal == "" {
		briefGoal := strings.TrimSpace(s.BriefGoal)
		if strings.TrimSpace(s.BriefPath) == "" || !acceptedBriefGoal(briefGoal) {
			s.clearGoalBinding()
			return fmt.Errorf("goal binding is required for %s: provide goal text or select a real non-stub accepted brief", namespace)
		}
		goal = derivedGoal
		s.Goal = goal
		source = GoalBindingSourceAcceptedBrief
		derived = true
	} else if s.GoalBindingDerived && goal == derivedGoal {
		source = GoalBindingSourceAcceptedBrief
		derived = true
	}
	digest := GoalBindingDigest(namespace, source, goal)
	s.GoalBindingSource = source
	s.GoalBindingNamespace = namespace
	s.GoalBindingText = goal
	s.GoalBindingDigest = digest
	s.GoalBindingDerived = derived
	s.GoalBindingVerified = true
	return nil
}

// GoalBindingDigest is the canonical digest shared by the wizard, artifact
// preparation, and the final readiness check. Keeping this in one package
// prevents those phases from silently binding different goal text.
func GoalBindingDigest(namespace, source, goal string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(namespace) + "\x00" + strings.TrimSpace(source) + "\x00" + strings.TrimSpace(goal)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func acceptedBriefGoal(goal string) bool {
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return false
	}
	lower := strings.ToLower(goal)
	return !strings.HasPrefix(lower, "todo:") &&
		!strings.Contains(lower, "one-sentence description of what this workstream ships")
}

func (s Spec) GoalBindingReview() string {
	if !s.GoalBindingVerified {
		return "unverified"
	}
	mode := "explicit"
	if s.GoalBindingDerived {
		mode = "brief-derived"
	}
	return fmt.Sprintf("%s · source=%s · namespace=%s · digest=%s · delivery=pending-dry-run · status=ready-for-preview", mode, s.GoalBindingSource, s.GoalBindingNamespace, s.GoalBindingDigest)
}

func (s *Spec) clearGoalBinding() {
	s.GoalBindingSource = ""
	s.GoalBindingNamespace = ""
	s.GoalBindingText = ""
	s.GoalBindingDigest = ""
	s.GoalBindingDerived = false
	s.GoalBindingVerified = false
}
