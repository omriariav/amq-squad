//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The fix round's watchdog ruling (item 10, N4): the Go adapter owns the
// PreToolUse watchdog on every platform: a guard child that hangs, with a
// sleeper it started, is killed with its whole process group and reaped;
// the adapter prints no decision and logs the timeout. (Under dash the
// shell watchdog this replaces did not work: set -m and kill -- -PID.)
func TestDecisionAdapterKillsAHungGuardGroup(t *testing.T) {
	dir := t.TempDir()
	pidf := filepath.Join(dir, "sleeper.pid")
	stub := filepath.Join(dir, "hung-guard")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nsleep 30 &\necho $! > "+pidf+"\nwait\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_SQUAD_BIN", stub)
	prevW, prevIn := guardWatchdog, hookStdin
	guardWatchdog, hookStdin = time.Second, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"true"}}`)
	t.Cleanup(func() { guardWatchdog, hookStdin = prevW, prevIn })
	start := time.Now()
	out, errOut, err := captureOutput(t, func() error { return runGuardDecision("claude", "cto") })
	if err != nil || out != "" || !strings.Contains(errOut, "fail-open: guard timeout") || time.Since(start) > 4*time.Second {
		t.Fatalf("a hung guard: err %v, out %q, log %q after %s", err, out, errOut, time.Since(start).Round(time.Millisecond))
	}
	raw, _ := os.ReadFile(pidf)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if pid <= 1 {
		t.Fatalf("no sleeper pid: %q", raw)
	}
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the guard's sleeper (pid %d) survived the watchdog", pid)
		}
	}
}
