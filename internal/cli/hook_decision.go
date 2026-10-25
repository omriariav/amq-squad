package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
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

// runGuardDecision is `guard --decision claude`: the verdict on stdin, the
// decision on stdout. An unparsable verdict prints nothing and exits 0 with
// one line on stderr (the hook's log): no decision, logged.
func runGuardDecision(kind string) error {
	if kind != "claude" {
		return hookErrorf("guard: --decision %q is not claude", kind)
	}
	raw, err := io.ReadAll(io.LimitReader(hookStdin, hookMaxPayload+1))
	if err != nil {
		return hookErrorf("guard: read the verdict: %v", err)
	}
	out, err := claudeDecision(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fail-open: unparsable guard output: %v\n", err)
		return nil
	}
	_, err = os.Stdout.Write(out)
	return err
}
