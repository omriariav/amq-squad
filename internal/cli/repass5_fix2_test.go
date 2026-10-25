//go:build unix

package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The narrow pass's item 1: the adapter's watchdog starts at its entry and
// covers the stdin read: a payload that never ends (stdin held open, no
// EOF) is a timeout with one log line, inside the watchdog, not Claude's
// 5 s kill with none.
func TestAdapterTimesOutOnAnOpenStdin(t *testing.T) {
	pr, w := io.Pipe()
	t.Cleanup(func() { w.Close() })
	prevW, prevIn := guardWatchdog, hookStdin
	guardWatchdog, hookStdin = 500*time.Millisecond, pr
	t.Cleanup(func() { guardWatchdog, hookStdin = prevW, prevIn })
	start := time.Now()
	type ran struct {
		out, errOut string
		err         error
	}
	done := make(chan ran, 1)
	go func() {
		out, errOut, err := captureOutput(t, func() error { return runGuardDecision("claude", "cto") })
		done <- ran{out, errOut, err}
	}()
	var r ran
	select {
	case r = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the adapter blocked on an open stdin past its watchdog")
	}
	out, errOut, err := r.out, r.errOut, r.err
	if err != nil || out != "" || !strings.Contains(errOut, "fail-open: guard timeout") || !strings.Contains(errOut, "stdin not closed") || time.Since(start) > 2*time.Second {
		t.Fatalf("an open stdin: err %v, out %q, log %q after %s", err, out, errOut, time.Since(start).Round(time.Millisecond))
	}
}

// The narrow pass's item 2: the guard child's stdout is capped at 64 KiB;
// a child that writes without end is no decision, logged, and killed.
func TestAdapterCapsTheGuardOutput(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "flooding-guard")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncat >/dev/null\nwhile :; do echo '{\"verdict\":\"allow\"}'; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_SQUAD_BIN", stub)
	prevIn := hookStdin
	hookStdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"true"}}`)
	t.Cleanup(func() { hookStdin = prevIn })
	start := time.Now()
	out, errOut, err := captureOutput(t, func() error { return runGuardDecision("claude", "cto") })
	if err != nil || out != "" || !strings.Contains(errOut, "guard output over") || time.Since(start) > 4*time.Second {
		t.Fatalf("a flooding guard: err %v, out %q, log %q after %s", err, out, errOut, time.Since(start).Round(time.Millisecond))
	}
}
