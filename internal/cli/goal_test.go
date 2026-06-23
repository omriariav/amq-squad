package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func fakeGoalGh(t *testing.T, body string, returnErr error, captured *[]string) {
	t.Helper()
	prev := goalGhRun
	goalGhRun = func(args ...string) ([]byte, error) {
		if captured != nil {
			*captured = append([]string(nil), args...)
		}
		if returnErr != nil {
			return nil, returnErr
		}
		return []byte(body), nil
	}
	t.Cleanup(func() { goalGhRun = prev })
}

func TestGoalDraftJSONIncludesMilestoneIssues(t *testing.T) {
	var captured []string
	fakeGoalGh(t, `[
  {"number":216,"title":"orchestrator fast path","url":"https://github.com/o/r/issues/216","state":"OPEN"},
  {"number":215,"title":"goal draft","url":"https://github.com/o/r/issues/215","state":"OPEN"}
]`, nil, &captured)

	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "deliver GitHub milestone v2.7.0",
			"--repo", "omriariav/amq-squad",
			"--milestone", "v2.7.0",
			"--session", "v2-7-0",
			"--profile", "codex-v2-7-0",
			"--codex-only",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	wantArgs := []string{"issue", "list", "--repo", "omriariav/amq-squad", "--milestone", "v2.7.0", "--state", "all", "--limit", "200", "--json", "number,title,url,state"}
	if fmt.Sprint(captured) != fmt.Sprint(wantArgs) {
		t.Fatalf("gh args = %v, want %v", captured, wantArgs)
	}
	var env jsonEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode envelope: %v\n%s", err, stdout)
	}
	if env.Kind != "goal_draft" {
		t.Fatalf("kind = %q, want goal_draft", env.Kind)
	}
	raw, err := json.Marshal(env.Data)
	if err != nil {
		t.Fatal(err)
	}
	var data goalDraftData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if !data.PreviewOnly || data.Composition != "seeded" {
		t.Fatalf("draft should be preview-only seeded: %+v", data)
	}
	if data.Session != "v2-7-0" || data.Profile != "codex-v2-7-0" {
		t.Fatalf("session/profile mismatch: %+v", data)
	}
	if len(data.IssueSources) != 2 || data.IssueSources[0].Number != 215 || data.IssueSources[1].Number != 216 {
		t.Fatalf("issues not sorted/included: %+v", data.IssueSources)
	}
	for _, want := range []string{"#215 goal draft", "https://github.com/o/r/issues/215", "/goal --goal"} {
		if !strings.Contains(data.BriefSkeleton+data.OrchestratorPrompt, want) {
			t.Errorf("draft missing %q:\n%+v", want, data)
		}
	}
}

func TestGoalDraftMarkdownIsPreviewOnly(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "fix flaky launch targeting",
			"--session", "issue-225",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# preview_only: true",
		"# composition: seeded",
		"## Brief Skeleton",
		"amq send --to user --thread gate/spawn-fullstack",
		"amq-squad team init",
		"amq-squad dispatch --session issue-225",
		"Seeded composition remains the default",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("markdown missing %q:\n%s", want, stdout)
		}
	}
}

func TestGoalDraftAutonomousPreviewRequiresAndEmitsPolicy(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "deliver milestone safely",
			"--session", "v2-7-0",
			"--composition", "autonomous",
			"--max-agents", "5",
			"--max-total-spawns", "4",
			"--allowed-roles", "runtime-dev,reviewer",
			"--budget-turns", "40",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft autonomous: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.Composition != "autonomous" || env.Data.AutonomousPolicy == nil {
		t.Fatalf("autonomous policy missing: %+v", env.Data)
	}
	if env.Data.AutonomousPolicy.MaxActiveAgents != 5 || env.Data.AutonomousPolicy.MaxTotalSpawns != 4 || env.Data.AutonomousPolicy.BudgetTurns != 40 {
		t.Fatalf("autonomous policy counters mismatch: %+v", env.Data.AutonomousPolicy)
	}
	if !strings.Contains(env.Data.BriefSkeleton, "## Autonomous policy") || !strings.Contains(env.Data.OrchestratorPrompt, "--composition autonomous") {
		t.Fatalf("autonomous draft missing policy/prompt:\n%s\n%s", env.Data.BriefSkeleton, env.Data.OrchestratorPrompt)
	}
}

func TestGoalDraftAutonomousRejectsMissingPolicy(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "deliver milestone", "--composition", "autonomous"})
	})
	if err == nil || !strings.Contains(err.Error(), "max_active_agents") {
		t.Fatalf("want missing autonomous policy error, got %v", err)
	}
}

func TestGoalDraftMilestoneRequiresRepo(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "deliver v2.7.0", "--milestone", "v2.7.0"})
	})
	if err == nil || !strings.Contains(err.Error(), "--milestone requires --repo") {
		t.Fatalf("want repo requirement, got %v", err)
	}
}

func TestGoalDraftMilestoneGhErrorNamesSource(t *testing.T) {
	fakeGoalGh(t, "", errors.New("not authenticated"), nil)
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "deliver v2.7.0", "--repo", "o/r", "--milestone", "v2.7.0"})
	})
	if err == nil || !strings.Contains(err.Error(), "milestone") || !strings.Contains(err.Error(), "not authenticated") {
		t.Fatalf("error should name milestone gh source, got %v", err)
	}
}
