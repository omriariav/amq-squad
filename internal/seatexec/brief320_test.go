package seatexec

import (
	"strings"
	"testing"
)

// Brief 3.2.0 rows 1 and 4: Codex 0.166.0 has an in-turn PostToolUse
// catalogue event, so the seat receives activity hooks for it and Stop. A
// prompt submission or permission wait alone is never configured as work.
func TestCodexActivityHooksUseOnlyWorkEvents(t *testing.T) {
	args := CodexActivityHookArgs("/opt/amq squad/bin", "/opt/amq/bin")
	joined := strings.Join(args, "\n")
	for _, want := range []string{"hooks.PostToolUse", "hooks.Stop", "evidence ingest", "wake dialog"} {
		if !strings.Contains(joined, want) {
			t.Errorf("activity hook args omit %q: %s", want, joined)
		}
	}
	for _, absent := range []string{"hooks.PermissionRequest", "hooks.UserPromptSubmit"} {
		if strings.Contains(joined, absent) {
			t.Errorf("non-work event configured as activity: %s", joined)
		}
	}
	if strings.Join(CodexActivityHookEvents, ",") != "PostToolUse,Stop" {
		t.Fatalf("catalogue selection = %v", CodexActivityHookEvents)
	}
}
