package runtimeaction

import (
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
)

func TestMemberForCapabilitiesDegradesFakeControllerActions(t *testing.T) {
	caps := runtimecontrol.TmuxCapabilities(true).
		With(runtimecontrol.CapabilitySendPrompt, false, "fake controller cannot send prompts").
		With(runtimecontrol.CapabilityGoalDeliver, false, "fake controller cannot deliver goals").
		With(runtimecontrol.CapabilityDispatch, false, "fake controller cannot dispatch")

	actions := MemberForCapabilities("/repo", "default", "issue-331", "dev", caps)
	byKind := map[string]Action{}
	for _, action := range actions {
		byKind[action.Kind] = action
	}

	if !byKind["focus"].Available {
		t.Fatalf("focus should remain available when the fake controller supports it")
	}
	for _, tc := range []struct {
		kind   string
		reason string
	}{
		{"send", "fake controller cannot send prompts"},
		{"goal_deliver", "fake controller cannot deliver goals"},
		{"dispatch", "fake controller cannot dispatch"},
	} {
		action := byKind[tc.kind]
		if action.Available || action.Reason != tc.reason || action.UnavailableReason != tc.reason {
			t.Fatalf("%s action = %+v, want unavailable reason %q", tc.kind, action, tc.reason)
		}
	}
	if !byKind["status"].Available || !byKind["resume"].Available || !byKind["task_list"].Available {
		t.Fatalf("session actions should stay available under degraded member capabilities: %+v", actions)
	}
}

func TestMemberKeepsTmuxDeadPaneCompatibility(t *testing.T) {
	actions := Member("/repo", "default", "issue-331", "dev", false)
	byKind := map[string]Action{}
	for _, action := range actions {
		byKind[action.Kind] = action
	}

	for _, kind := range []string{"focus", "send"} {
		if byKind[kind].Available || byKind[kind].Reason != "agent pane is not live" {
			t.Fatalf("%s action = %+v, want existing dead-pane behavior", kind, byKind[kind])
		}
	}
	if goal := byKind["goal_deliver"]; goal.Available || goal.Reason != "the current goal-deliver command requires a live native prompt target" {
		t.Fatalf("goal_deliver action = %+v, want executable-path failure", goal)
	}
	if !byKind["dispatch"].Available {
		t.Fatalf("dispatch must remain available for dead tmux panes")
	}
}
