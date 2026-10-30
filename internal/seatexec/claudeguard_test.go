package seatexec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// installedHooks returns the hooks.json path of the plugin installed in a
// config dir claudeseat.Write produced.
func installedHooks(t *testing.T, dir string) string {
	t.Helper()
	var installed struct {
		Plugins map[string][]struct {
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(raw, &installed) != nil {
		t.Fatal(err)
	}
	return filepath.Join(installed.Plugins[claudeseat.PluginKey][0].InstallPath, "hooks", "hooks.json")
}

// 3.0.1 t9: a Claude seat's wake needs its config dir to run the guard on
// PreToolUse for AskUserQuestion and ExitPlanMode. Each branch.
func TestClaudeDialogGuardWired(t *testing.T) {
	write := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "config")
		if _, err := claudeseat.Write(dir, "dontAsk", "", time.Unix(0, 0)); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dir := write(t)
	if ok, why := ClaudeDialogGuardWired(dir); !ok || why != "" {
		t.Fatalf("the config dir claudeseat writes: %t %q", ok, why)
	}
	setHooks := func(t *testing.T, dir, matcher, command string) {
		hooks := map[string]any{"hooks": map[string]any{"PreToolUse": []any{map[string]any{"matcher": matcher, "hooks": []any{map[string]any{"type": "command", "command": command}}}}}}
		b, _ := json.Marshal(hooks)
		p := installedHooks(t, dir)
		os.Chmod(p, 0o600)
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	guard := `sh "${CLAUDE_PLUGIN_ROOT}/hooks/amq-squad-hook.sh" pre-tool`
	for _, tc := range []struct {
		name, matcher, command string
		want                   bool
		why                    string
	}{
		{"both tools named", "AskUserQuestion|ExitPlanMode", guard, true, ""},
		{"empty matcher", "", guard, true, ""},
		{"Bash only", "Bash", guard, false, "no PreToolUse hook runs the guard"},
		{"one of the two", "AskUserQuestion", guard, false, "no PreToolUse hook runs the guard"},
		{"another verb", "*", `sh "${CLAUDE_PLUGIN_ROOT}/hooks/amq-squad-hook.sh" post-tool`, false, "no PreToolUse hook runs the guard"},
	} {
		dir := write(t)
		setHooks(t, dir, tc.matcher, tc.command)
		if ok, why := ClaudeDialogGuardWired(dir); ok != tc.want || !strings.Contains(why, tc.why) {
			t.Errorf("%s: %t %q", tc.name, ok, why)
		}
	}
	dir = write(t)
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"enabledPlugins":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, why := ClaudeDialogGuardWired(dir); ok || !strings.Contains(why, "does not enable") {
		t.Fatalf("plugin not enabled: %t %q", ok, why)
	}
	dir = write(t)
	elsewhere := t.TempDir()
	b, _ := json.Marshal(map[string]any{"plugins": map[string]any{claudeseat.PluginKey: []any{map[string]any{"installPath": elsewhere}}}})
	if err := os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, why := ClaudeDialogGuardWired(dir); ok || !strings.Contains(why, "not inside") {
		t.Fatalf("install outside the dir: %t %q", ok, why)
	}
	if ok, _ := ClaudeDialogGuardWired(t.TempDir()); ok {
		t.Fatal("an empty dir met the condition")
	}
}
