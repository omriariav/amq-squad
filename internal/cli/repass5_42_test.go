package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Phase 5 re-pass of 4-2, P2-3, after brief 31-1 (row 1): guard and
// evidence ingest each record a hook report, as context does; a changed
// config dir is no quarantine and guard does not deny for it.
func TestGuardAndIngestRecordAHookReport(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	wt, _ := filepath.EvalSymlinks(t.TempDir())
	os.MkdirAll(filepath.Join(root, "agents", "s1"), 0o700)
	dir := claudeseat.Dir(root, "s1", "launch-1")
	if _, err := claudeseat.Write(dir, "dontAsk", wt, time.Now()); err != nil {
		t.Fatal(err)
	}
	staged := launch.StagedDir(root, "s1")
	os.MkdirAll(staged, 0o700)
	rec := launch.Record{Handle: "s1", Root: root, Session: "s", Runtime: "claude", Worktree: wt, Branch: "seat/s1", LaunchID: "launch-1", PermissionMode: "dontAsk"}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ME", "s1")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_SESSION", "")
	since := time.Now().Add(-time.Second)
	guard := func() guardVerdict {
		t.Helper()
		out, _, err := captureOutput(t, func() error {
			return runGuard([]string{"--me", "s1", "--tool", "Bash", "--arg", "command=go test ./...", "--cwd", wt})
		})
		var v guardVerdict
		if err != nil || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatalf("guard: %v %q", err, out)
		}
		return v
	}
	ingest := func() {
		t.Helper()
		prev := hookStdin
		hookStdin = strings.NewReader(`{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_use_id":"toolu_1"}`)
		defer func() { hookStdin = prev }()
		if _, _, err := captureOutput(t, func() error { return runEvidenceIngest([]string{"--me", "s1"}) }); err != nil {
			t.Fatalf("ingest: %v", err)
		}
	}
	if v := guard(); v.Verdict == "deny" {
		t.Fatalf("an unchanged dir denied: %+v", v)
	}
	ingest()
	if n, _ := claudeseat.ReportsSince(root, "s1", since); n != 2 {
		t.Fatalf("guard and ingest made %d reports, want 2", n)
	}
	mustWrite(t, filepath.Join(dir, "settings.json"), `{"permissions":{"defaultMode":"dontAsk","allow":["Bash(*)"]}}`)
	if v := guard(); v.Verdict == "deny" {
		t.Fatalf("a changed dir denied: %+v", v)
	}
	if _, err := os.Stat(filepath.Join(task.DirFor(root), "quarantine")); !os.IsNotExist(err) {
		t.Fatalf("a quarantine was written: %v", err)
	}
	ingest()
	if n, _ := claudeseat.ReportsSince(root, "s1", since); n != 4 {
		t.Fatalf("%d reports, want 4", n)
	}
}

// Phase 5 re-pass of 4-2, P2-5: the Go adapter maps only one complete
// verdict object; a truncated or trailing one is no decision.
func TestClaudeDecisionNeedsTheCompleteVerdict(t *testing.T) {
	for _, raw := range []string{`{"verdict":"allow"`, `{"verdict":"allow"} {"x":1}`, `{"verdict":"maybe"}`, ``, `allow`} {
		if out, err := claudeDecision([]byte(raw)); err == nil {
			t.Errorf("%q mapped to %s", raw, out)
		}
	}
	out, err := claudeDecision([]byte(`{"verdict":"deny","reason_code":"force_push","reason":"a \"quoted\" \\ reason"}` + "\n"))
	var d struct {
		H struct{ PermissionDecision, PermissionDecisionReason string } `json:"hookSpecificOutput"`
	}
	if err != nil || json.Unmarshal(out, &d) != nil || d.H.PermissionDecision != "deny" || d.H.PermissionDecisionReason != `amq-squad guard: a "quoted" \ reason` {
		t.Fatalf("deny: %v %s", err, out)
	}
}
