package wizard

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
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
		"run start control deck", "◆ 1 Scope", "○ 2 Profile & run", "○ 3 Team", "○ 4 Run controls", "○ 5 Brief", "○ 6 Review", "What do you want to run?", "Project squad", "Global / NOC orchestrator",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("initial view missing %q:\n%s", want, view)
		}
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageProject || m.input.Value() != "/repo" || !strings.Contains(m.View(), "Which project owns this squad?") {
		t.Fatalf("project scope did not enter the project screen: stage=%v input=%q\n%s", m.stage, m.input.Value(), m.View())
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
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // scope
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
	m.cursor = 4 // high
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
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // scope
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
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // scope
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

func TestBubbleResumeActionScopedControls(t *testing.T) {
	tests := []struct {
		name    string
		records int
		state   RunState
		members []SessionMemberSummary
		model   string
		effort  string
	}{
		{name: "all restore", records: 2, state: RunStateStopped, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionRestore, SavedModel: "saved", SavedEffort: "high", SavedNativeArgs: []string{"--saved"}}, {Role: "qa", Binary: "codex", Action: MemberActionRestore}}},
		{name: "restore fresh", records: 1, state: RunStateStopped, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionRestore}, {Role: "qa", Binary: "codex", Action: MemberActionFresh}}, model: "qa=gpt-5.6-sol", effort: "qa=xhigh"},
		{name: "live fresh", state: RunStatePartly, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionLive}, {Role: "qa", Binary: "codex", Action: MemberActionFresh}}, model: "qa=gpt-5.6-sol"},
		{name: "live restore", records: 1, state: RunStatePartly, members: []SessionMemberSummary{{Role: "cto", Binary: "codex", Action: MemberActionLive}, {Role: "qa", Binary: "codex", Action: MemberActionRestore}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := SessionSummary{Name: "s", Source: SessionSourceMemberPin, Fingerprint: "fp", RecordCount: tt.records, Members: tt.members, Classification: RunClassification{State: tt.state, Backend: BackendResume, Executable: true, RestoreExisting: tt.records > 0}}
			profile := ProfileSummary{Name: "release", MemberCount: len(tt.members), OperatorMode: "lead_pane", Sessions: []SessionSummary{summary}}
			for _, member := range tt.members {
				profile.Members = append(profile.Members, MemberSummary{Role: member.Role, Binary: member.Binary, Model: member.Model, Effort: member.Effort})
			}
			m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "release"}, InspectProject: func(string) (ProjectContext, error) {
				return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			for _, member := range tt.members {
				if m.stage != stageResumeMember || m.currentResumeMember().Role != member.Role {
					t.Fatalf("member stage=%v member=%+v", m.stage, m.currentResumeMember())
				}
				if member.Action == MemberActionFresh {
					m.cursor = 1
				}
				m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
				if member.Action == MemberActionFresh {
					if m.stage != stageResumeEffort {
						t.Fatalf("fresh member effort stage=%v", m.stage)
					}
					if tt.effort != "" {
						for i, item := range m.choices() {
							if item.value == "xhigh" {
								m.cursor = i
							}
						}
					}
					m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
				}
			}
			if m.stage != stageTopology || m.spec.Model != tt.model || m.spec.Effort != tt.effort {
				t.Fatalf("post-member state stage=%v spec=%+v", m.stage, m.spec)
			}
			for _, wantStage := range []bubbleStage{stageLayoutPreset, stageOperator, stageOperatorNotifications, stageResumeBrief, stageConfirm} {
				m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
				if m.stage != wantStage {
					t.Fatalf("stage=%v want=%v", m.stage, wantStage)
				}
			}
			if _, err := m.spec.ResumeArgs(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestBubbleResumeUsesAgreedSavedPlacementAndShowsPreservedReviewEvidence(t *testing.T) {
	members := []SessionMemberSummary{
		{Role: "cto", Binary: "codex", Action: MemberActionRestore, SavedLaunchIdentity: "a", SavedTarget: "current-window"},
		{Role: "qa", Binary: "codex", Action: MemberActionRestore, SavedLaunchIdentity: "b", SavedTarget: "current-window"},
	}
	summary := SessionSummary{Name: "s", Source: SessionSourceLaunchHistory, Fingerprint: "fp", RecordCount: 2, BriefPath: "/repo/brief.md", BriefGoal: "line one\nline two", BriefSeed: "issue:431", Members: members, Classification: RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true, RestoreExisting: true}}
	profile := ProfileSummary{Name: "release", MemberCount: 2, OperatorMode: "lead_pane", Members: []MemberSummary{{Role: "cto", Binary: "codex"}, {Role: "qa", Binary: "codex"}}, Sessions: []SessionSummary{summary}}
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "release"}, InspectProject: func(string) (ProjectContext, error) {
		return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	for range members {
		m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	if m.stage != stageTopology || m.spec.Visibility != "current" || m.cursor != 1 {
		t.Fatalf("saved placement stage=%v visibility=%q cursor=%d", m.stage, m.spec.Visibility, m.cursor)
	}
	for _, want := range []string{"/repo/brief.md", "line one\nline two", "issue:431", "amq-squad resume", "--target current-window", "--exec"} {
		if !strings.Contains(m.summary(), want) {
			t.Fatalf("summary missing %q:\n%s", want, m.summary())
		}
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyUp})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.Visibility != "sibling-tabs" {
		t.Fatalf("explicit placement override=%q", m.spec.Visibility)
	}
}

func TestBubbleResumeGoalDefaultsNoAndRendersSafeEvidence(t *testing.T) {
	m := BubbleModel{stage: stageResumeGoal, spec: Spec{Backend: BackendResume, ResumeGoalPlan: ResumeGoalPlan{Eligible: true, Action: "redeliver", Goal: "safe\x1b[31mRED\x1b[0m\x00"}}}
	choices := m.choices()
	if len(choices) != 2 || choices[0].value != "no" || m.cursor != 0 {
		t.Fatalf("resume goal default choices=%+v cursor=%d", choices, m.cursor)
	}
	if guidance := m.note(); strings.ContainsRune(guidance, '\x1b') || strings.ContainsRune(guidance, '\x00') || strings.Contains(guidance, "[31m") {
		t.Fatalf("unsafe guidance: %q", guidance)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.RedeliverGoal || m.stage != stageConfirm {
		t.Fatalf("default No not preserved: stage=%v spec=%+v", m.stage, m.spec)
	}
	yes := BubbleModel{stage: stageResumeGoal, cursor: 1, spec: Spec{Backend: BackendResume, ResumeGoalPlan: ResumeGoalPlan{Eligible: true, Action: "redeliver", Goal: "safe"}}}
	yes = updateBubble(t, yes, tea.KeyMsg{Type: tea.KeyEnter})
	if !yes.spec.RedeliverGoal || yes.stage != stageConfirm {
		t.Fatalf("explicit Yes not preserved: stage=%v spec=%+v", yes.stage, yes.spec)
	}
	blocked := BubbleModel{stage: stageResumeBrief, spec: Spec{Backend: BackendResume, ResumeGoalPlan: ResumeGoalPlan{Eligible: false, Action: "blocked", Reason: "claim missing"}}}
	blocked = updateBubble(t, blocked, tea.KeyMsg{Type: tea.KeyEnter})
	if blocked.stage != stageConfirm || blocked.spec.RedeliverGoal {
		t.Fatalf("ineligible goal did not bypass choice: stage=%v spec=%+v", blocked.stage, blocked.spec)
	}
}

func TestBubblePhaseRailsAreScopeSpecific(t *testing.T) {
	project := BubbleModel{spec: Spec{Scope: "project"}}
	if got, want := strings.Join(project.phaseLabels(), "|"), "Scope|Profile & run|Team|Run controls|Brief|Review"; got != want {
		t.Fatalf("project rail=%q", got)
	}
	for _, branch := range []struct {
		name   string
		stages []struct {
			stage bubbleStage
			want  int
		}
	}{
		{name: "project existing", stages: []struct {
			stage bubbleStage
			want  int
		}{{stageProject, 0}, {stageProfile, 1}, {stageResumeMember, 2}, {stageTopology, 3}, {stageResumeBrief, 4}, {stageConfirm, 5}}},
		{name: "project new", stages: []struct {
			stage bubbleStage
			want  int
		}{{stageProject, 0}, {stageNewProfile, 1}, {stageRoleBinary, 2}, {stageRoleModel, 2}, {stageOperator, 3}, {stageGoal, 4}, {stageConfirm, 5}}},
	} {
		t.Run(branch.name, func(t *testing.T) {
			for _, item := range branch.stages {
				project.stage = item.stage
				if got := project.phaseIndex(); got != item.want {
					t.Fatalf("stage=%v index=%d want=%d", item.stage, got, item.want)
				}
			}
		})
	}

	for _, tc := range []struct {
		name   string
		stages []bubbleStage
	}{
		{name: "existing start catalog controls", stages: []bubbleStage{stageExistingModel, stageExistingModelCustom, stageExistingEffort, stageExistingEffortCustom}},
		{name: "resume catalog controls", stages: []bubbleStage{stageResumeMember, stageResumeModelCustom, stageResumeEffort, stageResumeEffortCustom}},
		{name: "fresh roster catalog controls", stages: []bubbleStage{stageRoleModel, stageRoleModelCustom, stageRoleEffort, stageRoleEffortCustom}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stage := range tc.stages {
				project.stage = stage
				if got := project.phaseIndex(); got != 2 {
					t.Fatalf("stage=%v index=%d want Team index 2", stage, got)
				}
			}
		})
	}
}

func TestBubbleProjectPhaseRailNeverRegressesAcrossMultiMemberFlows(t *testing.T) {
	walk := func(t *testing.T, m BubbleModel, next func(BubbleModel) tea.KeyMsg) []bubbleStage {
		t.Helper()
		stages := []bubbleStage{m.stage}
		previous := m.phaseIndex()
		for step := 0; step < 64 && m.stage != stageConfirm; step++ {
			m = updateBubble(t, m, next(m))
			current := m.phaseIndex()
			stages = append(stages, m.stage)
			if current < previous {
				t.Fatalf("phase rail regressed at step %d: stage %v index %d -> stage %v index %d; walk=%v", step, stages[len(stages)-2], previous, m.stage, current, stages)
			}
			previous = current
		}
		if m.stage != stageConfirm {
			t.Fatalf("flow did not reach review; final stage=%v walk=%v", m.stage, stages)
		}
		return stages
	}
	countStage := func(stages []bubbleStage, want bubbleStage) int {
		count := 0
		for _, stage := range stages {
			if stage == want {
				count++
			}
		}
		return count
	}

	t.Run("fresh multi-role roster", func(t *testing.T) {
		m, err := NewBubbleModel(NumberedOptions{
			Defaults: Spec{
				Scope: "project", Project: "/repo", Profile: "new", Session: "s",
				Roles: "cto,qa", Binary: "cto=codex,qa=claude", Lead: "cto",
			},
			InspectProject: func(string) (ProjectContext, error) {
				return ProjectContext{Project: "/repo"}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		stages := walk(t, m, func(BubbleModel) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEnter} })
		for _, stage := range []bubbleStage{stageRoleBinary, stageRoleModel, stageRoleEffort} {
			if got := countStage(stages, stage); got != 2 {
				t.Fatalf("stage %v visits=%d want=2; walk=%v", stage, got, stages)
			}
		}
	})

	t.Run("existing multi-member launch overrides", func(t *testing.T) {
		profile := ProfileSummary{
			Name: "release", MemberCount: 2, OperatorMode: "lead_pane",
			Members: []MemberSummary{
				{Role: "cto", Binary: "codex", Model: "gpt-5.6-sol", Effort: "high"},
				{Role: "qa", Binary: "claude", Model: "sonnet", Effort: "medium"},
			},
			Sessions: []SessionSummary{discoveredFreshSession("s", SessionSourceMemberPin, 2)},
		}
		m, err := NewBubbleModel(NumberedOptions{
			Defaults: Spec{Scope: "project", Project: "/repo", Profile: "release"},
			InspectProject: func(string) (ProjectContext, error) {
				return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		stages := walk(t, m, func(current BubbleModel) tea.KeyMsg {
			if current.stage == stageExistingOverride && current.cursor == 0 {
				return tea.KeyMsg{Type: tea.KeyDown}
			}
			return tea.KeyMsg{Type: tea.KeyEnter}
		})
		for _, stage := range []bubbleStage{stageExistingOverride, stageExistingModel, stageExistingEffort} {
			if got := countStage(stages, stage); got < 2 {
				t.Fatalf("stage %v visits=%d want at least 2; walk=%v", stage, got, stages)
			}
		}
	})
}

func TestGlobalBranchRunsThroughBothRealAdaptersToIdenticalReview(t *testing.T) {
	defaults := Spec{Scope: "project", GlobalRoot: "/neutral", GlobalAgent: "claude", GlobalWindow: "global-orch"}
	initial, err := NewBubbleModel(NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	program := tea.NewProgram(initial, tea.WithInput(nil), tea.WithOutput(&bytes.Buffer{}), tea.WithoutRenderer())
	type programResult struct {
		model tea.Model
		err   error
	}
	done := make(chan programResult, 1)
	go func() {
		model, runErr := program.Run()
		done <- programResult{model: model, err: runErr}
	}()
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyDown},
		{Type: tea.KeyEnter},
		{Type: tea.KeyEnter}, {Type: tea.KeyEnter}, {Type: tea.KeyEnter}, {Type: tea.KeyEnter},
		{Type: tea.KeyEnter}, {Type: tea.KeyEnter}, {Type: tea.KeyEnter},
	} {
		program.Send(key)
	}
	completed := <-done
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	bubbleModel, ok := completed.model.(BubbleModel)
	if !ok {
		t.Fatalf("bubble adapter returned %T", completed.model)
	}
	bubble := BubbleResult{Spec: bubbleModel.spec, Cancelled: bubbleModel.cancelled}

	var numberedOut bytes.Buffer
	numbered, err := RunNumbered(strings.NewReader("2\n\n\n\n\n\n\n"), &numberedOut, NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bubble.Spec, numbered) {
		t.Fatalf("adapter specs differ:\nbubble=%+v\nnumbered=%+v", bubble.Spec, numbered)
	}
	bubblePreview, bubbleLive, err := bubble.Spec.CommandForms()
	if err != nil {
		t.Fatal(err)
	}
	numberedPreview, numberedLive, err := numbered.CommandForms()
	if err != nil {
		t.Fatal(err)
	}
	if bubblePreview != numberedPreview || bubbleLive != numberedLive {
		t.Fatalf("command forms differ: bubble=(%q, %q) numbered=(%q, %q)", bubblePreview, bubbleLive, numberedPreview, numberedLive)
	}
	for _, want := range []string{"Review", bubblePreview, bubbleLive, "owns no wake mailbox"} {
		if !strings.Contains(numberedOut.String(), want) {
			t.Fatalf("numbered global review missing %q:\n%s", want, numberedOut.String())
		}
	}
	if !strings.Contains(bubble.Spec.Scope, "global") || bubble.Spec.Backend != BackendGlobalStart {
		t.Fatalf("bubble did not complete the global backend: %+v", bubble.Spec)
	}
}

func TestGlobalCatalogChoicesMatchAcrossAdapters(t *testing.T) {
	catalog := agentcatalog.Merge(agentcatalog.Builtins(), agentcatalog.Catalog{Binaries: map[string]agentcatalog.Binary{
		"claude": {
			Models:  []agentcatalog.Entry{{Value: "NeoModel", Label: "Neo model", Enabled: true}},
			Efforts: []agentcatalog.Entry{{Value: "UltraTier", Label: "Ultra tier", Enabled: true}},
		},
	}})
	loaded := []string{}
	load := func(root string) agentcatalog.Catalog {
		loaded = append(loaded, root)
		return catalog
	}
	defaults := Spec{Scope: "project", GlobalRoot: "/neutral", GlobalAgent: "claude", GlobalWindow: "global-orch"}
	m, err := NewBubbleModel(NumberedOptions{Defaults: defaults, LoadCatalog: load})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // global scope
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // root + catalog load
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // claude
	if m.stage != stageGlobalModel {
		t.Fatalf("global model stage = %v", m.stage)
	}
	for i, item := range m.choices() {
		if item.value == "NeoModel" {
			m.cursor = i
		}
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	for i, item := range m.choices() {
		if item.value == "UltraTier" {
			m.cursor = i
		}
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // native args
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // window
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // review

	var numberedOut bytes.Buffer
	numbered, err := RunNumbered(strings.NewReader("2\n\n\n6\n7\n\n\n"), &numberedOut, NumberedOptions{Defaults: defaults, LoadCatalog: load})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.spec, numbered) {
		t.Fatalf("catalog adapter specs differ:\nbubble=%+v\nnumbered=%+v", m.spec, numbered)
	}
	if m.spec.GlobalModel != "NeoModel" || m.spec.GlobalEffort != "UltraTier" {
		t.Fatalf("catalog selections = %+v", m.spec)
	}
	if got, want := loaded, []string{"/neutral", "/neutral"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("catalog roots = %v, want %v", got, want)
	}
	for _, want := range []string{"Neo model", "Ultra tier", "NeoModel", "UltraTier"} {
		if !strings.Contains(numberedOut.String(), want) {
			t.Fatalf("numbered output missing %q:\n%s", want, numberedOut.String())
		}
	}
}

func TestProjectCatalogChoicesMatchAcrossAdapters(t *testing.T) {
	catalog := agentcatalog.Merge(agentcatalog.Builtins(), agentcatalog.Catalog{Binaries: map[string]agentcatalog.Binary{
		"claude": {Efforts: []agentcatalog.Entry{
			{Value: "medium", Label: "medium", Enabled: false},
			{Value: "UltraTier", Label: "Ultra tier", Enabled: true},
		}},
	}})
	existing := ProfileSummary{
		Name: "release", MemberCount: 1, OperatorMode: "lead_pane",
		Members:  []MemberSummary{{Role: "qa", Binary: "claude", Effort: "low"}},
		Sessions: []SessionSummary{discoveredFreshSession("s", SessionSourceMemberPin, 1)},
	}
	resumeMembers := []SessionMemberSummary{{Role: "qa", Binary: "claude", Effort: "low", Action: MemberActionFresh}}
	resume := ProfileSummary{
		Name: "release", MemberCount: 1, OperatorMode: "lead_pane",
		Members: []MemberSummary{{Role: "qa", Binary: "claude", Effort: "low"}},
		Sessions: []SessionSummary{{
			Name: "s", Source: SessionSourceMemberPin, Fingerprint: "fp", Fresh: 1,
			Members:        resumeMembers,
			Classification: RunClassification{State: RunStateStopped, Backend: BackendResume, Executable: true},
		}},
	}

	choiceLabels := func(items []choice) []string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.label)
		}
		return out
	}
	numberedPromptLabels := func(t *testing.T, output, prompt string) []string {
		t.Helper()
		startToken := "\n" + prompt + ":\n"
		start := strings.Index(output, startToken)
		if start < 0 {
			t.Fatalf("numbered output missing %q prompt:\n%s", prompt, output)
		}
		block := output[start+len(startToken):]
		end := strings.Index(block, "Choose [")
		if end < 0 {
			t.Fatalf("numbered output missing chooser after %q:\n%s", prompt, output)
		}
		var labels []string
		for _, line := range strings.Split(block[:end], "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			_, label, ok := strings.Cut(line, ") ")
			if !ok {
				t.Fatalf("unexpected numbered choice line %q", line)
			}
			labels = append(labels, strings.TrimSuffix(label, " (default)"))
		}
		return labels
	}

	for _, tc := range []struct {
		name       string
		defaults   Spec
		ctx        ProjectContext
		input      string
		prompt     string
		bubble     BubbleModel
		wantValues []string
	}{
		{
			name:     "fresh roster",
			defaults: Spec{Scope: "project", Project: "/repo", Profile: "new", Session: "s", Roles: "qa", Binary: "qa=claude", Lead: "qa"},
			ctx:      ProjectContext{Project: "/repo", Catalog: catalog},
			input:    strings.Repeat("\n", 24),
			prompt:   "qa effort",
			bubble: BubbleModel{
				spec: Spec{Binary: "qa=claude"}, ctx: ProjectContext{Catalog: catalog},
				stage: stageRoleEffort, roleOrder: []string{"qa"}, roleIndex: 0,
			},
			wantValues: []string{"automatic", "low", "high", "xhigh", "max", "UltraTier", "custom"},
		},
		{
			name:     "existing start",
			defaults: Spec{Scope: "project", Project: "/repo", Profile: "release"},
			ctx:      ProjectContext{Project: "/repo", Catalog: catalog, Profiles: []ProfileSummary{existing}},
			input:    strings.Join([]string{"", "", "", "2"}, "\n") + "\n" + strings.Repeat("\n", 20),
			prompt:   "qa effort override",
			bubble: BubbleModel{
				ctx:   ProjectContext{Catalog: catalog, Profiles: []ProfileSummary{existing}},
				stage: stageExistingEffort, existingIndex: 0, roleIndex: 0,
			},
			wantValues: []string{"keep", "low", "high", "xhigh", "max", "UltraTier", "custom"},
		},
		{
			name:     "resume fresh member",
			defaults: Spec{Scope: "project", Project: "/repo", Profile: "release"},
			ctx:      ProjectContext{Project: "/repo", Catalog: catalog, Profiles: []ProfileSummary{resume}},
			input:    strings.Repeat("\n", 20),
			prompt:   "qa fresh-launch effort",
			bubble: BubbleModel{
				spec: Spec{ResumeMembers: resumeMembers}, ctx: ProjectContext{Catalog: catalog},
				stage: stageResumeEffort, roleIndex: 0,
			},
			wantValues: []string{"keep", "low", "high", "xhigh", "max", "UltraTier", "custom"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := make([]string, 0, len(tc.bubble.choices()))
			for _, item := range tc.bubble.choices() {
				values = append(values, item.value)
			}
			if !reflect.DeepEqual(values, tc.wantValues) {
				t.Fatalf("bubble values = %v, want %v", values, tc.wantValues)
			}

			var out bytes.Buffer
			_, err := RunNumbered(strings.NewReader(tc.input), &out, NumberedOptions{
				Defaults: tc.defaults,
				InspectProject: func(string) (ProjectContext, error) {
					return tc.ctx, nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := numberedPromptLabels(t, out.String(), tc.prompt), choiceLabels(tc.bubble.choices()); !reflect.DeepEqual(got, want) {
				t.Fatalf("adapter labels differ:\nnumbered=%v\nbubble=%v", got, want)
			}
			if strings.Contains(strings.Join(choiceLabels(tc.bubble.choices()), "\n"), "medium") {
				t.Fatal("disabled project catalog entry leaked into a wizard adapter")
			}
		})
	}
}

func TestGlobalCustomEffortIsPreservedAndWarnedInBothReviews(t *testing.T) {
	defaults := Spec{Scope: "project", GlobalRoot: "/neutral", GlobalAgent: "claude", GlobalEffort: "FutureTier", GlobalWindow: "global-orch"}
	m, err := NewBubbleModel(NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyDown}, {Type: tea.KeyEnter}, // global
		{Type: tea.KeyEnter}, // root
		{Type: tea.KeyEnter}, // agent
		{Type: tea.KeyEnter}, // automatic model
		{Type: tea.KeyEnter}, // custom effort row
		{Type: tea.KeyEnter}, // exact custom effort text
		{Type: tea.KeyEnter}, // native args
		{Type: tea.KeyEnter}, // window
	} {
		m = updateBubble(t, m, key)
	}
	if m.stage != stageConfirm || m.spec.GlobalEffort != "FutureTier" {
		t.Fatalf("bubble custom global state = stage %v spec %+v", m.stage, m.spec)
	}
	if review := m.summary(); !strings.Contains(review, "Warning: effort global=FutureTier") || !strings.Contains(review, "passed through exactly") {
		t.Fatalf("bubble review missing custom warning:\n%s", review)
	}

	var out bytes.Buffer
	numbered, err := RunNumbered(strings.NewReader("2\n\n\n\n\n\n\n\n"), &out, NumberedOptions{Defaults: defaults})
	if err != nil {
		t.Fatal(err)
	}
	if numbered.GlobalEffort != "FutureTier" || !strings.Contains(out.String(), "Warning: effort global=FutureTier") {
		t.Fatalf("numbered custom result=%+v output:\n%s", numbered, out.String())
	}
}

func TestBubbleResumeUsesInjectedCatalogAndPrefillsStoredCustomValues(t *testing.T) {
	catalog := agentcatalog.Merge(agentcatalog.Builtins(), agentcatalog.Catalog{Binaries: map[string]agentcatalog.Binary{
		"claude": {
			Models:  []agentcatalog.Entry{{Value: "NeoModel", Enabled: true}},
			Efforts: []agentcatalog.Entry{{Value: "UltraTier", Enabled: true}},
		},
	}})
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	m.ctx.Catalog = catalog
	m.spec.ResumeMembers = []SessionMemberSummary{{Role: "qa", Binary: "claude", Action: MemberActionFresh, Model: "StoredModel", Effort: "StoredTier"}}
	m.spec.Model = "qa=NeoModel"
	m.spec.Effort = "qa=UltraTier"
	m.roleIndex = 0
	m.stage = stageResumeMember
	m.configureStage()
	if got := m.choices()[m.cursor].value; got != "NeoModel" {
		t.Fatalf("resume model default = %q, want injected catalog value", got)
	}
	m.stage = stageResumeEffort
	m.configureStage()
	if got := m.choices()[m.cursor].value; got != "UltraTier" {
		t.Fatalf("resume effort default = %q, want injected catalog value", got)
	}

	m.spec.Model = ""
	m.stage = stageResumeModelCustom
	m.configureStage()
	if got := m.input.Value(); got != "StoredModel" {
		t.Fatalf("custom model prefill = %q, want stored value", got)
	}
	m.spec.Effort = ""
	m.stage = stageResumeEffortCustom
	m.configureStage()
	if got := m.input.Value(); got != "StoredTier" {
		t.Fatalf("custom effort prefill = %q, want stored value", got)
	}
}

func TestBubbleRunningAndBlockedExistingRunsRemainNonExecutable(t *testing.T) {
	for _, state := range []RunState{RunStateRunning, RunStateBlocked} {
		t.Run(string(state), func(t *testing.T) {
			summary := SessionSummary{Name: "s", Source: SessionSourceMemberPin, Fingerprint: "fp", Classification: RunClassification{State: state}}
			profile := ProfileSummary{Name: "release", MemberCount: 1, Members: []MemberSummary{{Role: "cto", Binary: "codex"}}, Sessions: []SessionSummary{summary}}
			m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Profile: "release"}, InspectProject: func(string) (ProjectContext, error) {
				return ProjectContext{Project: "/repo", Profiles: []ProfileSummary{profile}}, nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
			if !m.done || m.spec.RunExecutable || m.stage == stageConfirm {
				t.Fatalf("nonexec state reached execution: state=%s model=%+v", state, m)
			}
		})
	}
}

func TestNewBubbleModelRestartStartsAtProfileWithFreshContext(t *testing.T) {
	stale := Spec{Scope: "project", Project: "/repo", Profile: "release", ProfileBranch: ProfileBranchExisting, Session: "old", Backend: BackendResume, RunExecutable: true, DiscoveryFingerprint: "old", Model: "qa=old", Visibility: "current"}
	stale.InvalidateExistingRun()
	freshSummary := SessionSummary{Name: "new", Fingerprint: "fresh", Classification: RunClassification{State: RunStateRunning}}
	fresh := ProjectContext{Project: "/repo", OriginSlug: "fresh/context", Profiles: []ProfileSummary{{Name: "release", MemberCount: 1, Sessions: []SessionSummary{freshSummary}}}}
	m, err := NewBubbleModel(NumberedOptions{Defaults: stale, StartAtProfile: true, RestartMessage: "refresh required", InspectProject: func(string) (ProjectContext, error) { return fresh, nil }})
	if err != nil {
		t.Fatal(err)
	}
	if m.stage != stageProfile || m.spec.Profile != "release" || m.spec.ProfileBranch != ProfileBranchExisting || m.spec.Session != "" || m.spec.Backend != "" || m.ctx.OriginSlug != "fresh/context" || len(m.history) != 0 || m.err == nil || !strings.Contains(m.err.Error(), "refresh required") {
		t.Fatalf("restart model=%+v", m)
	}
	if m.defaultCursor() != 0 || len(m.choices()) != 2 || !strings.Contains(m.choices()[0].label, "new/running") {
		t.Fatalf("restart choices=%+v cursor=%d", m.choices(), m.defaultCursor())
	}
}

func TestBubbleBackRefreshRejectsChangedDiscoveryAndRetainsB(t *testing.T) {
	calls := 0
	ctxA := bubbleBackContext("reviewed-a", "context-a")
	ctxB := bubbleBackContext("reviewed-b", "context-b")
	m := bubbleAtExistingModel(t, func(string) (ProjectContext, error) {
		calls++
		if calls < 2 {
			return ctxA, nil
		}
		return ctxB, nil
	})
	m.spec.Goal, m.spec.Visibility, m.spec.Model = "stale goal", "current", "cto=stale"
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if calls != 2 || m.stage != stageProfile || m.ctx.OriginSlug != "context-b" || m.spec.Session != "" || m.spec.Backend != "" || m.spec.RunExecutable || m.spec.Goal != "" || m.spec.Visibility != "" || m.spec.Model != "" || m.spec.OperatorMode != "" || m.err == nil || len(m.history) != 0 || m.existingIndex != 0 {
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
	if calls != 2 || m.stage != stageExistingOverride || m.spec.Session != "s" || m.spec.DiscoveryFingerprint != "reviewed-a" || m.ctx.OriginSlug != "context-a-2" || m.existingIndex != 0 || m.err != nil {
		t.Fatalf("compatible refresh did not restore snapshot: calls=%d stage=%v ctx=%+v spec=%+v err=%v", calls, m.stage, m.ctx, m.spec, m.err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ctx.OriginSlug != "context-a-2" {
		t.Fatalf("subsequent Back resurrected pre-refresh context: %+v", m.ctx)
	}
}

func TestBubbleBackRefreshErrorRejectsAndRetainsLastKnownContext(t *testing.T) {
	calls := 0
	m := bubbleAtExistingModel(t, func(string) (ProjectContext, error) {
		calls++
		if calls == 2 {
			return ProjectContext{}, errors.New("discovery unavailable")
		}
		return bubbleBackContext("reviewed-a", fmt.Sprintf("context-a-%d", calls)), nil
	})
	m.spec.Goal, m.spec.OperatorMode = "stale goal", "lead_pane"
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if calls != 2 || m.stage != stageProject || m.ctx.OriginSlug != "context-a-1" || m.spec.Session != "" || m.spec.Goal != "" || m.spec.OperatorMode != "" || m.err == nil || !strings.Contains(m.err.Error(), "discovery unavailable") || len(m.history) != 0 || m.input.Value() != "/repo" {
		t.Fatalf("refresh error did not fail closed: calls=%d stage=%v ctx=%+v spec=%+v err=%v history=%d input=%q", calls, m.stage, m.ctx, m.spec, m.err, len(m.history), m.input.Value())
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.ctx.OriginSlug != "context-a-1" {
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
	for range 6 { // scope, project, profile, session, topology, layout
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
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
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
			value := strings.TrimPrefix(project, "/")
			return ProjectContext{Project: project, OriginSlug: value, Catalog: agentcatalog.Catalog{Binaries: map[string]agentcatalog.Binary{
				"claude": {Efforts: []agentcatalog.Entry{{Value: value, Enabled: true}}},
			}}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.input.SetValue("/two")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.ctx.OriginSlug != "two" {
		t.Fatalf("new context = %+v", m.ctx)
	}
	if _, ok := m.ctx.Catalog.Resolve("claude", agentcatalog.Efforts, "two"); !ok {
		t.Fatalf("project catalog was not injected: %+v", m.ctx.Catalog)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.stage != stageProject || m.spec.Project != "/one" || m.ctx.OriginSlug != "" || m.input.Value() != "/two" {
		t.Fatalf("restored project state = stage %v spec %+v ctx %+v input %q", m.stage, m.spec, m.ctx, m.input.Value())
	}
	m.input.SetValue("/three")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if _, ok := m.ctx.Catalog.Resolve("claude", agentcatalog.Efforts, "three"); !ok {
		t.Fatalf("changed project did not refresh catalog: %+v", m.ctx.Catalog)
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

func TestBubbleModelOffersCurrentClaudeEffortsWithCustomEscape(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo", Roles: "qa", Binary: "qa=claude"}})
	if err != nil {
		t.Fatal(err)
	}
	m.roleOrder = []string{"qa"}
	m.stage = stageRoleEffort
	m.configureStage()
	view := flattenBubbleView(m.View())
	for _, want := range []string{"automatic", "low", "medium", "high", "xhigh", "max", "custom"} {
		if !strings.Contains(view, want) {
			t.Fatalf("claude effort list missing %q:\n%s", want, view)
		}
	}
	m.cursor = len(m.choices()) - 1
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageRoleEffortCustom {
		t.Fatalf("custom effort stage = %v", m.stage)
	}
	m.input.SetValue("FutureTier")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.Effort != "qa=FutureTier" {
		t.Fatalf("custom effort = %q", m.spec.Effort)
	}
	if review := m.summary(); !strings.Contains(review, "Warning: effort qa=FutureTier") {
		t.Fatalf("custom effort warning missing from summary:\n%s", review)
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
