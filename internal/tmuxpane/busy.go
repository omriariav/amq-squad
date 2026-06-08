package tmuxpane

import (
	"os/exec"
	"regexp"
)

// busy.go implements the "don't talk over a working agent" guard: before a
// prompt is delivered into a pane, the caller can check whether the agent there
// is mid-turn. Pushing a prompt into a busy agent lands it in a tool-result
// buffer where the agent may never see it, so `send` refuses by default and
// offers --force. The capture is behind a seam so it is unit-testable.

// paneCapturer returns recent on-screen content for a pane. Production shells
// `tmux capture-pane`; tests inject a fake. A non-nil error means the capture
// could not run (e.g. pane gone) — callers treat that as "unknown", not busy.
var paneCapturer = defaultPaneCapturer

func defaultPaneCapturer(paneID string) (string, error) {
	// -p prints to stdout; -S -40 grabs the last ~40 lines (the live UI, where
	// an engine's "generating" indicator sits), avoiding the full scrollback.
	out, err := exec.Command("tmux", "capture-pane", "-p", "-t", paneID, "-S", "-40").Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// busyMarkerRE matches the on-screen indicators an agent shows while it is
// generating or running a tool — the moment a pushed prompt would be lost. The
// markers are intentionally conservative (strong, engine-shown phrases) to avoid
// false positives that would block a legitimate send into an idle agent:
//   - Claude Code / Codex show "esc to interrupt" (and a "· N tokens" meter)
//     only while a turn is in flight.
//   - "esc to cancel" / "Running…" cover tool-run and prompt states.
var busyMarkerRE = regexp.MustCompile(`(?i)esc to interrupt|esc to cancel|· \d+[.,]?\d*[km]? tokens|Running…|Running\.\.\.`)

// PaneBusy reports whether the agent on paneID appears to be mid-turn. It is
// best-effort: a capture error returns (false, err) so the caller can choose to
// proceed (a capture failure must never block delivery on its own); an empty or
// marker-free capture is treated as idle.
func PaneBusy(paneID string) (bool, error) {
	out, err := paneCapturer(paneID)
	if err != nil {
		return false, err
	}
	return busyMarkerRE.MatchString(out), nil
}
