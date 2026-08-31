// Package adoptionseam is the single call site through which amq-squad talks
// to launchapi. It never shells out to the amq CLI and never performs its own
// root discovery: every target coordinate comes from the caller's already-
// resolved internal/launchintent.Input, and a missing base root is refused
// before launchapi is ever called (gh#734) — the same bug class that let
// AMQ's own upward .amqrc discovery retarget writes into a parent repo's
// live base root from a nested worktree cwd. Identity env vars inherited
// from a live parent AMQ seat are stripped before any child sees them
// (gh#735).
//
// gh#768 (docs-only reframing, no behavior change): upstream #676 (amq
// v0.74.0) gave findRootInParents/findAmqrcForRoot the same innermost-git-
// worktree ceiling the general .amqrc walk-up already had, closing the
// specific relative-root and pre-resolved-root discovery paths gh#734
// exploited. Reproduced directly: porting v0.74.0's own regression test onto
// a v0.73.0 checkout fails 2 of 7 subtests there (a symlinked path into a
// nested worktree, and a relative .agent-mail root, both adopting the
// parent's live queue); all 7 pass on v0.74.0. But this refusal is not, and
// was never meant to be, conditional on that upstream fix: it never called
// the vulnerable discovery code in the first place (see BaseRootSeamStatus),
// and gh#734 itself is explicit that "even if upstream closes the discovery
// gap, this guard remains defense-in-depth." So ErrEmptyBaseRoot's refusal
// stays unconditional at every amq version — what changed is only the
// narrative: this is no longer the sole thing standing between amq-squad and
// the bug, since upstream also closed their own end now.
package adoptionseam

import (
	"context"
	"errors"
	"strings"

	"github.com/avivsinai/agent-message-queue/launchapi"
	"github.com/omriariav/amq-squad/v2/internal/launchintent"
)

// ErrEmptyBaseRoot is returned by Prepare before launchapi is ever called
// when the caller's intent carries no explicit base root. There is no
// fallback and no discovery: an empty base root is refused, not resolved.
// This refusal is unconditional at every amq version (see
// BaseRootSeamStatus) — it is never made conditional on which amq version
// go.mod happens to pin.
var ErrEmptyBaseRoot = errors.New("adoptionseam: target base_root is required")

// BaseRootSeamStatus documents, as of the adoption floor above, whether this
// package's fail-closed ErrEmptyBaseRoot refusal is the ONLY defense against
// gh#734's nested-worktree bug class ("required") or whether upstream has
// also independently closed the specific discovery paths it exploited
// ("belt_and_braces"). Purely informational: it never gates or weakens
// ErrEmptyBaseRoot's refusal, which stays unconditional regardless of this
// value (gh#734: "even if upstream closes the discovery gap, this guard
// remains defense-in-depth"). See the package doc comment for the gh#768
// measurement this records.
const BaseRootSeamStatus = "belt_and_braces"

// PrepareInput is Prepare's complete input. Intent is already-resolved by
// internal/launchintent, so its Target carries an explicit BaseRoot; this
// package adds no lookups of its own on top of it.
type PrepareInput struct {
	Intent    launchintent.Input
	Launcher  string
	Placement *launchapi.PlacementV1
	Caller    map[string]string
	// Env is the parent process environment. SanitizeEnv strips inherited
	// AMQ identity before it is attached to Prepared for any child command.
	Env []string
}

// Prepared bundles a successful Prepare call: the exact request that was
// sent to launchapi (so Apply can be built from it without re-deriving
// anything), the result launchapi returned, and the sanitized environment
// for whatever launches a child from this seam.
type Prepared struct {
	Request launchapi.PrepareRequestV1
	Result  launchapi.PrepareResultV1
	Env     []string
}

// Prepare compiles in.Intent and calls launchapi.Prepare with it. It never
// shells out: no os/exec, no amq CLI invocation, no upward root discovery.
// A missing base root fails closed with ErrEmptyBaseRoot before launchapi
// (or internal/launchintent) is ever touched. Once that passes, Prepare
// negotiates the adoption floor (gh#736) — an older compiled-in launchapi
// contract, or one missing a required feature, refuses here too, before
// launchintent.Compile or launchapi.Prepare ever run.
func Prepare(ctx context.Context, in PrepareInput) (Prepared, error) {
	if strings.TrimSpace(in.Intent.Target.BaseRoot) == "" {
		return Prepared{}, ErrEmptyBaseRoot
	}
	if _, err := negotiateAdoptionFloor(); err != nil {
		return Prepared{}, err
	}

	intent, target, err := launchintent.Compile(in.Intent)
	if err != nil {
		return Prepared{}, err
	}

	request := launchapi.PrepareRequestV1{
		RequestVersion: launchapi.RequestVersionV1,
		Target:         target,
		Launcher:       in.Launcher,
		Placement:      in.Placement,
		CallerContext:  in.Caller,
		Intent:         intent,
	}

	result, err := launchapi.Prepare(ctx, request)
	if err != nil {
		return Prepared{}, err
	}

	return Prepared{
		Request: request,
		Result:  result,
		Env:     SanitizeEnv(in.Env),
	}, nil
}

// Apply calls launchapi.Apply against a previously Prepared bundle and the
// operator/caller's decisions for any required actions Prepare surfaced.
//
// SubjectSchema must be forwarded from p.Result explicitly (gh#757 finding):
// launchapi.Apply's own zero-value default for an unset ApplyRequestV1.
// SubjectSchema is SubjectSchemaV1 (launchapi/apply.go), which disagrees
// with launchapi.Prepare's own hardcoded default of SubjectSchemaV2
// (launchapi/prepare.go) -- the two public entry points do not share a
// default. Since every caller here always sets PrepareRequestV1.CallerContext
// (callPrepare in team_launch_launchapi.go), an Apply that left this unset
// re-validated its embedded Prepare request at V1 and refused closed with
// "caller_context requires subject schema 2" on every real call that reached
// this point with any required action to decide -- confirmed live: no
// existing test had ever exercised a real (non-stubbed) Apply call with a
// non-empty Decisions list until TestWizardRealHandoffAppliesComputedDigestAndInvokesLaunch
// surfaced it, because every prior test replaces deps.Launch (and therefore
// this whole call) with a stub.
func Apply(ctx context.Context, p Prepared, decisions []launchapi.DecisionV1) (launchapi.ApplyResultV1, error) {
	request := launchapi.ApplyRequestV1{
		RequestVersion: launchapi.RequestVersionV1,
		Prepare:        p.Request,
		SubjectSchema:  p.Result.SubjectSchema,
		SubjectDigest:  p.Result.SubjectDigest,
		Decisions:      decisions,
	}
	return launchapi.Apply(ctx, request)
}

// sanitizedIdentityVars are the five AMQ root/session-pinning variables a
// live parent agent seat has injected into its own environment (gh#735).
// They must never reach a child this seam launches; everything else passes
// through untouched.
//
// AM_ME is deliberately NOT in this set, despite also being AMQ identity.
// gh#735's evidence is specifically about root/session PINNING failures
// ("evidence from AM_ROOT_ID, AM_BASE_ROOT_ID requires an exact
// AM_BASE_ROOT"), not about which handle a child acts as. Verified directly
// against the pinned launchapi v0.73.0 + internal/launch sources: neither
// package reads or sets AM_ME anywhere -- Prepare/Apply never touch it, and
// unlike an earlier draft of this comment claimed, a child's effective
// handle does NOT round-trip through ParticipantV1.EnvOverlay/
// CommandV1.EnvOverlay: every provider's committed-env allowlist
// (internal/launch/adapter_env.go's commonCommittedEnvRules, applied
// identically by every adapter_*.go) accepts only COLORTERM/LANG/LC_ALL/
// NO_COLOR/TERM, so an AM_ME entry there fails intent.Validate() with
// "committed environment key \"AM_ME\" is not allowed by adapter" (verified
// empirically -- see internal/launchintent's SeatFacts doc comment, gh#763).
// A child's effective handle instead reaches the launched process via
// ambient environment inheritance, same as every other AM_* variable. An
// inherited AM_ME in the parent's OS environment is therefore inert to
// launchapi either way: stripping it here would be a no-op for correctness,
// and keeping the set to exactly gh#735's five named variables keeps this
// table an honest match for its acceptance test rather than a superset
// nobody asked for.
var sanitizedIdentityVars = map[string]bool{
	"AM_ROOT":         true,
	"AM_BASE_ROOT":    true,
	"AM_ROOT_ID":      true,
	"AM_BASE_ROOT_ID": true,
	"AM_SESSION":      true,
}

// SanitizeEnv strips the five inherited AMQ root/session-pinning variables
// from env, leaving every other entry -- including AM_ME, see above --
// untouched. It is the seam's own dedicated contract (gh#735): it does not
// read or modify internal/cli/amq_env.go's envWithoutAMQIdentity, which
// strips a broader, CLI-specific set for a different call site.
func SanitizeEnv(env []string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok && sanitizedIdentityVars[key] {
			continue
		}
		out = append(out, entry)
	}
	return out
}
