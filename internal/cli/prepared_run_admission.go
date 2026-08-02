package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// #573: `team resume` previewed prepared and staged actors as will-launch and emitted a
// command that `agent up` then REFUSED, because admission requires a single-use spawn
// reservation the preview knew nothing about. Preview and execution disagreed, and the
// operator was handed a command the binary rejects.
//
// preparedRunActorAdmission is the SINGLE answer to "would admission refuse this actor for
// lack of a reservation, and what binds one?". Admission (launch.go) and the resume planner
// (team_resume.go) both call it, so they cannot disagree about the verdict OR about the
// wording. Duplicating the condition is what produced #573 in the first place: the
// `!prepared || !containsRole(manifest.StagedRoster, role)` clause was inlined at five
// sites, and the planner simply was not one of them.
//
// RECORD-AWARE ON PURPOSE. "Governed by an accepted prepared generation" does NOT predict
// admission. execRestoreRecord (restore.go) takes the token FROM THE PERSISTED RECORD and
// falls back to an EMPTY token when the record carries none -- which admission refuses. So a
// manifest-only predicate would report an actor bindable and then fail at exec, recreating
// the very preview/execution disagreement this type exists to kill, one layer down.
type preparedRunAdmission struct {
	// Governed is true when an accepted prepared generation covers this session.
	Governed bool
	// Staged is true when the actor is in the staged roster rather than the initial one.
	// It selects BOTH the refusal wording and the recovery command, which is why admission
	// has always had two distinct messages here.
	Staged bool
	// Bindable is true when a persisted launch record already carries a COMPLETE token, so
	// the managed restore path can bind without operator input.
	Bindable bool
	// Generation is the accepted prepared generation, named in the refusal so the operator
	// can tell which reservation is being asked for.
	Generation string
	// Reason is the refusal text, owned HERE rather than at each call site. Admission and
	// the planner render the same sentence because there is only one sentence.
	Reason string
	// Recovery is the exact operator-runnable command that binds a reservation. Empty when
	// no reservation is required.
	Recovery string
}

// required reports whether an actor with no in-process token would be refused.
func (a preparedRunAdmission) required() bool { return a.Governed }

// preparedRunActorAdmission classifies an actor against an ALREADY-LOADED manifest.
//
// The manifest is passed in rather than read here so admission, which has it in hand,
// does not re-read it. A second read would open a window in which the preview and the
// admission decision are made against different generations -- a different flavour of the
// same disagreement.
//
// rec may be nil, meaning "no persisted record was found": that is not bindable.
func preparedRunActorAdmission(manifest preparedRunManifest, digest string, prepared bool, role, handle string, rec *launch.Record) preparedRunAdmission {
	adm := preparedRunAdmission{Generation: strings.TrimSpace(manifest.Generation)}

	// #579 finding 1: Bindable required all four GENERATION fields and ignored
	// PreparedRunLaunchAttempt. token.complete() does not check it either. A record with a
	// complete generation but no reserved attempt previewed will-launch and then managed resume
	// refused it at prepared_run_state.go:312 -- the exact defect this predicate exists to kill,
	// one field over. Bindable now means "the managed restore path can actually bind this",
	// which requires the attempt.
	// #579 round 3 F1: shape is not agreement. A COMPLETE, claim-bound token from a SUPERSEDED
	// generation satisfied every field check, previewed as bindable (team_resume treats Bindable
	// as not-blocked and offers `agent resume <role>`), and exec-side validation then refused it
	// -- the refuse-on-exec/allow-in-preview defect this predicate exists to kill, one comparison
	// short. samePreparedRunGeneration exists for exactly this comparison.
	//
	// My round-2 commit claimed "Bindable now means the managed restore path can actually bind
	// this". That was false until this AND: it meant "the token has the right shape".
	bindable := false
	if rec != nil {
		token := preparedRunTokenFromRecord(*rec)
		bindable = token.complete() &&
			strings.TrimSpace(token.LaunchAttempt) != "" &&
			samePreparedRunGeneration(token, preparedRunTokenFromSnapshot(manifest, digest))
	}

	if !prepared {
		// #579 finding 2: returning here unconditionally ignored a record that CARRIES a
		// prepared token while the session reads unprepared. The preview then emitted a plain
		// launch that execution rejects, because the record's token is validated against a
		// manifest that is absent or different. A token-bearing record in a session with no
		// accepted generation is CONTRADICTORY evidence, and contradictory evidence is
		// ineligible, never best-effort.
		// #579 round 3 F2: empty() checks ONLY the four generation fields -- LaunchAttempt is not
		// among them -- so a record carrying an orphaned attempt and nothing else fell through as
		// UNGOVERNED and previewed a plain launch. An orphaned attempt IS prepared-run evidence,
		// and evidence that contradicts an unprepared session must be governed, not ignored.
		if rec != nil && recordCarriesPreparedRunEvidence(*rec) {
			adm.Governed = true
			adm.Reason = fmt.Sprintf("actor %s/%s carries a prepared-run token but this session has no accepted prepared generation; the token cannot be validated and execution will refuse it", role, handle)
			// Fold-in of the round-2 residual: this branch has no accepted manifest, so
			// manifest.Project is the zero value and the emitted form was --project '' -- an
			// empty-quoted flag that LOOKS filled beside placeholders that look like
			// placeholders. Blank fields render as visible placeholders.
			adm.Recovery = "amq-squad run start --project " + projectOrPlaceholder(manifest.Project) + " --profile <profile> --session <session>"
			return adm
		}
		return adm
	}
	adm.Governed = true
	adm.Staged = containsRole(manifest.StagedRoster, role)
	adm.Bindable = bindable

	if adm.Staged {
		adm.Reason = fmt.Sprintf("staged actor %s/%s requires an exact single-use spawn reservation for prepared generation %s",
			role, handle, adm.Generation)
	} else {
		adm.Reason = fmt.Sprintf("prepared actor %s/%s requires the exact reserved generation token", role, handle)
	}

	// #579 findings 3 and 4: every emitted form is now checked against the REAL command
	// surface. The previous staged form omitted the mandatory <binary> positional and failed
	// "agent up requires a binary", and the bindable form named a top-level `restore` verb that
	// is NOT registered -- I had already run the registry grep that showed no registration and
	// used the verb anyway. An emitted command must be executable, not plausible.
	switch {
	case adm.Bindable:
		// The record carries a complete claim-bound token, so the managed path binds it with
		// no operator-supplied secret. `agent resume <role>` is the registered verb
		// (agent.go: "agent resume <role>"); there is no top-level `restore`.
		adm.Recovery = "amq-squad agent resume " + shellQuote(role)
	case adm.Staged:
		// The staged pair is the ONLY operator-typable binding: there is no prepared-token
		// flag. <binary> is a required POSITIONAL, so it is present here rather than implied.
		// The claim ID stays a placeholder because it must come from the authoritative current
		// claim, and inventing one would be worse than asking for it.
		binary := "<binary>"
		if rec != nil && strings.TrimSpace(rec.Binary) != "" {
			binary = strings.TrimSpace(rec.Binary)
		}
		// #579 round 3 F4: rec.Binary was concatenated RAW. A path with spaces malforms the
		// command and a metacharacter becomes syntax the moment the operator copies it. This
		// disproved my round-2 claim that every emitted form was checked against the real command
		// surface -- I checked the FLAGS, never the interpolated value.
		adm.Recovery = "amq-squad agent up " + shellQuote(binary) + " --role " + shellQuote(role) +
			" --staged-spawn --staged-claim <exact active claim ID from: amq-squad status --json>"
	default:
		// Prepared, not staged, and not bindable: no operator-typable token flag exists, so
		// point at the command that re-reserves rather than inventing one.
		adm.Recovery = "amq-squad run start --project " + projectOrPlaceholder(manifest.Project) +
			" --profile " + fieldOrPlaceholder(manifest.Profile, "profile") +
			" --session " + fieldOrPlaceholder(manifest.Session, "session")
	}
	return adm
}

// preparedRunAdmissionForMember loads the manifest and classifies one member. Used by the
// resume planner, which -- unlike admission -- holds no manifest.
//
// preparedRunManifestForProjection is reused deliberately: it already distinguishes a
// genuinely non-prepared session from a DAMAGED accepted state. A fresh reader written here
// would almost certainly collapse that distinction into "not prepared", which is fail-OPEN
// in the planner and would preview a command admission rejects -- #573 again.
func preparedRunAdmissionForMember(project, profile, session, role, handle string, rec *launch.Record) (preparedRunAdmission, error) {
	manifest, digest, prepared, err := preparedRunManifestForProjection(project, profile, session)
	if err != nil {
		return preparedRunAdmission{}, err
	}
	return preparedRunActorAdmission(manifest, digest, prepared, role, handle, rec), nil
}

// recordCarriesPreparedRunEvidence reports whether a launch record carries ANY prepared-run
// field, including an orphaned launch attempt.
//
// #579 round 3 F2: token.empty() answers a narrower question -- are the four GENERATION fields
// all blank -- and LaunchAttempt is not one of them. Using empty() to mean "no prepared-run
// evidence" let a record with only an attempt read as an ordinary actor.
func recordCarriesPreparedRunEvidence(rec launch.Record) bool {
	token := preparedRunTokenFromRecord(rec)
	return !token.empty() || strings.TrimSpace(token.LaunchAttempt) != ""
}

// projectOrPlaceholder renders a project path for a COPYABLE command, or a visible placeholder
// when it is blank.
//
// A blank value shell-quotes to ” , which looks like a filled argument and fails at runtime.
// A placeholder looks unfinished, which is the honest rendering of an unknown value -- the same
// executable-not-plausible rule that produced findings F3 and F4.
func projectOrPlaceholder(project string) string {
	return fieldOrPlaceholder(project, "project")
}

func fieldOrPlaceholder(value, name string) string {
	if strings.TrimSpace(value) == "" {
		return "<" + name + ">"
	}
	return shellQuote(strings.TrimSpace(value))
}

// preparedRunRecoveryCommand reopens the canonical run-start flow for the exact
// namespace. It intentionally does not print --prepare: a valid preparation
// command also needs the accepted launch shape and goal binding, which these
// error sites cannot reconstruct safely. The guided preview emits that complete
// command without mutating immutable generation or launch evidence.
func preparedRunRecoveryCommand(project, profile, session string) string {
	return "amq-squad run start --project " + projectOrPlaceholder(project) +
		" --profile " + fieldOrPlaceholder(profile, "profile") +
		" --session " + fieldOrPlaceholder(session, "session")
}
