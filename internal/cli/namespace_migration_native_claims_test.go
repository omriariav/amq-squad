package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// #498 U7: BOTH DIRECTIONS for the fail-closed native-claim migration guard.
//
// The refusal half alone would be satisfied by a guard that blocked EVERY migration, so the compatibility half
// is not optional decoration -- it is the claim that this guard does not break migration for the population that
// exists today, which is legacy redelivery records.
func TestMigrationRefusesANamespaceHoldingNativeRecoveryClaims(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"

	for _, row := range []struct {
		name    string
		write   func(t *testing.T, dir string) string
		wantErr string
	}{
		{
			// THE REAL CASE: a current-prefix native claim, named exactly as the constructor would name it, so
			// this row cannot pass by using a name production never emits.
			name: "native claim refuses",
			write: func(t *testing.T, dir string) string {
				project := t.TempDir()
				_, path, err := newRecoveryTransitionRecord(
					completeNativeTransitionInput(project, profile, session, attemptID))
				if err != nil {
					t.Fatalf("the complete native fixture must be accepted: %v", err)
				}
				name := filepath.Base(path)
				if _, recognition := recognizeRecoveryTransitionName(name); recognition != recoveryNameRecognized {
					t.Fatalf("fixture is void: %q is not a recognized claim name, so the guard would ignore it", name)
				}
				full := filepath.Join(dir, name)
				if err := os.WriteFile(full, []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
				return full
			},
			wantErr: "cannot be migrated safely",
		},
		{
			// UNKNOWN IS NOT ABSENT. A transition-like name the parser cannot classify must block, because it
			// might BE a native claim and migrating it would corrupt the record we failed to read. Without this
			// row, a guard that treated malformed as "nothing here" would look correct.
			name: "malformed transition-like name refuses",
			write: func(t *testing.T, dir string) string {
				// Carries the current recovery prefix but no parseable kind/key, so the tri-state returns
				// Malformed rather than NotATransition.
				full := filepath.Join(dir, currentRecoveryTransitionPrefix+"not-a-valid-body.json")
				if _, recognition := recognizeRecoveryTransitionName(filepath.Base(full)); recognition != recoveryNameMalformed {
					t.Fatalf("fixture is void: %q is not classified Malformed", filepath.Base(full))
				}
				if err := os.WriteFile(full, []byte("{}"), 0o644); err != nil {
					t.Fatal(err)
				}
				return full
			},
			wantErr: "unknown is not absent",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			written := row.write(t, dir)

			var blockers []string
			inspectNamespaceNativeRecoveryClaims(dir, &blockers)

			if len(blockers) == 0 {
				t.Fatalf("no blocker raised for %s. Migrating it rewrites profile/session without recomputing "+
					"the namespace-derived claim key, leaving the claim unmatchable on read -- absent means no "+
					"prior claim, which means a SECOND DELIVERY.", written)
			}
			joined := strings.Join(blockers, "\n")
			if !strings.Contains(joined, row.wantErr) {
				t.Errorf("blocker must explain the refusal (want %q), got: %s", row.wantErr, joined)
			}
			if !strings.Contains(joined, filepath.Base(written)) {
				t.Errorf("blocker must NAME the offending file so an operator can act on it, got: %s", joined)
			}
		})
	}
}

// THE COMPATIBILITY HALF, AT THE HELPER LEVEL: a namespace holding only LEGACY redelivery records must not
// be blocked. RENAMED from TestMigrationStillAllows... per the ruling: it exercises the helper, not an
// actual plan or migration, and a name that claims more than the test checks is the same defect as a
// comment that outlives its code.
//
// Legacy claims carry no kind in the name and their identity is not namespace-derived, so the existing adapter
// rewrite is safe for them. If this row ever fails, the guard has become an outage for every namespace that has
// ever run a redelivery -- which is strictly worse than the corruption it was added to prevent.
func TestHelperAllowsANamespaceWithOnlyLegacyRedeliveryRecords(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, legacyRecoveryTransitionPrefix+strings.Repeat("c", 64)+".json")
	if err := os.WriteFile(legacy, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Anti-vacuity on the fixture itself: prove the guard's own parser classifies this as a recognized LEGACY
	// claim, not as an unrelated file. Otherwise this row would pass because the name was ignored entirely,
	// proving nothing about legacy tolerance.
	parsed, recognition := recognizeRecoveryTransitionName(filepath.Base(legacy))
	if recognition != recoveryNameRecognized || !parsed.Legacy {
		t.Fatalf("fixture is void: %q is not a recognized LEGACY claim (recognition=%v legacy=%t)",
			filepath.Base(legacy), recognition, parsed.Legacy)
	}

	// An ordinary attempt file alongside it, which must also be ignored.
	if err := os.WriteFile(filepath.Join(dir, "attempt-abc.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var blockers []string
	inspectNamespaceNativeRecoveryClaims(dir, &blockers)
	if len(blockers) != 0 {
		t.Fatalf("legacy-only namespace was BLOCKED: %s\nLegacy claims are not namespace-keyed, so the adapter "+
			"rewrite is safe for them; blocking here would make every namespace that ever ran a redelivery "+
			"unmigratable.", strings.Join(blockers, "\n"))
	}
}

// AN UNREADABLE DIRECTORY BLOCKS, for the same unknown-is-not-absent reason as a malformed name.
func TestMigrationRefusesWhenTheClaimDirectoryCannotBeInspected(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "session")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0o000); err != nil {
		t.Skipf("cannot make a directory unreadable in this environment: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(nested, 0o755) })

	var blockers []string
	inspectNamespaceNativeRecoveryClaims(dir, &blockers)
	if len(blockers) == 0 {
		t.Fatal("an unreadable claim directory raised no blocker. If the directory cannot be listed we do not " +
			"know whether native claims are present, and migrating anyway gambles the double-delivery outcome " +
			"on an unverified assumption.")
	}
	if !strings.Contains(strings.Join(blockers, "\n"), "cannot inspect") {
		t.Errorf("blocker must say the directory could not be inspected, got: %s", strings.Join(blockers, "\n"))
	}
}

// THE PLANNER-LEVEL WIRING TEST. This is the row dev-2's blocker 2 required and my first four rows could not
// substitute for.
//
// ALL FOUR of my other tests call inspectNamespaceNativeRecoveryClaims DIRECTLY, so deleting the single
// production call in namespace_migration_plan.go leaves every one of them green while migration happily
// corrupts native claims. That is the wiring gap I identified for the U7 revalidation earlier in this same
// delta, articulated as "the through-the-caller rule applies PER GUARD, not per file" — and then reproduced one
// file later in a guard I wrote myself. Naming a rule is not applying it.
//
// This row drives the REAL planner with the REAL fixture and requires the blocker, so removing the production
// call kills it.
func TestPlannerBlocksMigrationWhenTheSourceHoldsANativeRecoveryClaim(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)

	// The claim is placed under the SOURCE goal-attempts artifact the planner actually inspects, at a name the
	// REAL constructor produces -- not a hand-written filename, which could pass while production emits
	// something the guard never sees.
	goalDir := goalAttemptDir(fx.project, fx.source.Profile, fx.source.Session)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, claimPath, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(fx.project, fx.source.Profile, fx.source.Session, "attempt-abc"))
	if err != nil {
		t.Fatalf("the complete native fixture must be accepted: %v", err)
	}
	if err := os.WriteFile(claimPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ANTI-VACUITY ON PLACEMENT: if the claim did not land inside the directory the planner inspects, this row
	// would pass for the wrong reason once the guard was deleted... and fail to prove anything now.
	if filepath.Dir(claimPath) != goalDir {
		t.Fatalf("fixture is void: the claim landed at %q but the planner inspects %q", claimPath, goalDir)
	}

	plan, err := planNamespaceMigration(namespaceMigrationPlannerOptions{
		ProjectDir: fx.project, Source: fx.source, Target: fx.target,
		DryRun: true, Now: time.Now().UTC(), Probe: livenessProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("planning must succeed and REPORT a blocker rather than erroring: %v", err)
	}

	joined := strings.Join(plan.Blockers, "\n")
	if !strings.Contains(joined, "cannot be migrated safely") {
		t.Fatalf("the PLAN did not carry the native-claim blocker.\nMigration rewrites profile/session without "+
			"recomputing the namespace-derived claim key, so this namespace must refuse at plan time.\n"+
			"blockers:\n%s", joined)
	}
	if !strings.Contains(joined, filepath.Base(claimPath)) {
		t.Errorf("the blocker must NAME the offending claim so an operator can act on it; got:\n%s", joined)
	}
}

// A MISSING directory is genuinely absence and must NOT block -- the distinction the malformed case turns on.
func TestMigrationDoesNotBlockWhenThereIsNoClaimDirectoryAtAll(t *testing.T) {
	var blockers []string
	inspectNamespaceNativeRecoveryClaims(filepath.Join(t.TempDir(), "does-not-exist"), &blockers)
	if len(blockers) != 0 {
		t.Fatalf("a nonexistent goal-attempts tree blocked migration: %s\nNo directory means no claims to "+
			"corrupt; that absence is unambiguous, unlike an unreadable or unclassifiable one.",
			strings.Join(blockers, "\n"))
	}
}
