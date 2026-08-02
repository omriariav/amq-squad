package cli

import (
	"os"
	"path/filepath"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// RC1 bisect deltas 2 and 3 (#598 root cause 1).
//
// Delta 1 (--from-profile cloning) is excluded. Remaining differences between
// the passing fixture and the live brick:
//
//	delta 2  three members with mixed binaries (claude lead + claude + codex)
//	         vs a single-member cto fixture. The bootstrap embeds the current-team
//	         routing block, which lists EVERY member, so a multi-member mixed
//	         roster is materially more render surface.
//	delta 3  AMQ root resolution answering differently once
//	         .agent-mail/<profile>/<session> exists.
//
// Delta 3 carries a trap I need to disarm first. My earlier fixtures called
// materializeSpawnState, which creates the agent mailbox directory and thereby
// the AMQ root as a side effect. If preparation ALREADY created that root, then
// "before" and "after" both ran with the root present and the absent-to-present
// transition was never exercised at all -- meaning delta 3 is untested rather
// than excluded. TestRC1Delta3PreparationRootMaterializationIsObserved settles
// that explicitly instead of leaving it as an assumption.

// prepareRunStartMixedRosterFixture prepares a namespace with the live squad's
// actual shape: a claude lead and two workers, one claude and one codex.
func prepareRunStartMixedRosterFixture(t *testing.T, shape string) (project, profile, session string) {
	t.Helper()
	project = t.TempDir()
	profile, session = team.DefaultProfile, "prepared"

	// The live squad used CUSTOM roles (amq-dev-1, amq-dev-2), which must be
	// staged under .amq-squad/roles/<id>.md before preparation will accept
	// them. Seeding them is closer to the live shape than substituting catalog
	// roles would be.
	rolesDir := filepath.Join(project, ".amq-squad", "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, roleID := range []string{"amq-dev-1", "amq-dev-2"} {
		doc := "---\nid: " + roleID + "\nlabel: " + roleID + "\n---\n\n# Role: " + roleID + "\n\nImplement assigned work.\n"
		if err := os.WriteFile(filepath.Join(rolesDir, roleID+".md"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", project, "--profile", profile, "--session", session,
			"--roles", "cto,amq-dev-1,amq-dev-2",
			"--binary", "cto=claude", "--binary", "amq-dev-1=claude", "--binary", "amq-dev-2=codex",
			"--lead", "cto",
			// The live squad had all three members in ONE working directory,
			// which readiness fails closed on without a recorded exception.
			// Reproducing that shape is the point of this delta.
			"--shared-cwd-exception", "rc1 bisect fixture reproduces the live single-checkout squad",
			"--launch-shape", shape, "--goal", "Execute the accepted mixed-roster fixture",
			"--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatalf("prepare mixed-roster fixture: %v", err)
	}
	return project, profile, session
}

// TestRC1Delta3PreparationRootMaterializationIsObserved records whether
// preparation creates the AMQ session root, because that determines whether the
// other bisect fixtures ever exercised the root absent-to-present transition at
// all. It asserts nothing about which answer is correct; it exists so the bisect
// cannot quietly rest on an untested assumption.
func TestRC1Delta3PreparationRootMaterializationIsObserved(t *testing.T) {
	project, profile, session := prepareRunStartMixedRosterFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	root := squadnamespace.AMQRoot(project, profile, session)

	_, err := os.Stat(root)
	switch {
	case err == nil:
		t.Logf("DELTA 3 NOTE: preparation ALREADY materializes the AMQ root at %s. Every fixture that calls materializeSpawnState therefore ran both readiness passes with the root PRESENT, so the absent-to-present transition is NOT covered by them and delta 3 remains open.", root)
	case os.IsNotExist(err):
		t.Logf("DELTA 3 NOTE: preparation does NOT create the AMQ root; it first appears when spawn state is materialized. The existing fixtures therefore DO cover the absent-to-present transition, and delta 3 is excluded by them.")
	default:
		t.Fatalf("stat AMQ root %s: %v", root, err)
	}

	// Whether or not the root exists, record what the per-agent directories look
	// like, since those feed RolePath/LaunchPath in the bootstrap render.
	for _, handle := range []string{"cto", "amq-dev-1", "amq-dev-2"} {
		agentDir := filepath.Join(root, "agents", handle)
		if _, statErr := os.Stat(agentDir); statErr == nil {
			t.Logf("  agent dir already present after preparation: %s", agentDir)
		}
	}
}

// TestRC1Delta2MixedBinaryRosterReadinessSurvivesSpawnMaterialization is delta
// 2: does a three-member mixed-binary roster reproduce the drift that a
// single-member fixture does not?
func TestRC1Delta2MixedBinaryRosterReadinessSurvivesSpawnMaterialization(t *testing.T) {
	project, profile, session := prepareRunStartMixedRosterFixture(t, runwizard.LaunchShapeWorkingTeamTogether)

	manifest, err := readPreparedRunManifest(project, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	accepted := acceptedRunContext{Version: manifest.Environment.BinaryVersion, Topology: manifest.Topology}

	before := calculateRunReadinessWithContext(project, profile, session, accepted)
	if !before.Ready {
		for _, row := range before.Rows {
			if row.Status != "ready" {
				t.Logf("pre-spawn row %s/%s: %s", row.Artifact, row.Status, row.Evidence)
			}
		}
		t.Fatalf("mixed-roster fixture must be ready before spawn")
	}

	tm, err := team.ReadProfile(project, profile)
	if err != nil {
		t.Fatal(err)
	}
	binding := acceptedGoalBinding{
		Text: manifest.GoalText, Source: manifest.GoalSource,
		Digest: manifest.GoalDigest, Namespace: manifest.GoalNamespace,
	}
	acceptedRender := map[string]string{}
	for _, member := range tm.Members {
		prompt, renderErr := preparedBootstrap(project, profile, session, binding, tm, member, accepted)
		if renderErr != nil {
			t.Fatalf("accepted render for %s: %v", member.Role, renderErr)
		}
		acceptedRender[member.Role] = prompt
	}

	for _, roleID := range manifest.InitialRoster {
		materializeSpawnState(t, project, profile, session, roleID, roleID)
	}

	after := calculateRunReadinessWithContext(project, profile, session, accepted)
	if after.Ready {
		t.Log("DELTA 2 EXCLUDED: a three-member mixed-binary roster does not reproduce the RC1 drift either")
		return
	}

	beforeRows := readinessRowsByArtifact(before)
	for _, row := range after.Rows {
		if row.Status == "ready" {
			continue
		}
		t.Errorf("row %q flipped %q -> %q after spawn\n  evidence: %s", row.Artifact, beforeRows[row.Artifact].Status, row.Status, row.Evidence)
	}
	for _, member := range tm.Members {
		post, renderErr := preparedBootstrap(project, profile, session, binding, tm, member, accepted)
		if renderErr != nil {
			t.Errorf("post-spawn render for %s failed: %v", member.Role, renderErr)
			continue
		}
		diff := diffPromptLines(acceptedRender[member.Role], post)
		if len(diff) == 0 {
			continue
		}
		t.Errorf("DELTA 2 REPRODUCES: bootstrap render for %s changed across spawn; %d differing line(s):", member.Role, len(diff))
		for i, line := range diff {
			if i >= 10 {
				t.Errorf("  ... %d more", len(diff)-10)
				break
			}
			t.Errorf("  %s", line)
		}
	}
	t.Fatalf("mixed-binary roster bricks: readiness passed pre-spawn and failed post-spawn (#598 RC1 delta 2)")
}
