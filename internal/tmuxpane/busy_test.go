package tmuxpane

import (
	"errors"
	"strings"
	"testing"
)

// setCapturer swaps the capture seam for a test and restores it afterwards.
func setCapturer(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	prev := paneCapturer
	paneCapturer = fn
	t.Cleanup(func() { paneCapturer = prev })
}

func TestPaneBusyDetectsInFlightAgent(t *testing.T) {
	busyCaptures := []string{
		"· 1.2k tokens · esc to interrupt",                     // Claude Code generating
		"● Generating · 88 tokens · esc to interrupt",          // token meter in footer
		"Press esc to cancel",                                  // prompt/cancel state
		"foo\nbar\n✳ Thinking · 412 tokens · esc to interrupt", // marker in the footer line
	}
	for _, capture := range busyCaptures {
		setCapturer(t, func(string) (string, error) { return capture, nil })
		busy, err := PaneBusy("%1")
		if err != nil || !busy {
			t.Errorf("capture %q should read as busy (busy=%v err=%v)", capture, busy, err)
		}
	}
}

func TestPaneBusyTreatsIdleAsIdle(t *testing.T) {
	idleCaptures := []string{
		"",                            // empty
		"cto $ ",                      // a bare shell prompt
		"> \n  ? for shortcuts",       // an idle agent input box
		"Done. 3 files changed.\n\n>", // finished turn, waiting
	}
	for _, capture := range idleCaptures {
		setCapturer(t, func(string) (string, error) { return capture, nil })
		busy, err := PaneBusy("%1")
		if err != nil || busy {
			t.Errorf("capture %q should read as idle (busy=%v err=%v)", capture, busy, err)
		}
	}
}

func TestPaneBusyIgnoresScrollbackMarkers(t *testing.T) {
	// A marker that appears in scrollback CONTENT (an assistant response or help
	// text) but not in the live footer must NOT read as busy — only the tail is
	// inspected.
	var b strings.Builder
	b.WriteString("note: press esc to interrupt to stop generation\n") // content, near the top
	for i := 0; i < 20; i++ {
		b.WriteString("ordinary output line\n")
	}
	b.WriteString("Done.\n\n> \n  ? for shortcuts\n") // idle footer
	setCapturer(t, func(string) (string, error) { return b.String(), nil })
	busy, err := PaneBusy("%1")
	if err != nil || busy {
		t.Errorf("a marker in scrollback (not the footer) must not read as busy: busy=%v err=%v", busy, err)
	}
}

func TestPaneBusyCaptureErrorIsNotBusy(t *testing.T) {
	// A capture failure must surface the error and report NOT busy, so the caller
	// never blocks delivery on a failed check.
	setCapturer(t, func(string) (string, error) { return "", errors.New("pane gone") })
	busy, err := PaneBusy("%1")
	if busy {
		t.Error("a capture error must not be reported as busy")
	}
	if err == nil {
		t.Error("a capture error must be surfaced")
	}
}
