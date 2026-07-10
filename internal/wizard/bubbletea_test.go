package wizard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

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

func TestBubbleModelExistingProfileOverridesAreLaunchOnly(t *testing.T) {
	profile := ProfileSummary{
		Name: "review", MemberCount: 1, PinnedSession: "review-work", Lead: "cto", LeadMode: "planner",
		Members: []MemberSummary{{Role: "cto", Binary: "codex", Model: "stored-model", Effort: "medium"}},
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
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // project
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // profile
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter}) // pinned session
	if m.stage != stageExistingOverride {
		t.Fatalf("stage = %v, want existing override", m.stage)
	}
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyDown})
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageExistingModel {
		t.Fatalf("stage = %v, want existing model", m.stage)
	}
	m.input.SetValue("launch-model")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	m.cursor = 3 // high
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageTopology {
		t.Fatalf("stage = %v, want topology", m.stage)
	}
	if m.spec.Model != "cto=launch-model" || m.spec.Effort != "cto=high" {
		t.Fatalf("launch overrides = model %q effort %q", m.spec.Model, m.spec.Effort)
	}
	if profile.Members[0].Model != "stored-model" || profile.Members[0].Effort != "medium" {
		t.Fatalf("source profile mutated: %+v", profile.Members[0])
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
	if m.stage != stageGoal {
		t.Fatalf("stage = %v, want goal", m.stage)
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
	m.stage = stageExistingModel
	m.configureStage()
	m.input.SetValue("")
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.spec.Model != "" {
		t.Fatalf("cleared model override = %q", m.spec.Model)
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
		Project: "/repo", Profile: "review", Session: "issue-393", Visibility: "detached", Effort: "cto=high",
	}})
	if err != nil {
		t.Fatal(err)
	}
	m.stage = stageConfirm
	m.configureStage()
	view := m.View()
	for _, want := range []string{
		"Review the read-only preview", "existing profile (authoritative)", "cto=high (launch only)",
		"detached squad", "Run the canonical read-only preview",
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
