package seatexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// SeatDeniedDialogTools are the Claude Code tools that open a dialog in the
// seat's pane. The guard denies both in a seat (internal/cli hook_guard.go):
// a seat never answers a dialog, and a seat's question to anyone is AMQ mail
// to the lead. An AMQ wake doorbell's Enter could otherwise answer one.
var SeatDeniedDialogTools = []string{"AskUserQuestion", "ExitPlanMode"}

// ClaudeDialogGuardWired reports whether a Claude seat's config dir runs the
// guard on PreToolUse for both SeatDeniedDialogTools: the amq-squad plugin
// is enabled in settings.json, installed inside dir, and its hooks.json has
// a PreToolUse group whose matcher covers both tools and runs the hook
// script's pre-tool verb. Read only; the reason names files and keys only.
func ClaudeDialogGuardWired(dir string) (bool, string) {
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if !readJSON(filepath.Join(dir, "settings.json"), &settings) || !settings.EnabledPlugins[claudeseat.PluginKey] {
		return false, "settings.json does not enable " + claudeseat.PluginKey
	}
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	if !readJSON(filepath.Join(dir, "plugins", "installed_plugins.json"), &installed) || len(installed.Plugins[claudeseat.PluginKey]) == 0 {
		return false, "plugins/installed_plugins.json does not install " + claudeseat.PluginKey
	}
	install := filepath.Clean(installed.Plugins[claudeseat.PluginKey][0].InstallPath)
	if rel, err := filepath.Rel(dir, install); err != nil || !filepath.IsAbs(install) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false, "the plugin's installPath is not inside the seat's config dir"
	}
	var hooks struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if !readJSON(filepath.Join(install, "hooks", "hooks.json"), &hooks) {
		return false, "the plugin's hooks/hooks.json cannot be read"
	}
	for _, group := range hooks.Hooks["PreToolUse"] {
		if !matcherCovers(group.Matcher, SeatDeniedDialogTools) {
			continue
		}
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "amq-squad-hook.sh") && strings.Contains(h.Command, "pre-tool") {
				return true, ""
			}
		}
	}
	return false, "no PreToolUse hook runs the guard for " + strings.Join(SeatDeniedDialogTools, " and ")
}

// matcherCovers reports whether a Claude Code hook matcher ("" or "*" for
// every tool, else a regular expression over the tool name) matches every
// tool.
func matcherCovers(matcher string, tools []string) bool {
	if matcher == "" || matcher == "*" {
		return true
	}
	re, err := regexp.Compile("^(?:" + matcher + ")$")
	if err != nil {
		return false
	}
	for _, t := range tools {
		if !re.MatchString(t) {
			return false
		}
	}
	return true
}

func readJSON(path string, v any) bool {
	raw, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(raw, v) == nil
}
