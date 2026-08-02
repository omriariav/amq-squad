package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// seedPreparedTeardownFixture creates the prepared-state footprint teardown is
// supposed to clear: the accepted manifest and its generation directory, plus
// the AMQ root and brief that rm already knew about.
func seedPreparedTeardownFixture(t *testing.T, project, profile, session string) (manifest, generations string) {
	t.Helper()
	manifest = preparedRunPath(project, profile, session)
	generations = preparedRunGenerationsPath(project, profile, session)
	if err := os.MkdirAll(filepath.Dir(manifest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte(`{"schema":3,"session":"`+session+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(generations, "abc123"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(generations, "abc123", "manifest.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return manifest, generations
}

func seedRmSessionRoot(t *testing.T, baseRoot, session string) string {
	t.Helper()
	root := filepath.Join(baseRoot, session)
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRmRemovesPreparedManifestAndGenerationState is the #598 root cause 2
// regression. rm printed "session removed" while leaving
// .amq-squad/prepared/<profile>/<session>.json and <session>.generations on
// disk. Every later launch then loaded that stale accepted preview and drifted
// permanently, which is what turned a failed launch into a bricked namespace.
func TestRmRemovesPreparedManifestAndGenerationState(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile, session := team.DefaultProfile, "bricked"

	seedRmSessionRoot(t, baseRoot, session)
	manifest, generations := seedPreparedTeardownFixture(t, project, profile, session)

	out, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: session,
		Profile: profile, Yes: true,
	})
	if err != nil {
		t.Fatalf("rm: %v", err)
	}
	if _, statErr := os.Stat(manifest); !os.IsNotExist(statErr) {
		t.Errorf("prepared manifest survived rm: %s (stat err %v)", manifest, statErr)
	}
	if _, statErr := os.Stat(generations); !os.IsNotExist(statErr) {
		t.Errorf("prepared generation state survived rm: %s (stat err %v)", generations, statErr)
	}
	// The print must be true, not merely reassuring: a verb that claims to have
	// removed the session while leaving the state that bricks it is worse than
	// one that refuses.
	for _, want := range []string{manifest, generations} {
		if !strings.Contains(out, want) {
			t.Errorf("rm output does not name the removed path %q\noutput:\n%s", want, out)
		}
	}
}

// TestRmOfOneSessionLeavesAnotherSessionsPreparedStateIntact is the scoping
// negative. The prepared tree is shared by every session in a profile, so a
// teardown that reached one directory too far would be a cross-session
// data-loss bug rather than a local one.
func TestRmOfOneSessionLeavesAnotherSessionsPreparedStateIntact(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile := team.DefaultProfile

	seedRmSessionRoot(t, baseRoot, "alpha")
	seedRmSessionRoot(t, baseRoot, "beta")
	_, _ = seedPreparedTeardownFixture(t, project, profile, "alpha")
	betaManifest, betaGenerations := seedPreparedTeardownFixture(t, project, profile, "beta")

	if _, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: "alpha",
		Profile: profile, Yes: true,
	}); err != nil {
		t.Fatalf("rm alpha: %v", err)
	}

	if _, err := os.Stat(betaManifest); err != nil {
		t.Errorf("rm of session alpha destroyed session beta's prepared manifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(betaGenerations, "abc123", "manifest.json")); err != nil {
		t.Errorf("rm of session alpha destroyed session beta's generation state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseRoot, "beta")); err != nil {
		t.Errorf("rm of session alpha destroyed session beta's AMQ root: %v", err)
	}
}

// TestRmClearsAnOrphanedPreparedManifestWithNoRootOrBrief closes the recovery
// deadlock. After a failed launch the AMQ root and brief can already be gone
// while the prepared manifest survives -- the exact bricked state. rm used to
// refuse that with "nothing to remove", so the one verb able to clear the
// orphan was the one that declined to look at it.
func TestRmClearsAnOrphanedPreparedManifestWithNoRootOrBrief(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile, session := team.DefaultProfile, "orphaned"
	if err := os.MkdirAll(baseRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	manifest, generations := seedPreparedTeardownFixture(t, project, profile, session)

	_, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: session,
		Profile: profile, Yes: true,
	})
	if err != nil {
		t.Fatalf("rm must clear an orphaned prepared manifest instead of refusing: %v", err)
	}
	if _, statErr := os.Stat(manifest); !os.IsNotExist(statErr) {
		t.Errorf("orphaned prepared manifest survived: %s", manifest)
	}
	if _, statErr := os.Stat(generations); !os.IsNotExist(statErr) {
		t.Errorf("orphaned generation state survived: %s", generations)
	}
}

// TestRmPreviewNamesPreparedStateBeforeRemoving keeps the confirm gate honest.
// The operator approves what the preview lists, so prepared state must appear
// there before rm is allowed to delete it.
func TestRmPreviewNamesPreparedStateBeforeRemoving(t *testing.T) {
	project := t.TempDir()
	baseRoot := filepath.Join(project, ".agent-mail")
	profile, session := team.DefaultProfile, "previewed"

	seedRmSessionRoot(t, baseRoot, session)
	manifest, generations := seedPreparedTeardownFixture(t, project, profile, session)

	// A declined confirmation renders the full preview and makes zero
	// filesystem changes, which is exactly the observation this needs.
	out, err := runRmExec(t, rmExecution{
		ProjectDir: project, BaseRoot: baseRoot, Session: session,
		Profile: profile, Confirm: strings.NewReader("n\n"),
	})
	if err != nil {
		t.Fatalf("declined rm must not error: %v", err)
	}
	for _, want := range []string{manifest, generations} {
		if !strings.Contains(out, want) {
			t.Errorf("preview omits prepared path %q\noutput:\n%s", want, out)
		}
	}
	if _, statErr := os.Stat(manifest); statErr != nil {
		t.Errorf("preview must not delete anything, but the manifest is gone: %v", statErr)
	}
	if _, statErr := os.Stat(generations); statErr != nil {
		t.Errorf("preview must not delete anything, but the generation state is gone: %v", statErr)
	}
}

// TestPreparedTeardownRefusesSymlinkedPreparedParentEscapingTheProject is
// Reviewer A's blocker 2, reproduced and closed.
//
// The original containment compared filepath.Dir(path) against
// filepath.Dir(manifest) -- the same value by construction -- so it was
// tautological and proved nothing. Making .amq-squad/prepared/<profile> a
// symlink to an external directory left the lexical paths looking
// project-local while the real files sat outside, and deleteSession would have
// RemoveAll'd state belonging to another project entirely.
//
// This is the destructive-cross-boundary direction, so the assertion is not
// merely "returns an error": the victim files must still exist afterwards.
func TestPreparedTeardownRefusesSymlinkedPreparedParentEscapingTheProject(t *testing.T) {
	project := t.TempDir()
	external := t.TempDir()
	profile, session := team.DefaultProfile, "victim"

	// The external state that must survive.
	victimManifest := filepath.Join(external, session+".json")
	victimGenerations := filepath.Join(external, session+".generations")
	if err := os.WriteFile(victimManifest, []byte(`{"schema":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(victimGenerations, "gen0001"), 0o755); err != nil {
		t.Fatal(err)
	}

	// .amq-squad/prepared/<profile> -> external
	preparedParent := filepath.Dir(preparedRunPath(project, profile, session))
	if err := os.MkdirAll(filepath.Dir(preparedParent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, preparedParent); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	var target rmTarget
	err := resolvePreparedRunTeardown(&target, "rm", project, profile, session)
	if err == nil {
		t.Fatalf("teardown accepted a prepared parent that resolves outside the project; target=%+v", target)
	}
	// The refusal reason tightened with blocker 3: escaping the project is now
	// a special case of failing the canonical-namespace identity check, so that
	// is the message. Still a refusal, on a strictly stronger rule.
	if !strings.Contains(err.Error(), "canonical prepared namespace") {
		t.Errorf("refusal must name the canonical-namespace requirement, got: %v", err)
	}
	if target.PreparedHas || target.GenerationsHas {
		t.Errorf("escaping paths must never be marked removable: %+v", target)
	}

	// The point of the whole check: the external state is untouched.
	if _, statErr := os.Stat(victimManifest); statErr != nil {
		t.Errorf("external manifest was destroyed: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(victimGenerations, "gen0001")); statErr != nil {
		t.Errorf("external generation state was destroyed: %v", statErr)
	}
}

// TestPreparedTeardownRefusesWhenContainmentCannotBeResolved pins the
// fail-closed direction. A teardown that cannot PROVE what it is about to
// delete must not delete it, which matches this verb's existing conservative
// posture everywhere else.
func TestPreparedTeardownRefusesWhenContainmentCannotBeResolved(t *testing.T) {
	project := filepath.Join(t.TempDir(), "does-not-exist")
	var target rmTarget
	err := resolvePreparedRunTeardown(&target, "rm", project, team.DefaultProfile, "s")
	if err == nil {
		t.Fatalf("an unresolvable project must refuse, got target=%+v", target)
	}
	if !strings.Contains(err.Error(), "cannot resolve project directory") {
		t.Errorf("refusal must name what could not be resolved, got: %v", err)
	}
}

// TestPathWithinResolvedRootRejectsSiblingPrefix guards the separator rule. A
// plain string prefix would accept "/tmp/project-evil" as inside "/tmp/project".
func TestPathWithinResolvedRootRejectsSiblingPrefix(t *testing.T) {
	root := filepath.Join("/tmp", "project")
	for path, want := range map[string]bool{
		root:                                  true,
		filepath.Join(root, "child"):          true,
		filepath.Join(root, "a", "b"):         true,
		filepath.Join("/tmp", "project-evil"): false,
		filepath.Join("/tmp", "other"):        false,
		filepath.Join("/tmp"):                 false,
	} {
		if got := pathWithinResolvedRoot(root, path); got != want {
			t.Errorf("pathWithinResolvedRoot(%q, %q) = %v, want %v", root, path, got, want)
		}
	}
}

// TestPreparedTeardownRefusesInProjectPreparedRedirects is Reviewer A's blocker
// 3, both variants, reproduced and closed.
//
// The previous containment proved only that a redirected prepared directory
// stayed somewhere inside the project. That is ancestry, and the invariant this
// function needs is identity: the directory IS this profile's prepared
// namespace, or teardown does not touch it. Redirecting at the project root or
// at any unrelated in-project directory passed the old check and made
// same-named artifacts belonging to something else removable.
func TestPreparedTeardownRefusesInProjectPreparedRedirects(t *testing.T) {
	for name, redirect := range map[string]func(project string) string{
		// The project root itself. pathWithinResolvedRoot allows equality, so
		// this variant passed the old check most explicitly of all.
		"project root": func(project string) string { return project },
		// Any unrelated in-project directory.
		"unrelated in-project dir": func(project string) string {
			dir := filepath.Join(project, "unrelated")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			return dir
		},
	} {
		t.Run(name, func(t *testing.T) {
			project := t.TempDir()
			profile, session := team.DefaultProfile, "victim"
			victimDir := redirect(project)

			// State that belongs to something else and must survive.
			victimManifest := filepath.Join(victimDir, session+".json")
			victimGenerations := filepath.Join(victimDir, session+".generations")
			if err := os.WriteFile(victimManifest, []byte(`{"schema":3}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(victimGenerations, "gen0001"), 0o755); err != nil {
				t.Fatal(err)
			}

			preparedParent := filepath.Dir(preparedRunPath(project, profile, session))
			if err := os.MkdirAll(filepath.Dir(preparedParent), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victimDir, preparedParent); err != nil {
				t.Skipf("symlinks unavailable on this platform: %v", err)
			}

			var target rmTarget
			err := resolvePreparedRunTeardown(&target, "rm", project, profile, session)
			if err == nil {
				t.Fatalf("teardown accepted an in-project prepared redirect; target=%+v", target)
			}
			if !strings.Contains(err.Error(), "canonical prepared namespace") {
				t.Errorf("refusal must name the canonical-namespace requirement, got: %v", err)
			}
			if target.PreparedHas || target.GenerationsHas {
				t.Errorf("redirected paths must never be marked removable: %+v", target)
			}
			if _, statErr := os.Stat(victimManifest); statErr != nil {
				t.Errorf("unrelated in-project manifest was destroyed: %v", statErr)
			}
			if _, statErr := os.Stat(filepath.Join(victimGenerations, "gen0001")); statErr != nil {
				t.Errorf("unrelated in-project generation state was destroyed: %v", statErr)
			}
		})
	}
}

// TestPreparedTeardownAcceptsTheCanonicalPreparedDirectory is the companion
// property, proved directly rather than inferred from the refusals above: the
// tightened rule must still accept ordinary, unredirected prepared state, or it
// would have broken teardown for every real namespace.
func TestPreparedTeardownAcceptsTheCanonicalPreparedDirectory(t *testing.T) {
	project := t.TempDir()
	profile, session := team.DefaultProfile, "ordinary"
	manifest, generations := seedPreparedTeardownFixture(t, project, profile, session)

	var target rmTarget
	if err := resolvePreparedRunTeardown(&target, "rm", project, profile, session); err != nil {
		t.Fatalf("canonical prepared state must be accepted: %v", err)
	}
	if !target.PreparedHas || !target.GenerationsHas {
		t.Fatalf("canonical prepared state must be marked removable: %+v", target)
	}
	if target.Prepared != manifest || target.Generations != generations {
		t.Errorf("target paths = %q,%q want %q,%q", target.Prepared, target.Generations, manifest, generations)
	}
}
