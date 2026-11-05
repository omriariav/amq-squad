package seatexec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// AMQ's Codex dialog hook (3.0.1). A Codex seat's AMQ wake types the
// doorbell into the pane; a doorbell at a Codex approval prompt cancels the
// pending command and runs as a new prompt (AMQ docs/wake-operations.md:
// 458-473 at v0.116.2). AMQ's dialog hook entries in <codex home>/hooks.json
// hold the doorbell while a prompt is open. One read-only detection serves
// the launch's wake gate (seatlaunch) and doctor's row (cli).

// CodexDialogHookEvents are the events AMQ's Codex dialog hook wires
// (internal/cli/wake_dialog_codex_unix.go:37-44 at AMQ v0.116.2).
var CodexDialogHookEvents = []string{"Stop", "UserPromptSubmit", "Interrupt", "SessionStart", "SessionEnd", "PermissionRequest"}

// CodexActivityHookEvents are the installed 0.166.0 catalogue events that
// prove work. PostToolUse is the in-turn producer and Stop closes the turn.
// PermissionRequest and UserPromptSubmit intentionally do not appear.
var CodexActivityHookEvents = []string{"PostToolUse", "Stop"}

// CodexDialogHookMarker prefixes every AMQ dialog hook command
// (internal/cli/wake_dialog_unix.go:32 at AMQ v0.116.2).
const CodexDialogHookMarker = "AMQ_DIALOG_HOOK=1 "

// CodexDialogHookFix is the owner's step that installs the hook.
const CodexDialogHookFix = "amq wake install-dialog-hook --harness codex, then open /hooks in Codex and trust the AMQ_DIALOG_HOOK entries"

// CodexActivityHookArgs overlays per-seat activity handlers without writing
// the owner's CODEX_HOME. The hooks send Codex's original stdin payload to
// the same evidence/activity producer Claude and pi use. Stop also preserves
// AMQ's dialog-close handler because a per-event overlay replaces that event.
func CodexActivityHookArgs(squadBin, amqBin string) []string {
	activity := shellWord(squadBin) + ` evidence ingest --me "$AM_ME"`
	handler := func(command string, timeout int) string {
		return fmt.Sprintf(`{type="command",command=%s,timeout=%d}`, strconv.Quote(command), timeout)
	}
	post := fmt.Sprintf(`hooks.PostToolUse=[{hooks=[%s]}]`, handler(activity, 5))
	stopHandlers := handler(activity, 5)
	if strings.TrimSpace(amqBin) != "" {
		closeDialog := shellWord(amqBin) + ` --no-update-check wake dialog --harness codex close || true`
		stopHandlers += "," + handler(closeDialog, 5)
	}
	stop := fmt.Sprintf(`hooks.Stop=[{hooks=[%s]}]`, stopHandlers)
	return []string{"-c", post, "-c", stop}
}

func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// CodexDir is the Codex home a seat reads: $CODEX_HOME, else ~/.codex
// (seat-exec overrides neither).
func CodexDir(home, codexHome string) string {
	if strings.TrimSpace(codexHome) != "" {
		return codexHome
	}
	return filepath.Join(home, ".codex")
}

// CodexDialogHook is what the detection read: config.toml keys that turn
// user hooks off, events with no AMQ entry, or a hooks file it could not
// read. It holds key and event names only, never a command or a value.
type CodexDialogHook struct {
	ConfigPath string
	HooksPath  string
	HooksOff   []string // "features.hooks", "features.codex_hooks" when false
	Missing    []string // events with no AMQ_DIALOG_HOOK entry
	Unreadable string   // why hooks.json could not be read ("" when it could, or is absent)
}

// Ready reports that every AMQ entry is present and user hooks are on.
// Whether Codex trusts the entries is Codex's own hash ('amq doctor --ops').
func (h CodexDialogHook) Ready() bool {
	return len(h.HooksOff) == 0 && len(h.Missing) == 0 && h.Unreadable == ""
}

// ReadCodexDialogHook reads codexDir's config.toml ([features] hooks and
// codex_hooks) and hooks.json (AMQ's marked entries per event). Read only.
func ReadCodexDialogHook(codexDir string) CodexDialogHook {
	h := CodexDialogHook{ConfigPath: filepath.Join(codexDir, "config.toml"), HooksPath: filepath.Join(codexDir, "hooks.json")}
	var cfg struct {
		Features map[string]any `toml:"features"`
	}
	if raw, err := os.ReadFile(h.ConfigPath); err == nil {
		_, _ = toml.Decode(string(raw), &cfg)
	}
	for _, key := range []string{"hooks", "codex_hooks"} {
		if v, ok := cfg.Features[key].(bool); ok && !v {
			h.HooksOff = append(h.HooksOff, "features."+key)
		}
	}
	present := map[string]bool{}
	raw, err := os.ReadFile(h.HooksPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		h.Unreadable = "cannot read it"
		if u := errors.Unwrap(err); u != nil {
			h.Unreadable += " (" + u.Error() + ")"
		}
	default:
		var file struct {
			Hooks map[string][]struct {
				Hooks []struct {
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"hooks"`
		}
		if json.Unmarshal(raw, &file) != nil {
			h.Unreadable = "not a Codex hooks file amq-squad can read"
			break
		}
		for event, groups := range file.Hooks {
			for _, g := range groups {
				for _, e := range g.Hooks {
					if strings.HasPrefix(e.Command, CodexDialogHookMarker) {
						present[event] = true
					}
				}
			}
		}
	}
	for _, event := range CodexDialogHookEvents {
		if !present[event] {
			h.Missing = append(h.Missing, event)
		}
	}
	return h
}
