package cli

import "testing"

// TestGeneralOperationFloorUnchanged locks gh#736's "two floors,
// deliberately" contract: the general-operation AMQ floor (wake/mail path,
// proven on amq 0.60.x) is untouched by the v2.30.0 milestone. The opt-in
// launchapi backend gets its own, separate, higher floor
// (internal/adoptionseam.AdoptionFloorAMQVersion / AdoptionFloorContractSemver)
// so users who never touch launchapi are not taxed for zero benefit. The two
// floors merge only when the launchapi backend becomes the auto default
// (gh#733's v2.31.0+ line).
func TestGeneralOperationFloorUnchanged(t *testing.T) {
	const wantUnchanged = "0.60.0"
	if doctorMinAMQVersion != wantUnchanged {
		t.Fatalf("doctorMinAMQVersion = %q, want it unchanged at %q by the v2.30.0 milestone (raise it only alongside a deliberate general-operation floor bump, not as a side effect of the opt-in launchapi backend)", doctorMinAMQVersion, wantUnchanged)
	}
}
