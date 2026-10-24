package seatexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Claude seats: the hook and the permission mode are one unit (plan 4.3,
// R36, ruling 2026-10-24). In a seat the permission boundary is guard, through
// the user-scope amq-squad plugin's PreToolUse hook; the launch sets the
// permission mode explicitly and records it. seat-exec refuses a Claude seat
// whose plugin is absent, not enabled, or not at user scope in the seat's
// HOME (claude_hooks_missing), and one whose argv does not carry exactly the
// recorded permission mode (claude_permission_mismatch). A Claude seat never
// runs with the hook absent.

// Reason codes of the Claude preflight.
const (
	ClaudeHooksMissing       = "claude_hooks_missing"
	ClaudePermissionMismatch = "claude_permission_mismatch"
)

// PluginName is the amq-squad plugin's name in Claude Code's plugin keys
// ("amq-squad@<marketplace>").
const PluginName = "amq-squad"

// claudeConfigDir is CLAUDE_CONFIG_DIR, else <home>/.claude.
func claudeConfigDir(home string, env []string) string {
	if d := Getenv(env, "CLAUDE_CONFIG_DIR"); d != "" {
		return d
	}
	return filepath.Join(home, ".claude")
}

// CheckClaudeHooks verifies that the amq-squad plugin is installed at user
// scope, enabled in the user settings, and that its installed hooks.json
// wires the amq-squad hook script into PreToolUse.
func CheckClaudeHooks(home string, env []string) error {
	dir := claudeConfigDir(home, env)
	missing := func(format string, args ...any) error {
		return refusef(ClaudeHooksMissing, "the amq-squad Claude Code plugin is not installed and enabled at user scope in %s: "+format+" (install: claude plugin install amq-squad@<marketplace> --scope user)", append([]any{dir}, args...)...)
	}
	var settings struct {
		EnabledPlugins map[string]json.RawMessage `json:"enabledPlugins"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || json.Unmarshal(raw, &settings) != nil {
		return missing("settings.json is missing or unreadable")
	}
	var installed struct {
		Plugins map[string]json.RawMessage `json:"plugins"`
	}
	raw, err = os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(raw, &installed) != nil {
		return missing("plugins/installed_plugins.json is missing or unreadable")
	}
	for key, enabled := range settings.EnabledPlugins {
		if !strings.HasPrefix(key, PluginName+"@") || strings.TrimSpace(string(enabled)) != "true" {
			continue
		}
		for _, entry := range pluginEntries(installed.Plugins[key]) {
			if entry.Scope == "user" && hooksWired(entry.InstallPath) {
				return nil
			}
		}
	}
	return missing("no enabled %s@<marketplace> entry at user scope whose hooks/hooks.json runs amq-squad-hook.sh on PreToolUse", PluginName)
}

type pluginEntry struct {
	Scope       string `json:"scope"`
	InstallPath string `json:"installPath"`
}

// pluginEntries reads one installed_plugins.json value: a list of
// installations (version 2) or a single one.
func pluginEntries(raw json.RawMessage) []pluginEntry {
	var list []pluginEntry
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one pluginEntry
	if json.Unmarshal(raw, &one) == nil {
		return []pluginEntry{one}
	}
	return nil
}

// hooksWired reports whether an installed plugin directory's hooks.json runs
// the amq-squad hook script on PreToolUse and the script is there.
func hooksWired(installPath string) bool {
	if !filepath.IsAbs(installPath) {
		return false
	}
	if st, err := os.Stat(filepath.Join(installPath, "hooks", "amq-squad-hook.sh")); err != nil || !st.Mode().IsRegular() {
		return false
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(filepath.Join(installPath, "hooks", "hooks.json"))
	if err != nil || json.Unmarshal(raw, &hooks) != nil {
		return false
	}
	for _, group := range hooks.Hooks["PreToolUse"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "amq-squad-hook.sh") && strings.Contains(h.Command, "pre-tool") {
				return true
			}
		}
	}
	return false
}

// CheckClaudePermission: the argv carries exactly the permission mode the
// launch recorded, once, and no permission bypass.
func CheckClaudePermission(recorded string, argv []string) error {
	if recorded == "" {
		return refusef(ClaudePermissionMismatch, "the launch record names no Claude permission mode; the launch sets it with the hook as one unit")
	}
	var modes []string
	for i, a := range argv {
		switch {
		case a == "--dangerously-skip-permissions" || a == "--allow-dangerously-skip-permissions":
			return refusef(ClaudePermissionMismatch, "Claude argv carries %s", a)
		case a == "--permission-mode" && i+1 < len(argv):
			modes = append(modes, argv[i+1])
		case strings.HasPrefix(a, "--permission-mode="):
			modes = append(modes, strings.TrimPrefix(a, "--permission-mode="))
		}
	}
	if len(modes) != 1 || modes[0] != recorded {
		return refusef(ClaudePermissionMismatch, "Claude argv permission mode %v; the launch record says %q", modes, recorded)
	}
	return nil
}
