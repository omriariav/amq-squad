package cli

import (
	"syscall"
	"testing"
)

// TestNewSignalTerminatorMapsForceToSIGKILL pins the flag wiring: down with no
// --force sends SIGTERM, --force escalates to an unignorable SIGKILL.
func TestNewSignalTerminatorMapsForceToSIGKILL(t *testing.T) {
	soft := newSignalTerminator(false)
	if soft.sig != syscall.SIGTERM {
		t.Errorf("default terminator sig = %v, want SIGTERM", soft.sig)
	}
	if soft.SignalName() != "SIGTERM" {
		t.Errorf("default SignalName = %q, want SIGTERM", soft.SignalName())
	}
	hard := newSignalTerminator(true)
	if hard.sig != syscall.SIGKILL {
		t.Errorf("--force terminator sig = %v, want SIGKILL", hard.sig)
	}
	if hard.SignalName() != "SIGKILL" {
		t.Errorf("--force SignalName = %q, want SIGKILL", hard.SignalName())
	}
}
