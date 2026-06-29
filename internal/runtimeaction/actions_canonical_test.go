package runtimeaction

import "testing"

func TestMemberActionsCanonicalFields(t *testing.T) {
	actions := Member("/proj", "default", "sess", "cto", true)
	byKind := map[string]Action{}
	for _, a := range actions {
		byKind[a.Kind] = a
	}

	// ID must mirror Kind for every action.
	for _, a := range actions {
		if a.ID != a.Kind {
			t.Errorf("action %q: ID = %q, want %q", a.Kind, a.ID, a.Kind)
		}
	}

	// ActionKind must be "display" for read-only actions.
	for _, kind := range []string{"focus", "status", "task_list"} {
		if a, ok := byKind[kind]; !ok {
			t.Errorf("action %q not found in Member actions", kind)
		} else if a.ActionKind != "display" {
			t.Errorf("action %q: action_kind = %q, want display", kind, a.ActionKind)
		}
	}

	// ActionKind must be "run" for mutating actions.
	for _, kind := range []string{"send", "goal_deliver", "dispatch", "resume"} {
		if a, ok := byKind[kind]; !ok {
			t.Errorf("action %q not found in Member actions", kind)
		} else if a.ActionKind != "run" {
			t.Errorf("action %q: action_kind = %q, want run", kind, a.ActionKind)
		}
	}

	// UnavailableReason must be empty when pane is alive (all actions start available or
	// were always available).
	for _, a := range actions {
		if a.Available && a.UnavailableReason != "" {
			t.Errorf("action %q: UnavailableReason = %q, want empty (action is available)", a.Kind, a.UnavailableReason)
		}
	}
}

func TestMemberActionsCanonicalFieldsDeadPane(t *testing.T) {
	actions := Member("/proj", "default", "sess", "cto", false)
	byKind := map[string]Action{}
	for _, a := range actions {
		byKind[a.Kind] = a
	}

	// Pane-gated actions must have UnavailableReason set.
	for _, kind := range []string{"focus", "send", "goal_deliver"} {
		a, ok := byKind[kind]
		if !ok {
			t.Errorf("action %q not found in Member actions (dead pane)", kind)
			continue
		}
		if a.Available {
			t.Errorf("action %q: Available = true, want false (dead pane)", kind)
		}
		if a.UnavailableReason == "" {
			t.Errorf("action %q: UnavailableReason is empty, want non-empty (dead pane)", kind)
		}
		if a.UnavailableReason != a.Reason {
			t.Errorf("action %q: UnavailableReason = %q, want Reason = %q", kind, a.UnavailableReason, a.Reason)
		}
	}

	// Always-available actions must remain available with no UnavailableReason.
	for _, kind := range []string{"dispatch", "resume", "status", "task_list"} {
		a, ok := byKind[kind]
		if !ok {
			t.Errorf("action %q not found in Member actions (dead pane)", kind)
			continue
		}
		if !a.Available {
			t.Errorf("action %q: Available = false, want true (dead pane does not affect this action)", kind)
		}
		if a.UnavailableReason != "" {
			t.Errorf("action %q: UnavailableReason = %q, want empty (action is available)", kind, a.UnavailableReason)
		}
	}
}

func TestSessionActionsCanonicalFields(t *testing.T) {
	actions := Session("/proj", "default", "sess", "my-tmux-session")
	byKind := map[string]Action{}
	for _, a := range actions {
		byKind[a.Kind] = a
	}

	// ID mirrors Kind.
	for _, a := range actions {
		if a.ID != a.Kind {
			t.Errorf("session action %q: ID = %q, want %q", a.Kind, a.ID, a.Kind)
		}
	}

	displayActions := []string{"status", "resume_preview", "task_list", "attach_control"}
	for _, kind := range displayActions {
		if a, ok := byKind[kind]; !ok {
			t.Errorf("session action %q not found", kind)
		} else if a.ActionKind != "display" {
			t.Errorf("session action %q: action_kind = %q, want display", kind, a.ActionKind)
		}
	}

	runActions := []string{"resume_current_window", "resume_new_session", "stop", "stop_close_panes"}
	for _, kind := range runActions {
		if a, ok := byKind[kind]; !ok {
			t.Errorf("session action %q not found", kind)
		} else if a.ActionKind != "run" {
			t.Errorf("session action %q: action_kind = %q, want run", kind, a.ActionKind)
		}
	}
}

func TestThreadActionsCanonicalFields(t *testing.T) {
	actions := Thread("/proj", "default", "sess", "p2p/cto__developer")
	for _, a := range actions {
		if a.ID != a.Kind {
			t.Errorf("thread action %q: ID = %q, want %q", a.Kind, a.ID, a.Kind)
		}
		if a.ActionKind != "display" {
			t.Errorf("thread action %q: action_kind = %q, want display", a.Kind, a.ActionKind)
		}
	}
}

func TestSyncUnavailableReason(t *testing.T) {
	a := Action{Kind: "send", Available: true, Reason: "", UnavailableReason: ""}
	SyncUnavailableReason(&a)
	if a.UnavailableReason != "" {
		t.Errorf("SyncUnavailableReason on available action: UnavailableReason = %q, want empty", a.UnavailableReason)
	}

	a.Available = false
	a.Reason = "policy blocked"
	SyncUnavailableReason(&a)
	if a.UnavailableReason != "policy blocked" {
		t.Errorf("SyncUnavailableReason: UnavailableReason = %q, want %q", a.UnavailableReason, "policy blocked")
	}

	// Clearing available resets UnavailableReason.
	a.Available = true
	a.Reason = ""
	SyncUnavailableReason(&a)
	if a.UnavailableReason != "" {
		t.Errorf("SyncUnavailableReason after re-enabling: UnavailableReason = %q, want empty", a.UnavailableReason)
	}
}
