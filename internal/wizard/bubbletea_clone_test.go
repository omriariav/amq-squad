package wizard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestBubbleOffersCloneWhenSuggestionMismatchesPinnedSession mirrors the
// numbered-adapter coverage of #523's original operator complaint: an
// existing profile pinned to one workstream, requested for a different one.
func TestBubbleOffersCloneWhenSuggestionMismatchesPinnedSession(t *testing.T) {
	profile := ProfileSummary{
		Name: "release", MemberCount: 1,
		PinnedSession: "v1",
		Members:       []MemberSummary{{Role: "cto", Binary: "codex"}},
		Sessions:      []SessionSummary{discoveredFreshSession("v1", SessionSourceMemberPin, 1)},
	}
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "release"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", SessionSuggestion: "v2", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // scope
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // profile -> release
	if m.stage != stageExistingSession {
		t.Fatalf("mismatched suggestion should show the session choice list, got stage=%v", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyDown})  // move to the clone sentinel
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // select clone
	if m.stage != stageCloneSession {
		t.Fatalf("clone selection should transition to stageCloneSession, got %v", m.stage)
	}
	m.input.SetValue("v2")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageCloneProfile {
		t.Fatalf("expected stageCloneProfile after typing the new session, got %v", m.stage)
	}
	m.input.SetValue("release-v2")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageExistingOverride {
		t.Fatalf("expected stageExistingOverride after cloning, got %v", m.stage)
	}
	if m.spec.FromProfile != "release" {
		t.Fatalf("FromProfile = %q, want release", m.spec.FromProfile)
	}
	if m.spec.ProfileBranch != ProfileBranchNew {
		t.Fatalf("ProfileBranch = %q, want %q", m.spec.ProfileBranch, ProfileBranchNew)
	}
	if m.spec.Profile != "release-v2" || m.spec.Session != "v2" {
		t.Fatalf("Profile/Session = %q/%q, want release-v2/v2", m.spec.Profile, m.spec.Session)
	}
	if m.spec.Roles != "" || m.spec.Lead != "" {
		t.Fatalf("clone must not collect fresh-roster fields: %+v", m.spec)
	}
}

// TestBubbleSingleSessionMatchingSuggestionStaysFrictionless guards the same
// no-added-friction guarantee as the numbered adapter: a single known session
// that already matches the desired one needs no extra screen.
func TestBubbleSingleSessionMatchingSuggestionStaysFrictionless(t *testing.T) {
	profile := ProfileSummary{
		Name: "release", MemberCount: 1,
		PinnedSession: "v1",
		Members:       []MemberSummary{{Role: "cto", Binary: "codex"}},
		Sessions:      []SessionSummary{discoveredFreshSession("v1", SessionSourceMemberPin, 1)},
	}
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "release"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", SessionSuggestion: "v1", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // scope
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // profile -> release
	if m.stage == stageExistingSession {
		t.Fatalf("matching suggestion must not show a session choice screen")
	}
	if m.spec.Session != "v1" || m.spec.FromProfile != "" {
		t.Fatalf("expected frictionless accept of the matching known session, got %+v", m.spec)
	}
}

// TestBubbleTemplateProfileAcceptsTypedSession covers #451: an unpinned
// template profile routes straight to a free-text session prompt, with no
// clone involved.
func TestBubbleTemplateProfileAcceptsTypedSession(t *testing.T) {
	profile := ProfileSummary{Name: "pm-squad", MemberCount: 1, Unpinned: true, Members: []MemberSummary{{Role: "cto", Binary: "codex"}}}
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "pm-squad"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // scope
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // profile -> pm-squad
	if m.stage != stageTemplateSession {
		t.Fatalf("template profile should route to stageTemplateSession, got %v", m.stage)
	}
	m.input.SetValue("issue-42")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.Session != "issue-42" {
		t.Fatalf("Session = %q, want issue-42", m.spec.Session)
	}
	if m.spec.FromProfile != "" || m.spec.Profile != "pm-squad" {
		t.Fatalf("template launch must not clone: Profile=%q FromProfile=%q", m.spec.Profile, m.spec.FromProfile)
	}
}

// TestBubbleProfileChoicesPresentTemplateFraming covers #451 item 3: the
// profile picker's choice list should frame an unpinned profile as a
// reusable template.
func TestBubbleProfileChoicesPresentTemplateFraming(t *testing.T) {
	profile := ProfileSummary{Name: "pm-squad", MemberCount: 2, Unpinned: true, Members: []MemberSummary{{Role: "cto", Binary: "codex"}, {Role: "fullstack", Binary: "claude"}}}
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "pm-squad"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", SessionSuggestion: "issue-42", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // scope
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	found := false
	for _, c := range m.choices() {
		if c.value == "pm-squad" && strings.Contains(c.label, "template — launch for a new workstream") {
			found = true
		}
	}
	if !found {
		t.Fatalf("profile choices did not present pm-squad as a template: %+v", m.choices())
	}
}
