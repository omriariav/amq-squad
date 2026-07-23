package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// worktreeIsolationReadinessRow is #497's planning-level readiness check:
// launch fails closed when 2+ mutation-capable members would share one
// resolved working directory (one Git index/checkout) without an explicit
// recorded exception (team.Team.SharedCwdException). This is a static check
// over the accepted team profile; the live worktree drift/staleness doctor
// rows are a separate runtime concern owned elsewhere.
func worktreeIsolationReadinessRow(t team.Team) runReadinessRow {
	groups := map[string][]string{}
	for _, m := range t.Members {
		if team.EffectiveActorMode(t, m) != team.ActorModeImplementation {
			continue
		}
		cwd := m.EffectiveCWD(t.Project)
		groups[cwd] = append(groups[cwd], m.Role)
	}
	var collisions []string
	for cwd, roles := range groups {
		if len(roles) < 2 {
			continue
		}
		sort.Strings(roles)
		collisions = append(collisions, fmt.Sprintf("%s: %s", cwd, strings.Join(roles, ", ")))
	}
	if len(collisions) == 0 {
		return runReadinessRow{Artifact: "worktree_isolation", Status: "ready", Evidence: "no 2+ mutation-capable members share one working directory"}
	}
	sort.Strings(collisions)
	evidence := strings.Join(collisions, "; ")
	if reason := strings.TrimSpace(t.SharedCwdException); reason != "" {
		return runReadinessRow{Artifact: "worktree_isolation", Status: "ready", Evidence: fmt.Sprintf("shared-cwd collision accepted (%s); exception: %s", evidence, reason)}
	}
	return runReadinessRow{
		Artifact: "worktree_isolation",
		Status:   "blocked",
		Evidence: fmt.Sprintf("2+ mutation-capable members share one working directory without a recorded exception: %s", evidence),
		Fix:      `record an explicit exception with 'amq-squad team shared-cwd-exception set "<reason>"', or give each mutation-capable member its own --cwd (isolated worktree)`,
	}
}
