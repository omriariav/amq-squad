package adoptionseam

import (
	"fmt"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

// AdoptionFloorAMQVersion documents the released amq version
// AdoptionFloorContractSemver was verified against (launchapi.Compatibility()
// on v0.70.0). It is not itself compared at runtime: Negotiate checks the
// compiled-in launchapi contract this binary was built against, not an
// on-disk amq version, since this seam never shells out to the amq CLI (see
// the package doc comment).
const AdoptionFloorAMQVersion = "v0.70.0"

// AdoptionFloorContractSemver is the minimum launchapi contract semver this
// backend requires. Deliberately distinct from internal/cli's
// doctorMinAMQVersion (see TestGeneralOperationFloorUnchanged): this
// launchapi backend is opt-in, so raising the general-operation floor for
// every user who never touches it would tax them for zero benefit — the
// wake/mail path is proven on amq 0.60.x. The two floors merge only when
// the launchapi backend becomes the auto default (gh#733's v2.31.0+ line).
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
// lifecycle_v1, plan_only_commands_v1 — real features on v0.70.0, but
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
