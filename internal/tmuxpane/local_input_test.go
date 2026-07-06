package tmuxpane

import (
	"errors"
	"strings"
	"testing"
)

func TestAnalyzeLocalInputBlockerDetectsDestructiveApprovalPrompt(t *testing.T) {
	blocker, ok := AnalyzeLocalInputBlocker(`
planning output
Permission rule Bash(rm -rf *) requires confirmation
`)
	if !ok {
		t.Fatal("approval prompt should be detected")
	}
	if blocker.Kind != "approval_prompt" {
		t.Fatalf("kind = %q, want approval_prompt", blocker.Kind)
	}
	if !blocker.Destructive {
		t.Fatalf("destructive prompt should carry risk hint: %+v", blocker)
	}
	if !strings.Contains(blocker.Summary, "requires confirmation") {
		t.Fatalf("summary should include prompt line: %+v", blocker)
	}
	for _, forbidden := range []string{"auto-approve", "--force"} {
		if strings.Contains(strings.ToLower(blocker.Recovery), forbidden) {
			t.Fatalf("destructive recovery must not suggest %q: %q", forbidden, blocker.Recovery)
		}
	}
	if !strings.Contains(blocker.Recovery, "non-destructive alternative") {
		t.Fatalf("destructive recovery should name non-destructive alternative: %q", blocker.Recovery)
	}
}

func TestAnalyzeLocalInputBlockerDetectsAllowQuestion(t *testing.T) {
	blocker, ok := AnalyzeLocalInputBlocker("Do you want to allow this command?\n")
	if !ok {
		t.Fatal("allow question should be detected")
	}
	if blocker.Destructive {
		t.Fatalf("generic allow prompt should not be marked destructive: %+v", blocker)
	}
	if !strings.Contains(blocker.Recovery, "AMQ gate") {
		t.Fatalf("generic recovery should suggest AMQ gate routing: %q", blocker.Recovery)
	}
}

func TestAnalyzeLocalInputBlockerIgnoresScrollbackMarkers(t *testing.T) {
	var b strings.Builder
	b.WriteString("Permission rule Bash(rm -rf *) requires confirmation\n")
	for i := 0; i < 20; i++ {
		b.WriteString("ordinary output line\n")
	}
	b.WriteString("Done.\n\n> \n  ? for shortcuts\n")
	if blocker, ok := AnalyzeLocalInputBlocker(b.String()); ok {
		t.Fatalf("scrollback prompt must not read as blocked: %+v", blocker)
	}
}

func TestDetectLocalInputBlockerCaptureErrorDegradesUnknown(t *testing.T) {
	setCapturer(t, func(string) (string, error) { return "", errors.New("pane gone") })
	blocker, ok, err := DetectLocalInputBlocker("%1")
	if ok {
		t.Fatalf("capture error must not report a blocker: %+v", blocker)
	}
	if err == nil {
		t.Fatal("capture error should be returned to caller")
	}
}
