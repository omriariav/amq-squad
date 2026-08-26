package adoptionseam

import (
	"context"
	"errors"
	"testing"

	"github.com/avivsinai/agent-message-queue/launchapi"
)

// TestNegotiateAdoptionFloorSatisfiedByPinnedAMQ is the positive control:
// the floor this package declares must actually be satisfied by the
// launchapi version pinned in go.mod today. If this ever fails, the floor
// constants drifted ahead of the pinned dependency.
func TestNegotiateAdoptionFloorSatisfiedByPinnedAMQ(t *testing.T) {
	if _, err := NegotiateAdoptionFloor(); err != nil {
		t.Fatalf("NegotiateAdoptionFloor() against the pinned launchapi dependency: %v", err)
	}
}

// TestNegotiateRejectsBelowAdoptionFloor proves the refusal mechanism gh#736
// asks for: an older contract, or a missing required feature, refuses
// before any Prepare call. The compiled-in launchapi contract itself can't
// be swapped mid-test, so this exercises the same launchapi.Negotiate
// entrypoint NegotiateAdoptionFloor uses with requirements engineered to be
// unsatisfiable by the pinned v0.70.0 contract — proving Negotiate (and by
// extension Prepare, wired below) fails closed rather than silently
// proceeding.
func TestNegotiateRejectsBelowAdoptionFloor(t *testing.T) {
	cases := []struct {
		name        string
		requirement launchapi.RequirementV1
	}{
		{
			name: "contract semver ahead of what is compiled in",
			requirement: launchapi.RequirementV1{
				ContractSemver: ">=99.0.0",
				IntentVersion:  launchapi.IntentVersionV1,
				ResultVersion:  launchapi.ResultVersionV1,
				Features:       AdoptionFloorFeatures,
			},
		},
		{
			name: "missing required feature",
			requirement: launchapi.RequirementV1{
				ContractSemver: AdoptionFloorContractSemver,
				IntentVersion:  launchapi.IntentVersionV1,
				ResultVersion:  launchapi.ResultVersionV1,
				Features:       append(append([]string(nil), AdoptionFloorFeatures...), "not_a_real_feature_v99"),
			},
		},
		{
			name: "unsupported intent version",
			requirement: launchapi.RequirementV1{
				ContractSemver: AdoptionFloorContractSemver,
				IntentVersion:  999,
				ResultVersion:  launchapi.ResultVersionV1,
				Features:       AdoptionFloorFeatures,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := launchapi.Negotiate(tc.requirement); err == nil {
				t.Fatalf("Negotiate(%+v) succeeded, want a refusal", tc.requirement)
			}
		})
	}
}

// TestPrepareFailsClosedBelowAdoptionFloor proves the guard is actually
// wired into Prepare, not just available as a standalone function: with a
// valid intent and a valid (non-empty) base root, Prepare still refuses
// before touching launchintent.Compile or launchapi.Prepare if the
// negotiated floor cannot be met. It substitutes the injectable
// negotiateAdoptionFloor var with a stub that refuses, since the compiled-in
// launchapi contract itself can't be swapped mid-test.
func TestPrepareFailsClosedBelowAdoptionFloor(t *testing.T) {
	stubErr := errors.New("stub: below adoption floor")
	original := negotiateAdoptionFloor
	negotiateAdoptionFloor = func() (launchapi.NegotiatedV1, error) { return launchapi.NegotiatedV1{}, stubErr }
	t.Cleanup(func() { negotiateAdoptionFloor = original })

	projectRoot := t.TempDir()
	in := PrepareInput{
		Intent:   baseIntentInput(t, projectRoot),
		Launcher: "tmux",
	}
	_, err := Prepare(context.Background(), in)
	if err == nil {
		t.Fatal("expected Prepare to refuse below the adoption floor")
	}
	if !errors.Is(err, stubErr) {
		t.Fatalf("Prepare err = %v, want it to wrap the floor-negotiation refusal", err)
	}
}
