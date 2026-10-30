package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// writeCodexHooks writes a Codex hooks.json with AMQ's marked entry for each
// event (the command is a canary that never reaches the row's detail).
func writeCodexHooks(t *testing.T, dir string, events []string) {
	t.Helper()
	hooks := map[string]any{}
	for _, e := range events {
		hooks[e] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "AMQ_DIALOG_HOOK=1 /opt/canary-bin/amq wake dialog close canary-zz9"}}}}
	}
	// The owner's own hook on PermissionRequest: an entry without the marker
	// never counts as AMQ's.
	hooks["PermissionRequest"] = append(asSlice(hooks["PermissionRequest"]), map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/opt/owner-own-hook canary-owner"}}})
	b, err := json.Marshal(map[string]any{"hooks": hooks})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hooks.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// 3.0.1 t9 (4): a Codex seat's wake needs AMQ's Codex dialog hook on the
// host, or a doorbell can answer a Codex approval prompt. The row names the
// missing events and the owner's fix, reads features.hooks by key name, and
// never prints a hook command or a config value.
func TestCodexWakeDialogHookRow(t *testing.T) {
	dir := t.TempDir() // a temp Codex home; never the real HOME

	c := codexDialogHookCheck(dir)
	if c.Status != doctorWarn || c.Kind != "codex_dialog_hook_missing" || !strings.Contains(c.Detail, "PermissionRequest") || !strings.Contains(c.Detail, seatexec.CodexDialogHookFix) {
		t.Fatalf("no hooks.json: %+v", c)
	}

	writeCodexHooks(t, dir, []string{"Stop", "UserPromptSubmit", "Interrupt", "SessionStart", "SessionEnd"})
	c = codexDialogHookCheck(dir)
	if c.Status != doctorWarn || c.Kind != "codex_dialog_hook_missing" || !strings.Contains(c.Detail, "missing for PermissionRequest in") || strings.Contains(c.Detail, "UserPromptSubmit") {
		t.Fatalf("one event missing: %+v", c)
	}

	writeCodexHooks(t, dir, seatexec.CodexDialogHookEvents)
	c = codexDialogHookCheck(dir)
	if c.Status != doctorOK || !strings.Contains(c.Detail, "all 6 events") {
		t.Fatalf("installed: %+v", c)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"canary-model-zz9\"\n\n[features]\nhooks = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c = codexDialogHookCheck(dir)
	if c.Status != doctorWarn || c.Kind != "codex_dialog_hook_disabled" || !strings.Contains(c.Detail, "features.hooks is false") {
		t.Fatalf("hooks off: %+v", c)
	}

	for _, c := range []doctorCheck{codexDialogHookCheck(dir), codexDialogHookCheck(t.TempDir())} {
		if strings.Contains(c.Detail, "canary") {
			t.Fatalf("the row printed a value: %s", c.Detail)
		}
	}
}

// The row runs in doctor's Codex checks, on the Codex home doctor reads
// (CODEX_HOME here, a temp dir), whenever codex is on the host.
func TestCodexWakeDialogHookRowIsInDoctor(t *testing.T) {
	home, codexHome := t.TempDir(), t.TempDir()
	h := &doctorHost{Home: home, CodexHome: codexHome}
	det := registry.Detection{Tools: []registry.Tool{{Name: "codex", Found: true}}}
	var row *doctorCheck
	for _, c := range h.codexChecks(det, t.TempDir()) {
		if c.Name == "codex wake dialog hook" {
			row = &c
		}
	}
	if row == nil || row.Kind != "codex_dialog_hook_missing" || !strings.Contains(row.Detail, filepath.Join(codexHome, "hooks.json")) {
		t.Fatalf("doctor's codex rows lack the wake dialog hook row for CODEX_HOME: %+v", row)
	}
	if rows := h.codexChecks(registry.Detection{}, t.TempDir()); len(rows) != 0 {
		t.Fatalf("no codex on the host, rows %+v", rows)
	}
}
