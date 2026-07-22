package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func withPreparedStagedAuthorizerResolver(t *testing.T) {
	t.Helper()
	old := stagedAdmissionResolveAuthorizer
	stagedAdmissionResolveAuthorizer = func(_ string, profile string, session string, _ team.Team) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: "cto", Handle: "cto", Profile: profile, Session: session, PaneID: "%1"}, nil
	}
	t.Cleanup(func() {
		stagedAdmissionResolveAuthorizer = old
	})
}

func TestTeamMemberAdmitAndReplaceCreateVisibleImmutableClaims(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	withPreparedStagedAuthorizerResolver(t)

	out, _, err := captureOutput(t, func() error {
		return runTeamMemberStagedAdmission([]string{
			"qa", "--project", project, "--session", "prepared", "--actor-mode", "review", "--reason", "independent review",
		}, false)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"admitted staged member qa/qa", "actor_mode=review", "authorizer: cto/cto launch=initial-launch-id", "terminal launch remains pending"} {
		if !strings.Contains(out, want) {
			t.Fatalf("admit output missing %q:\n%s", want, out)
		}
	}
	first, err := currentPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, "qa")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err = captureOutput(t, func() error {
		return runTeamMemberStagedAdmission([]string{
			"qa", "--project", project, "--session", "prepared", "--actor-mode", "review",
			"--claim", first.ClaimID, "--reason", "replace completed reviewer",
		}, true)
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := currentPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, "qa")
	if err != nil {
		t.Fatal(err)
	}
	if second.ClaimID == first.ClaimID || second.Lifecycle.SupersedesClaimID != first.ClaimID || !strings.Contains(out, "supersedes: "+first.ClaimID) {
		t.Fatalf("replacement first=%+v second=%+v output=%s", first, second, out)
	}
}

func TestTeamMemberStagedAdmissionRequiresExplicitActorModeAndExactAuthorizerNamespace(t *testing.T) {
	project, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, project, token)
	withPreparedStagedAuthorizerResolver(t)
	if err := runTeamMemberStagedAdmission([]string{"qa", "--project", project, "--session", "prepared"}, false); err == nil || !strings.Contains(err.Error(), "--actor-mode is required") {
		t.Fatalf("missing actor mode error=%v", err)
	}
	stagedAdmissionResolveAuthorizer = func(_ string, profile string, _ string, _ team.Team) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: "cto", Handle: "cto", Profile: profile, Session: "other", PaneID: "%1"}, nil
	}
	if err := runTeamMemberStagedAdmission([]string{"qa", "--project", project, "--session", "prepared", "--actor-mode", "review"}, false); err == nil || !strings.Contains(err.Error(), "not default/prepared") {
		t.Fatalf("wrong namespace authorizer error=%v", err)
	}
}
