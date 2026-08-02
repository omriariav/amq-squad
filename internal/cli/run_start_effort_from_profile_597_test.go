package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// #597 guard 4: --effort was unusable with --from-profile.
//
// --roles and --from-profile are mutually exclusive roster sources, so under
// --from-profile the --roles-derived selection is empty and every --effort key
// read as "not selected by --roles". The guard was not wrong about its own
// predicate; it was being asked the wrong question.

func seedEffortSourceProfile(t *testing.T, project string) {
	t.Helper()
	// "challenger" is a custom role, so preparation requires it staged under
	// .amq-squad/roles/ before it will accept the roster.
	rolesDir := filepath.Join(project, ".amq-squad", "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "---\nid: challenger\nlabel: challenger\n---\n\n# Role: challenger\n\nChallenge the plan.\n"
	if err := os.WriteFile(filepath.Join(rolesDir, "challenger.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(project, "source-squad", team.Team{
		Orchestrated: true,
		Lead:         "cto",
		// Two mutation-capable members in one checkout, recorded on the SOURCE
		// so the clone inherits it. The run-start flag only reaches a profile
		// created via `new team`, which the --from-profile path does not use.
		SharedCwdException: "597 guard 4 regression fixture",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "earlier"},
			{Role: "challenger", Binary: "claude", Handle: "challenger", Session: "earlier"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// preflightEffortIssue runs the real preflight entry point and returns any
// invalid-effort issue.
//
// It goes through runStartPreflight rather than calling the validator directly
// ON PURPOSE. The fix changes the validator's signature, so a direct unit test
// cannot compile at the parent commit, and a test that only fails to BUILD
// proves nothing about behavior. runStartPreflight's signature is stable, so
// these cases compile at the parent and fail there for the right reason.
func preflightEffortIssue(project, roles, fromProfile, effort string) string {
	res := runStartPreflight(runStartPreflightInput{
		Project: project, Profile: "target-squad", Session: "tonight",
		Roles: roles, FromProfile: fromProfile, FromProfileSet: fromProfile != "",
		Effort: effort, EffortSet: effort != "",
	})
	for _, issue := range res.Issues {
		if issue.Code == runStartPreflightInvalidEffort {
			return issue.Detail
		}
	}
	return ""
}

// TestRunStartEffortAcceptsRolesFromClonedProfile is the relaxation: an effort
// key naming a role that exists in the cloned roster is now accepted.
func TestRunStartEffortAcceptsRolesFromClonedProfile(t *testing.T) {
	project := t.TempDir()
	seedEffortSourceProfile(t, project)

	for _, effort := range []string{"cto=xhigh", "challenger=high", "cto=xhigh,challenger=low"} {
		if detail := preflightEffortIssue(project, "", "source-squad", effort); detail != "" {
			t.Errorf("--effort %q with --from-profile must be accepted, got: %s", effort, detail)
		}
	}
}

// TestRunStartEffortStillRefusesRolesInNeitherSource is the protection that had
// to survive the relaxation. Relaxing a guard is not deleting it: a typo'd role
// silently doing nothing is the failure this check exists to prevent, and it is
// still refused.
func TestRunStartEffortStillRefusesRolesInNeitherSource(t *testing.T) {
	project := t.TempDir()
	seedEffortSourceProfile(t, project)

	detail := preflightEffortIssue(project, "", "source-squad", "ctoo=xhigh")
	if detail == "" {
		t.Fatal("an --effort key naming a role in neither --roles nor the cloned roster must still be refused")
	}
	if !strings.Contains(detail, "ctoo") || !strings.Contains(detail, "--from-profile") {
		t.Errorf("refusal must name the unknown role AND the real selection source, got: %s", detail)
	}

	// And the pre-existing --roles path is untouched.
	if detail := preflightEffortIssue(project, "cto", "", "nope=high"); detail == "" {
		t.Error("--effort naming a role outside --roles must still be refused")
	}
}

// TestRunStartEffortMissingSourceProfileDefersToItsOwnRefusal keeps this check
// from burying a better error. A --from-profile that cannot be read has a
// dedicated refusal that says so; reporting it here as an effort problem would
// point the operator at the wrong flag.
func TestRunStartEffortMissingSourceProfileDefersToItsOwnRefusal(t *testing.T) {
	project := t.TempDir()
	if detail := preflightEffortIssue(project, "", "does-not-exist", "cto=xhigh"); detail != "" {
		t.Errorf("a missing source profile must not surface as an effort error: %s", detail)
	}
}

// TestRunStartEffortIsActuallyAppliedToTheClonedRoster is the blocker
// regression, and it goes through the OPERATOR ENTRY POINT deliberately.
//
// Two things it has to survive that earlier attempts did not. It must assert
// the PRESENCE OF THE EFFECT rather than the absence of a refusal, because the
// first version of this fix was a silent no-op that a refusal-absence test
// passed. And it must call only stable signatures, because the fix changes
// runStartCloneRosterProfile's arity: a test calling that helper directly
// cannot compile at either parent, so it can prove nothing about them without
// being hand-edited per commit, which is not evidence.
//
// runRunStart's signature is stable across all three states, so this exact
// checked-in test compiles and runs at 08d03da, at 883df06, and here.
func TestRunStartEffortIsActuallyAppliedToTheClonedRoster(t *testing.T) {
	project := t.TempDir()
	seedEffortSourceProfile(t, project)

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", project, "--profile", "target-squad", "--session", "tonight",
			"--from-profile", "source-squad", "--lead", "cto",
			"--effort", "cto=xhigh",
			"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
			"--goal", "Execute the guard 4 effort fixture",
			"--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatalf("run start --from-profile --effort --prepare: %v", err)
	}

	cloned, err := team.ReadProfile(project, "target-squad")
	if err != nil {
		t.Fatal(err)
	}
	native := map[string]string{}
	for _, m := range cloned.Members {
		native[m.Role] = strings.Join(append(append([]string{}, m.CodexArgs...), m.ClaudeArgs...), " ")
	}
	// THE EFFECT: the requested effort reached the cloned member's native args.
	if !strings.Contains(native["cto"], "xhigh") {
		t.Errorf("--effort cto=xhigh was accepted but never applied to the clone; cto native args = %q", native["cto"])
	}
	// And did not leak onto a member it did not name.
	if strings.Contains(native["challenger"], "xhigh") {
		t.Errorf("effort leaked onto an unnamed member; challenger native args = %q", native["challenger"])
	}

	// NO-REWRITE: --from-profile must not mutate what it clones from.
	source, err := team.ReadProfile(project, "source-squad")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range source.Members {
		joined := strings.Join(append(append([]string{}, m.CodexArgs...), m.ClaudeArgs...), " ")
		if strings.Contains(joined, "xhigh") {
			t.Errorf("cloning mutated the SOURCE member %q: %q", m.Role, joined)
		}
		if m.Session != "earlier" {
			t.Errorf("cloning re-pinned the SOURCE member %q to %q", m.Role, m.Session)
		}
	}
}
