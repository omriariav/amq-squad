package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestInspectRunStartWizardProjectFindsGitRootOriginAndBranchSession(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.MkdirAll(filepath.Join(gitDir, "refs", "heads", "feat"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/feat/393-wizard\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n\turl = git@github.com:omriariav/amq-squad.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, err := inspectRunStartWizardProject(nested)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Project != root || ctx.OriginSlug != "omriariav/amq-squad" || ctx.Branch != "feat/393-wizard" || ctx.SessionSuggestion != "issue-393" || ctx.NewProfileSuggestion != "squad-issue-393" {
		t.Fatalf("context = %+v", ctx)
	}
}

func TestSuggestRunStartSessionPriority(t *testing.T) {
	if got := suggestRunStartSession("release/v2.19.0", "/repo/app"); got != "v2-19-0" {
		t.Fatalf("version suggestion = %q", got)
	}
	if got := suggestRunStartSession("fix/390-notify", "/repo/app"); got != "issue-390" {
		t.Fatalf("issue suggestion = %q", got)
	}
	if got := suggestRunStartSession("main", "/repo/My App"); got != "my-app" {
		t.Fatalf("project suggestion = %q", got)
	}
}

func TestRunStartWizardProfilesExposePinnedRosterFacts(t *testing.T) {
	dir := t.TempDir()
	op := team.DefaultOperator()
	op.Notifications = &team.OperatorNotificationPolicy{Enabled: true, DeliverySemantics: "attention_only"}
	if err := team.WriteProfile(dir, "review", team.Team{
		Project:      dir,
		Orchestrated: true,
		Lead:         "cto",
		LeadMode:     team.LeadModePlanner,
		Operator:     &op,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Session: "issue-393", CodexArgs: []string{"-c", "model_reasoning_effort=high"}},
			{Role: "qa", Binary: "claude", Session: "issue-393", ClaudeArgs: []string{"--effort", "medium"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	profiles, err := runStartWizardProfiles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Name != "review" || profiles[0].PinnedSession != "issue-393" || profiles[0].LeadMode != "planner" || !profiles[0].OperatorNotifications {
		t.Fatalf("profiles = %+v", profiles)
	}
	if profiles[0].Members[0].Effort != "high" || profiles[0].Members[1].Effort != "medium" {
		t.Fatalf("member efforts = %+v", profiles[0].Members)
	}
}

func TestClassifyRunStartWizardExistingProfileUsesSharedPlannerActions(t *testing.T) {
	got := classifyRunStartWizardExistingProfile(2, 0, []resumePlan{{Action: resumeLive}, {Action: resumeFresh}}, false)
	if got.State != "partly_running" || got.Backend != "resume" || !got.Executable || got.RestoreExisting {
		t.Fatalf("live+fresh/no-record classification = %+v", got)
	}
	blocked := classifyRunStartWizardExistingProfile(1, 1, []resumePlan{{Action: resumeBlocked}}, false)
	if blocked.State != "blocked" || blocked.Executable {
		t.Fatalf("blocked planner action = %+v", blocked)
	}
}

func TestRunStartWizardBriefDiscoveryCarriesSeedProvenanceAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "brief.md")
	body := "---\nsource: issue:431\ngenerated_at: 2026-07-12T10:00:00Z\ngenerator: deterministic\n---\n\n# Goal\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := runStartWizardBriefDiscovery(path)
	if got.Path != path || got.Source != "issue:431" || got.Provenance != "source:issue:431|generated_at:2026-07-12T10:00:00Z|generator:deterministic" || got.ContentDigest == "" {
		t.Fatalf("brief discovery = %+v", got)
	}
}
