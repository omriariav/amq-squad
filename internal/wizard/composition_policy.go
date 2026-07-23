package wizard

import (
	"fmt"
	"strings"
)

// RecommendWorktreeIsolation is #497's composition-time advisory: a proposed
// roster with 2+ mutation-capable developers recommends isolated worktrees
// by default. It is preview-only -- it never creates a worktree or branch
// (that materialization surface is a separate runtime concern, out of this
// package's scope) and never blocks anything; the wizard just prints the
// recommendation and the operator proceeds either way.
//
// A planner lead is not mutation-capable (it delegates rather than
// implementing directly, mirroring team.EffectiveActorMode's fallback for a
// planner-mode lead), so it is excluded from the count.
func RecommendWorktreeIsolation(roles []string, lead, leadMode string) (recommend bool, mutationCapableCount int, rationale string) {
	lead = strings.ToLower(strings.TrimSpace(lead))
	planner := strings.EqualFold(strings.TrimSpace(leadMode), "planner")
	count := 0
	for _, r := range roles {
		role := strings.ToLower(strings.TrimSpace(r))
		if role == "" {
			continue
		}
		if role == lead && planner {
			continue
		}
		count++
	}
	if count < 2 {
		return false, count, ""
	}
	return true, count, fmt.Sprintf(
		"%d mutation-capable developers would share one Git index by default; isolated worktrees (one branch/checkout per developer) avoid checkout/index collisions. Record an explicit shared-cwd exception (`amq-squad team shared-cwd-exception set \"<reason>\"`) if they should share one on purpose.",
		count,
	)
}
