package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMailboxMessage(t *testing.T, path, id string) {
	t.Helper()
	body := `---json
{"schema":1,"id":"` + id + `","from":"cto","to":["qa"],"thread":"p2p/cto__qa","subject":"hello","created":"2026-07-06T10:00:00Z","kind":"status"}
---
body
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestScanMailboxSkipsNonRegularMarkdownFile(t *testing.T) {
	agentDir := t.TempDir()
	target := filepath.Join(agentDir, "target.md")
	writeMailboxMessage(t, target, "target")
	link := filepath.Join(agentDir, "inbox", "new", "link.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}

	msgs, warns := scanMailbox(agentDir, "cto", func() time.Time { return testNow })
	if len(msgs) != 0 {
		t.Fatalf("symlinked message file must be skipped, got %+v", msgs)
	}
	if len(warns) != 1 {
		t.Fatalf("warnings = %d, want 1: %+v", len(warns), warns)
	}
	if warns[0].Path != link || !strings.Contains(warns[0].Reason, "non-regular") {
		t.Fatalf("warning should name non-regular link, got %+v", warns[0])
	}
}

func TestScanMailboxAllowsRegularFileThroughSymlinkedParent(t *testing.T) {
	root := t.TempDir()
	realAgent := filepath.Join(root, "real-agent")
	writeMailboxMessage(t, filepath.Join(realAgent, "inbox", "new", "m1.md"), "m1")
	linkAgent := filepath.Join(root, "linked-agent")
	if err := os.Symlink(realAgent, linkAgent); err != nil {
		t.Skipf("symlink unsupported in this environment: %v", err)
	}

	msgs, warns := scanMailbox(linkAgent, "cto", func() time.Time { return testNow })
	if len(warns) != 0 {
		t.Fatalf("regular file under symlinked parent should not warn: %+v", warns)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" {
		t.Fatalf("regular file under symlinked parent should parse, got %+v", msgs)
	}
}
