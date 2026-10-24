package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/drafter"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestTeamRulesDraftValidatesAndWrapsOnlyEditableProse(t *testing.T) {
	tm := team.Team{
		Project: t.TempDir(), Session: "work",
		Members: []team.Member{{Role: "researcher", Handle: "researcher", Binary: "codex", Session: "work"}},
	}
	raw := validTeamRulesDraft("researcher")
	prose, err := validateTeamRulesDraft(raw, []string{"researcher"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := renderTeamRulesWithTemplateDraft(tm, "custom", &prose)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Team Charter", "Investigate ambiguous behavior", "Owns evidence gathering and reports findings",
		workspaceSafetySection, tangibleProgressSection, "## Typed Gates",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("rendered rules missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "## Team Charter") != 1 {
		t.Fatalf("generated charter duplicated in canonical wrapper:\n%s", body)
	}
}

func TestValidateTeamRulesDraftRejectsStructuralDrift(t *testing.T) {
	valid := validTeamRulesDraft("researcher")
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "extra heading", body: strings.Replace(valid, "## Custom Role Scopes", "## Authority\nNone.\n\n## Custom Role Scopes", 1), want: "unexpected heading"},
		{name: "changed role", body: strings.Replace(valid, "`researcher`", "`operator`", 1), want: "missing or changed role"},
		{name: "duplicate role", body: valid + "- `researcher`: Duplicate.\n", want: "has 2 bullets; want 1"},
		{name: "empty charter", body: strings.Replace(valid, "Investigate ambiguous behavior and turn evidence into reviewable recommendations.\n\n", "", 1), want: "Team Charter cannot be empty"},
		{name: "three charter paragraphs", body: strings.Replace(valid, "Investigate ambiguous behavior and turn evidence into reviewable recommendations.", "First paragraph.\n\nSecond paragraph.\n\nThird paragraph.", 1), want: "at most two prose paragraphs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateTeamRulesDraft(tt.body, []string{"researcher"})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validation error = %v, want %q", err, tt.want)
			}
		})
	}
}

func setupTeamRulesDraftProfile(t *testing.T, cfg *drafter.Config) (string, string) {
	t.Helper()
	project := t.TempDir()
	const profile = "custom-team"
	if err := team.Write(project, team.Team{
		Project: project, Session: "work",
		Members: []team.Member{{Role: "researcher", Handle: "researcher", Binary: "codex", Session: "work"}},
	}); err != nil {
		t.Fatal(err)
	}
	return project, profile
}

func installTeamRulesDraftRunner(t *testing.T, runner cliDrafterRunner) {
	t.Helper()
	previous := runDraft
	runDraft = runner
	t.Cleanup(func() { runDraft = previous })
}

func validTeamRulesDraft(role string) string {
	return "## Team Charter\n" +
		"Investigate ambiguous behavior and turn evidence into reviewable recommendations.\n\n" +
		"## Custom Role Scopes\n" +
		"- `" + role + "`: Owns evidence gathering and reports findings to the task sender.\n"
}
