package seatexec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
// 596-597 at v0.140.0). AMQ's dialog hook entries in <codex home>/hooks.json
// hold the doorbell while a prompt is open. One read-only detection serves
// the launch's wake gate (seatlaunch) and doctor's row (cli).

// CodexDialogHookEvents are the events AMQ's Codex dialog hook wires
// (internal/cli/wake_dialog_codex_unix.go:38-45 at AMQ v0.140.0).
var CodexDialogHookEvents = []string{"Stop", "UserPromptSubmit", "Interrupt", "SessionStart", "SessionEnd", "PermissionRequest"}

// CodexActivityHookEvents are the installed 0.166.0 catalogue events that
// prove work. PostToolUse is the in-turn producer and Stop closes the turn.
// PermissionRequest and UserPromptSubmit intentionally do not appear.
var CodexActivityHookEvents = []string{"PostToolUse", "Stop"}

// CodexDialogHookMarker prefixes every AMQ dialog hook command
// (internal/dialogguard/dialogguard.go:14 at AMQ v0.140.0).
const CodexDialogHookMarker = "AMQ_DIALOG_HOOK=1 "

// CodexDialogHookFix is the owner's step that installs the hook.
const CodexDialogHookFix = "amq wake install-dialog-hook --harness codex, then open /hooks in Codex and trust the AMQ_DIALOG_HOOK entries"

const codexSessionHookSource = "/<session-flags>/config.toml"

// CodexActivityHookArgs overlays per-seat activity handlers without writing
// the owner's CODEX_HOME. The hooks send Codex's original stdin payload to
// the same evidence/activity producer Claude and pi use. Stop also preserves
// AMQ's dialog-close handler because a per-event overlay replaces that event.
func CodexActivityHookArgs(squadBin, amqBin string) []string {
	home, _ := os.UserHomeDir()
	return CodexActivityHookArgsForHome(squadBin, amqBin, home)
}

func CodexActivityHookArgsForHome(squadBin, amqBin, home string) []string {
	activity := shellWord(squadBin) + ` evidence ingest --me "$AM_ME"`
	handler := func(command string, timeout int) string {
		return fmt.Sprintf(`{type="command",command=%s,timeout=%d}`, strconv.Quote(command), timeout)
	}
	post := fmt.Sprintf(`hooks.PostToolUse=[{hooks=[%s]}]`, handler(activity, 5))
	stopHandlers := handler(activity, 5)
	if strings.TrimSpace(amqBin) != "" {
		closeDialog := codexDialogCloseCommand(amqBin, home)
		stopHandlers += "," + handler(closeDialog, 5)
	}
	stop := fmt.Sprintf(`hooks.Stop=[{hooks=[%s]}]`, stopHandlers)
	return []string{"-c", post, "-c", stop}
}

func shellWord(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// CodexActivityTrust reports whether Codex 0.166 will execute every handler
// in the -c activity overlay. Hashes include the exact command, including the
// installed binary path.
type CodexActivityTrust struct {
	ConfigPath string
	Missing    []string
	Unreadable string
}

func (t CodexActivityTrust) Ready() bool { return t.Unreadable == "" && len(t.Missing) == 0 }

type codexActivityEntry struct {
	key     string
	event   string
	command string
	timeout int
}

func codexActivityEntries(squadBin, amqBin, home string) []codexActivityEntry {
	activity := shellWord(squadBin) + ` evidence ingest --me "$AM_ME"`
	entries := []codexActivityEntry{
		{codexSessionHookSource + ":post_tool_use:0:0", "post_tool_use", activity, 5},
		{codexSessionHookSource + ":stop:0:0", "stop", activity, 5},
	}
	if strings.TrimSpace(amqBin) != "" {
		entries = append(entries, codexActivityEntry{codexSessionHookSource + ":stop:0:1", "stop", codexDialogCloseCommand(amqBin, home), 5})
	}
	return entries
}

// CodexActivityTrustEntryCount is the number of exact declarations Codex must
// trust for this overlay. An empty AMQ path omits the dialog-close declaration.
func CodexActivityTrustEntryCount(squadBin, amqBin, home string) int {
	return len(codexActivityEntries(squadBin, amqBin, home))
}

func codexActivityTrustFix(entries int) string {
	count := strconv.Itoa(entries)
	switch entries {
	case 2:
		count = "two"
	case 3:
		count = "three"
	}
	return fmt.Sprintf("launch one Codex seat, run /hooks, and trust the %s /<session-flags>/config.toml amq-squad activity entries", count)
}

// CodexActivityTrustWarning is the owner-facing admission warning. It names
// every declaration key and exact command string whose hash Codex trusts. A
// binary path is part of that string, so moving either installation invalidates
// the corresponding approval.
func CodexActivityTrustWarning(squadBin, amqBin, home string) string {
	entries := codexActivityEntries(squadBin, amqBin, home)
	exact := make([]string, 0, len(entries))
	for _, entry := range entries {
		exact = append(exact, entry.key+" = "+entry.command)
	}
	return "codex seats will not renew on their own until you trust the amq-squad hooks: " + codexActivityTrustFix(len(entries)) +
		". The trust covers these exact command strings: " + strings.Join(exact, "; ") +
		". Reinstalling amq-squad or amq at another path changes the command string and drops that trust."
}

// CodexActivityHookHash reproduces Codex 0.166's version_for_toml identity:
// TOML converted to JSON, recursively key-sorted, then SHA-256. encoding/json
// sorts map keys, and these values contain no nested unordered maps. Codex's
// serde_json writes &, <, >, U+2028 and U+2029 literally, so HTML escaping
// must be disabled and encoding/json's two JavaScript-safe escapes restored to
// UTF-8. Encoder.Encode's trailing newline is not part of the fingerprint.
func CodexActivityHookHash(event, command string, timeout int) string {
	identity := map[string]any{
		"event_name": event,
		"hooks": []any{map[string]any{
			"async": false, "command": command, "timeout": timeout, "type": "command",
		}},
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(identity)
	raw := bytes.TrimSuffix(buf.Bytes(), []byte{'\n'})
	raw = rawJSONLineSeparators(raw)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// rawJSONLineSeparators matches serde_json without corrupting literal text
// such as "\\u2028". A JSON Unicode escape is active only when the run of
// backslashes immediately before its own backslash is even.
func rawJSONLineSeparators(raw []byte) []byte {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); {
		if i+6 <= len(raw) && raw[i] == '\\' && raw[i+1] == 'u' &&
			(string(raw[i+2:i+6]) == "2028" || string(raw[i+2:i+6]) == "2029") {
			preceding := 0
			for j := i - 1; j >= 0 && raw[j] == '\\'; j-- {
				preceding++
			}
			if preceding%2 == 0 {
				if raw[i+5] == '8' {
					out = append(out, []byte("\u2028")...)
				} else {
					out = append(out, []byte("\u2029")...)
				}
				i += 6
				continue
			}
		}
		out = append(out, raw[i])
		i++
	}
	return out
}

// ReadCodexActivityTrust reads the owner's persisted [hooks.state] only. It
// never writes CODEX_HOME and never weakens hook trust.
func ReadCodexActivityTrust(codexDir, squadBin, amqBin, home string) CodexActivityTrust {
	t := CodexActivityTrust{ConfigPath: filepath.Join(codexDir, "config.toml")}
	var cfg struct {
		Hooks struct {
			State map[string]struct {
				Enabled     *bool  `toml:"enabled"`
				TrustedHash string `toml:"trusted_hash"`
			} `toml:"state"`
		} `toml:"hooks"`
	}
	raw, err := os.ReadFile(t.ConfigPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		t.Unreadable = err.Error()
		return t
	default:
		if _, err := toml.Decode(string(raw), &cfg); err != nil {
			t.Unreadable = err.Error()
			return t
		}
	}
	for _, entry := range codexActivityEntries(squadBin, amqBin, home) {
		state, ok := cfg.Hooks.State[entry.key]
		disabled := ok && state.Enabled != nil && !*state.Enabled
		if !ok || disabled || state.TrustedHash != CodexActivityHookHash(entry.event, entry.command, entry.timeout) {
			t.Missing = append(t.Missing, entry.key)
		}
	}
	return t
}

const codexDialogNoMarkerTest = `{ case "${AM_ROOT-}" in */./*|*/../*|*/.|*/..) false ;; /*[[:alnum:]._/-]) ;; *) false ;; esac && ` +
	`case "$AM_ME" in *[![:alnum:]_-]*) false ;; esac && ` +
	`[ -d "$AM_ROOT/agents/$AM_ME" ] && ` +
	`[ ! -e "$AM_ROOT/agents/$AM_ME/.wake.dialog" ] && ` +
	`[ ! -L "$AM_ROOT/agents/$AM_ME/.wake.dialog" ]; }`

// codexDialogCloseCommand mirrors AMQ v0.140.0's codexDialogHookCommand
// with the verb "close" (internal/cli/wake_dialog_codex_unix.go:54-62).
func codexDialogCloseCommand(amqBin, home string) string {
	store := filepath.Join(home, ".amq", "codex-threads", "current")
	gate := `[ -n "${AM_ME-}" ] && ` + codexDialogNoMarkerTest + ` && exit 0; `
	gate += `[ -n "${AM_ME-}" ] || [ -d ` + shellWord(store) + ` ] || exit 0; `
	return CodexDialogHookMarker + gate + shellWord(amqBin) + ` --no-update-check wake dialog --harness codex close || true`
}

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
