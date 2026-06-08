package tmuxpane

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
)

// DeadPaneError reports that a tmux pane targeted for control no longer exists
// (or is not writable). Callers surface it as a clear "pane is gone" message
// instead of a generic tmux failure.
type DeadPaneError struct {
	PaneID string
	Err    error
}

func (e *DeadPaneError) Error() string {
	return fmt.Sprintf("tmux pane %s is not available (it may have been closed): %v", e.PaneID, e.Err)
}

func (e *DeadPaneError) Unwrap() error { return e.Err }

// deliverExec is the seam for tmux subprocesses used by prompt delivery. Unlike
// execRunner it accepts optional stdin (so prompt text reaches tmux via
// `load-buffer -` rather than argv) and returns combined output for diagnostics.
// Production runs the real tmux binary; tests inject a recorder.
var deliverExec = func(stdin string, args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// promptBufferSeq makes each staged prompt buffer name unique within a process.
var promptBufferSeq atomic.Uint64

// nextPromptBuffer returns a tmux paste-buffer name unique to this send. A fixed
// name would let concurrent sends interleave (A load, B load, A paste -> A's
// pane receives B's prompt) and could clobber a user buffer of the same name.
// pid scopes it across processes; the counter scopes it within one.
func nextPromptBuffer() string {
	return fmt.Sprintf("amq-squad-prompt-%d-%d", os.Getpid(), promptBufferSeq.Add(1))
}

// PaneExists reports whether a pane id is still present and addressable. It is
// the cheap liveness probe used before delivery.
func PaneExists(paneID string) bool {
	if strings.TrimSpace(paneID) == "" {
		return false
	}
	_, err := deliverExec("", "display-message", "-p", "-t", paneID, "#{pane_id}")
	return err == nil
}

// SendKeysToPane sends literal tmux key names (e.g. "Enter", "C-c") to the exact
// pane id. It is for control keys only — prompt TEXT must go through
// SendPromptToPane so it is never reinterpreted as key names.
func SendKeysToPane(paneID string, keys ...string) error {
	if strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("send-keys: empty pane id")
	}
	if len(keys) == 0 {
		return nil
	}
	args := append([]string{"send-keys", "-t", paneID}, keys...)
	if out, err := deliverExec("", args...); err != nil {
		return fmt.Errorf("tmux send-keys -t %s: %w: %s", paneID, err, strings.TrimSpace(out))
	}
	return nil
}

// SendPromptToPane delivers prompt to the exact tmux pane and submits it with an
// explicit Enter. The prompt is staged into a tmux paste buffer via stdin
// (`load-buffer -`), never a shell string or argv, so multi-line text, quotes,
// and shell metacharacters are preserved verbatim. paste-buffer -p requests
// bracketed paste when the target app supports it, so embedded newlines do not
// submit the prompt prematurely; the trailing Enter is the single submit event.
// Returns *DeadPaneError when the pane is gone.
func SendPromptToPane(paneID, prompt string) error {
	if strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("send: empty pane id")
	}
	if !PaneExists(paneID) {
		return &DeadPaneError{PaneID: paneID, Err: fmt.Errorf("display-message returned no pane")}
	}
	// Stage the prompt in a unique buffer via stdin — the text never appears on
	// a command line, so no quoting/metacharacter handling is required, and a
	// per-send name keeps concurrent sends from clobbering each other.
	buf := nextPromptBuffer()
	if out, err := deliverExec(prompt, "load-buffer", "-b", buf, "-"); err != nil {
		return fmt.Errorf("tmux load-buffer: %w: %s", err, strings.TrimSpace(out))
	}
	// Paste into the exact pane (bracketed when supported) and delete the buffer.
	if out, err := deliverExec("", "paste-buffer", "-d", "-p", "-b", buf, "-t", paneID); err != nil {
		return fmt.Errorf("tmux paste-buffer -t %s: %w: %s", paneID, err, strings.TrimSpace(out))
	}
	// Submit with one explicit Enter key event.
	return SendKeysToPane(paneID, "Enter")
}

// FindPaneByID returns the live pane carrying paneID. Callers validate further
// identity (e.g. cwd) before trusting it, since tmux pane ids can be reused
// after a server restart.
func FindPaneByID(paneID string, panes []TmuxPane) (TmuxPane, bool) {
	if strings.TrimSpace(paneID) == "" {
		return TmuxPane{}, false
	}
	for _, p := range panes {
		if p.PaneID == paneID {
			return p, true
		}
	}
	return TmuxPane{}, false
}

// TargetFromPane builds a focus TmuxTarget from a pane (carrying title and
// window-name for the cross-session focus path).
func TargetFromPane(p TmuxPane) TmuxTarget { return targetFromPane(p) }

// TargetForPaneID finds the live pane carrying paneID and returns a TmuxTarget
// addressing it. Returns false when no live pane has that id.
func TargetForPaneID(paneID string, panes []TmuxPane) (TmuxTarget, bool) {
	if p, ok := FindPaneByID(paneID, panes); ok {
		return targetFromPane(p), true
	}
	return TmuxTarget{}, false
}
