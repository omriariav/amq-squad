package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/adoptionseam"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// TestDoctorReportsLaunchapiCompatibility is gh#766's first named acceptance
// test: doctor reports launchapi.Compatibility()'s negotiated contract
// (semver + feature surface) alongside amq doctor's own version report. The
// check is pure/static, so it never fails and never needs amq env.
func TestDoctorReportsLaunchapiCompatibility(t *testing.T) {
	got := doctorCheckLaunchapiCompatibility(doctorExecution{})
	if got.Status != doctorOK {
		t.Fatalf("status = %q, want ok (this check is pure and cannot fail): %q", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "contract") {
		t.Fatalf("detail missing the contract semver: %q", got.Detail)
	}
}

// TestDoctorFlagsAMQBinaryBelowAdoptionFloor is gh#766's second named
// acceptance test: a PATH amq older than adoptionseam.AdoptionFloorAMQVersion
// warns (never fails) -- advisory, since the actual floor enforcement for
// the in-process launchapi path is the go.mod pin, not the PATH binary.
func TestDoctorFlagsAMQBinaryBelowAdoptionFloor(t *testing.T) {
	d := doctorExecution{
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: "v0.60.0"}, nil
		},
	}
	got := doctorCheckAdoptionFloor(d)
	if got.Status != doctorWarn {
		t.Fatalf("status = %q, want warn: %q", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "v0.60.0") || !strings.Contains(got.Detail, adoptionseam.AdoptionFloorAMQVersion) {
		t.Fatalf("detail must name both the PATH version and the floor: %q", got.Detail)
	}

	atFloor := doctorCheckAdoptionFloor(doctorExecution{
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: adoptionseam.AdoptionFloorAMQVersion}, nil
		},
	})
	if atFloor.Status != doctorOK {
		t.Fatalf("at-floor status = %q, want ok: %q", atFloor.Status, atFloor.Detail)
	}
}

// TestDoctorFlagsAMQBinaryAboveModulePin is gh#766's third named acceptance
// test, now on the tool row: a PATH amq newer than the go.mod-pinned
// agent-message-queue module warns (never fails); at the pin it is ok.
func TestDoctorFlagsAMQBinaryAboveModulePin(t *testing.T) {
	pin := strings.TrimPrefix(registry.PinnedAMQModule, "v")
	newer := registry.Tool{Name: "amq", Found: true, Path: "/bin/amq", Version: "999.0.0",
		VerifiedVersion: pin, VersionCheck: "newer", Login: "n/a"}
	if got := toolCheck(newer); got.Status != doctorWarn || !strings.Contains(got.Detail, pin) || !strings.Contains(got.Detail, "999.0.0") {
		t.Fatalf("newer amq: %+v", got)
	}
	at := newer
	at.Version, at.VersionCheck = pin, "match"
	if got := toolCheck(at); got.Status != doctorOK {
		t.Fatalf("amq at the pin: %+v", got)
	}
}
