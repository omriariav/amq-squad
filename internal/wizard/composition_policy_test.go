package wizard

import (
	"strings"
	"testing"
)

func TestRecommendWorktreeIsolationSingleDevNoRecommendation(t *testing.T) {
	recommend, count, rationale := RecommendWorktreeIsolation([]string{"cto"}, "cto", "builder")
	if recommend {
		t.Fatalf("single-dev roster should not recommend isolation: count=%d rationale=%q", count, rationale)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

func TestRecommendWorktreeIsolationTwoImplementationDevsRecommends(t *testing.T) {
	recommend, count, rationale := RecommendWorktreeIsolation([]string{"cto", "qa"}, "cto", "builder")
	if !recommend {
		t.Fatal("2 mutation-capable devs should recommend isolation")
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	if !strings.Contains(rationale, "2 mutation-capable") {
		t.Fatalf("rationale should cite the count: %q", rationale)
	}
	if !strings.Contains(rationale, "shared-cwd-exception") {
		t.Fatalf("rationale should point at the exception escape hatch: %q", rationale)
	}
}

func TestRecommendWorktreeIsolationPlannerLeadExcludedFromCount(t *testing.T) {
	// cto (planner lead) + one worker: only 1 mutation-capable member.
	recommend, count, _ := RecommendWorktreeIsolation([]string{"cto", "qa"}, "cto", "planner")
	if recommend {
		t.Fatalf("planner lead + 1 worker should not recommend isolation, count=%d", count)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (planner lead excluded)", count)
	}
}

func TestRecommendWorktreeIsolationPlannerLeadWithTwoWorkersStillRecommends(t *testing.T) {
	recommend, count, _ := RecommendWorktreeIsolation([]string{"cto", "qa", "fullstack"}, "cto", "planner")
	if !recommend {
		t.Fatal("planner lead + 2 workers should still recommend isolation for the 2 workers")
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
}

func TestRecommendWorktreeIsolationBuilderLeadCountsAsMutationCapable(t *testing.T) {
	recommend, count, _ := RecommendWorktreeIsolation([]string{"cto", "qa"}, "cto", "builder")
	if !recommend || count != 2 {
		t.Fatalf("builder lead should count as mutation-capable: recommend=%t count=%d", recommend, count)
	}
}

func TestRecommendWorktreeIsolationEmptyRolesIgnored(t *testing.T) {
	recommend, count, _ := RecommendWorktreeIsolation([]string{"cto", "", "qa", "  "}, "cto", "builder")
	if !recommend || count != 2 {
		t.Fatalf("blank role entries should be ignored: recommend=%t count=%d", recommend, count)
	}
}
