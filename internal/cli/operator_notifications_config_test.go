package cli

import (
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
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
