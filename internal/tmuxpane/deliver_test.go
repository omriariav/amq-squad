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
	t.Cleanup(func() { deliverExec = prev })
	return &calls
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
	// 2) load-buffer carries the prompt via STDIN, never argv
	lb := (*calls)[1]
	if !reflect.DeepEqual(lb.args, []string{"load-buffer", "-b", promptBufferName, "-"}) {
		t.Fatalf("load-buffer argv = %v", lb.args)
	}
	if lb.stdin != prompt {
		t.Fatalf("prompt not delivered verbatim via stdin:\n got %q\nwant %q", lb.stdin, prompt)
	}
	for _, a := range lb.args {
		if strings.Contains(a, "line one") || strings.Contains(a, "rm -rf") {
			t.Fatalf("prompt text leaked into argv: %v", lb.args)
		}
	}
	// 3) paste-buffer deletes the buffer, requests bracketed paste, targets pane
	if got := (*calls)[2].args; !reflect.DeepEqual(got, []string{"paste-buffer", "-d", "-p", "-b", promptBufferName, "-t", "%265"}) {
		t.Fatalf("paste-buffer argv = %v", got)
	}
	// 4) explicit Enter submit
	if got := (*calls)[3].args; !reflect.DeepEqual(got, []string{"send-keys", "-t", "%265", "Enter"}) {
		t.Fatalf("send-keys argv = %v", got)
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
