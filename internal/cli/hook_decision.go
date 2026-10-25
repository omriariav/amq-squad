package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// claudeDecision is the Go adapter from a guard verdict to Claude Code's
// PreToolUse decision (phase 5 re-pass of 4-2, P2-5; the plugin's shell used
// to map a prefix match of the output). The whole verdict must parse as one
// JSON object with a known verdict; anything else (a truncated object,
// trailing bytes, an unknown verdict) is an error, which the hook treats as
// no decision and logs.
//
//	allow       -> permissionDecision allow
//	deny        -> permissionDecision deny, reason = the guard's reason
//	needs_gate  -> permissionDecision deny, reason "raise the gate for
//	               <action_class> <target>" (never ask: an unattended seat
//	               must not stop on a prompt)
func claudeDecision(raw []byte) ([]byte, error) {
	var v guardVerdict
	if err := json.Unmarshal(bytes.TrimSpace(raw), &v); err != nil {
		return nil, fmt.Errorf("the guard verdict is not one complete JSON object: %v", err)
	}
	decision, reason := "", ""
	switch v.Verdict {
	case "allow":
		decision, reason = "allow", "amq-squad guard: allowed"
	case "deny":
		decision, reason = "deny", "amq-squad guard: "+v.Reason
	case "needs_gate":
		decision, reason = "deny", "raise the gate for "+v.ActionClass+" "+v.Target
	default:
		return nil, fmt.Errorf("the guard verdict %q is not allow, deny or needs_gate", v.Verdict)
	}
	out := map[string]any{"hookSpecificOutput": map[string]string{
		"hookEventName": "PreToolUse", "permissionDecision": decision, "permissionDecisionReason": reason}}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// guardWatchdog is the PreToolUse watchdog: the hook's configured timeout
// (5 s) minus 200 ms, so the adapter ends the guard and logs before Claude
// kills the hook.
var guardWatchdog = 4800 * time.Millisecond

// runGuardDecision is `guard --decision claude [--me H] --stdin`, the whole
// PreToolUse adapter (the fix round's watchdog ruling): it reads the hook
// payload, runs `guard --stdin` as a child in its own process group through
// the Go exec API (the watchdog kills and reaps the group on every
// platform; the hook script runs no watchdog of its own, so dash and bash
// behave alike), parses the complete verdict object and prints the
// decision. A timeout, a guard exit that is not 0, or a verdict that does
// not parse prints nothing (no decision) and one log line on stderr, which
// the hook sends to its log descriptor; the adapter exits 0.
func runGuardDecision(kind, me string) error {
	if kind != "claude" {
		return hookErrorf("guard: --decision %q is not claude", kind)
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "%s pre-tool %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), fmt.Sprintf(format, args...))
	}
	payload, err := readHookPayload("guard")
	if err != nil {
		logf("fail-open: %v", err)
		return nil
	}
	// The guard is the binary the hook names (AMQ_SQUAD_BIN, which seat-exec
	// pins to the launching binary), else this one.
	bin := strings.TrimSpace(os.Getenv("AMQ_SQUAD_BIN"))
	if bin == "" {
		if bin, err = os.Executable(); err != nil {
			logf("fail-open: %v", err)
			return nil
		}
	}
	args := []string{"guard"}
	if me != "" {
		args = append(args, "--me", me)
	}
	args = append(args, "--stdin")
	out, code, timedOut, err := runGuardChild(bin, args, payload, guardWatchdog)
	switch {
	case timedOut:
		logf("fail-open: guard timeout after %.1f s (killed; no decision)", guardWatchdog.Seconds())
		return nil
	case err != nil:
		logf("fail-open: guard did not run: %v", err)
		return nil
	case code != 0:
		logf("fail-open: guard exit %d", code)
		return nil
	}
	dec, err := claudeDecision(out)
	if err != nil {
		logf("fail-open: unparsable guard output: %v", err)
		return nil
	}
	_, err = os.Stdout.Write(dec)
	return err
}

// runGuardChild runs the guard child with stdin, in its own process group
// (guardGroup), killing the group at the deadline and once more after the
// child is reaped (a descendant it left behind); it returns stdout, the
// exit code and whether the watchdog fired.
func runGuardChild(bin string, args []string, stdin []byte, limit time.Duration) ([]byte, int, bool, error) {
	cmd := exec.Command(bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, os.Stderr
	cmd.WaitDelay = 200 * time.Millisecond
	guardGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, 0, false, err
	}
	var fired atomic.Bool
	timer := time.AfterFunc(limit, func() { fired.Store(true); killGuardGroup(cmd) })
	err := cmd.Wait()
	timer.Stop()
	killGuardGroup(cmd)
	if fired.Load() {
		return nil, 0, true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return stdout.Bytes(), ee.ExitCode(), false, nil
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return nil, 0, false, err
	}
	return stdout.Bytes(), 0, false, nil
}
