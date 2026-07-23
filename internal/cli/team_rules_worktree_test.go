package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRenderTeamRulesSingleDevOmitsWorktreeIsolation(t *testing.T) {
	body, err := renderTeamRules(team.Team{
		Project: t.TempDir(),
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-393"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "## Worktree Isolation") {
		t.Fatalf("single-dev roster should not get the worktree isolation section:\n%s", body)
	}
}

func TestRenderTeamRulesTwoImplementationDevsGetWorktreeIsolation(t *testing.T) {
	body, err := renderTeamRules(team.Team{
		Project: t.TempDir(),
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-393", ActorMode: team.ActorModeImplementation},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-393", ActorMode: team.ActorModeImplementation},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "## Worktree Isolation") {
		t.Fatalf("2 mutation-capable devs should get the worktree isolation section:\n%s", body)
	}
	if !strings.Contains(body, "2 mutation-capable developers") {
		t.Fatalf("section should cite the count:\n%s", body)
	}
	if !strings.Contains(body, "shared-cwd-exception") {
		t.Fatalf("section should mention the exception verb:\n%s", body)
	}
}

func TestRenderTeamRulesReviewerExcludedFromWorktreeIsolationCount(t *testing.T) {
	body, err := renderTeamRules(team.Team{
		Project: t.TempDir(),
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-393", ActorMode: team.ActorModeImplementation},
			{Role: "reviewer", Binary: "codex", Handle: "reviewer", Session: "issue-393", ActorMode: team.ActorModeReview},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "## Worktree Isolation") {
		t.Fatalf("1 implementation + 1 reviewer should not trip the 2-mutation-capable threshold:\n%s", body)
	}
}

func TestRenderTeamRulesShowsRecordedSharedCwdException(t *testing.T) {
	body, err := renderTeamRules(team.Team{
		Project:            t.TempDir(),
		SharedCwdException: "hotspot serialization",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-393", ActorMode: team.ActorModeImplementation},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-393", ActorMode: team.ActorModeImplementation},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "Recorded shared-cwd exception for this profile: hotspot serialization") {
		t.Fatalf("expected the recorded exception to be quoted verbatim:\n%s", body)
	}
}
