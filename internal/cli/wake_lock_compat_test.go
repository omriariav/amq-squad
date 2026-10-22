package cli

import (
	"strings"
	"testing"
)

func TestDecodeWakeLockRejectsIncompleteStateBinding(t *testing.T) {
	_, err := decodeWakeLockFile([]byte(`{"pid":1,"generation":7,"state_generation":7,"wake_mode":"inject-via-v1"}`))
	if err == nil || !strings.Contains(err.Error(), "incomplete state binding") {
		t.Fatalf("decode error = %v, want incomplete binding", err)
	}
}

// TestDecodeWakeLockRejectsWrongTypeMachineID, TestDecodeWakeLockRejectsDuplicateMachineID,
// and TestDecodeWakeLockRejectsNullMachineID are the schema-parity coverage
// for #488 (amq 0.60.4+): machine_id is a known, trust-bearing field, so
// amq-squad's strict decoder must reject an ambiguous machine_id exactly
// like it already does for pid.
func TestDecodeWakeLockRejectsWrongTypeMachineID(t *testing.T) {
	_, err := decodeWakeLockFile([]byte(`{"pid":1,"machine_id":12345}`))
	if err == nil || !strings.Contains(err.Error(), "decode wake lock fields") {
		t.Fatalf("decode error = %v, want a wrong-type field rejection", err)
	}
}

func TestDecodeWakeLockRejectsDuplicateMachineID(t *testing.T) {
	_, err := decodeWakeLockFile([]byte(`{"pid":1,"machine_id":"a","MACHINE_ID":"b"}`))
	if err == nil || !strings.Contains(err.Error(), "duplicate known field") {
		t.Fatalf("decode error = %v, want duplicate known field rejection", err)
	}
}

func TestDecodeWakeLockRejectsNullMachineID(t *testing.T) {
	_, err := decodeWakeLockFile([]byte(`{"pid":1,"machine_id":null}`))
	if err == nil || !strings.Contains(err.Error(), `field "machine_id" must not be null`) {
		t.Fatalf("decode error = %v, want null field rejection", err)
	}
}

// TestWakeLockSameMachine is direct coverage of the #488 same-machine gate:
// a recorded MachineID is always treated as unverifiable (amq-squad does not
// reimplement AMQ's platform-identity reader to compute its own), a mismatched
// legacy Hostname is decisive the same way, and an absent/matching Hostname
// with no MachineID is the only case that trusts local PID inspection.
func TestWakeLockSameMachine(t *testing.T) {
	previous := currentWakeLockHostname
	currentWakeLockHostname = func() (string, error) { return "this-host", nil }
	t.Cleanup(func() { currentWakeLockHostname = previous })

	for _, tc := range []struct {
		name string
		lock wakeLockFile
		want bool
	}{
		{"no identity fields", wakeLockFile{PID: 1}, true},
		{"matching hostname, no machine id", wakeLockFile{PID: 1, Hostname: "this-host"}, true},
		{"mismatched hostname, no machine id", wakeLockFile{PID: 1, Hostname: "other-host"}, false},
		{"any machine id is unverifiable, even matching hostname", wakeLockFile{PID: 1, MachineID: "m-1", Hostname: "this-host"}, false},
		{"different machine id", wakeLockFile{PID: 1, MachineID: "m-2"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wakeLockSameMachine(tc.lock); got != tc.want {
				t.Fatalf("wakeLockSameMachine(%+v) = %v, want %v", tc.lock, got, tc.want)
			}
		})
	}
}
