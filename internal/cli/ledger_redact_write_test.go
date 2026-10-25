package cli

import (
	"os"
	"strings"
	"testing"
)

// v3-5-verifier fix (ii): the hook ledger is redacted on write, not only on
// read. A malformed report carrying a token is kept as its length, sha256
// and redacted printable prefix, never verbatim; a well-formed one has its
// secret-named values and credential shapes replaced before they reach disk,
// and its usage counters kept.
func TestMalformedReportCarryingATokenLeavesNoTokenOnDisk(t *testing.T) {
	f := newHookFixture(t)
	token := "gh" + "p_" + strings.Repeat("Q7rT2", 8) // built at run time, never a literal
	plain := "plain" + "-secret-" + "value"
	for _, payload := range []string{
		`{"tool_name":"Bash","tool_input":{"command":"echo ` + token,
		`{"tool_name":"Bash","tool_input":{"command":"echo ` + token + `"},"api_token":"` + plain + `","usage":{"prompt_tokens":12}}`,
	} {
		withHookStdin(t, []byte(payload))
		if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", "cto"}, "test") }); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(hookExtPath(f.root, "ledger", "cto.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), token) || strings.Contains(string(raw), plain) {
		t.Fatal("a credential reached the hook ledger on disk")
	}
	lines := readLedger(t, hookExtPath(f.root, "ledger", "cto.jsonl"))
	if len(lines) != 2 || !lines[0].Malformed || lines[0].PayloadPrefix == "" || lines[0].PayloadSHA256 == "" {
		t.Fatalf("the malformed line keeps no prefix or digest: %d lines", len(lines))
	}
	if !strings.Contains(string(lines[1].Payload), `"prompt_tokens":12`) {
		t.Fatal("a usage counter was redacted on write")
	}
}
