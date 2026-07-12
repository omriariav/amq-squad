package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
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
	profiles, err := runStartWizardProfiles(dir, "issue-393")
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

func TestRunStartWizardProfilesDiscoversSuggestedFirstWithPlannerFingerprint(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	if err := team.WriteProfile(dir, "review", team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner,
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}},
	}); err != nil {
		t.Fatal(err)
	}
	profiles, err := runStartWizardProfiles(dir, "issue-444")
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || len(profiles[0].Sessions) != 1 {
		t.Fatalf("suggested-first discovery = %+v", profiles)
	}
	summary := profiles[0].Sessions[0]
	if summary.Name != "issue-444" || summary.Source != runwizard.SessionSourceSuggestedFirst || summary.Fingerprint == "" {
		t.Fatalf("suggested-first identity = %+v", summary)
	}
	if summary.Classification.State != runwizard.RunStateNotStarted || summary.Classification.Backend != runwizard.BackendRunStart || !summary.Classification.Executable || summary.Fresh != 1 {
		t.Fatalf("suggested-first planner classification = %+v", summary)
	}
	tm, err := team.ReadProfile(dir, "review")
	if err != nil {
		t.Fatal(err)
	}
	expected := discoverRunStartWizardSession(tm, "review", "issue-444", runwizard.SessionSourceSuggestedFirst, nil, nil)
	wrongHistorySet := discoverRunStartWizardSession(tm, "review", "issue-444", runwizard.SessionSourceSuggestedFirst, []string{"issue-444"}, nil)
	if summary.Fingerprint != expected.Fingerprint || summary.Fingerprint == wrongHistorySet.Fingerprint {
		t.Fatalf("suggested-first fingerprint did not preserve empty actual-history set: summary=%s expected=%s wrong=%s", summary.Fingerprint, expected.Fingerprint, wrongHistorySet.Fingerprint)
	}
}

func TestDiscoverWizardSessionCarriesRecordCountAndStructuredMemberEvidence(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	oldPlan := runStartWizardPlanMemberResume
	t.Cleanup(func() { runStartWizardPlanMemberResume = oldPlan })
	runStartWizardPlanMemberResume = func(in memberPlanInput) (resumePlan, error) {
		plan := resumePlan{Role: in.Member.Role}
		switch in.Member.Role {
		case "cto":
			plan.Action, plan.HasRestoreRecord, plan.SavedLaunchIdentity = resumeLive, true, "live-record"
			plan.Saved = &resumeSavedLaunchSummary{Binary: "codex", Model: "saved-live", Effort: "high", NativeArgs: []string{"codex", "--saved-live"}}
		case "qa":
			plan.Action, plan.HasRestoreRecord, plan.SavedLaunchIdentity = resumeRestore, true, "restore-record"
			plan.Saved = &resumeSavedLaunchSummary{Binary: "claude", Model: "saved-restore", Effort: "medium", NativeArgs: []string{"claude", "--saved-restore"}}
		default:
			plan.Action = resumeFresh
		}
		return plan, nil
	}
	tm := team.Team{Project: dir, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}, {Role: "qa", Handle: "qa", Binary: "claude", Session: "s"}, {Role: "dev", Handle: "dev", Binary: "codex", Session: "s", Model: "stored-fresh"}}}
	summary := discoverRunStartWizardSession(tm, team.DefaultProfile, "s", runwizard.SessionSourceMemberPin, []string{"s"}, nil)
	if summary.RecordCount != 2 || !summary.Classification.RestoreExisting || summary.Classification.Backend != runwizard.BackendResume || len(summary.Members) != 3 {
		t.Fatalf("summary=%+v", summary)
	}
	if summary.Members[0].Action != runwizard.MemberActionLive || summary.Members[0].SavedLaunchIdentity != "live-record" || summary.Members[0].SavedModel != "saved-live" || summary.Members[0].SavedEffort != "high" || len(summary.Members[0].SavedNativeArgs) != 2 {
		t.Fatalf("live structured evidence=%+v", summary.Members[0])
	}
	if summary.Members[1].Action != runwizard.MemberActionRestore || summary.Members[1].SavedLaunchIdentity != "restore-record" || summary.Members[1].SavedBinary != "claude" {
		t.Fatalf("restore structured evidence=%+v", summary.Members[1])
	}
	if summary.Members[2].Action != runwizard.MemberActionFresh || summary.Members[2].Model != "stored-fresh" || summary.Members[2].SavedLaunchIdentity != "" {
		t.Fatalf("fresh structured evidence=%+v", summary.Members[2])
	}
	summary.Members[0].SavedNativeArgs[0] = "mutated"
	again := discoverRunStartWizardSession(tm, team.DefaultProfile, "s", runwizard.SessionSourceMemberPin, []string{"s"}, nil)
	if again.Members[0].SavedNativeArgs[0] != "codex" {
		t.Fatalf("saved argv was not deep-copied: %+v", again.Members[0])
	}
}

func TestRunStartWizardSuggestedFirstBlocksPlannerAndNamespaceFailures(t *testing.T) {
	t.Run("planner blocked", func(t *testing.T) {
		dir := t.TempDir()
		setupFakeAMQSessionRoots(t)
		oldPlan := runStartWizardPlanMemberResume
		t.Cleanup(func() { runStartWizardPlanMemberResume = oldPlan })
		runStartWizardPlanMemberResume = func(in memberPlanInput) (resumePlan, error) {
			return resumePlan{}, fmt.Errorf("planner inspection failed for %s", in.Member.Role)
		}
		if err := team.WriteProfile(dir, "review", team.Team{
			Project: dir, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}},
		}); err != nil {
			t.Fatal(err)
		}
		profiles, err := runStartWizardProfiles(dir, "issue-444")
		if err != nil {
			t.Fatal(err)
		}
		summary := profiles[0].Sessions[0]
		if summary.Source != runwizard.SessionSourceSuggestedFirst || summary.Fingerprint == "" || summary.Classification.State != runwizard.RunStateBlocked || summary.Classification.Executable || summary.Classification.Backend == runwizard.BackendRunStart || summary.Blocked == 0 {
			t.Fatalf("planner-blocked suggested-first = %+v", summary)
		}
	})

	t.Run("namespace blocked", func(t *testing.T) {
		dir := t.TempDir()
		setupFakeAMQSessionRoots(t)
		if err := team.WriteProfile(dir, "review", team.Team{Project: dir, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}}}); err != nil {
			t.Fatal(err)
		}
		legacy := filepath.Join(dir, ".agent-mail", "issue-444", "agents", "legacy")
		if err := os.MkdirAll(legacy, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(legacy, "inbox"), []byte("durable\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		profiles, err := runStartWizardProfiles(dir, "issue-444")
		if err != nil {
			t.Fatal(err)
		}
		summary := profiles[0].Sessions[0]
		if summary.Fingerprint == "" || summary.Classification.State != runwizard.RunStateBlocked || summary.Classification.Executable || summary.Classification.Backend == runwizard.BackendRunStart || summary.Blocked == 0 {
			t.Fatalf("namespace-blocked suggested-first = %+v", summary)
		}
	})
}

func TestRunStartWizardSuggestedFirstBlocksMixedPinnedSubset(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	if err := team.WriteProfile(dir, "review", team.Team{Project: dir, Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: "other"},
		{Role: "qa", Handle: "qa", Binary: "codex"},
	}}); err != nil {
		t.Fatal(err)
	}
	profiles, err := runStartWizardProfiles(dir, "issue-444")
	if err != nil {
		t.Fatal(err)
	}
	summary := profiles[0].Sessions[0]
	if summary.Classification.State != runwizard.RunStateBlocked || summary.Classification.Executable || summary.Blocked == 0 || summary.Fingerprint == "" {
		t.Fatalf("mixed-pin suggested-first executed subset: %+v", summary)
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
