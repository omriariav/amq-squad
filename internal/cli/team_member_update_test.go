package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTeamMemberUpdateChangesOnlyPassedFields(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96", Model: "sonnet"},
		},
	})
	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "qa", "--model", "opus"})
	})
	if err != nil {
		t.Fatalf("member update: %v", err)
	}
	got := teamMembers(t, dir)[0]
	if got.Model != "opus" {
		t.Fatalf("model = %q, want opus", got.Model)
	}
	if got.Session != "issue-96" || got.Handle != "qa" {
		t.Fatalf("untouched fields changed: %+v", got)
	}
	if !strings.Contains(out, "updated qa") {
		t.Fatalf("expected human confirmation, got %q", out)
	}
}

func TestTeamMemberUpdateSession(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	if err := runTeamMember([]string{"update", "qa", "--session", "issue-97"}); err != nil {
		t.Fatalf("member update: %v", err)
	}
	if got := teamMembers(t, dir)[0].Session; got != "issue-97" {
		t.Fatalf("session = %q, want issue-97", got)
	}
}

func TestTeamMemberUpdateNoSessionPinClearsSession(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	if err := runTeamMember([]string{"update", "qa", "--no-session-pin"}); err != nil {
		t.Fatalf("member update: %v", err)
	}
	if got := teamMembers(t, dir)[0].Session; got != "" {
		t.Fatalf("session = %q, want cleared", got)
	}
}

func TestTeamMemberUpdateRejectsSessionAndNoSessionPinTogether(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	err := runTeamMember([]string{"update", "qa", "--session", "issue-97", "--no-session-pin"})
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Fatalf("expected a conflict error, got %v", err)
	}
}

func TestTeamMemberUpdateRequiresAtLeastOneChange(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	err := runTeamMember([]string{"update", "qa"})
	if err == nil || !strings.Contains(err.Error(), "no changes given") {
		t.Fatalf("expected a no-changes error, got %v", err)
	}
}

func TestTeamMemberUpdateUnknownRoleFails(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	err := runTeamMember([]string{"update", "researcher", "--model", "sonnet"})
	if err == nil || !strings.Contains(err.Error(), "not a team member") {
		t.Fatalf("expected an unknown-role error, got %v", err)
	}
}

func TestTeamMemberUpdateRejectsCrossBinaryArgs(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	err := runTeamMember([]string{"update", "qa", "--claude-args", "--chrome"})
	if err == nil || !strings.Contains(err.Error(), "--claude-args applies only to claude members") {
		t.Fatalf("expected a binary-mismatch error, got %v", err)
	}
}

// The recorded #451 friction: `rm` refuses to remove the orchestration lead,
// so today the only way to touch its session/model is remove-then-add, which
// is impossible for the lead. `update` must work directly on the lead role.
func TestTeamMemberUpdateWorksOnOrchestrationLead(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	})
	if err := runTeamMember([]string{"rm", "cto"}); err == nil {
		t.Fatalf("expected rm to still refuse removing the lead")
	}
	if err := runTeamMember([]string{"update", "cto", "--session", "issue-97"}); err != nil {
		t.Fatalf("update on the lead role should succeed: %v", err)
	}
	got := teamMembers(t, dir)
	if got[0].Session != "issue-97" {
		t.Fatalf("lead session = %q, want issue-97", got[0].Session)
	}
}

func TestTeamMemberUpdateJSONEnvelope(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "qa", "--model", "opus", "--json"})
	})
	if err != nil {
		t.Fatalf("member update --json: %v", err)
	}
	updated := decodeJSONEnvelope[mutationResult](t, stdout)
	if updated.Kind != "team_member_update" || updated.Data.Status != "updated" || updated.Data.Role != "qa" {
		t.Fatalf("bad member update envelope: %+v", updated)
	}
	if strings.Contains(stdout, "updated qa") {
		t.Fatalf("--json must not include human output:\n%s", stdout)
	}
}

func TestTeamMemberUpdateDryRunDoesNotMutate(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"}},
	})
	if err := runTeamMember([]string{"update", "qa", "--model", "opus", "--dry-run"}); err != nil {
		t.Fatalf("member update --dry-run: %v", err)
	}
	if got := teamMembers(t, dir)[0].Model; got != "" {
		t.Fatalf("dry-run mutated the profile: model = %q", got)
	}
}
