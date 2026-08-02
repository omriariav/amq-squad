package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func prepareRosterMutationFixture(t *testing.T, shape string) string {
	t.Helper()
	dir := t.TempDir()
	args := []string{
		"--project", dir,
		"--profile", team.DefaultProfile,
		"--session", "prepared",
		"--roles", "cto,qa",
		"--binary", "cto=codex,qa=codex",
		"--lead", "cto",
		"--launch-shape", shape,
		"--goal", "Keep roster mutations one-step",
		"--visibility", "detached",
		"--prepare",
		"--shared-cwd-exception", "test fixture: roster acceptance only",
	}
	if shape == runwizard.LaunchShapeLeadOnlyStaged {
		args = append(args, "--staged-roles", "qa")
	}
	_, _, err := captureOutput(t, func() error {
		return runRunStart(args, "test")
	})
	if err != nil {
		t.Fatalf("prepare roster fixture: %v", err)
	}
	return dir
}

func assertPreparedRosterReady(t *testing.T, dir string, beforeGeneration string) preparedRunManifest {
	t.Helper()
	manifest, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatalf("read refreshed preparation: %v", err)
	}
	if manifest.Generation == beforeGeneration {
		t.Fatalf("prepared generation was not refreshed: %s", manifest.Generation)
	}
	result := calculateRunReadinessWithContext(dir, team.DefaultProfile, "prepared", acceptedRunContext{
		Version:  manifest.Environment.BinaryVersion,
		Topology: manifest.Topology,
	})
	if !result.Ready {
		t.Fatalf("refreshed preparation is not ready: %+v", result.Rows)
	}
	return manifest
}

func TestTeamMemberUpdateRefreshesReadyPreparedGeneration(t *testing.T) {
	dir := prepareRosterMutationFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	before, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "qa", "--project", dir, "--effort", "xhigh"})
	})
	if err != nil {
		t.Fatalf("one-step member update: %v", err)
	}
	if !strings.Contains(out, "no separate prepare/accept round trip") {
		t.Fatalf("update did not report refreshed preparation:\n%s", out)
	}
	after := assertPreparedRosterReady(t, dir, before.Generation)
	if got := after.Members["qa"].Effort; got != "xhigh" {
		t.Fatalf("accepted qa effort = %q, want xhigh", got)
	}
}

func TestTeamMemberBinaryUpdateRefreshesReadyPreparedGeneration(t *testing.T) {
	dir := prepareRosterMutationFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	before, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if err := runTeamMember([]string{"update", "qa", "--project", dir, "--binary", "claude", "--effort", "xhigh"}); err != nil {
		t.Fatalf("one-step member binary update: %v", err)
	}
	after := assertPreparedRosterReady(t, dir, before.Generation)
	qa := after.Members["qa"]
	if qa.Binary != "claude" || qa.Effort != "xhigh" {
		t.Fatalf("accepted qa identity = %+v, want claude/xhigh", qa)
	}
	stored := teamMembers(t, dir)[1]
	if stored.Binary != "claude" || len(stored.CodexArgs) != 0 {
		t.Fatalf("stored binary switch retained incompatible args: %+v", stored)
	}
}

func TestTeamMemberUpdateDoesNotAcceptAlreadyDriftedPreparation(t *testing.T) {
	dir := prepareRosterMutationFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	before, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPathForProfile(dir, team.DefaultProfile, "prepared"), []byte("# unrelated drift\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error {
		return runTeamMember([]string{"update", "qa", "--project", dir, "--model", "new-model"})
	})
	if err != nil {
		t.Fatalf("member update on drifted preparation: %v", err)
	}
	after, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation {
		t.Fatalf("already-drifted preparation was implicitly accepted: before=%s after=%s", before.Generation, after.Generation)
	}
	if strings.Contains(out, "no separate prepare/accept round trip") {
		t.Fatalf("drifted preparation reported a refresh:\n%s", out)
	}
	if got := teamMembers(t, dir)[1].Model; got != "new-model" {
		t.Fatalf("roster update was not preserved: model=%q", got)
	}
	result := calculateRunReadinessWithContext(dir, team.DefaultProfile, "prepared", acceptedRunContext{
		Version: before.Environment.BinaryVersion, Topology: before.Topology,
	})
	if result.Ready {
		t.Fatal("unrelated brief drift was erased by the roster update")
	}
}

func TestTeamMemberUpdateRejectsConcurrentProfileChangeWithoutOverwritingIt(t *testing.T) {
	dir := prepareRosterMutationFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	before, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	previousHook := rosterPreparedBeforeMutation
	rosterPreparedBeforeMutation = func() {
		current, readErr := team.ReadProfile(dir, team.DefaultProfile)
		if readErr != nil {
			t.Fatalf("read concurrent team: %v", readErr)
		}
		current.Members[1].Model = "concurrent-model"
		if writeErr := team.WriteProfile(dir, team.DefaultProfile, current); writeErr != nil {
			t.Fatalf("write concurrent team: %v", writeErr)
		}
	}
	t.Cleanup(func() { rosterPreparedBeforeMutation = previousHook })

	err = runTeamMember([]string{"update", "qa", "--project", dir, "--effort", "xhigh"})
	if err == nil || !strings.Contains(err.Error(), "changed after accepted-readiness verification") {
		t.Fatalf("concurrent profile update error = %v", err)
	}
	if got := teamMembers(t, dir)[1].Model; got != "concurrent-model" {
		t.Fatalf("concurrent profile update was overwritten: model=%q", got)
	}
	after, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation {
		t.Fatalf("rejected concurrent update changed accepted generation: before=%s after=%s", before.Generation, after.Generation)
	}
}

func TestTeamMemberAddPreservesRosterWhenPreparedRefreshCannotValidateNewRole(t *testing.T) {
	dir := prepareRosterMutationFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	before, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	_, stderr, err := captureOutput(t, func() error {
		return runTeamMember([]string{"add", "new-role", "--project", dir, "--binary", "codex", "--json"})
	})
	if err != nil {
		t.Fatalf("member add with missing role contract: %v", err)
	}
	if !strings.Contains(stderr, "roster changed, but its accepted preparation was not refreshed") {
		t.Fatalf("missing refresh recovery warning:\n%s", stderr)
	}
	members := teamMembers(t, dir)
	if got := members[len(members)-1].Role; got != "new-role" {
		t.Fatalf("successful roster add was rolled back: %+v", members)
	}
	after, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation {
		t.Fatalf("invalid new role contract was accepted: before=%s after=%s", before.Generation, after.Generation)
	}
}

func TestTeamMemberRemoveAndAddRefreshReadyPreparedGeneration(t *testing.T) {
	for _, shape := range []string{runwizard.LaunchShapeWorkingTeamTogether, runwizard.LaunchShapeLeadOnlyStaged} {
		t.Run(shape, func(t *testing.T) {
			dir := prepareRosterMutationFixture(t, shape)
			before, err := readPreparedRunManifest(dir, team.DefaultProfile, "prepared")
			if err != nil {
				t.Fatal(err)
			}
			if err := runTeamMember([]string{"rm", "qa", "--project", dir}); err != nil {
				t.Fatalf("one-step member remove: %v", err)
			}
			afterRemove := assertPreparedRosterReady(t, dir, before.Generation)
			if _, exists := afterRemove.Members["qa"]; exists || len(afterRemove.StagedRoster) != 0 {
				t.Fatalf("removed qa remains accepted: members=%v staged=%v", afterRemove.Members, afterRemove.StagedRoster)
			}

			if err := runTeamMember([]string{"add", "qa", "--project", dir, "--binary", "codex"}); err != nil {
				t.Fatalf("one-step member add: %v", err)
			}
			afterAdd := assertPreparedRosterReady(t, dir, afterRemove.Generation)
			if shape == runwizard.LaunchShapeLeadOnlyStaged {
				if got := strings.Join(afterAdd.StagedRoster, ","); got != "qa" {
					t.Fatalf("lead-only staged roster = %q, want qa", got)
				}
				if _, exists := afterAdd.StagedMembers["qa"]; !exists {
					t.Fatalf("lead-only re-added qa is not accepted as staged: %+v", afterAdd.StagedMembers)
				}
			} else if _, exists := afterAdd.Members["qa"]; !exists {
				t.Fatalf("working-team re-added qa is not accepted: %+v", afterAdd.Members)
			}
		})
	}
}
