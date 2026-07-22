//go:build linux

package procinfo

import "testing"

func TestTTYDeviceFromStatUsesControllingTTYFieldAfterComplexComm(t *testing.T) {
	stat := "4242 (agent (review) worker) S 10 20 30 34816 40 50"
	if got, ok := ttyDeviceFromStat(stat); !ok || got != 34816 {
		t.Fatalf("ttyDeviceFromStat = %d, %v; want 34816, true", got, ok)
	}
}

func TestTTYDeviceFromStatRejectsNoControllingTTY(t *testing.T) {
	if got, ok := ttyDeviceFromStat("4242 (agent) S 10 20 30 0 40"); ok || got != 0 {
		t.Fatalf("ttyDeviceFromStat = %d, %v; want 0, false", got, ok)
	}
}

func TestTTYPathForDeviceDoesNotPromoteRedirectedFDZero(t *testing.T) {
	devices := map[string]uint64{"/dev/null": 1, "/dev/pts/7": 34816}
	device := func(path string) (uint64, bool) { value, ok := devices[path]; return value, ok }
	got, ok := ttyPathForDevice(34816, []string{"/dev/null", "/dev/pts/7"}, device)
	if !ok || got != "/dev/pts/7" {
		t.Fatalf("ttyPathForDevice = %q, %v; redirected fd 0 must not be authority", got, ok)
	}
}

func TestTTYPathForDeviceRejectsUnmappedDevice(t *testing.T) {
	device := func(path string) (uint64, bool) { return 1, path == "/dev/null" }
	if got, ok := ttyPathForDevice(34816, []string{"/dev/null"}, device); ok || got != "" {
		t.Fatalf("ttyPathForDevice = %q, %v; want unmapped refusal", got, ok)
	}
}
