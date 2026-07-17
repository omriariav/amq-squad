package wizard

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestApplyLaunchShapeSwitchRestoresAuthoredComposition(t *testing.T) {
	s := Spec{
		Roles:       "cto,platform-dev,runtime-dev",
		Binary:      "cto=codex,platform-dev=codex,runtime-dev=claude",
		Model:       "cto=gpt,platform-dev=gpt,runtime-dev=sonnet",
		Effort:      "cto=high,platform-dev=medium,runtime-dev=high",
		ToolProfile: "cto=full,platform-dev=coding,runtime-dev=coding",
		Lead:        "cto",
		LaunchShape: LaunchShapeLeadOnlyStaged,
	}
	if err := s.ApplyLaunchShape(); err != nil {
		t.Fatal(err)
	}
	if s.Roles != "cto" || s.StagedRoles != "platform-dev,runtime-dev" || s.Binary != "cto=codex" {
		t.Fatalf("lead-only projection = roles %q staged %q binary %q", s.Roles, s.StagedRoles, s.Binary)
	}
	if err := s.ApplyLaunchShape(); err != nil {
		t.Fatal(err)
	}
	if s.Roles != "cto" || s.StagedRoles != "platform-dev,runtime-dev" {
		t.Fatalf("lead-only projection is not idempotent: roles %q staged %q", s.Roles, s.StagedRoles)
	}

	s.LaunchShape = LaunchShapeWorkingTeamTogether
	if err := s.ApplyLaunchShape(); err != nil {
		t.Fatal(err)
	}
	if s.Roles != "cto,platform-dev,runtime-dev" || s.StagedRoles != "" {
		t.Fatalf("working-team switch did not restore authored roster: roles %q staged %q", s.Roles, s.StagedRoles)
	}
	if s.Binary != "cto=codex,platform-dev=codex,runtime-dev=claude" ||
		s.Model != "cto=gpt,platform-dev=gpt,runtime-dev=sonnet" ||
		s.Effort != "cto=high,platform-dev=medium,runtime-dev=high" ||
		s.ToolProfile != "cto=full,platform-dev=coding,runtime-dev=coding" {
		t.Fatalf("working-team switch lost authored assignments: %+v", s)
	}
}

func TestBubbleLaunchShapeBackAndReselectRestoresWorkers(t *testing.T) {
	m, err := NewBubbleModel(NumberedOptions{Defaults: Spec{Project: "/repo"}})
	if err != nil {
		t.Fatal(err)
	}
	m.spec = Spec{
		Project:     "/repo",
		Profile:     "default",
		Session:     "s",
		Roles:       "cto,platform-dev,runtime-dev",
		Binary:      "cto=codex,platform-dev=codex,runtime-dev=claude",
		Lead:        "cto",
		LeadMode:    "planner",
		LaunchShape: LaunchShapeWorkingTeamTogether,
	}
	m.roleOrder = []string{"cto", "platform-dev", "runtime-dev"}
	m.stage = stageLaunchShape
	m.configureStage()
	m.cursor = 1
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageStagedRoles || m.spec.Roles != "cto" {
		t.Fatalf("lead-only choice = stage %v roles %q", m.stage, m.spec.Roles)
	}

	backModel, _ := m.back()
	m = backModel.(BubbleModel)
	if m.stage != stageLaunchShape || m.spec.Roles != "cto,platform-dev,runtime-dev" {
		t.Fatalf("Back did not restore authored snapshot: stage %v roles %q", m.stage, m.spec.Roles)
	}
	m.cursor = 0
	m = updateBubble(t, m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.stage != stageStagedRoles || m.spec.Roles != "cto,platform-dev,runtime-dev" || m.spec.StagedRoles != "" {
		t.Fatalf("working-team reselect lost workers: stage %v roles %q staged %q", m.stage, m.spec.Roles, m.spec.StagedRoles)
	}
}
