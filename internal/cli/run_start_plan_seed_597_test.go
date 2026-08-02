package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// #597 guard 3: --prepare-plan is the READ-ONLY proposal stage, but the
// accepted-brief goal binding resolved against a brief path that only --prepare
// creates. On a fresh namespace it refused even though --seed-from named the
// content on the same command line.
//
// Tests drive runRunStart, the stable operator entry point, so the same
// unmodified file runs at the parent commit.

func seedPlanStageFixture(t *testing.T) (project, seedRef string) {
	t.Helper()
	project = t.TempDir()
	seedPath := filepath.Join(project, "seed-brief.md")
	if err := os.WriteFile(seedPath, []byte("# Tonight\n\nReal seeded brief content.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return project, "file:" + seedPath
}

func runPlanStage(t *testing.T, project, seedRef, goal string) (string, error) {
	t.Helper()
	args := []string{
		"--project", project, "--profile", team.DefaultProfile, "--session", "tonight",
		"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--visibility", "detached", "--prepare-plan",
	}
	if seedRef != "" {
		args = append(args, "--seed-from", seedRef)
	}
	if goal != "" {
		args = append(args, "--goal", goal)
	}
	out, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
	return out, err
}

// TestPlanStageBindsTheSeededGoal is the relaxation, and it asserts the EFFECT:
// the plan stage produces a goal binding, not merely that the old refusal
// stopped firing.
func TestPlanStageBindsTheSeededGoal(t *testing.T) {
	project, seedRef := seedPlanStageFixture(t)

	out, err := runPlanStage(t, project, seedRef, "")
	if err != nil {
		t.Fatalf("--prepare-plan with --seed-from must not refuse on a fresh namespace: %v", err)
	}
	// The observable outcome: the proposal reports a ready goal_binding row
	// carrying the accepted-brief source and a computed digest.
	if !strings.Contains(out, "goal_binding") {
		t.Fatalf("plan output has no goal_binding row:\n%s", out)
	}
	for _, want := range []string{runwizard.GoalBindingSourceAcceptedBrief, "digest="} {
		if !strings.Contains(out, want) {
			t.Errorf("goal binding does not carry %q; the plan stage must BIND the goal, not just stop refusing\n%s", want, out)
		}
	}
	// Read-only really is read-only: the plan stage must not have written the
	// brief it bound against.
	if _, statErr := os.Stat(briefPathForProfile(project, team.DefaultProfile, "tonight")); !os.IsNotExist(statErr) {
		t.Errorf("--prepare-plan wrote the brief; it must stay read-only (stat err %v)", statErr)
	}
}

// TestPlanStageStillRefusesWithNoGoalSource is what the guard still refuses.
// Relaxing it is not deleting it: a plan stage with no brief, no seed and no
// explicit goal has nothing to bind and must still say so.
func TestPlanStageStillRefusesWithNoGoalSource(t *testing.T) {
	project, _ := seedPlanStageFixture(t)

	_, err := runPlanStage(t, project, "", "")
	if err == nil {
		t.Fatal("a plan stage with no brief, no --seed-from and no --goal must still be refused")
	}
	// And the refusal must name BOTH escape hatches. Naming one is how the next
	// operator files the same issue.
	for _, want := range []string{"--seed-from", "--goal"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name %s; got: %v", want, err)
		}
	}
}

// TestPlanStageExplicitGoalStillWorks pins the pre-existing escape hatch, so
// the relaxation cannot quietly become the only way through.
func TestPlanStageExplicitGoalStillWorks(t *testing.T) {
	project, _ := seedPlanStageFixture(t)
	if _, err := runPlanStage(t, project, "", "Execute the explicit goal"); err != nil {
		t.Fatalf("--prepare-plan with an explicit --goal must work: %v", err)
	}
}

// runPrepareStage drives the MATERIALIZING stage on the same namespace.
func runPrepareStage(t *testing.T, project, seedRef, goal string) (string, error) {
	t.Helper()
	args := []string{
		"--project", project, "--profile", team.DefaultProfile, "--session", "tonight",
		"--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
		"--launch-shape", runwizard.LaunchShapeWorkingTeamTogether,
		"--visibility", "detached", "--prepare",
	}
	if seedRef != "" {
		args = append(args, "--seed-from", seedRef)
	}
	if goal != "" {
		args = append(args, "--goal", goal)
	}
	out, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
	return out, err
}

// TestSeededPlanAndPrepareConvergeEndToEnd is the guard 3 end-to-end proof.
//
// The first guard 3 attempt relaxed the PROPOSAL only. Preparation resolves the
// same binding BEFORE it writes the seeded brief, so --prepare-plan started
// succeeding while the matching --prepare still refused: the relaxation moved
// the failure later instead of removing it, and no test called prepare, so
// nothing caught it.
//
// A relaxation is not done when the refusal stops firing; it is done when the
// operator's whole workflow succeeds. This drives plan THEN prepare on one
// fresh namespace and asserts the final state, including the equality the
// design rests on: the accepted binding after prepare must equal the
// plan-stage binding, or plan and prepare silently disagree.
func TestSeededPlanAndPrepareConvergeEndToEnd(t *testing.T) {
	project, seedRef := seedPlanStageFixture(t)

	planOut, err := runPlanStage(t, project, seedRef, "")
	if err != nil {
		t.Fatalf("plan stage refused: %v", err)
	}
	planDigest := extractGoalBindingDigest(t, planOut)

	if _, err := runPrepareStage(t, project, seedRef, ""); err != nil {
		t.Fatalf("prepare refused after the plan stage accepted the same inputs; the relaxation moved the failure instead of removing it: %v", err)
	}

	// The brief the binding named must now exist and carry the seeded content.
	briefPath := briefPathForProfile(project, team.DefaultProfile, "tonight")
	data, readErr := os.ReadFile(briefPath)
	if readErr != nil {
		t.Fatalf("prepare must create the seeded brief at %s: %v", briefPath, readErr)
	}
	if !strings.Contains(string(data), "Real seeded brief content") {
		t.Errorf("brief does not carry the seeded content:\n%s", data)
	}

	// THE EQUALITY THE DESIGN RESTS ON.
	manifest, err := readPreparedRunManifest(project, team.DefaultProfile, "tonight")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.GoalDigest != planDigest {
		t.Errorf("accepted goal digest after prepare (%s) differs from the plan stage (%s); plan and prepare must bind the SAME goal", manifest.GoalDigest, planDigest)
	}
	if strings.TrimSpace(manifest.GoalText) == "" {
		t.Error("accepted goal text is empty after prepare")
	}
}

// TestPrepareWithNoSeedAndNoGoalStaysRefused keeps the materializing refusal.
// Relaxing the seeded path must not open an unseeded one.
func TestPrepareWithNoSeedAndNoGoalStaysRefused(t *testing.T) {
	project, _ := seedPlanStageFixture(t)
	if _, err := runPrepareStage(t, project, "", ""); err == nil {
		t.Fatal("prepare with no brief, no --seed-from and no --goal must still be refused")
	}
}

func extractGoalBindingDigest(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "goal_binding") {
			continue
		}
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "digest=") {
				return strings.TrimPrefix(field, "digest=")
			}
		}
	}
	t.Fatalf("no goal_binding digest in plan output:\n%s", out)
	return ""
}

// TestPlanStageRefusesAnUnresolvableSeed is the second guard 3 blocker.
//
// The first repair derived "a seed is pending" from the FLAG BEING NONEMPTY,
// so any garbage bypassed the missing-brief refusal and produced a ready plan
// while the later prepare resolved the reference for real and failed. Plan and
// prepare diverged again, just for a different input class. Flag presence is a
// string; resolution is evidence.
func TestPlanStageRefusesAnUnresolvableSeed(t *testing.T) {
	project, _ := seedPlanStageFixture(t)

	for name, ref := range map[string]string{
		"missing file":     "file:/definitely/not/here/brief.md",
		"unknown scheme":   "nonsense:whatever",
		"no scheme at all": "just-a-string",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runPlanStage(t, project, ref, "")
			if err == nil {
				t.Fatalf("--prepare-plan must refuse an unresolvable --seed-from %q instead of producing a ready plan", ref)
			}
			// PIN THE GUARD. Asserting only err != nil would pass on an
			// unrelated environment, roster, or missing-brief refusal, so it
			// would not prove the surviving guard is the one claimed.
			assertSeedResolutionRefusal(t, err, ref)
		})
	}
}

// TestPrepareRefusesAnUnresolvableSeedBeforeBinding holds a DIRECT prepare, with
// no plan stage first, to the identical standard.
func TestPrepareRefusesAnUnresolvableSeedBeforeBinding(t *testing.T) {
	project, _ := seedPlanStageFixture(t)
	const ref = "file:/definitely/not/here/brief.md"
	_, err := runPrepareStage(t, project, ref, "")
	if err == nil {
		t.Fatal("--prepare must refuse an unresolvable --seed-from")
	}
	assertSeedResolutionRefusal(t, err, ref)
	// And it must not have written a brief on the way to refusing.
	if _, statErr := os.Stat(briefPathForProfile(project, team.DefaultProfile, "tonight")); !os.IsNotExist(statErr) {
		t.Errorf("a refused prepare must leave no brief behind (stat err %v)", statErr)
	}
}

// assertSeedResolutionRefusal pins WHICH guard refused.
//
// A bare err != nil proves only that something objected, and several unrelated
// refusals (environment, roster, missing brief) reach the same call. The claim
// under test is specifically that the seed reference failed to resolve, so the
// message must name that and the offending reference.
func assertSeedResolutionRefusal(t *testing.T, err error, ref string) {
	t.Helper()
	if !strings.Contains(err.Error(), "--seed-from cannot be resolved") {
		t.Errorf("refusal does not name the seed-resolution guard; got: %v", err)
	}
	if !strings.Contains(err.Error(), ref) {
		t.Errorf("refusal does not name the offending reference %q; got: %v", ref, err)
	}
	// It must NOT be the missing-brief refusal wearing a different hat.
	if strings.Contains(err.Error(), "accepted brief goal binding") {
		t.Errorf("refusal came from the missing-brief guard, not seed resolution; got: %v", err)
	}
}
