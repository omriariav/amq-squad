package ledger

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHasActivitySinceRequiresACompleteOwnToolResultAtOrAfterTheBoundary(t *testing.T) {
	since := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		lines []map[string]any
		raw   string
		want  bool
	}{
		{"T1 PostToolUse at boundary", []map[string]any{{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "PostToolUse", "tool": "Bash", "outcome": "ok"}}, "", true},
		{"T2 PostToolUse before boundary", []map[string]any{{"at": since.Add(-time.Nanosecond).Format(time.RFC3339Nano), "handle": "dev", "event": "PostToolUse", "tool": "Bash", "outcome": "ok"}}, "", false},
		{"T3 tool_result after boundary", []map[string]any{{"at": since.Add(time.Second).Format(time.RFC3339Nano), "handle": "dev", "event": "tool_result", "tool": "Bash", "outcome": "ok"}}, "", true},
		{"T4 malformed row", []map[string]any{{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "PostToolUse", "tool": "Bash", "outcome": "ok", "malformed": true}}, "", false},
		{"T5 truncated row", []map[string]any{{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "PostToolUse", "tool": "Bash", "outcome": "ok", "truncated": true}}, "", false},
		{"T6 other events", []map[string]any{
			{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "Stop", "tool": "Bash", "outcome": "ok"},
			{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "UserPromptSubmit", "tool": "Bash", "outcome": "ok"},
			{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "PermissionRequest", "tool": "Bash", "outcome": "ok"},
			{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "SessionStart", "tool": "Bash", "outcome": "ok"},
		}, "", false},
		{"T7 missing outcome", []map[string]any{{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "PostToolUse", "tool": "Bash"}}, "", false},
		{"T8 missing tool", []map[string]any{{"at": since.Format(time.RFC3339Nano), "handle": "dev", "event": "PostToolUse", "outcome": "ok"}}, "", false},
		{"T9 another handle", []map[string]any{{"at": since.Format(time.RFC3339Nano), "handle": "other", "event": "PostToolUse", "tool": "Bash", "outcome": "ok"}}, "", false},
		{"T10 unparsable rows", []map[string]any{{"at": "not-a-time", "handle": "dev", "event": "PostToolUse", "tool": "Bash", "outcome": "ok"}}, "not json\n", false},
		{"T11 missing ledger", nil, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, line := range tt.lines {
				if _, err := Append(Path(root, "dev"), func(seq uint64) any {
					copy := map[string]any{"schema": LineSchema, "seq": seq}
					for k, v := range line {
						copy[k] = v
					}
					return copy
				}); err != nil {
					t.Fatal(err)
				}
			}
			if tt.raw != "" {
				if err := os.MkdirAll(filepath.Dir(Path(root, "dev")), 0o700); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(Path(root, "dev"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.WriteString(tt.raw); err != nil {
					f.Close()
					t.Fatal(err)
				}
				if err := f.Close(); err != nil {
					t.Fatal(err)
				}
			}
			got, err := HasActivitySince(root, "dev", since)
			if err != nil || got != tt.want {
				t.Fatalf("%s: got %v, err %v, want %v and nil", tt.name, got, err, tt.want)
			}
		})
	}
}
