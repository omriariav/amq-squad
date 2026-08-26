// Package adoptionseam is the single call site through which amq-squad talks
// to launchapi. It never shells out to the amq CLI and never performs its own
// root discovery: every target coordinate comes from the caller's already-
// resolved internal/launchintent.Input, and a missing base root is refused
// before launchapi is ever called (gh#734) — the same bug class that let
// AMQ's own upward .amqrc discovery retarget writes into a parent repo's
// live base root from a nested worktree cwd. Identity env vars inherited
// from a live parent AMQ seat are stripped before any child sees them
// (gh#735).
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
var ErrEmptyBaseRoot = errors.New("adoptionseam: target base_root is required")

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
func Apply(ctx context.Context, p Prepared, decisions []launchapi.DecisionV1) (launchapi.ApplyResultV1, error) {
	request := launchapi.ApplyRequestV1{
		RequestVersion: launchapi.RequestVersionV1,
		Prepare:        p.Request,
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
// against the pinned launchapi v0.70.0 + internal/launch sources: neither
// package reads or sets AM_ME anywhere -- Prepare/Apply never touch it, and
// a child's effective handle is carried explicitly through each
// ParticipantV1.EnvOverlay (compiled by internal/launchintent), then
// surfaced back out as CommandV1.EnvOverlay for whatever launches the
// child. An inherited AM_ME in the parent's OS environment is therefore
// inert to launchapi either way: stripping it here would be a no-op for
// correctness, and keeping the set to exactly gh#735's five named
// variables keeps this table an honest match for its acceptance test
// rather than a superset nobody asked for.
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
