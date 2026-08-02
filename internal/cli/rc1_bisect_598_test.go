package cli

import (
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// RC1 bisect (#598 root cause 1).
//
// The issue's stated mechanism is already FALSIFIED: preparing a fresh
// namespace, materializing exactly what spawn writes, and recalculating leaves
// readiness ready (see TestFreshNamespaceReadinessSurvivesSpawnMaterialization),
// and the render is deterministic so no timestamp reaches the digest.
//
// What remains is to find which condition of the LIVE brick the passing fixture
// does not reproduce. The live run differed in four ways, bisected one at a
// time rather than all at once. This file is delta 1: the namespace was created
// with --from-profile, cloning an existing roster, instead of being built
// directly from --roles.
//
// The fixture asserts the same invariant either way. If it stays ready, delta 1
// is excluded and the bisect moves on; if it drifts, the failure names the row
// and the differing lines, which is the mechanism.

// prepareRunStartClonedFixture prepares a namespace whose profile was CLONED
// from another profile, mirroring the live `--from-profile OLD --profile NEW`
// launch rather than the direct `--roles` construction used elsewhere.
func prepareRunStartClonedFixture(t *testing.T, shape string) (project, profile, session string) {
	t.Helper()
	project = t.TempDir()
	profile, session = "squad-clone-target", "prepared"

	// The source roster the live run cloned from, pinned to a DIFFERENT session
	// so the clone has to restamp it, exactly as the live path did.
	if err := team.WriteProfile(project, "squad-source", team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Trust:        "approve-for-me",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "earlier"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", project, "--profile", profile, "--session", session,
			"--from-profile", "squad-source", "--lead", "cto",
			"--launch-shape", shape, "--goal", "Execute the accepted RC1 bisect fixture",
			"--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatalf("prepare cloned-profile fixture: %v", err)
	}
	return project, profile, session
}

// TestRC1Delta1ClonedProfileReadinessSurvivesSpawnMaterialization is delta 1 of
// the bisect: does creating the namespace via --from-profile reproduce the
// drift that a directly-constructed namespace does not?
func TestRC1Delta1ClonedProfileReadinessSurvivesSpawnMaterialization(t *testing.T) {
	project, profile, session := prepareRunStartClonedFixture(t, runwizard.LaunchShapeWorkingTeamTogether)

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
		t.Fatalf("cloned-profile fixture must be ready before spawn")
	}

	// Capture the accepted render per role BEFORE spawn, in memory. The
	// persisted preview is #597 operator tooling; the investigation does not
	// need it and must not wait for it.
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
		t.Log("DELTA 1 EXCLUDED: --from-profile cloning does not reproduce the RC1 drift; readiness survives spawn materialization on a cloned profile too")
		return
	}

	beforeRows := readinessRowsByArtifact(before)
	for _, row := range after.Rows {
		if row.Status == "ready" {
			continue
		}
		t.Errorf("row %q flipped %q -> %q after spawn\n  evidence: %s", row.Artifact, beforeRows[row.Artifact].Status, row.Status, row.Evidence)
	}
	// Name the mechanism rather than only asserting a drift: re-render each role
	// post-spawn and diff it against the accepted render captured above.
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
		t.Errorf("DELTA 1 REPRODUCES: bootstrap render for %s changed across spawn; %d differing line(s):", member.Role, len(diff))
		for i, line := range diff {
			if i >= 10 {
				t.Errorf("  ... %d more", len(diff)-10)
				break
			}
			t.Errorf("  %s", line)
		}
	}
	t.Fatalf("cloned-profile namespace bricks: readiness passed pre-spawn and failed post-spawn (#598 RC1 delta 1)")
}
