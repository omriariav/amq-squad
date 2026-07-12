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
	// pasteSettleInterval is the gap between readiness captures before a
	// bracketed (multi-line) paste; pasteSettleMax caps how many we take before
	// pasting anyway. Vars so tests can zero the wait.
	pasteSettleInterval = 80 * time.Millisecond
	pasteSettleMax      = 8
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

// BracketedPasteLeakError reports that a multi-line prompt's bracketed-paste
// markers (ESC[200~/ESC[201~) were echoed into the pane as literal text instead
// of being consumed — the signature of pasting into an agent TUI that had not
// yet enabled bracketed-paste mode (DECSET 2004), typically a pane still
// booting. The prompt did NOT land cleanly, so the caller should retry once the
// pane has settled. For a dispatch the durable AMQ message is unaffected, so
// this is a wake miss, not a lost task.
type BracketedPasteLeakError struct{ PaneID string }

func (e *BracketedPasteLeakError) Error() string {
	return fmt.Sprintf("tmux pane %s did not ingest the bracketed paste (markers leaked as text); the agent is likely still starting up — prompt not delivered, retry shortly", e.PaneID)
}

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
// and shell metacharacters are preserved verbatim. It first waits, bounded, for
// the target pane to stop drawing so single-line prompts do not race a cold TUI's
// input readiness and multi-line bracketed paste is not echoed as literal marker
// text. For MULTI-LINE text it pastes with `-p` (bracketed paste) so embedded
// newlines do not submit the prompt prematurely; the trailing Enter is the
// single submit event. A SINGLE-LINE prompt is pasted WITHOUT `-p` (see
// pasteBufferArgs). Returns *DeadPaneError when the pane is gone.
func SendPromptToPane(paneID, prompt string) error {
	if strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("send: empty pane id")
	}
	if !PaneExists(paneID) {
		return &DeadPaneError{PaneID: paneID, Err: fmt.Errorf("display-message returned no pane")}
	}
	// Freshly-spawned agent TUIs can accept pane text before their input layer is
	// ready to submit it. Keep this one bounded wait at the delivery boundary so
	// all pane prompt paths (dispatch fallback, manual send, /rename, /goal) share
	// the same cold-pane protection.
	waitPaneSettled(paneID)
	bracketed := promptNeedsBracketedPaste(prompt)
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
	// Backstop: if the bracketed-paste markers still leaked as literal text (the
	// pane was not ready after the settle wait), the prompt did NOT land cleanly.
	// Report a clear, retryable failure rather than pressing Enter on a mangled
	// input — a dispatch keeps the durable message queued and surfaces the miss.
	if bracketed {
		time.Sleep(submitSettleDelay) // let the paste render before inspecting
		if bracketedPasteLeaked(paneID) {
			return &BracketedPasteLeakError{PaneID: paneID}
		}
	}
	// Submit robustly — the Enter must not race the paste (the #86 hang).
	return submitStagedPrompt(paneID)
}

// promptNeedsBracketedPaste reports whether a prompt must be delivered with a
// bracketed paste: it contains a line break (CR or LF) whose Enter must be
// buffered as paste content rather than submitting. A single-line prompt has no
// line break to protect, so it is pasted plainly (no ESC[200~ wrappers that
// could leak into a not-yet-ready TUI — the freshly-spawned Codex `[200~` hang).
func promptNeedsBracketedPaste(prompt string) bool {
	return strings.ContainsAny(prompt, "\r\n")
}

// pasteBufferArgs builds the `tmux paste-buffer` argv for a staged prompt,
// requesting bracketed paste (`-p`) only for multi-line prompts (see
// promptNeedsBracketedPaste). The text is staged via `load-buffer -` stdin, so
// dropping `-p` never affects quoting/metacharacters — `-p` is purely about
// keeping embedded newlines from submitting early.
func pasteBufferArgs(buf, paneID, prompt string) []string {
	if promptNeedsBracketedPaste(prompt) {
		return []string{"paste-buffer", "-d", "-p", "-b", buf, "-t", paneID}
	}
	return []string{"paste-buffer", "-d", "-b", buf, "-t", paneID}
}

// waitPaneSettled blocks (bounded) until the pane's visible content stops
// changing across a capture interval — the signal that a freshly-spawned TUI has
// finished drawing and (with it) enabled bracketed-paste mode. Best-effort: it
// returns only when the pane cannot be captured (a capture error), and proceeds
// anyway after pasteSettleMax captures so a pane that never fully quiesces (e.g.
// a footer clock) never blocks delivery. A BLANK capture is treated as an
// observed state, not a reason to stop: a just-spawned pane is often momentarily
// blank before it draws — the riskiest window for bracketed-paste mode not yet
// being on — so we keep polling through blank -> drawn -> steady. Uses the same
// capture seam as PaneBusy.
func waitPaneSettled(paneID string) {
	prev, err := paneCapturer(paneID)
	if err != nil {
		return
	}
	for i := 0; i < pasteSettleMax; i++ {
		time.Sleep(pasteSettleInterval)
		cur, err := paneCapturer(paneID)
		if err != nil {
			return
		}
		if cur == prev {
			return // output unchanged over the interval -> settled
		}
		prev = cur
	}
}

// bracketedPasteLeaked reports whether a bracketed-paste control marker is
// visible as literal text in the pane — the signature of a paste that landed
// before the TUI enabled bracketed-paste mode, so tmux's ESC[200~/ESC[201~
// wrappers were echoed rather than consumed. Best-effort: a capture failure
// reports false (never block delivery on a capture we cannot trust).
func bracketedPasteLeaked(paneID string) bool {
	out, err := paneCapturer(paneID)
	if err != nil {
		return false
	}
	return strings.Contains(out, "[200~") || strings.Contains(out, "[201~")
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
		if beforeOK && queuedInputVisible(before) {
			return &QueuedInputError{PaneID: paneID}
		}
		if err := SendKeysToPane(paneID, "Enter"); err != nil {
			return err
		}
		time.Sleep(submitVerifyDelay)
		after, afterOK := captureInputRegion(paneID)
		if afterOK && queuedInputVisible(after) {
			return &QueuedInputError{PaneID: paneID}
		}
		// Submitted when the input region changed; fail open when either snapshot
		// is unavailable (don't block or retry on a capture we can't trust).
		if !beforeOK || !afterOK || after != before {
			return nil
		}
		// Unchanged: the Enter was dropped — retry.
	}
	return &SubmitUnconfirmedError{PaneID: paneID, Attempts: submitAttempts}
}

// QueuedInputError reports the explicit Codex busy-input state. When Codex is
// mid-turn it keeps newly pasted text in the input area and renders the
// "tab to queue message" footer. Repeated Enter attempts cannot prove a submit
// in that state, but the staged text is not lost: Codex will process it when the
// current turn goes idle. Callers with a durable fallback should report this as
// queued rather than collapsing it into an ambiguous SubmitUnconfirmedError.
type QueuedInputError struct{ PaneID string }

func (e *QueuedInputError) Error() string {
	return fmt.Sprintf("delivered the prompt to pane %s; queued in the pane input and it will submit when the agent goes idle", e.PaneID)
}

func queuedInputVisible(region string) bool {
	// Codex renders the queue hint as footer chrome on the final non-blank line
	// of the input box. Do not search the whole captured region: the staged
	// prompt itself is user-controlled and may legitimately contain these words.
	// Matching the complete footer line also makes an unknown/new footer degrade
	// to the conservative SubmitUnconfirmedError path instead of guessing that a
	// queued state is present.
	lines := strings.Split(strings.ReplaceAll(region, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		footer := strings.TrimSpace(lines[i])
		if footer == "" {
			continue
		}
		return strings.EqualFold(footer, "tab to queue message")
	}
	return false
}

// SubmitUnconfirmedError reports that the prompt was pasted and Enter pressed,
// but the input region never changed, so submission could not be CONFIRMED. It
// is a soft, ambiguous signal, not a hard failure: the Enter may simply have
// been dropped (the agent still needs a manual Enter), OR the agent had already
// moved on — e.g. an amq wake sidecar drained the durable message first and the
// agent is now working, so its input box looks unchanged. Callers that have a
// durable fallback (dispatch) should treat it accordingly rather than as a lost
// delivery; a bare `amq-squad send` surfaces it so the operator can retry.
type SubmitUnconfirmedError struct {
	PaneID   string
	Attempts int
}

func (e *SubmitUnconfirmedError) Error() string {
	return fmt.Sprintf("delivered the prompt to pane %s but could not confirm it submitted after %d Enter attempts; the agent may still need a manual Enter", e.PaneID, e.Attempts)
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
