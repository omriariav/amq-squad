package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/adoptionseam"
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
// test: a PATH amq newer than the go.mod-pinned agent-message-queue module
// warns (never fails) -- amq-squad's launchapi integration runs in-process
// against the pinned module regardless of what is on PATH.
func TestDoctorFlagsAMQBinaryAboveModulePin(t *testing.T) {
	previous := pinnedAMQModuleVersion
	defer func() { pinnedAMQModuleVersion = previous }()
	pinnedAMQModuleVersion = func() (string, bool) { return "v0.75.0", true }

	d := doctorExecution{
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: "v0.76.0"}, nil
		},
	}
	got := doctorCheckAMQModulePin(d)
	if got.Status != doctorWarn {
		t.Fatalf("status = %q, want warn: %q", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "v0.76.0") || !strings.Contains(got.Detail, "v0.75.0") {
		t.Fatalf("detail must name both the PATH version and the pinned version: %q", got.Detail)
	}

	atOrBelow := doctorCheckAMQModulePin(doctorExecution{
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: "v0.75.0"}, nil
		},
	})
	if atOrBelow.Status != doctorOK {
		t.Fatalf("at-pin status = %q, want ok: %q", atOrBelow.Status, atOrBelow.Detail)
	}

	pinnedAMQModuleVersion = func() (string, bool) { return "", false }
	unavailable := doctorCheckAMQModulePin(doctorExecution{
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: "v0.75.0"}, nil
		},
	})
	if unavailable.Status != doctorOK {
		t.Fatalf("unavailable-pin status = %q, want ok (skip, not warn/fail): %q", unavailable.Status, unavailable.Detail)
	}
}
