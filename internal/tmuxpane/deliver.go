package tmuxpane

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"
)

// Submit-tuning knobs (package vars so tests can zero the sleeps). Plain tmux
// (non iTerm2 -CC) does not buffer a bracketed paste the way -CC does, so the
// agent TUI needs a brief moment to ingest the pasted body before the Enter —
// otherwise the Enter races the paste and is dropped, leaving the prompt hanging
// staged in the input box (#86). We settle, submit, verify the prompt left the
// input box, and retry the Enter before giving up with a clear error.
var (
	submitSettleDelay = 120 * time.Millisecond
	submitVerifyDelay = 200 * time.Millisecond
	submitAttempts    = 3
	// inputBoxLines is how many bottom lines of the pane count as the input
	// region for the "did it leave the input box?" check.
	inputBoxLines = 4
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
// and shell metacharacters are preserved verbatim. For MULTI-LINE text it pastes
// with `-p` (bracketed paste) so embedded newlines do not submit the prompt
// prematurely; the trailing Enter is the single submit event. A SINGLE-LINE
// prompt is pasted WITHOUT `-p` (see pasteBufferArgs). Returns *DeadPaneError
// when the pane is gone.
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
	// Paste into the exact pane and delete the buffer.
	if out, err := deliverExec("", pasteBufferArgs(buf, paneID, prompt)...); err != nil {
		return fmt.Errorf("tmux paste-buffer -t %s: %w: %s", paneID, err, strings.TrimSpace(out))
	}
	// Submit robustly — the Enter must not race the paste (the #86 hang).
	return submitStagedPrompt(paneID)
}

// pasteBufferArgs builds the `tmux paste-buffer` argv for a staged prompt. It
// requests bracketed paste (`-p`) ONLY when the prompt spans multiple lines: a
// multi-line body needs its embedded newlines buffered as paste content so they
// do not submit early, leaving the trailing Enter as the sole submit. A
// single-line prompt has no line break to protect, and the bracketed-paste START
// marker (ESC[200~) can leak as literal "[200~" text into an agent TUI that has
// not yet enabled bracketed-paste mode — the stuck-input hang observed when
// nudging a freshly-spawned Codex pane. Omitting `-p` for single-line prompts
// removes that failure mode entirely; the Enter-retry in submitStagedPrompt
// still covers a plain paste/Enter race. We test for CR as well as LF so a bare
// "\r" (which a terminal may treat like Enter) keeps the bracketed protection.
func pasteBufferArgs(buf, paneID, prompt string) []string {
	if strings.ContainsAny(prompt, "\r\n") {
		return []string{"paste-buffer", "-d", "-p", "-b", buf, "-t", paneID}
	}
	return []string{"paste-buffer", "-d", "-b", buf, "-t", paneID}
}

// submitStagedPrompt presses Enter to submit a just-pasted prompt and confirms
// it actually submitted, retrying the Enter if not. It exists because a bare
// paste-then-Enter often hangs in plain tmux: the Enter arrives before the agent
// TUI has ingested the bracketed paste and is dropped, so the prompt sits staged
// until a manual Enter.
//
// Each attempt snapshots the input region (the bottom of the pane), presses
// Enter, then re-snapshots: a successful submit CHANGES that region (the staged
// prompt leaves the input box, replaced by an empty prompt / the agent's
// response), while a dropped Enter leaves it byte-for-byte identical, which
// triggers a retry. Comparing the region (rather than searching for the prompt
// text) is robust to line wrapping and input-box borders, and engine-agnostic.
// If submission can never be confirmed it returns a clear error rather than
// silently leaving the text staged. Best-effort: if the region cannot be
// captured it fails open (one Enter, no retry) so a capture problem never blocks
// delivery or spins.
func submitStagedPrompt(paneID string) error {
	for attempt := 0; attempt < submitAttempts; attempt++ {
		time.Sleep(submitSettleDelay)
		before, beforeOK := captureInputRegion(paneID)
		if err := SendKeysToPane(paneID, "Enter"); err != nil {
			return err
		}
		time.Sleep(submitVerifyDelay)
		after, afterOK := captureInputRegion(paneID)
		// Submitted when the input region changed; fail open when either snapshot
		// is unavailable (don't block or retry on a capture we can't trust).
		if !beforeOK || !afterOK || after != before {
			return nil
		}
		// Unchanged: the Enter was dropped — retry.
	}
	return fmt.Errorf("delivered the prompt to pane %s but could not confirm it submitted after %d Enter attempts; the agent may still need a manual Enter", paneID, submitAttempts)
}

// captureInputRegion snapshots the bottom inputBoxLines of the pane (the input
// region) for the before/after submit comparison. ok is false when the pane
// cannot be captured or the region is blank (nothing to compare), so the caller
// fails open instead of treating an empty capture as "unchanged".
func captureInputRegion(paneID string) (region string, ok bool) {
	out, err := paneCapturer(paneID)
	if err != nil {
		return "", false
	}
	region = tailLines(out, inputBoxLines)
	if strings.TrimSpace(region) == "" {
		return "", false
	}
	return region, true
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
