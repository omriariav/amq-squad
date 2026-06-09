package tmuxpane

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type deliverCall struct {
	stdin string
	args  []string
}

// swapDeliver records every deliverExec call and lets the test drive the result
// per tmux subcommand (keyed by the first arg).
func swapDeliver(t *testing.T, results map[string]error) *[]deliverCall {
	t.Helper()
	// Make submit deterministic and fast: no real sleeps, and a capturer that
	// reports the prompt left the input box (submitted on the first Enter) so the
	// submit loop never shells real `tmux capture-pane`. Individual tests can
	// override paneCapturer afterwards to exercise the retry/error paths.
	prevSettle, prevVerify, prevCap := submitSettleDelay, submitVerifyDelay, paneCapturer
	submitSettleDelay, submitVerifyDelay = 0, 0
	paneCapturer = func(string) (string, error) { return "", nil }
	var calls []deliverCall
	prev := deliverExec
	deliverExec = func(stdin string, args ...string) (string, error) {
		calls = append(calls, deliverCall{stdin: stdin, args: append([]string(nil), args...)})
		if len(args) > 0 {
			if err, ok := results[args[0]]; ok {
				return "", err
			}
		}
		return "", nil
	}
	t.Cleanup(func() {
		deliverExec = prev
		submitSettleDelay, submitVerifyDelay, paneCapturer = prevSettle, prevVerify, prevCap
	})
	return &calls
}

func enterCount(calls []deliverCall) int {
	n := 0
	for _, c := range calls {
		if len(c.args) > 0 && c.args[0] == "send-keys" && c.args[len(c.args)-1] == "Enter" {
			n++
		}
	}
	return n
}

func TestSendPromptToPaneDeliversVerbatimWithEnter(t *testing.T) {
	calls := swapDeliver(t, nil) // all subcommands succeed
	// Prompt with newlines, quotes, and shell metacharacters — all hostile to a
	// shell-string approach, all fine through a stdin-loaded paste buffer.
	prompt := "line one\nwith \"quotes\" and $(rm -rf /) and `backticks` and ; | &\nlast line"
	if err := SendPromptToPane("%265", prompt); err != nil {
		t.Fatalf("SendPromptToPane: %v", err)
	}
	if len(*calls) != 4 {
		t.Fatalf("expected 4 tmux calls (exists, load-buffer, paste-buffer, send-keys), got %d: %+v", len(*calls), *calls)
	}
	// 1) liveness probe targets the exact pane
	if got := (*calls)[0].args; !reflect.DeepEqual(got, []string{"display-message", "-p", "-t", "%265", "#{pane_id}"}) {
		t.Fatalf("liveness probe argv = %v", got)
	}
	// 2) load-buffer carries the prompt via STDIN, never argv, into a unique buf
	lb := (*calls)[1]
	if len(lb.args) != 4 || lb.args[0] != "load-buffer" || lb.args[1] != "-b" || lb.args[3] != "-" {
		t.Fatalf("load-buffer argv = %v", lb.args)
	}
	buf := lb.args[2]
	if !strings.HasPrefix(buf, "amq-squad-prompt-") {
		t.Fatalf("buffer name not unique-prefixed: %q", buf)
	}
	if lb.stdin != prompt {
		t.Fatalf("prompt not delivered verbatim via stdin:\n got %q\nwant %q", lb.stdin, prompt)
	}
	for _, a := range lb.args {
		if strings.Contains(a, "line one") || strings.Contains(a, "rm -rf") {
			t.Fatalf("prompt text leaked into argv: %v", lb.args)
		}
	}
	// 3) paste-buffer deletes the SAME buffer, requests bracketed paste, targets pane
	if got := (*calls)[2].args; !reflect.DeepEqual(got, []string{"paste-buffer", "-d", "-p", "-b", buf, "-t", "%265"}) {
		t.Fatalf("paste-buffer argv = %v (buf %q)", got, buf)
	}
	// 4) explicit Enter submit
	if got := (*calls)[3].args; !reflect.DeepEqual(got, []string{"send-keys", "-t", "%265", "Enter"}) {
		t.Fatalf("send-keys argv = %v", got)
	}
}

func TestSendPromptUsesUniqueBufferPerCall(t *testing.T) {
	calls := swapDeliver(t, nil)
	if err := SendPromptToPane("%1", "a"); err != nil {
		t.Fatal(err)
	}
	if err := SendPromptToPane("%1", "b"); err != nil {
		t.Fatal(err)
	}
	// load-buffer is the 2nd call of each 4-call send (indices 1 and 5).
	buf1, buf2 := (*calls)[1].args[2], (*calls)[5].args[2]
	if buf1 == buf2 {
		t.Fatalf("concurrent sends must use distinct buffers, both = %q", buf1)
	}
}

func TestSendPromptToDeadPaneErrors(t *testing.T) {
	calls := swapDeliver(t, map[string]error{"display-message": errors.New("can't find pane %999")})
	err := SendPromptToPane("%999", "hello")
	if err == nil {
		t.Fatal("expected error for dead pane")
	}
	var dead *DeadPaneError
	if !errors.As(err, &dead) {
		t.Fatalf("want *DeadPaneError, got %T: %v", err, err)
	}
	if dead.PaneID != "%999" {
		t.Errorf("DeadPaneError.PaneID = %q, want %%999", dead.PaneID)
	}
	// It must NOT attempt to load/paste into a dead pane.
	if len(*calls) != 1 {
		t.Fatalf("dead pane should stop after the liveness probe, got %d calls: %+v", len(*calls), *calls)
	}
}

func TestSendPromptPasteBufferFailurePropagates(t *testing.T) {
	swapDeliver(t, map[string]error{"paste-buffer": errors.New("pane not writable")})
	if err := SendPromptToPane("%5", "hi"); err == nil || !strings.Contains(err.Error(), "paste-buffer") {
		t.Fatalf("paste-buffer failure should surface, got %v", err)
	}
}

func TestSendKeysToPaneArgv(t *testing.T) {
	calls := swapDeliver(t, nil)
	if err := SendKeysToPane("%5", "Enter"); err != nil {
		t.Fatalf("SendKeysToPane: %v", err)
	}
	if got := (*calls)[0].args; !reflect.DeepEqual(got, []string{"send-keys", "-t", "%5", "Enter"}) {
		t.Fatalf("send-keys argv = %v", got)
	}
	if err := SendKeysToPane("", "Enter"); err == nil {
		t.Fatal("empty pane id should error")
	}
}

func TestTargetForPaneID(t *testing.T) {
	panes := []TmuxPane{
		{Session: "main", Window: "0", Pane: "1", PaneID: "%5", Title: "amq:issue-96:cto", WindowName: "squad"},
		{Session: "main", Window: "0", Pane: "2", PaneID: "%6"},
	}
	tgt, ok := TargetForPaneID("%5", panes)
	if !ok {
		t.Fatal("pane %5 should resolve")
	}
	if tgt.Session != "main" || tgt.Pane != "1" || tgt.Title != "amq:issue-96:cto" || tgt.WindowName != "squad" {
		t.Fatalf("target built wrong: %+v", tgt)
	}
	if _, ok := TargetForPaneID("%999", panes); ok {
		t.Fatal("missing pane id must not resolve")
	}
	if _, ok := TargetForPaneID("", panes); ok {
		t.Fatal("empty pane id must not resolve")
	}
}

// The #86 fix: if the first Enter does not submit (the input region is
// unchanged), submit retries the Enter rather than leaving the message hanging.
func TestSendPromptRetriesEnterWhenInputUnchanged(t *testing.T) {
	calls := swapDeliver(t, nil)
	// before/after per attempt: attempt 1 sees the input region UNCHANGED (Enter
	// dropped) -> retry; attempt 2 sees it CHANGE (submitted).
	const staged = "│ please review the long set of changes │\n  ? for shortcuts"
	const cleared = "> \n  ? for shortcuts"
	n := 0
	paneCapturer = func(string) (string, error) {
		n++
		switch n {
		case 1, 2, 3: // attempt1 before, attempt1 after, attempt2 before
			return staged, nil
		default: // attempt2 after
			return cleared, nil
		}
	}
	if err := SendPromptToPane("%5", "do it\nplease review the long set of changes"); err != nil {
		t.Fatalf("SendPromptToPane: %v", err)
	}
	if got := enterCount(*calls); got != 2 {
		t.Fatalf("want 2 Enter attempts (one retry), got %d", got)
	}
}

// If the input region never changes, the prompt never submitted: return a clear
// error rather than silently leaving text staged (#86 acceptance criterion).
func TestSendPromptErrorsWhenNeverConfirmed(t *testing.T) {
	calls := swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) { return "│ hang me │\n  ? for shortcuts", nil } // unchanged forever
	err := SendPromptToPane("%5", "x\nhang me")
	if err == nil || !strings.Contains(err.Error(), "could not confirm it submitted") {
		t.Fatalf("want a clear not-submitted error, got %v", err)
	}
	if got := enterCount(*calls); got != submitAttempts {
		t.Errorf("want %d Enter attempts before erroring, got %d", submitAttempts, got)
	}
}

// A changed input region means submitted on the first Enter; a blank/unavailable
// capture fails open (one Enter, no retry, no error).
func TestSendPromptSubmitsOnInputChangeOrFailsOpen(t *testing.T) {
	// Region CHANGES after Enter -> submitted, single Enter.
	calls := swapDeliver(t, nil)
	n := 0
	paneCapturer = func(string) (string, error) {
		n++
		if n == 1 {
			return "│ staged │\n? for shortcuts", nil // before
		}
		return "● Working… esc to interrupt\n>", nil // after: changed
	}
	if err := SendPromptToPane("%5", "go"); err != nil {
		t.Fatal(err)
	}
	if got := enterCount(*calls); got != 1 {
		t.Fatalf("a changed input region should submit in one Enter, got %d", got)
	}

	// Blank capture -> can't verify -> fail open (single Enter, no error).
	calls2 := swapDeliver(t, nil) // its capturer returns "" (blank)
	if err := SendPromptToPane("%5", "go"); err != nil {
		t.Fatalf("blank capture must fail open, got %v", err)
	}
	if got := enterCount(*calls2); got != 1 {
		t.Fatalf("blank capture should fail open after one Enter, got %d", got)
	}
}

func TestCaptureInputRegion(t *testing.T) {
	prev := paneCapturer
	t.Cleanup(func() { paneCapturer = prev })
	paneCapturer = func(string) (string, error) { return "a\nb\nc\nd\ne\nf", nil }
	region, ok := captureInputRegion("%1")
	if !ok || !strings.Contains(region, "f") || strings.Contains(region, "a") {
		t.Errorf("captureInputRegion should return the bottom lines: ok=%v region=%q", ok, region)
	}
	paneCapturer = func(string) (string, error) { return "   \n  ", nil } // blank
	if _, ok := captureInputRegion("%1"); ok {
		t.Error("a blank region must report ok=false (nothing to compare)")
	}
	paneCapturer = func(string) (string, error) { return "", errors.New("pane gone") }
	if _, ok := captureInputRegion("%1"); ok {
		t.Error("a capture error must report ok=false")
	}
}
