package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestEvidenceListReadsTheLedgerTailRedacted(t *testing.T) {
	f := newHookFixture(t)
	payloads := []string{
		`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls","env":{"GH_TOKEN":"ghp_secret","HOME":"/h"}},"tool_response":{"stdout":"ok"},"usage":{"input_tokens":12}}`,
		`{"hook_event_name":"PostToolUse","tool_name":"Edit","tool_response":{"is_error":true}}`,
		`{broken`,
	}
	for _, p := range payloads {
		withHookStdin(t, []byte(p))
		if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "ingest", "--me", "cto"}, "test") }); err != nil {
			t.Fatal(err)
		}
	}
	before, _ := os.ReadFile(hookExtPath(f.root, "ledger", "cto.jsonl"))
	out, _, err := captureOutput(t, func() error {
		return Run([]string{"evidence", "list", "--me", "cto", "--root", f.root, "--last", "2", "--json"}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	env := decodeJSONEnvelope[evidenceListData](t, out)
	if env.Kind != "evidence_list" || len(env.Data.Lines) != 2 {
		t.Fatalf("list --last 2 = %+v", env)
	}
	var second hookLedgerLine
	if err := json.Unmarshal(env.Data.Lines[1], &second); err != nil || !second.Malformed || second.Seq != 3 {
		t.Fatalf("last line = %+v (%v)", second, err)
	}
	all, _, err := captureOutput(t, func() error {
		return Run([]string{"evidence", "list", "--me", "cto", "--root", f.root, "--json"}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(all, "ghp_secret") || !strings.Contains(all, `"GH_TOKEN": "[redacted]"`) || !strings.Contains(all, `"HOME": "/h"`) || !strings.Contains(all, `"input_tokens": 12`) {
		t.Fatalf("redaction: %s", all)
	}
	text, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "list", "--me", "cto", "--root", f.root}, "test") })
	if err != nil || !strings.Contains(text, "PostToolUse\tEdit\terror") {
		t.Fatalf("text (%v):\n%s", err, text)
	}
	after, _ := os.ReadFile(hookExtPath(f.root, "ledger", "cto.jsonl"))
	if string(after) != string(before) {
		t.Fatal("evidence list wrote the ledger")
	}
	if none, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "list", "--me", "qa", "--root", f.root}, "test") }); err != nil || !strings.Contains(none, "No evidence") {
		t.Fatalf("missing ledger (%v): %s", err, none)
	}
	if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "list", "--me", "cto", "--root", "rel"}, "test") }); err == nil {
		t.Fatal("a relative root was accepted")
	}
	if _, _, err := captureOutput(t, func() error { return Run([]string{"evidence", "run", "t1"}, "test") }); err == nil {
		t.Fatal("the removed evidence run subverb still dispatches")
	}
}
