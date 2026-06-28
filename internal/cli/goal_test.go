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
	if data.GoalBinding.Mode != "native_goal_pending" || !data.GoalBinding.NativeGoal || data.GoalBinding.Verified {
		t.Fatalf("goal binding mismatch: %+v", data.GoalBinding)
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
		"# visibility: sibling-tabs",
		"## Brief Skeleton",
		"amq send --to user --thread gate/spawn-fullstack",
		"amq-squad team init",
		"amq-squad agent up codex",
		"-- '/goal --goal",
		"amq-squad dispatch --profile issue-225 --session issue-225",
		"Default visibility is sibling-tabs",
		"Seeded composition remains the default",
		"Visible lead binding: native_goal_pending",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("markdown missing %q:\n%s", want, stdout)
		}
	}
}

func TestGoalDraftNamedProfileCommandsCarryNamespace(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship deterministic namespaces",
			"--session", "main",
			"--profile", "release",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.Namespace.ID != "release/main" {
		t.Fatalf("namespace = %+v, want release/main", env.Data.Namespace)
	}
	if env.Data.GoalBinding.Mode != "native_goal_pending" || env.Data.GoalBinding.Source != "orchestrator-prompt" {
		t.Fatalf("goal binding = %+v", env.Data.GoalBinding)
	}
	for _, dispatch := range env.Data.Dispatches {
		for _, want := range []string{"--profile release", "--session main"} {
			if !strings.Contains(dispatch.Command, want) {
				t.Fatalf("dispatch command missing %q: %s", want, dispatch.Command)
			}
		}
	}
	for _, mutation := range env.Data.ApplyableMutations {
		switch mutation.Title {
		case "write brief", "add t1", "add t2", "add t3":
			if !strings.Contains(mutation.Command, "--profile release") {
				t.Fatalf("%s mutation dropped profile: %s", mutation.Title, mutation.Command)
			}
		}
	}
}

func TestGoalDraftCustomLeadCarriesThroughPlan(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship release through visible lead",
			"--session", "v2-9-0-release",
			"--profile", "codex-v2-9-0",
			"--lead", "release-lead",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.Lead != "release-lead" {
		t.Fatalf("lead = %q, want release-lead", env.Data.Lead)
	}
	if env.Data.Roster[0].Role != "release-lead" {
		t.Fatalf("first roster member = %+v, want release-lead lead", env.Data.Roster[0])
	}
	for _, mutation := range env.Data.ApplyableMutations {
		if mutation.Title == "initialize profile" && !strings.Contains(mutation.Command, "--lead release-lead") {
			t.Fatalf("team init mutation dropped lead: %s", mutation.Command)
		}
	}
	for _, dispatch := range env.Data.Dispatches {
		if !strings.Contains(dispatch.Thread, "release-lead") || !strings.Contains(dispatch.Body, "release-lead over AMQ") {
			t.Fatalf("dispatch does not route reports to lead: %+v", dispatch)
		}
	}
	if !strings.Contains(env.Data.OrchestratorPrompt, "--lead release-lead") {
		t.Fatalf("orchestrator prompt dropped custom lead: %s", env.Data.OrchestratorPrompt)
	}
}

func TestGoalDraftExecutionModeContract(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraftWithVersion([]string{
			"--goal", "deliver mode-safe orchestration",
			"--session", "v2-10-0",
			"--profile", "codex-v2-10-0",
			"--lead", "release-lead",
			"--mode", "global_orchestrator",
			"--control-root", "/tmp/control",
			"--target-project-root", "/tmp/project",
			"--target-contract", "2.10.0",
			"--json",
		}, "2.9.0")
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	exec := env.Data.Execution
	if exec.Mode != executionModeGlobalOrchestrator || exec.ImplementationAllowed {
		t.Fatalf("execution = %+v, want global orchestrator without implementation", exec)
	}
	if exec.ControlRoot != "/tmp/control" || exec.TargetProjectRoot != "/tmp/project" {
		t.Fatalf("execution roots = %q/%q", exec.ControlRoot, exec.TargetProjectRoot)
	}
	if exec.MutableActor != "" || exec.ModeError == "" || !exec.PollingRequired {
		t.Fatalf("global orchestrator boundary missing: %+v", exec)
	}
	if exec.VersionCompatibility.Compatible || exec.VersionCompatibility.RunningVersion != "2.9.0" || exec.VersionCompatibility.TargetContract != "2.10.0" {
		t.Fatalf("version compatibility = %+v, want 2.9.0 older than 2.10.0", exec.VersionCompatibility)
	}
	foundInit := false
	for _, mutation := range env.Data.ApplyableMutations {
		if mutation.Title == "initialize profile" && !strings.Contains(mutation.Command, "--mode global_orchestrator") {
			t.Fatalf("initialize profile command dropped mode: %s", mutation.Command)
		}
		if mutation.Title == "initialize profile" {
			foundInit = true
		}
	}
	if !foundInit {
		t.Fatalf("initialize profile mutation missing: %+v", env.Data.ApplyableMutations)
	}
	if !strings.Contains(env.Data.OrchestratorPrompt, "--mode global_orchestrator") || !strings.Contains(env.Data.OrchestratorPrompt, "--target-contract 2.10.0") {
		t.Fatalf("orchestrator prompt dropped execution metadata: %s", env.Data.OrchestratorPrompt)
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

func TestGoalDraftJSONIncludesVisibleLaunchMutation(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runGoalDraft([]string{
			"--goal", "ship visible setup handoff",
			"--session", "visible-setup",
			"--profile", "codex-visible-setup",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[goalDraftData](t, stdout)
	if env.Data.GoalBinding.Command != env.Data.OrchestratorPrompt {
		t.Fatalf("draft binding command should be the visible lead prompt:\n%q\n%q", env.Data.GoalBinding.Command, env.Data.OrchestratorPrompt)
	}
	if got := nativeGoalBindingFromArgs([]string{env.Data.OrchestratorPrompt}); got == nil || !got.NativeGoal {
		t.Fatalf("generated visible lead prompt must be launch-record detectable as native /goal: %+v", got)
	}
	found := false
	for _, mutation := range env.Data.ApplyableMutations {
		if mutation.Title != "launch visible lead" {
			continue
		}
		found = true
		for _, want := range []string{
			"amq-squad agent up codex",
			"--session visible-setup",
			"--root .agent-mail/codex-visible-setup/visible-setup",
			"--team-profile codex-visible-setup",
			"-- '/goal --goal",
		} {
			if !strings.Contains(mutation.Command, want) {
				t.Fatalf("visible launch command missing %q: %q", want, mutation.Command)
			}
		}
		if !strings.Contains(mutation.Command, "ship visible setup handoff") {
			t.Fatalf("visible launch command = %q", mutation.Command)
		}
		if !strings.Contains(mutation.Reason, "native /goal prompt") {
			t.Fatalf("visible launch reason = %q", mutation.Reason)
		}
	}
	if !found {
		t.Fatalf("visible launch mutation missing: %+v", env.Data.ApplyableMutations)
	}
}

func TestGoalDraftVisibilityOverrides(t *testing.T) {
	cases := []struct {
		visibility string
		wantTitle  string
		wantCmd    string
	}{
		{"detached", "launch detached visible lead", "-- '/goal --goal"},
		{"current", "launch visible lead in current pane", "-- '/goal --goal"},
		{"plan", "preview visible lead launch", "--dry-run"},
	}
	for _, tc := range cases {
		t.Run(tc.visibility, func(t *testing.T) {
			stdout, stderr, err := captureOutput(t, func() error {
				return runGoalDraft([]string{
					"--goal", "ship topology",
					"--session", "topo",
					"--visibility", tc.visibility,
					"--json",
				})
			})
			if err != nil {
				t.Fatalf("goal draft: %v\nstderr:\n%s", err, stderr)
			}
			env := decodeJSONEnvelope[goalDraftData](t, stdout)
			if env.Data.Visibility != tc.visibility {
				t.Fatalf("visibility = %q, want %q", env.Data.Visibility, tc.visibility)
			}
			found := false
			for _, mutation := range env.Data.ApplyableMutations {
				if mutation.Title == tc.wantTitle {
					found = true
					if !strings.Contains(mutation.Command, tc.wantCmd) || !strings.Contains(mutation.Command, "amq-squad agent up codex") {
						t.Fatalf("command = %q, want containing %q", mutation.Command, tc.wantCmd)
					}
				}
			}
			if !found {
				t.Fatalf("mutation %q missing: %+v", tc.wantTitle, env.Data.ApplyableMutations)
			}
		})
	}
}

func TestGoalDraftRejectsUnknownVisibility(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGoalDraft([]string{"--goal", "ship topology", "--visibility", "hidden"})
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported visibility") {
		t.Fatalf("want unsupported visibility error, got %v", err)
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
