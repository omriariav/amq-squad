package adoptionseam

import (
	"fmt"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

// AdoptionFloorAMQVersion documents the released amq version
// AdoptionFloorContractSemver was verified against (launchapi.Compatibility()
// on v0.72.0, go.mod pinned to v0.73.0 -- gh#746). It is not itself compared
// at runtime: Negotiate checks the compiled-in launchapi contract this
// binary was built against, not an on-disk amq version, since this seam
// never shells out to the amq CLI (see the package doc comment).
const AdoptionFloorAMQVersion = "v0.72.0"

// AdoptionFloorContractSemver is the minimum launchapi contract semver this
// backend requires. Deliberately distinct from internal/cli's
// doctorMinAMQVersion (see TestGeneralOperationFloorUnchanged): this
// launchapi backend is opt-in, so raising the general-operation floor for
// every user who never touches it would tax them for zero benefit — the
// wake/mail path is proven on amq 0.60.x. The two floors merge only when
// the launchapi backend becomes the auto default (gh#733's v2.31.0+ line).
//
// gh#746: the launchapi package's negotiable contract is unchanged since
// v0.70.0. launchapi.ContractSemverV1 is still "0.61.1" on both v0.72.0 and
// v0.73.0 -- verified byte-identical to v0.70.0 (md5 of every non-test .go
// file in the launchapi package matches exactly across v0.70.0 and v0.73.0).
// No launchapi.Feature* constant, and no raw string in
// platformCompatibilityFeaturesV1, names scoped grants, approvals_reviewer,
// or project-root authority on either version -- upstream's #648 asks landed
// as changes to the module's internals and to Prepare's RUNTIME output
// instead (PrepareResultV1.Preview.Capabilities[].GrammarVersion bumped 1 ->
// 2, .AllowedArgumentForms gained "-n"/"--name"), not as anything
// Compatibility()/Negotiate() can see ahead of time.
//
// So this adoption floor is enforced two ways, neither of which is
// Negotiate: (a) the go.mod pin itself, since launchapi and its internals
// run inside this binary -- TestPinnedAMQModuleAtOrAboveAdoptionFloor proves
// the linked module version is >= AdoptionFloorAMQVersion via
// runtime/debug.ReadBuildInfo(); (b) a runtime check on the observed
// GrammarVersion in Prepare's own PreviewV1.Capabilities, added in t10.
// TestNegotiateRejectsBelowAdoptionFloor below still exercises Negotiate's
// real refusal paths (an ahead-of-compiled-in semver, a missing feature, an
// unsupported intent version) -- those remain true refusal mechanisms, just
// not ones gh#746's version bump itself changes anything about.
const AdoptionFloorContractSemver = ">=0.61.1"

// AdoptionFloorFeatures are the launchapi features this backend actually
// exercises today, each justified against the call sites that need it:
//   - launch_intent_v1: internal/launchintent compiles directly to this wire shape.
//   - prepare_apply_v1: Prepare/Apply below are the only launchapi calls this seam makes.
//   - base_root: gh#734's explicit-target contract depends on this being negotiated, not assumed present.
//   - initial_input: bootstrap prompts ride InitialInputV1, never argv (gh#732).
//   - managed_tmux_v1: the only launcher this milestone ships (opt-in tmux backend, gh#733).
//   - caller_context: PrepareInput.Caller is a real, always-populated field
//     (the gh#733 backend sets profile/workstream on every call), not an
//     optional passthrough — this seam genuinely depends on CallerContext
//     being honored, not just accepted.
//
// Not required here: on_live, placement, executable_identity, wrapper,
// lifecycle_v1, plan_only_commands_v1 — real features on v0.70.0 through
// v0.73.0 (unchanged, see AdoptionFloorContractSemver's doc comment), but
// nothing in this package calls the paths that need them yet. Extending
// this list is a one-line change when that changes.
var AdoptionFloorFeatures = []string{
	launchapi.FeatureBaseRoot,
	launchapi.FeatureInitialInput,
	launchapi.FeatureCallerContext,
	"launch_intent_v1",
	"prepare_apply_v1",
	"managed_tmux_v1",
}

// negotiateAdoptionFloor is a package var so tests can substitute a stub
// that refuses, proving Prepare's fail-closed wiring without needing to
// swap the compiled-in launchapi contract mid-test. Production always uses
// NegotiateAdoptionFloor.
var negotiateAdoptionFloor = NegotiateAdoptionFloor

// NegotiateAdoptionFloor calls launchapi.Negotiate against the adoption
// floor requirement above. It is the fail-closed guard Prepare runs before
// touching launchintent.Compile or launchapi.Prepare: an older compiled-in
// contract, or one missing a required feature, refuses here instead of
// falling through into a Prepare call that may not actually be safe.
func NegotiateAdoptionFloor() (launchapi.NegotiatedV1, error) {
	negotiated, err := launchapi.Negotiate(launchapi.RequirementV1{
		ContractSemver: AdoptionFloorContractSemver,
		IntentVersion:  launchapi.IntentVersionV1,
		ResultVersion:  launchapi.ResultVersionV1,
		Features:       AdoptionFloorFeatures,
	})
	if err != nil {
		return launchapi.NegotiatedV1{}, fmt.Errorf("adoptionseam: below adoption floor %s (%s): %w", AdoptionFloorAMQVersion, AdoptionFloorContractSemver, err)
	}
	return negotiated, nil
}
