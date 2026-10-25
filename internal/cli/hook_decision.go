package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
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
// the Go exec API (the watchdog kills the group on every platform; the hook
// script runs no watchdog of its own, so dash and bash behave alike),
// parses the complete verdict object and prints the decision. The 4.8 s
// watchdog starts at the adapter's entry and covers the stdin read too (the
// narrow pass's item 1). A timeout anywhere, a guard exit that is not 0, a
// guard output over 64 KiB (item 2) or a verdict that does not parse prints
// nothing and one log line on stderr, which the hook sends to its log
// descriptor; the adapter exits 0. That no decision is fail-open by design:
// the hooks are UX, enforcement is in the core (plan 4.3).
func runGuardDecision(kind, me string) error {
	if kind != "claude" {
		return hookErrorf("guard: --decision %q is not claude", kind)
	}
	deadline := time.Now().Add(guardWatchdog)
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "%s pre-tool %s\n", time.Now().UTC().Format("2006-01-02T15:04:05Z"), fmt.Sprintf(format, args...))
	}
	timeout := func(where string) error {
		logf("fail-open: guard timeout after %.1f s (%s; no decision)", guardWatchdog.Seconds(), where)
		return nil
	}
	type read struct {
		raw []byte
		err error
	}
	got := make(chan read, 1)
	in := hookStdin // the reader goroutine may outlive the adapter's answer
	go func() {
		raw, err := io.ReadAll(io.LimitReader(in, hookMaxPayload+1))
		if err == nil && len(raw) > hookMaxPayload {
			err = fmt.Errorf("guard: payload exceeds %d bytes", hookMaxPayload)
		}
		got <- read{raw, err}
	}()
	var payload []byte
	select {
	case r := <-got:
		if r.err != nil {
			logf("fail-open: %v", r.err)
			return nil
		}
		payload = r.raw
	case <-time.After(time.Until(deadline)):
		return timeout("stdin not closed")
	}
	// The guard is the binary the hook names (AMQ_SQUAD_BIN, which seat-exec
	// pins to the launching binary), else this one.
	bin := strings.TrimSpace(os.Getenv("AMQ_SQUAD_BIN"))
	if bin == "" {
		var err error
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
	out, code, how, err := runGuardChild(bin, args, payload, time.Until(deadline))
	switch {
	case how == childTimedOut:
		return timeout("killed")
	case how == childOverflow:
		logf("fail-open: guard output over %d bytes (killed; no decision)", maxGuardOutput)
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

// maxGuardOutput caps the guard child's stdout (one verdict line is a few
// hundred bytes).
const maxGuardOutput = 64 << 10

const (
	childDone = iota
	childTimedOut
	childOverflow
)

// cappedBuffer keeps at most max bytes and calls over once when more come.
type cappedBuffer struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	max  int
	over func()
	hit  bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hit {
		return len(p), nil
	}
	if c.buf.Len()+len(p) > c.max {
		c.hit = true
		c.over()
		return len(p), nil
	}
	return c.buf.Write(p)
}

// runGuardChild runs the guard child with stdin, in its own process group
// (guardGroup). The group is killed once, before Wait returns, when the
// watchdog fires or the output passes the cap; never after the reap, when
// the group id can already be reused (the narrow pass's item 3, the
// identity proof's pattern).
func runGuardChild(bin string, args []string, stdin []byte, limit time.Duration) ([]byte, int, int, error) {
	if limit <= 0 {
		return nil, 0, childTimedOut, nil
	}
	cmd := exec.Command(bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var how atomic.Int32
	out := &cappedBuffer{max: maxGuardOutput, over: func() {
		if how.CompareAndSwap(childDone, childOverflow) {
			killGuardGroup(cmd)
		}
	}}
	cmd.Stdout, cmd.Stderr = out, os.Stderr
	cmd.WaitDelay = 200 * time.Millisecond
	guardGroup(cmd)
	if err := cmd.Start(); err != nil {
		return nil, 0, childDone, err
	}
	timer := time.AfterFunc(limit, func() {
		if how.CompareAndSwap(childDone, childTimedOut) {
			killGuardGroup(cmd)
		}
	})
	err := cmd.Wait()
	timer.Stop()
	if h := int(how.Load()); h != childDone {
		return nil, 0, h, nil
	}
	out.mu.Lock()
	raw := append([]byte(nil), out.buf.Bytes()...)
	out.mu.Unlock()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return raw, ee.ExitCode(), childDone, nil
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) {
		return nil, 0, childDone, err
	}
	return raw, 0, childDone, nil
}
