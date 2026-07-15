package tmuxpane

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
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
	prevSettle, prevVerify, prevCap, prevPaste := submitSettleDelay, submitVerifyDelay, paneCapturer, pasteSettleInterval
	submitSettleDelay, submitVerifyDelay, pasteSettleInterval = 0, 0, 0
	var calls []deliverCall
	paneCapturer = func(string) (string, error) {
		if len(calls) > 0 {
			last := calls[len(calls)-1].args
			if len(last) > 0 && last[0] == "send-keys" {
				return "submitted", nil
			}
		}
		return "staged", nil
	}
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
		submitSettleDelay, submitVerifyDelay, paneCapturer, pasteSettleInterval = prevSettle, prevVerify, prevCap, prevPaste
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

func TestSendPromptBracketedPasteOnlyForMultiline(t *testing.T) {
	// A single-line prompt (the dispatch drain-nudge shape) must paste WITHOUT
	// -p: the bracketed-paste start marker (ESC[200~) can leak as literal text
	// into a just-spawned Codex TUI before it enables bracketed-paste mode,
	// leaving the input stuck and unsubmitted.
	calls := swapDeliver(t, nil)
	if err := SendPromptToPane("%7", "run `amq drain --include-body` and act on it"); err != nil {
		t.Fatalf("SendPromptToPane: %v", err)
	}
	paste := (*calls)[2].args // exists, load-buffer, paste-buffer, send-keys
	buf := (*calls)[1].args[2]
	if want := []string{"paste-buffer", "-d", "-b", buf, "-t", "%7"}; !reflect.DeepEqual(paste, want) {
		t.Fatalf("single-line paste argv = %v, want %v (no -p)", paste, want)
	}
	for _, a := range paste {
		if a == "-p" {
			t.Fatalf("single-line prompt must NOT request bracketed paste: %v", paste)
		}
	}

	// A multi-line prompt keeps -p so embedded newlines don't submit early.
	calls2 := swapDeliver(t, nil)
	if err := SendPromptToPane("%8", "first line\nsecond line"); err != nil {
		t.Fatalf("SendPromptToPane multi-line: %v", err)
	}
	paste2 := (*calls2)[2].args
	buf2 := (*calls2)[1].args[2]
	if want := []string{"paste-buffer", "-d", "-p", "-b", buf2, "-t", "%8"}; !reflect.DeepEqual(paste2, want) {
		t.Fatalf("multi-line paste argv = %v, want %v (with -p)", paste2, want)
	}
}

func TestSendPromptSingleLineWaitsForPaneToSettleBeforePaste(t *testing.T) {
	calls := swapDeliver(t, nil)
	seq := []string{"", "booting", "ready", "ready", "│ staged │", "● Working\n>"}
	var captures []string
	paneCapturer = func(string) (string, error) {
		if len(seq) == 0 {
			return "● Working\n>", nil
		}
		got := seq[0]
		seq = seq[1:]
		captures = append(captures, got)
		return got, nil
	}
	if err := SendPromptToPane("%7", "run `amq drain --include-body` and act on it"); err != nil {
		t.Fatalf("SendPromptToPane: %v", err)
	}
	if len(captures) < 4 || captures[0] != "" || captures[1] != "booting" || captures[2] != "ready" || captures[3] != "ready" {
		t.Fatalf("single-line send did not wait through blank/drawing/stable captures: %q", captures)
	}
	paste := (*calls)[2].args
	for _, arg := range paste {
		if arg == "-p" {
			t.Fatalf("single-line prompt must still avoid bracketed paste after settle: %v", paste)
		}
	}
}

func TestSendPromptMultilineLeakReturnsError(t *testing.T) {
	// A multi-line prompt pasted into a not-yet-ready TUI leaves the bracketed-
	// paste START marker as literal text. We must detect that and FAIL clearly
	// rather than press Enter on a mangled input.
	calls := swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) {
		return "│ [200~first line\nsecond line │\n  ? for shortcuts", nil
	}
	err := SendPromptToPane("%9", "first line\nsecond line")
	var leak *BracketedPasteLeakError
	if !errors.As(err, &leak) {
		t.Fatalf("want *BracketedPasteLeakError, got %T: %v", err, err)
	}
	if leak.PaneID != "%9" {
		t.Errorf("leak PaneID = %q, want %%9", leak.PaneID)
	}
	// It must NOT submit a mangled input.
	if got := enterCount(*calls); got != 0 {
		t.Fatalf("a leaked bracketed paste must not press Enter, got %d", got)
	}
}

func TestSendPromptMultilineEndMarkerLeakReturnsErrorBeforeEnter(t *testing.T) {
	calls := swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) {
		return "│ first line\nsecond line[201~ │\n  ? for shortcuts", nil
	}
	err := SendPromptToPane("%10", "first line\nsecond line")
	var leak *BracketedPasteLeakError
	if !errors.As(err, &leak) || leak.PaneID != "%10" {
		t.Fatalf("want *BracketedPasteLeakError for end marker, got %T: %v", err, err)
	}
	if got := enterCount(*calls); got != 0 {
		t.Fatalf("a leaked bracketed-paste end marker must not press Enter, got %d", got)
	}
}

func TestSendPromptMultilineUnavailableInspectionReturnsTypedErrorBeforeEnter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		capture    func(string) (string, error)
		wantCause  bool
		wantDetail string
	}{
		{
			name: "capture error",
			capture: func(string) (string, error) {
				return "", errors.New("capture denied")
			},
			wantCause:  true,
			wantDetail: "capture failed",
		},
		{
			name: "blank capture",
			capture: func(string) (string, error) {
				return "  \n\t", nil
			},
			wantDetail: "capture was blank",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := swapDeliver(t, nil)
			paneCapturer = tc.capture
			err := SendPromptToPane("%11", "first line\nsecond line")
			var unavailable *BracketedPasteCheckUnavailableError
			if !errors.As(err, &unavailable) {
				t.Fatalf("want *BracketedPasteCheckUnavailableError, got %T: %v", err, err)
			}
			if unavailable.PaneID != "%11" || !strings.Contains(unavailable.Detail, tc.wantDetail) {
				t.Fatalf("unavailable error = %+v, want pane %%11 detail %q", unavailable, tc.wantDetail)
			}
			if tc.wantCause != (unavailable.Cause != nil) {
				t.Fatalf("unavailable cause = %v, want present=%v", unavailable.Cause, tc.wantCause)
			}
			if got := enterCount(*calls); got != 0 {
				t.Fatalf("unavailable bracketed-paste inspection must not press Enter, got %d", got)
			}
		})
	}
}

func TestSendPromptSingleLineDoesNotRunBracketedPasteInspection(t *testing.T) {
	calls := swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) { return "", errors.New("capture denied") }
	err := SendPromptToPane("%12", "single line")
	var unavailable *BracketedPasteCheckUnavailableError
	if errors.As(err, &unavailable) {
		t.Fatalf("single-line delivery must not run bracketed-paste inspection: %v", err)
	}
	var unconfirmed *SubmitUnconfirmedError
	if !errors.As(err, &unconfirmed) {
		t.Fatalf("single-line capture failure should retain post-Enter submit ambiguity, got %T: %v", err, err)
	}
	if got := enterCount(*calls); got != 1 {
		t.Fatalf("single-line delivery should still attempt one Enter, got %d", got)
	}
	for _, call := range *calls {
		if len(call.args) == 0 || call.args[0] != "paste-buffer" {
			continue
		}
		for _, arg := range call.args {
			if arg == "-p" {
				t.Fatalf("single-line delivery must not use bracketed paste: %v", call.args)
			}
		}
	}
}

func TestSendPromptSingleLineIgnoresStrayMarker(t *testing.T) {
	// The leak backstop applies ONLY to bracketed (multi-line) pastes. A single-
	// line nudge sends no markers, so a stray "[200~" already on screen (e.g. in
	// scrollback) must not be misread as a failed delivery.
	swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) { return "[200~ from earlier\n> ", nil }
	err := SendPromptToPane("%9", "run `amq drain --include-body` and act on it")
	var leak *BracketedPasteLeakError
	if errors.As(err, &leak) {
		t.Fatalf("single-line delivery must not raise a bracketed-paste leak error: %v", err)
	}
}

func TestSendPromptMultilineCleanSucceeds(t *testing.T) {
	// Multi-line, no leaked marker, input region changes after Enter -> success.
	swapDeliver(t, nil)
	n := 0
	paneCapturer = func(string) (string, error) {
		n++
		// distinct, marker-free captures: settle proceeds, no leak, submit
		// sees the region change on the Enter.
		return fmt.Sprintf("clean input box render %d\n> ", n), nil
	}
	if err := SendPromptToPane("%9", "line one\nline two"); err != nil {
		t.Fatalf("clean multi-line send should succeed, got %v", err)
	}
}

func TestWaitPaneSettledProceedsWhenStable(t *testing.T) {
	prevCap, prevInt := paneCapturer, pasteSettleInterval
	pasteSettleInterval = 0
	t.Cleanup(func() { paneCapturer, pasteSettleInterval = prevCap, prevInt })

	// Starts BLANK (the riskiest fresh-startup window), draws, then holds steady:
	// waitPaneSettled must poll through the blank instead of bailing on it, and
	// return once output stops changing (never block).
	seq := []string{"", "boot", "ready", "ready", "ready"}
	i := 0
	paneCapturer = func(string) (string, error) {
		s := seq[i]
		if i < len(seq)-1 {
			i++
		}
		return s, nil
	}
	done := make(chan struct{})
	go func() { waitPaneSettled("%1"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("waitPaneSettled did not return on a stabilizing pane")
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
	const ready = "> ready"
	const staged = "│ please review the long set of changes │\n  ? for shortcuts"
	const cleared = "> \n  ? for shortcuts"
	n := 0
	paneCapturer = func(string) (string, error) {
		n++
		switch n {
		case 1, 2: // settle wait
			return ready, nil
		case 3, 4, 5: // attempt1 before, attempt1 after, attempt2 before
			return staged, nil
		default: // attempt2 after
			return cleared, nil
		}
	}
	if err := SendPromptToPane("%5", "please review the long set of changes"); err != nil {
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
	var unconfirmed *SubmitUnconfirmedError
	if !errors.As(err, &unconfirmed) {
		t.Fatalf("want a *SubmitUnconfirmedError so callers can distinguish it, got %T", err)
	}
	if got := enterCount(*calls); got != submitAttempts {
		t.Errorf("want %d Enter attempts before erroring, got %d", submitAttempts, got)
	}
}

func TestSendPromptReportsCodexQueuedInputWithoutRetryingEnter(t *testing.T) {
	calls := swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) {
		return "│ review the change │\n  tab to queue message", nil
	}
	err := SendPromptToPane("%5", "review the change")
	var queued *QueuedInputError
	if !errors.As(err, &queued) {
		t.Fatalf("want *QueuedInputError, got %T: %v", err, err)
	}
	if queued.PaneID != "%5" || !strings.Contains(err.Error(), "will submit when the agent goes idle") {
		t.Fatalf("queued error = %+v: %v", queued, err)
	}
	if got := enterCount(*calls); got != 0 {
		t.Fatalf("queued Codex input should not receive repeated Enter attempts, got %d", got)
	}
}

func TestQueuedInputVisibleIsAnchoredToCaseInsensitiveFooter(t *testing.T) {
	if !queuedInputVisible("│ staged prompt │\n  TAB TO QUEUE MESSAGE\n") {
		t.Fatal("queued footer should be detected case-insensitively")
	}
	if queuedInputVisible("? for shortcuts") {
		t.Fatal("ordinary idle footer must not be classified as queued")
	}
	if queuedInputVisible("│ please explain the phrase tab to queue message │\n  ? for shortcuts") {
		t.Fatal("prompt text containing the queue phrase must not be classified as queued")
	}
}

func TestChangedQueuedFooterDegradesToSubmitUnconfirmed(t *testing.T) {
	calls := swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) {
		return "│ review the change │\n  tab to queue messages", nil
	}
	err := SendPromptToPane("%5", "review the change")
	var queued *QueuedInputError
	if errors.As(err, &queued) {
		t.Fatalf("changed footer must not be guessed as queued: %v", err)
	}
	var unconfirmed *SubmitUnconfirmedError
	if !errors.As(err, &unconfirmed) {
		t.Fatalf("changed footer must degrade to *SubmitUnconfirmedError, got %T: %v", err, err)
	}
	if got := enterCount(*calls); got != submitAttempts {
		t.Fatalf("changed footer should take the ordinary confirmation path (%d Enters), got %d", submitAttempts, got)
	}
}

// A changed input region means submitted on the first Enter; a blank/unavailable
// capture remains explicitly unconfirmed after one Enter.
func TestSendPromptSubmitsOnInputChangeOrReportsUnavailable(t *testing.T) {
	// Region CHANGES after Enter -> submitted, single Enter.
	calls := swapDeliver(t, nil)
	n := 0
	paneCapturer = func(string) (string, error) {
		n++
		switch n {
		case 1, 2:
			return "> ready", nil // settle wait
		case 3:
			return "│ staged │\n? for shortcuts", nil // before
		default:
			return "● Working... esc to interrupt\n>", nil // after: changed
		}
	}
	if err := SendPromptToPane("%5", "go"); err != nil {
		t.Fatal(err)
	}
	if got := enterCount(*calls); got != 1 {
		t.Fatalf("a changed input region should submit in one Enter, got %d", got)
	}

	// Blank capture -> can't verify -> explicit ambiguity (single Enter).
	calls2 := swapDeliver(t, nil)
	paneCapturer = func(string) (string, error) { return "", nil }
	err := SendPromptToPane("%5", "go")
	var unconfirmed *SubmitUnconfirmedError
	if !errors.As(err, &unconfirmed) || unconfirmed.Attempts != 1 {
		t.Fatalf("blank capture must be explicit submit ambiguity, got %T: %v", err, err)
	}
	if got := enterCount(*calls2); got != 1 {
		t.Fatalf("blank capture should stop after one unconfirmed Enter, got %d", got)
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
