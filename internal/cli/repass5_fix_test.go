//go:build unix

package cli

import (
	"os"
	"os/exec"
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
	// The stand-in publishes its sleeper's pid atomically (write, then rename)
	// so a reader never sees the file created but empty; the pid file is the
	// stand-in's readiness signal.
	script := "#!/bin/sh\nsleep 30 &\necho $! > \"$SLEEPER_PID.tmp\"\nmv \"$SLEEPER_PID.tmp\" \"$SLEEPER_PID\"\nwait\n"
	if err := os.WriteFile(stub, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// Measure how long the stand-in takes to become ready on this host right
	// now (shell start, fork, pid published): the adapter's watchdog must not
	// fire before the stand-in is ready, or there is no sleeper to look for.
	// The watchdog is the larger of 1 s and ten times that measured step.
	warmPid := filepath.Join(dir, "warm.pid")
	warm := exec.Command(stub)
	warm.Env = append(os.Environ(), "SLEEPER_PID="+warmPid)
	warm.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	warmStart := time.Now()
	if err := warm.Start(); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(warmPid); err == nil {
			break
		}
		if time.Since(warmStart) > 30*time.Second {
			syscall.Kill(-warm.Process.Pid, syscall.SIGKILL)
			t.Fatal("the stand-in guard never became ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	ready := time.Since(warmStart)
	if raw, err := os.ReadFile(warmPid); err == nil {
		if wp, _ := strconv.Atoi(strings.TrimSpace(string(raw))); wp > 1 {
			syscall.Kill(wp, syscall.SIGKILL)
		}
	}
	syscall.Kill(-warm.Process.Pid, syscall.SIGKILL)
	warm.Wait()
	watchdog := 10 * ready
	if watchdog < time.Second {
		watchdog = time.Second
	}
	t.Setenv("SLEEPER_PID", pidf)
	t.Setenv("AMQ_SQUAD_BIN", stub)
	prevW, prevIn := guardWatchdog, hookStdin
	guardWatchdog, hookStdin = watchdog, strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"true"}}`)
	t.Cleanup(func() { guardWatchdog, hookStdin = prevW, prevIn })
	start := time.Now()
	out, errOut, err := captureOutput(t, func() error { return runGuardDecision("claude", "cto") })
	if err != nil || out != "" || !strings.Contains(errOut, "fail-open: guard timeout") || time.Since(start) > watchdog+3*time.Second {
		t.Fatalf("a hung guard: err %v, out %q, log %q after %s (watchdog %s)", err, out, errOut, time.Since(start).Round(time.Millisecond), watchdog)
	}
	// The pid file is the readiness point; wait for it (hang guard) rather
	// than reading it once.
	var pid int
	for deadline := time.Now().Add(30 * time.Second); pid <= 1; time.Sleep(10 * time.Millisecond) {
		raw, _ := os.ReadFile(pidf)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		if pid <= 1 && time.Now().After(deadline) {
			t.Fatalf("no sleeper pid: %q", raw)
		}
	}
	for deadline := time.Now().Add(2 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the guard's sleeper (pid %d) survived the watchdog", pid)
		}
	}
}
