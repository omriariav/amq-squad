package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestWorktreeIsolationReadinessRowSingleDevIsReady(t *testing.T) {
	tm := team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", ActorMode: team.ActorModeImplementation}}}
	row := worktreeIsolationCheck(tm, team.DefaultProfile)
	if row.Status != "ready" {
		t.Fatalf("single-dev row = %+v, want ready", row)
	}
}

func TestWorktreeIsolationReadinessRowIsolatedCwdsAreReady(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", ActorMode: team.ActorModeImplementation, CWD: "/repo-worktrees/cto"},
			{Role: "qa", Binary: "codex", ActorMode: team.ActorModeImplementation, CWD: "/repo-worktrees/qa"},
		},
	}
	row := worktreeIsolationCheck(tm, team.DefaultProfile)
	if row.Status != "ready" {
		t.Fatalf("isolated-cwd row = %+v, want ready", row)
	}
}

func TestWorktreeIsolationReadinessRowMixedImplementationReviewIsReady(t *testing.T) {
	// One implementation, one read-only reviewer sharing the default cwd:
	// only one mutation-capable member, so there is nothing to collide.
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", ActorMode: team.ActorModeImplementation},
			{Role: "reviewer", Binary: "codex", ActorMode: team.ActorModeReview},
		},
	}
	row := worktreeIsolationCheck(tm, team.DefaultProfile)
	if row.Status != "ready" {
		t.Fatalf("mixed implementation/review row = %+v, want ready", row)
	}
}

func TestWorktreeIsolationReadinessRowSharedCwdBlocksWithoutException(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", ActorMode: team.ActorModeImplementation},
			{Role: "qa", Binary: "codex", ActorMode: team.ActorModeImplementation},
		},
	}
	row := worktreeIsolationCheck(tm, team.DefaultProfile)
	if row.Status != "blocked" {
		t.Fatalf("shared-cwd row = %+v, want blocked", row)
	}
	if !strings.Contains(row.Evidence, "cto") || !strings.Contains(row.Evidence, "qa") {
		t.Fatalf("evidence should name both colliding roles: %q", row.Evidence)
	}
	if !strings.Contains(row.Fix, "shared-cwd-exception") {
		t.Fatalf("fix should point at the exception verb: %q", row.Fix)
	}
}

func TestWorktreeIsolationReadinessRowSharedCwdReadyWithException(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", ActorMode: team.ActorModeImplementation},
			{Role: "qa", Binary: "codex", ActorMode: team.ActorModeImplementation},
		},
		SharedCwdException: "hotspot serialization",
	}
	row := worktreeIsolationCheck(tm, team.DefaultProfile)
	if row.Status != "ready" {
		t.Fatalf("exception row = %+v, want ready", row)
	}
	if !strings.Contains(row.Evidence, "hotspot serialization") {
		t.Fatalf("evidence should record the exception reason: %q", row.Evidence)
	}
}

func TestWorktreeIsolationReadinessRowPlannerLeadNotCounted(t *testing.T) {
	// A planner lead is ActorMode=review by EffectiveActorMode's fallback
	// (see team.EffectiveActorMode), so only "worker" remains mutation
	// capable -- no collision with just one.
	tm := team.Team{
		Project:  "/repo",
		Lead:     "cto",
		LeadMode: team.LeadModePlanner,
		Members: []team.Member{
			{Role: "cto", Binary: "codex"},
			{Role: "worker", Binary: "codex"},
		},
	}
	row := worktreeIsolationCheck(tm, team.DefaultProfile)
	if row.Status != "ready" {
		t.Fatalf("planner-lead row = %+v, want ready", row)
	}
}
