package wizard

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// flattenBubbleView strips the panel border and collapses the word-wrap that
// lipgloss applies, so tests can assert full phrases regardless of wrap points.
func flattenBubbleView(view string) string {
	replaced := strings.NewReplacer("│", " ", "╭", " ", "╮", " ", "╰", " ", "╯", " ", "─", " ").Replace(view)
	return strings.Join(strings.Fields(replaced), " ")
}

func TestBubbleModelStartsWithProjectDefaultsAndPhaseRail(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{
		Project: "/repo", Profile: "default", Session: "issue-393", Visibility: "sibling-tabs",
	}})
	if err != nil {
		t.Fatal(err)
	}
	view := m.View()
	for _, want := range []string{
		"run start control deck", "◆ 1 Project", "○ 2 Team", "Choose the project root", "/repo",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("initial view missing %q:\n%s", want, view)
		}
	}
}

func TestBubbleSelfOperatorAllowBackDeselectReselect(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Lead: "cto"}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageOperator
	m.configureStage()
	m.cursor = 3
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageSelfOperatorAllow || m.spec.SelfOperatorAllow != "" {
		t.Fatal("allowlist was preselected")
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.SelfOperatorAllow != "merge" || m.stage != stageSelfOperatorAllow {
		t.Fatal("merge not selected")
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.spec.SelfOperatorAllow != "" {
		t.Fatal("back did not restore zero selection")
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageOperatorNotifications || m.spec.SelfOperatorAllow != "merge" {
		t.Fatal("reselect/continue failed")
	}
}

func TestBubbleModelExistingProfileOverridesAndExplicitNotificationMismatchArePreserved(t *testing.T) {
	profile := ProfileSummary{
		Name: "review", MemberCount: 1, PinnedSession: "review-work", Lead: "cto", LeadMode: "planner", OperatorMode: "separate_terminal",
		Members: []MemberSummary{{Role: "cto", Binary: "codex", Model: "stored-model", Effort: "medium"}}, Sessions: []SessionSummary{discoveredFreshSession("review-work", SessionSourceMemberPin, 1)},
	}
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "review", Visibility: "sibling-tabs", OperatorNotifications: true, OperatorNotificationsRequested: true, OperatorNotificationsSet: true},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // profile
	if m.stage != stageExistingOverride {
		t.Fatalf("stage = %v, want existing override", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageExistingModel {
		t.Fatalf("stage = %v, want existing model", m.stage)
	}
	m.cursor = 3 // custom: type a model name
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageExistingModelCustom {
		t.Fatalf("stage = %v, want existing model custom", m.stage)
	}
	m.input.SetValue("launch-model")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.cursor = 3 // high
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageTopology || m.spec.OperatorMode != "separate_terminal" {
		t.Fatalf("topology stage = %v mode %q", m.stage, m.spec.OperatorMode)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageLayoutPreset {
		t.Fatalf("stage = %v, want layout", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageOperator {
		t.Fatalf("stage = %v, want operator", m.stage)
	}
	operatorView := flattenBubbleView(m.View())
	for _, want := range []string{"Self-operator / delegated approval", "locked: the stored profile contract decides", "Change it with 'amq-squad team operator set'"} {
		if !strings.Contains(operatorView, want) {
			t.Fatalf("existing operator view missing %q:\n%s", want, operatorView)
		}
	}
	if strings.Contains(operatorView, "ships in") {
		t.Fatalf("shipped capability still advertised as future:\n%s", operatorView)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageOperatorNotifications {
		t.Fatalf("stage = %v, want operator notifications", m.stage)
	}
	if !strings.Contains(m.View(), "authoritative policy") {
		t.Fatalf("notification view should identify authoritative policy:\n%s", m.View())
	}
	if m.spec.OperatorNotifications {
		t.Fatal("disabled authoritative policy changed to enabled")
	}
	if !m.spec.OperatorNotificationsRequested || !m.spec.OperatorNotificationsSet {
		t.Fatalf("explicit notification prefill setness was lost before authoritative mismatch check: %+v", m.spec)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageLauncherPane {
		t.Fatalf("stage = %v, want launcher", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageGoal {
		t.Fatalf("stage = %v, want goal", m.stage)
	}
	if m.spec.Model != "cto=launch-model" || m.spec.Effort != "cto=high" {
		t.Fatalf("launch overrides = model %q effort %q", m.spec.Model, m.spec.Effort)
	}
	if profile.Members[0].Model != "stored-model" || profile.Members[0].Effort != "medium" {
		t.Fatalf("source profile mutated: %+v", profile.Members[0])
	}
}

func TestBubbleModelPinnedSessionIsDerivedWithoutFreeText(t *testing.T) {
	profile := ProfileSummary{
		Name: "default", MemberCount: 1, PinnedSession: "issue-136",
		Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{discoveredFreshSession("issue-136", SessionSourceMemberPin, 1)},
	}
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "default"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // profile: default
	if m.stage != stageExistingOverride || m.spec.Session != "issue-136" {
		t.Fatalf("pinned session was not derived: stage=%v session=%q", m.stage, m.spec.Session)
	}
	if m.isTextStage() {
		t.Fatal("pinned existing profile reached a text-input stage")
	}
}

func TestWizardAdaptersConsumeSameSuggestedFirstSummary(t *testing.T) {
	summary := discoveredFreshSession("issue-444", SessionSourceSuggestedFirst, 1)
	profile := ProfileSummary{Name: "review", MemberCount: 1, Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{summary}}
	ctx := ProjectContext{Project: "/repo", SessionSuggestion: "issue-444", Profiles: []ProfileSummary{profile}}
	inspect := func(string) (ProjectContext, error) { return ctx, nil }

	numbered, err := RunNumbered(strings.NewReader(strings.Repeat("\n", 12)), &bytes.Buffer{}, NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "review", Visibility: "sibling-tabs"}, InspectProject: inspect,
	})
	if err != nil {
		t.Fatal(err)
	}
	bubble, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "review"}, InspectProject: inspect})
	if err != nil {
		t.Fatal(err)
	}
	bubble = updateBubble(t, bubble, tea.KeyMsg{Type: tea.KeyEnter})
	bubble = updateBubble(t, bubble, tea.KeyMsg{Type: tea.KeyEnter})
	for name, got := range map[string]Spec{"numbered": numbered, "bubble": bubble.spec} {
		if got.Session != summary.Name || got.SessionSource != summary.Source || got.DiscoveryFingerprint != summary.Fingerprint || got.RunState != summary.Classification.State || got.Backend != summary.Classification.Backend || got.RunExecutable != summary.Classification.Executable {
			t.Fatalf("%s adapter did not consume exact summary: %+v want=%+v", name, got, summary)
		}
	}
}

func TestBubbleExistingProfileWithoutCLIDiscoveryFailsClosed(t *testing.T) {
	ctx := ProjectContext{Project: "/repo", SessionSuggestion: "issue-444", Profiles: []ProfileSummary{{Name: "review", MemberCount: 1}}}
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "review"}, InspectProject: func(string) (ProjectContext, error) { return ctx, nil }})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.done || m.err == nil || m.spec.RunState != RunStateBlocked || m.spec.RunExecutable || m.spec.Backend != "" || m.spec.DiscoveryFingerprint != "" {
		t.Fatalf("empty discovery did not fail closed: done=%t err=%v spec=%+v", m.done, m.err, m.spec)
	}
}

func TestBubbleMultipleKnownSessionsUseListAndBackDropsStaleRun(t *testing.T) {
	profile := ProfileSummary{Name: "release", MemberCount: 1, Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{
		{Name: "history-a", Source: SessionSourceLaunchHistory, Fingerprint: "a", Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true}, Fresh: 1},
		{Name: "history-b", Source: SessionSourceLaunchHistory, Fingerprint: "b", Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true}, Fresh: 1},
	}}
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "release"}, InspectProject: func(string) (ProjectContext, error) {
		return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // profile
	if m.stage != stageExistingSession || m.isTextStage() {
		t.Fatalf("multiple sessions did not use selection list: stage=%v", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // history-a
	if m.spec.Session != "history-a" || m.stage != stageExistingOverride {
		t.Fatalf("first selection = stage %v spec %+v", m.stage, m.spec)
	}
	m.spec.Goal = "must-not-return"
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.stage != stageExistingSession || m.spec.Goal != "" || m.spec.Session != "" {
		t.Fatalf("back restored incompatible run answers: stage=%v spec=%+v", m.stage, m.spec)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.Session != "history-b" || m.spec.DiscoveryFingerprint != "b" {
		t.Fatalf("second selection = %+v", m.spec)
	}
}

func TestBubbleBackRefreshRejectsChangedDiscoveryAndRetainsB(t *testing.T) {
	calls := 0
	ctxA := bubbleBackContext("reviewed-a", "context-a")
	ctxB := bubbleBackContext("reviewed-b", "context-b")
	m := bubbleAtExistingModel(t, func(string) (ProjectContext, error) {
		calls++
		if calls < 3 {
			return ctxA, nil
		}
		return ctxB, nil
	})
	m.spec.Goal, m.spec.Visibility, m.spec.Model = "stale goal", "current", "cto=stale"
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if calls != 3 || m.stage != stageProfile || m.ctx.OriginSlug != "context-b" || m.spec.Session != "" || m.spec.Backend != "" || m.spec.RunExecutable || m.spec.Goal != "" || m.spec.Visibility != "" || m.spec.Model != "" || m.spec.OperatorMode != "" || m.err == nil || len(m.history) != 0 || m.existingIndex != 0 {
		t.Fatalf("changed refresh was not rejected safely: calls=%d stage=%v ctx=%+v spec=%+v err=%v history=%d index=%d", calls, m.stage, m.ctx, m.spec, m.err, len(m.history), m.existingIndex)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ctx.OriginSlug != "context-b" {
		t.Fatalf("subsequent Back resurrected context A: %+v", m.ctx)
	}
}

func TestBubbleBackRefreshRestoresCompatibleSnapshotAndRetainsFreshA(t *testing.T) {
	calls := 0
	m := bubbleAtExistingModel(t, func(string) (ProjectContext, error) {
		calls++
		return bubbleBackContext("reviewed-a", fmt.Sprintf("context-a-%d", calls)), nil
	})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if calls != 3 || m.stage != stageExistingOverride || m.spec.Session != "s" || m.spec.DiscoveryFingerprint != "reviewed-a" || m.ctx.OriginSlug != "context-a-3" || m.existingIndex != 0 || m.err != nil {
		t.Fatalf("compatible refresh did not restore snapshot: calls=%d stage=%v ctx=%+v spec=%+v err=%v", calls, m.stage, m.ctx, m.spec, m.err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ctx.OriginSlug != "context-a-3" {
		t.Fatalf("subsequent Back resurrected pre-refresh context: %+v", m.ctx)
	}
}

func TestBubbleBackRefreshErrorRejectsAndRetainsLastKnownContext(t *testing.T) {
	calls := 0
	m := bubbleAtExistingModel(t, func(string) (ProjectContext, error) {
		calls++
		if calls == 3 {
			return ProjectContext{}, errors.New("discovery unavailable")
		}
		return bubbleBackContext("reviewed-a", fmt.Sprintf("context-a-%d", calls)), nil
	})
	m.spec.Goal, m.spec.OperatorMode = "stale goal", "lead_pane"
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if calls != 3 || m.stage != stageProject || m.ctx.OriginSlug != "context-a-2" || m.spec.Session != "" || m.spec.Goal != "" || m.spec.OperatorMode != "" || m.err == nil || !strings.Contains(m.err.Error(), "discovery unavailable") || len(m.history) != 0 || m.input.Value() != "/repo" {
		t.Fatalf("refresh error did not fail closed: calls=%d stage=%v ctx=%+v spec=%+v err=%v history=%d input=%q", calls, m.stage, m.ctx, m.spec, m.err, len(m.history), m.input.Value())
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ctx.OriginSlug != "context-a-2" {
		t.Fatalf("subsequent Back changed last-known context: %+v", m.ctx)
	}
}

func TestSnapshotCompatibleFailsClosedOnMissingFingerprints(t *testing.T) {
	current := bubbleBackContext("current", "context")
	base := bubbleSnapshot{spec: Spec{Project: "/repo", Profile: "release", ProfileBranch: ProfileBranchExisting, Session: "s", DiscoveryFingerprint: "current"}}
	tests := []struct {
		name     string
		snapshot bubbleSnapshot
		current  ProjectContext
		want     bool
	}{
		{name: "same fingerprint", snapshot: base, current: current, want: true},
		{name: "saved fingerprint empty", snapshot: bubbleSnapshot{spec: Spec{Profile: "release", ProfileBranch: ProfileBranchExisting, Session: "s"}}, current: current},
		{name: "current fingerprint empty", snapshot: base, current: bubbleBackContext("", "context")},
		{name: "changed fingerprint", snapshot: base, current: bubbleBackContext("changed", "context")},
		{name: "missing profile", snapshot: base, current: ProjectContext{Project: "/repo"}},
		{name: "missing session", snapshot: base, current: ProjectContext{Project: "/repo", Profiles: []ProfileSummary{{Name: "release"}}}},
		{name: "preselection", snapshot: bubbleSnapshot{spec: Spec{Profile: "release", ProfileBranch: ProfileBranchExisting}}, current: current, want: true},
		{name: "new profile", snapshot: bubbleSnapshot{spec: Spec{ProfileBranch: ProfileBranchNew}}, current: current, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := snapshotCompatible(tt.snapshot, tt.current); got != tt.want {
				t.Fatalf("snapshotCompatible=%t want=%t", got, tt.want)
			}
		})
	}
}

func bubbleBackContext(fingerprint, origin string) ProjectContext {
	return ProjectContext{Project: "/repo", OriginSlug: origin, Profiles: []ProfileSummary{{
		Name: "release", MemberCount: 1, OperatorMode: "lead_pane", Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{{
			Name: "s", Source: SessionSourceMemberPin, Fingerprint: fingerprint, Classification: RunClassification{State: RunStateNotStarted, Backend: BackendRunStart, Executable: true}, Fresh: 1,
		}},
	}}}
}

func bubbleAtExistingModel(t *testing.T, inspect func(string) (ProjectContext, error)) BubbleModel {
	t.Helper()
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "release"}, InspectProject: inspect})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageExistingModel {
		t.Fatalf("fixture stage=%v spec=%+v", m.stage, m.spec)
	}
	return m
}

func TestBubbleNewProfileUsesProfileAndSessionSuggestions(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo"}, InspectProject: func(string) (ProjectContext, error) {
		return ProjectContext{Project: "/repo", SessionSuggestion: "issue-431", NewProfileSuggestion: "squad-issue-431", Profiles: []ProfileSummary{{Name: "default", MemberCount: 1, PinnedSession: "main"}}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.cursor = len(m.choices()) - 1
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageNewProfile || m.input.Value() != "squad-issue-431" {
		t.Fatalf("new profile suggestion = stage %v input %q", m.stage, m.input.Value())
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageSession || m.input.Value() != "issue-431" {
		t.Fatalf("new session suggestion = stage %v input %q", m.stage, m.input.Value())
	}
}

func TestBubbleFreshAndNamedOnlyProjectsUseSingleNewProfilePath(t *testing.T) {
	tests := []struct {
		name     string
		profiles []ProfileSummary
	}{
		{name: "zero profiles"},
		{name: "named only", profiles: []ProfileSummary{{Name: "release", MemberCount: 1, PinnedSession: "main"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "brand-new", Session: "explicit-session"}, InspectProject: func(string) (ProjectContext, error) {
				return ProjectContext{Project: "/repo", SessionSuggestion: "suggested-session", NewProfileSuggestion: "squad-suggested-session", Profiles: tc.profiles}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if len(m.choices()) != len(tc.profiles)+1 || m.choices()[len(m.choices())-1].value != "__create__" {
				t.Fatalf("profile choices include a synthetic fresh row: %+v", m.choices())
			}
			if m.cursor != len(m.choices())-1 {
				t.Fatalf("unknown prefill cursor=%d choices=%+v", m.cursor, m.choices())
			}
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.stage != stageNewProfile || m.input.Value() != "brand-new" || m.spec.ProfileBranch != ProfileBranchNew {
				t.Fatalf("new profile path = stage %v input %q spec %+v", m.stage, m.input.Value(), m.spec)
			}
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if m.stage != stageSession || m.input.Value() != "explicit-session" {
				t.Fatalf("new session path = stage %v input %q", m.stage, m.input.Value())
			}
		})
	}
}

func TestBubbleModelHydratesEnabledAuthoritativeNotifications(t *testing.T) {
	profile := ProfileSummary{
		Name: "review", MemberCount: 1, PinnedSession: "review-work", OperatorMode: "noc", OperatorNotifications: true,
		Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{discoveredFreshSession("review-work", SessionSourceMemberPin, 1)},
	}
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/repo", Profile: "review", Visibility: "sibling-tabs"},
		InspectProject: func(string) (ProjectContext, error) {
			return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 5 { // project, profile, session, topology, layout
		m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if m.stage != stageOperator || m.spec.OperatorMode != "noc" || !m.spec.OperatorNotifications {
		t.Fatalf("hydrated operator state = stage %v spec %+v", m.stage, m.spec)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageOperatorNotifications || !m.spec.OperatorNotifications {
		t.Fatalf("notification stage = %v spec %+v", m.stage, m.spec)
	}
	view := m.View()
	if !strings.Contains(view, "authoritative policy") || !strings.Contains(view, "enabled=true") || strings.Contains(view, "No notifications") {
		t.Fatalf("authoritative notification view offered mutation:\n%s", view)
	}
	if !strings.Contains(m.summary(), "notifications=true") || strings.Count(strings.Join(m.spec.Args(), " "), "--operator-notifications") != 1 {
		t.Fatalf("notification state not preserved in summary/args: summary=%q args=%q", m.summary(), m.spec.Args())
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageLauncherPane || !m.spec.OperatorNotifications {
		t.Fatalf("authoritative continue mutated policy: stage=%v spec=%+v", m.stage, m.spec)
	}
}

func TestBubbleModelCapabilityRowsAreGatedByInjectedCatalog(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageOperator
	m.configureStage()
	m.cursor = 3
	view := m.View()
	if !strings.Contains(view, "Self-operator / delegated approval") {
		t.Fatalf("capability missing from view:\n%s", view)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageSelfOperatorAllow || m.spec.OperatorMode != "self_operator" {
		t.Fatalf("default capability selection failed: stage=%v err=%v spec=%+v", m.stage, m.err, m.spec)
	}

	caps := DefaultCapabilities()
	capability := caps[CapabilitySelfOperator]
	capability.Available = true
	caps[CapabilitySelfOperator] = capability
	m, err = NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo"}, Capabilities: caps})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageOperator
	m.configureStage()
	m.cursor = 3
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageSelfOperatorAllow || m.spec.OperatorMode != "self_operator" {
		t.Fatalf("enabled selection = stage %v mode %q", m.stage, m.spec.OperatorMode)
	}
}

func TestBubbleExistingSelfOperatorShowsAuthoritativePolicy(t *testing.T) {
	p := ProfileSummary{Name: "default", OperatorMode: "self_operator", OperatorNotifications: true, SelfOperatorLead: "cto", SelfOperatorAllow: "merge", SelfOperatorRevision: 7, SelfOperatorPaused: true}
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", OperatorMode: "self_operator", OperatorNotifications: true}, InspectProject: func(string) (ProjectContext, error) {
		return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{p}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	m.existingIndex = 0
	m.stage = stageOperator
	m.configureStage()
	view := m.View()
	for _, want := range []string{"lead=cto", "allow=merge", "revision=7", "paused=true", "notifications=true"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q: %s", want, view)
		}
	}
}

func TestBubbleModelDetachedDefaultsSeparateOperatorTerminal(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Visibility: "detached"}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageOperator
	m.configureStage()
	if m.cursor != 1 {
		t.Fatalf("detached operator cursor = %d, want separate terminal", m.cursor)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.OperatorMode != "separate_terminal" || m.stage != stageOperatorNotifications {
		t.Fatalf("detached operator selection = stage %v mode %q", m.stage, m.spec.OperatorMode)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageLauncherPane {
		t.Fatalf("notification stage = %v, want launcher", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageGoal || m.spec.LauncherPane != "keep" {
		t.Fatalf("detached launcher = stage %v policy %q", m.stage, m.spec.LauncherPane)
	}
}

func TestBubbleModelBackRestoresChoiceCursor(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Visibility: "sibling-tabs"}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageTopology
	m.configureStage()
	m.cursor = 2
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageLayoutPreset {
		t.Fatalf("stage = %v, want layout", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.stage != stageTopology || m.cursor != 2 || m.spec.Visibility != "sibling-tabs" {
		t.Fatalf("back state = stage %v cursor %d visibility %q", m.stage, m.cursor, m.spec.Visibility)
	}
}

func TestBubbleModelBackRestoresProjectContext(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{
		Defaults: Spec{Project: "/one"},
		InspectProject: func(project string) (ProjectContext, error) {
			return ProjectContext{Project: project, OriginSlug: strings.TrimPrefix(project, "/")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m.input.SetValue("/two")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ctx.OriginSlug != "two" {
		t.Fatalf("new context = %+v", m.ctx)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.stage != stageProject || m.spec.Project != "/one" || m.ctx.OriginSlug != "one" || m.input.Value() != "/two" {
		t.Fatalf("restored project state = stage %v spec %+v ctx %+v input %q", m.stage, m.spec, m.ctx, m.input.Value())
	}
}

func TestBubbleModelBlankExistingModelRemovesEarlierLaunchOverride(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Model: "cto=launch-model"}})
	if err != nil {
		t.Fatal(err)
	}
	m.ctx.Profiles = []ProfileSummary{{Name: "review", Members: []MemberSummary{{Role: "cto", Binary: "codex"}}}}
	m.existingIndex = 0
	m.stage = stageExistingModelCustom
	m.configureStage()
	m.input.SetValue("")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.Model != "" {
		t.Fatalf("cleared model override = %q", m.spec.Model)
	}
}

func TestBubbleModelOffersModelsPerBinaryWithCustomEscape(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Roles: "cto", Binary: "cto=claude"}})
	if err != nil {
		t.Fatal(err)
	}
	m.roleOrder = []string{"cto"}
	m.stage = stageRoleModel
	m.configureStage()
	view := flattenBubbleView(m.View())
	for _, want := range []string{"automatic", "fable", "opus", "sonnet", "haiku", "custom"} {
		if !strings.Contains(view, want) {
			t.Fatalf("claude model list missing %q:\n%s", want, view)
		}
	}
	m.cursor = 3 // sonnet
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageRoleEffort || m.spec.Model != "cto=sonnet" {
		t.Fatalf("curated pick = stage %v model %q", m.stage, m.spec.Model)
	}

	m, err = NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Roles: "cto", Binary: "cto=codex"}})
	if err != nil {
		t.Fatal(err)
	}
	m.roleOrder = []string{"cto"}
	m.stage = stageRoleModel
	m.configureStage()
	choices := m.choices()
	m.cursor = len(choices) - 1 // custom
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageRoleModelCustom {
		t.Fatalf("custom escape = stage %v", m.stage)
	}
	m.input.SetValue("gpt-5.7-experimental")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageRoleEffort || m.spec.Model != "cto=gpt-5.7-experimental" {
		t.Fatalf("custom model = stage %v model %q", m.stage, m.spec.Model)
	}
}

func TestBubbleModelEditingRolesPrunesAssignmentsAndInvalidLead(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{
		Project: "/repo", Roles: "cto,qa", Binary: "cto=codex,qa=claude", Model: "cto=gpt,qa=opus", Effort: "cto=high,qa=medium", Lead: "qa",
	}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageRoles
	m.configureStage()
	m.input.SetValue("cto")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.Binary != "cto=codex" || m.spec.Model != "cto=gpt" || m.spec.Effort != "cto=high" || m.spec.Lead != "" {
		t.Fatalf("pruned spec = %+v", m.spec)
	}
}

func TestBubbleModelCancelDoesNotProducePreview(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageProfile
	m.configureStage()
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if !m.cancelled || m.done {
		t.Fatalf("cancel state = cancelled %t done %t", m.cancelled, m.done)
	}
}

func TestBubbleModelConfirmationShowsCanonicalReadOnlyBoundary(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{
		Project: "/repo", Profile: "review", Session: "issue-393", Visibility: "detached", Effort: "cto=high", OperatorMode: "noc",
	}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageConfirm
	m.configureStage()
	view := m.View()
	for _, want := range []string{
		"Review answers before canonical preview", "existing profile (authoritative)", "cto=high (launch only)",
		"detached squad", "Operator", "noc · NOC/global orchestrator owns polling", "Run canonical preview, then decide launch separately",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("confirmation missing %q:\n%s", want, view)
		}
	}
}

func TestTopologyPreviewGolden(t *testing.T) {
	var blocks []string
	for _, visibility := range []string{"sibling-tabs", "current", "detached"} {
		blocks = append(blocks, "## "+visibility+"\n"+TopologyPreview(visibility))
	}
	got := strings.Join(blocks, "\n\n") + "\n"
	want, err := os.ReadFile(filepath.Join("testdata", "topology.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("topology rendering changed\n--- got ---\n%s--- want ---\n%s", got, string(want))
	}
}

func updateBubble(t *testing.T, m BubbleModel, msg tea.Msg) BubbleModel {
	t.Helper()
	next, _ := m.Update(msg)
	got, ok := next.(BubbleModel)
	if !ok {
		t.Fatalf("Update returned %T", next)
	}
	return got
}
