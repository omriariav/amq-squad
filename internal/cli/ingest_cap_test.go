package cli

import (
	"os"
	"strings"
	"testing"
)

// v3-5-verifier, item 3 of the third round (the cap rule at the ingest
// prefix): a malformed payload whose credential value sits right after the
// 4 KiB cut is redacted before the cut, and the cut keeps no partial line.
func TestIngestPrefixRedactsBeforeTheCut(t *testing.T) {
	f := newHookFixture(t)
	value := "Ab9" + "xY7qR" + "Lm2Wq8ZtPv4" // built at run time, never a literal
	withHookStdin(t, []byte(strings.Repeat(" ", 4070)+"{\"password\":\n\""+value+"\"} trailing"))
	if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", "cto"}, "test") }); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(hookExtPath(f.root, "ledger", "cto.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), value[:8]) {
		t.Fatal("a credential after the prefix cut reached the hook ledger")
	}
	if lines := readLedger(t, hookExtPath(f.root, "ledger", "cto.jsonl")); len(lines) != 1 || !lines[0].Malformed {
		t.Fatalf("the malformed line: %d lines", len(lines))
	}
}
