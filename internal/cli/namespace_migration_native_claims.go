package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

// #498 U7: FAIL-CLOSED GUARD against migrating a namespace that holds NATIVE recovery-transition claims.
//
// WHY THIS EXISTS. The migration adapter rewrites a transition record's Profile and Session and re-marshals it
// (namespace_migration_adapters.go, the .resume-redelivery- branch). For a NATIVE claim that is silent
// corruption: the claim key is derived from NamespaceID(profile, session) + pause generation + attempt, so
// rewriting profile/session WITHOUT recomputing the identity leaves a record whose id no longer matches its own
// namespace. Unmatchable on read means treated as ABSENT, and absent means "no prior claim" -- which is a SECOND
// DELIVERY of an audited resume. That is precisely the failure #498 exists to prevent, reached through a code
// path nobody had enumerated until the re-decider sweep.
//
// The adapter also only recognises the LEGACY ".resume-redelivery-" prefix, so a current-prefix native file
// falls through to the goal-attempt branch and fails the migration in a confusing way rather than a clear one.
//
// SCOPE, deliberately minimal and ruled: this guard REFUSES. It does not recompute claim keys and it does not
// teach the adapter the native prefix. Full native-migration support is a separate v2.26.0 issue ranked above
// #583 because it is a correctness bug rather than breadth. Silent corruption becomes an explicit refusal, and
// that is the whole intended delta.
//
// IT IS A NAMESPACE PRE-CHECK, not a per-file refusal, and that placement is load-bearing: a per-file refusal
// discovered mid-rewrite would abort a multi-file migration halfway, which is its own corruption mode. The
// question this answers is "is this namespace safe to migrate AT ALL", asked before anything is rewritten.
func inspectNamespaceNativeRecoveryClaims(goalAttemptsSource string, blockers *[]string) {
	if goalAttemptsSource == "" {
		return
	}
	entries, err := os.ReadDir(goalAttemptsSource)
	if err != nil {
		if os.IsNotExist(err) {
			// No goal-attempts tree at all means no claims to corrupt. Absence here is genuinely absence,
			// unlike the malformed case below, because the directory's non-existence is not ambiguous.
			return
		}
		// UNREADABLE IS NOT EMPTY. If the directory cannot be listed we do not know whether native claims are
		// present, and proceeding would gamble the double-delivery outcome on an unverified assumption.
		*blockers = append(*blockers, fmt.Sprintf(
			"cannot inspect %s for native recovery-transition claims, so migration safety cannot be established: %v",
			goalAttemptsSource, err))
		return
	}
	for _, entry := range entries {
		// SYMLINKS ARE SKIPPED EXPLICITLY, before the directory branch.
		//
		// os.ReadDir reports entry types from the directory record without following links, so a symlink to a
		// directory already has IsDir()==false and could not drive recursion. This guard therefore changes no
		// behaviour -- it makes the cycle-safety LOCAL instead of inherited from a stdlib detail every future
		// reader has to re-derive. A traversal whose termination depends on an unstated property of its
		// iteration primitive is correct by coincidence.
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		// RECURSE TO ANY DEPTH. The comment here previously said "recurse one level" while the code recursed on
		// every directory entry -- the behaviour was intended and the comment was wrong, which is
		// comment-outlives-code at birth. Unbounded is CORRECT rather than merely tolerated: claims live beside
		// attempt records under per-session directories, and a depth guess would silently miss a native claim
		// one level below wherever the guess landed, which is exactly the failure this guard exists to prevent.
		if entry.IsDir() {
			inspectNamespaceNativeRecoveryClaims(filepath.Join(goalAttemptsSource, entry.Name()), blockers)
			continue
		}
		// TWO-STEP RECOGNITION, IN THE SCAN'S ORDER -- companion base FIRST, then the name parser (codex
		// finding 2).
		//
		// THE COMMENT THAT USED TO BE HERE WAS FALSE, and its falsity is the whole defect. It said
		// "ONE RECOGNITION OWNER: recognizeRecoveryTransitionName is the same parser the pause scan uses, so
		// this guard cannot drift from the scanner's idea of what a claim file is." The scan recognises in TWO
		// steps -- companionReservationBase at recovery_transition_scan.go:95, THEN
		// recognizeRecoveryTransitionName at :101. This guard adopted the second and not the first, then
		// asserted non-drift. A PARTIAL COPY OF A TWO-STEP DECIDER, under a comment claiming it is the whole
		// decider, is worse than an honest second opinion: the comment is what stops the next reader looking.
		//
		// WHAT THE MISS COST. recognizeRecoveryTransitionName classifies .consumed.json/.bound.json as
		// recoveryNameNotATransition (recovery_transition.go:189) because companions "are handled by their own
		// path" -- true in the scan, which has that other path, and false here, which had none. So a native
		// CONSUMED companion raised no blocker; the adapter copies it content-unchanged
		// (namespace_migration_adapters.go:131-137) and the copier preserves its basename verbatim
		// (namespace_migration_tx.go:442), carrying the OLD namespace-derived claim key into the new
		// namespace. In the destination, companionBelongsToPause matches by substring against the NEW claim
		// key (scan.go:218-220), so the companion is never collected -- which means neither the consumed-state
		// blocker NOR the orphan-companion blocker can fire for it. The prior consumption becomes invisible to
		// the claim-once decision, and invisible prior delivery is a SECOND delivery.
		//
		// NO RESERVATION-REQUIRED PRECONDITION, ruled. It is tempting to block only companions whose
		// reservation is absent, since a present native reservation already blocks on its own. That would make
		// this guard's correctness depend on the ORDER files are read and on another entry existing, and the
		// question here is "is this namespace safe to migrate AT ALL". A companion carrying a namespace-derived
		// key in its NAME is exactly as unmigratable as its reservation, so it is judged on its own.
		name := entry.Name()
		inspect, isCompanion := name, false
		if base, ok := companionReservationBase(name); ok {
			// The companion is judged by the RESERVATION NAME it belongs to, because that is where the claim
			// key lives -- companion paths are the reservation path plus a suffix (resume_goal.go:431,435).
			inspect, isCompanion = base+".json", true
		}
		subject := "native recovery-transition claim"
		unreadable := "recovery-transition-like file"
		if isCompanion {
			subject = "native recovery-transition COMPANION (consumption/binding evidence)"
			unreadable = "recovery-transition-like COMPANION file"
		}
		parsed, recognition := recognizeRecoveryTransitionName(inspect)
		switch recognition {
		case recoveryNameRecognized:
			// LEGACY-shaped records are what the adapter already handles: they carry no kind in the name and
			// their identity does not depend on the namespace, so rewriting profile/session is safe for them.
			// Only current-derivation claims are refused.
			if parsed.Legacy {
				continue
			}
			*blockers = append(*blockers, fmt.Sprintf(
				"%s %s cannot be migrated safely: its claim key is derived from the namespace, so rewriting profile/session would make it unmatchable on read (treated as ABSENT, permitting a second delivery). CONSUME the claim, or complete the resume it records, before migrating -- do NOT delete the reservation: removing it leaves an orphan companion, which the pause scan treats as tampering and which this guard would then be the only thing standing between you and a corrupted record. Native-aware migration is tracked separately",
				subject, filepath.Join(goalAttemptsSource, name)))
		case recoveryNameMalformed:
			// UNKNOWN IS NOT ABSENT -- the same rule the pause scan enforces, and the reason this case is
			// listed explicitly rather than folded into the default. A transition-like name we cannot classify
			// might be a native claim; treating it as "no native claims present" would migrate anyway and
			// corrupt exactly the record we failed to read.
			*blockers = append(*blockers, fmt.Sprintf(
				"%s %s cannot be classified, so it cannot be shown safe to migrate: unknown is not absent, and a native claim here would be corrupted by a namespace rewrite",
				unreadable, filepath.Join(goalAttemptsSource, name)))
		}
		// recoveryNameNotATransition: an ordinary attempt/claim file. Ignored, because blocking on those would
		// make every namespace unmigratable, which is amendment 1 overcorrected into an outage. A companion of
		// an ordinary file lands here too, and correctly: <attempt>.claim.json is not a companion suffix, so
		// nothing ordinary reaches the companion branch above.
	}
}
