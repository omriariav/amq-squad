package cli

import (
	"encoding/json"
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

func TestOperatorDeliveryShowsAttentionPolicyWithoutSecrets(t *testing.T) {
	op := team.DefaultOperator()
	op.Notifications = &team.OperatorNotificationPolicy{Enabled: true, Sinks: []team.OperatorNotificationSinkConfig{{ID: "hook", Type: "command", Argv: []string{"secret-wrapper"}}}}
	delivery := operatorDeliveryForTeam(team.Team{Operator: &op})
	if !delivery.NotificationsEnabled || delivery.NotificationSemantics != "attention_only" || len(delivery.NotificationSinkTypes) != 1 || delivery.NotificationSinkTypes[0] != "command" {
		t.Fatalf("delivery = %+v", delivery)
	}
	statusJSON, err := json.Marshal(delivery)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(statusJSON), "secret-wrapper") {
		t.Fatalf("status delivery leaked command argv: %s", statusJSON)
	}
	prompt, err := buildBootstrapPrompt(bootstrapContext{Role: "cto", Handle: "cto", Operator: team.EffectiveOperator(team.Team{Operator: &op}), OperatorDelivery: delivery, OperatorGates: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"enabled=true; semantics=attention_only; sink_types=[command]", "attention-only sinks run through the supervised scoped notification watcher"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("bootstrap missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "secret-wrapper") {
		t.Fatal("bootstrap leaked command argv")
	}
}
