package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMalformedTypedApprovalContextIsRetainedAsBarrierEvidence(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agents", "cto", "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := `---json
{"schema":1,"id":"a1","from":"user","to":["cto"],"thread":"gate/x","subject":"APPROVED: x","created":"2026-07-11T00:00:00Z","kind":"answer","context":{"approval":{"schema_version":1,"source":"human","self_approved":false,"unknown":true}}}
---
Action: protected_branch_push
Target: x
`
	if err := os.WriteFile(filepath.Join(dir, "a1.md"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, warnings := ScanSessionMessages(root, time.Now)
	if len(warnings) != 0 || len(msgs) != 1 {
		t.Fatalf("msgs=%d warnings=%v", len(msgs), warnings)
	}
	msg := msgs[0]
	if !msg.ApprovalPresent || msg.ApprovalValid || msg.Approval != nil || msg.ApprovalError == "" {
		t.Fatalf("malformed approval was discarded/trusted: %+v", msg)
	}
	if msg.Context == nil {
		t.Fatal("raw context was discarded")
	}
}
