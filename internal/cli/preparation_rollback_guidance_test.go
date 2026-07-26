package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// #538 F3: the rollback guidance must be true for the case it is printed in, and
// it must be driven through the REAL preparation path.
//
// An earlier version of this test called the helper in isolation with a
// hand-built snapshot slice. That is precisely why F3 looked closed while being
// broken twice over in production: the profile was not in the preparation snapshot
// set at all, and the roster is created BEFORE preparation snapshots, so a freshly
// created profile would have looked pre-existing even if it had been. A helper test
// cannot see either problem. This one runs `run start --prepare` and reads the
// error the operator would actually get.
func TestFreshProfilePrepareFailureSaysTheProfileWasCreatedAndRemoved(t *testing.T) {
	project := t.TempDir()
	chdir(t, project)
	setupStrictFreshProjectAMQ(t)

	// A brand-new project: preparation will CREATE the profile, and the roster has
	// two mutation-capable members sharing one working directory, so
	// worktree_isolation blocks and preparation rolls back.
	args := []string{
		"--project", project, "--profile", "squad", "--session", "s1",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--roles", "cto,fullstack,backend-dev",
		"--binary", "cto=claude,fullstack=claude,backend-dev=codex",
		"--lead", "cto",
		"--goal", "exercise the fresh-profile rollback guidance",
		"--prepare",
	}
	_, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
	if err == nil {
		t.Fatal("a shared-cwd roster must fail preparation; got success")
	}
	msg := err.Error()
	if !strings.Contains(msg, "readiness failed") {
		t.Fatalf("fixture never reached readiness, so it does not exercise the guidance at all: %v", err)
	}
	if !strings.Contains(msg, "CREATED") || !strings.Contains(msg, "removed") {
		t.Fatalf("a fresh-profile prepare failure must say the profile was created and removed; got: %s", msg)
	}
	if !strings.Contains(msg, "new profile NAME") {
		t.Fatalf("must point at the creation-time forms, which do not need an existing profile; got: %s", msg)
	}
	// And the claim must be TRUE: the profile really is gone.
	if _, statErr := os.Stat(team.ProfilePath(project, "squad")); !os.IsNotExist(statErr) {
		t.Fatalf("guidance says the profile was removed, but %s still exists (stat err: %v)",
			team.ProfilePath(project, "squad"), statErr)
	}
}

// The complement, and the case the previous wording got wrong: when the profile
// already existed, it survives, so the fix-existing commands DO apply and the
// creation form would fail.
func TestPreExistingProfilePrepareFailureSaysTheProfileSurvives(t *testing.T) {
	project := t.TempDir()
	chdir(t, project)
	setupStrictFreshProjectAMQ(t)

	// Create the blocked roster FIRST, so preparation does not create it.
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"profile", "squad", "--project", project,
			"--roles", "cto,fullstack,backend-dev",
			"--binary", "cto=claude,fullstack=claude,backend-dev=codex",
			"--actor-mode", "fullstack=implementation,backend-dev=implementation",
			"--orchestrated", "--lead", "cto",
			"--session", "s1"})
	}); err != nil {
		t.Fatalf("seed pre-existing profile: %v", err)
	}
	profilePath := team.ProfilePath(project, "squad")
	if _, err := os.Stat(profilePath); err != nil {
		t.Fatalf("fixture profile missing: %v", err)
	}

	args := []string{
		"--project", project, "--profile", "squad", "--session", "s1",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--goal", "exercise the pre-existing rollback guidance",
		"--prepare",
	}
	_, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
	if err == nil {
		t.Fatal("a shared-cwd roster must fail preparation; got success")
	}
	msg := err.Error()
	if !strings.Contains(msg, "readiness failed") {
		t.Fatalf("fixture never reached readiness, so it does not exercise the guidance at all: %v", err)
	}
	if strings.Contains(msg, "CREATED") {
		t.Fatalf("this run did NOT create the profile; guidance must not claim it did. got: %s", msg)
	}
	if strings.Contains(msg, "new profile NAME") {
		t.Fatalf("the profile exists, so `new profile NAME` would fail; must not be offered. got: %s", msg)
	}
	// And the claim must be TRUE: the profile really does survive.
	if _, statErr := os.Stat(profilePath); statErr != nil {
		t.Fatalf("guidance says the profile is unchanged, but it is gone: %v", statErr)
	}
}

// The wording itself, pinned directly so the two branches cannot converge.
func TestPreparationRollbackGuidanceBranchesDiffer(t *testing.T) {
	created := preparationRollbackGuidance(false)
	survived := preparationRollbackGuidance(true)
	if created == survived {
		t.Fatal("the created and pre-existing branches must not produce the same text")
	}
	if !strings.Contains(created, "CREATED") || strings.Contains(created, "unchanged") {
		t.Fatalf("created branch wording is wrong: %s", created)
	}
	if !strings.Contains(survived, "unchanged") || strings.Contains(survived, "CREATED") {
		t.Fatalf("pre-existing branch wording is wrong: %s", survived)
	}
	_ = filepath.Separator
}
