package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// v3-5-verifier, the fifth round's P1, the ledger line: a hook payload whose
// tool output carries YAML on one line (a bare key, a quoted key, a flow
// mapping) or an indentless list under a credential key leaves no value on
// disk.
func TestOneLineYAMLCredentialNeverReachesTheHookLedger(t *testing.T) {
	f := newHookFixture(t)
	value := "Ab9" + "xY7qR" + "Lm2Wq8Zt" // built at run time, never a literal
	for _, out := range []string{
		"password: " + value,
		"'password': " + value,
		"creds: {password: " + value + ", user: kept}",
		"password:\n- " + value + "\nuser: kept",
	} {
		raw, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_response": map[string]string{"stdout": out}})
		withHookStdin(t, raw)
		if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", "cto"}, "test") }); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(hookExtPath(f.root, "ledger", "cto.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), value[:8]) {
		t.Fatal("a one-line YAML credential reached the hook ledger")
	}
}
