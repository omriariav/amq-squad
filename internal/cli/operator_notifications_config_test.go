package cli

import (
	"bytes"
	"crypto/sha256"
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTeamInitPersistsDefaultOperatorNotifications(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := runTeamInit([]string{"--roles", "cto", "--session", "s", "--operator-notifications"}); err != nil {
		t.Fatal(err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Operator == nil || got.Operator.Notifications == nil || len(got.Operator.Notifications.Sinks) != 1 || got.Operator.Notifications.Sinks[0].Type != "desktop" {
		t.Fatalf("persisted notification policy = %+v", got.Operator)
	}
	policy := team.EffectiveOperatorNotifications(got.Operator)
	if !policy.Enabled || policy.DeliverySemantics != "attention_only" || policy.Sinks[0].Type != "desktop" {
		t.Fatalf("effective notification policy = %+v", policy)
	}
}

func TestRunStartExistingNotificationMismatchStructured(t *testing.T) {
	dir := seedTeam(t, team.Team{Operator: func() *team.OperatorConfig { op := team.DefaultOperator(); return &op }(), Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}}})
	result := runStartPreflight(runStartPreflightInput{Project: dir, Profile: "default", ProfileExplicit: true, Session: "s", Visibility: "sibling-tabs", OperatorNotifications: true, OperatorNotificationsSet: true})
	if len(result.Issues) == 0 || result.Issues[0].Code != runStartPreflightExistingOperatorNotifications {
		t.Fatalf("issues = %+v", result.Issues)
	}
}

func TestRunStartExistingNotificationMismatchDoesNotMutateOrLaunch(t *testing.T) {
	op := team.DefaultOperator()
	dir := seedTeam(t, team.Team{
		Operator: &op, Orchestrated: true, Lead: "cto",
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	path := team.ProfilePath(dir, team.DefaultProfile)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeSHA := sha256.Sum256(before)
	backend := useFakeTmuxBackend(t)

	_, _, err = captureOutput(t, func() error {
		return runRunStart([]string{"--project", dir, "--session", "s", "--operator-notifications", "--go"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "does not match existing profile") || !strings.Contains(err.Error(), "never rewrites") {
		t.Fatalf("mismatch refusal = %v", err)
	}
	if len(backend.launches) != 0 {
		t.Fatalf("mismatch launched agents: %+v", backend.launches)
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	afterSHA := sha256.Sum256(after)
	if !bytes.Equal(after, before) || afterSHA != beforeSHA {
		t.Fatalf("existing team.json changed: before_sha256=%x after_sha256=%x", beforeSHA, afterSHA)
	}
}

func TestOperatorDeliveryShowsAttentionPolicyWithoutSecrets(t *testing.T) {
	op := team.DefaultOperator()
	op.Notifications = &team.OperatorNotificationPolicy{Enabled: true, Sinks: []team.OperatorNotificationSinkConfig{{ID: "hook", Type: "command", Argv: []string{"secret-wrapper"}}}}
	delivery := operatorDeliveryForTeam(team.Team{Operator: &op})
	if !delivery.NotificationsEnabled || delivery.NotificationSemantics != "attention_only" || len(delivery.NotificationSinkTypes) != 1 || delivery.NotificationSinkTypes[0] != "command" {
		t.Fatalf("delivery = %+v", delivery)
	}
	prompt, err := buildBootstrapPrompt(bootstrapContext{Role: "cto", Handle: "cto", Operator: team.EffectiveOperator(team.Team{Operator: &op}), OperatorDelivery: delivery, OperatorGates: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enabled=true; semantics=attention_only; sink_types=[command]", "attention-only sinks run on the operator-watch host"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("bootstrap missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "secret-wrapper") {
		t.Fatal("bootstrap leaked command argv")
	}
}
