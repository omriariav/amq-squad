package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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

func TestCodexActivityTrustWarningUsesTheRequiredOwnerStep(t *testing.T) {
	c := codexActivityTrustCheck(t.TempDir(), "/opt/amq-squad", "/opt/amq", "/home/owner")
	want := "codex seats will not renew on their own until you trust the amq-squad hooks: launch one Codex seat, run /hooks, and trust the three /<session-flags>/config.toml amq-squad activity entries"
	if c.Status != doctorWarn || !strings.Contains(c.Detail, want) ||
		!strings.Contains(c.Detail, `'/opt/amq-squad' evidence ingest --me "$AM_ME"`) ||
		!strings.Contains(c.Detail, `'/opt/amq' --no-update-check wake dialog --harness codex close`) ||
		!strings.Contains(c.Detail, "Reinstalling amq-squad or amq at another path changes the command string and drops that trust") {
		t.Fatalf("activity trust row = %+v, want %q", c, want)
	}
}

func TestCodexActivityTrustWarningDerivesTwoEntriesWithoutAMQ(t *testing.T) {
	c := codexActivityTrustCheck(t.TempDir(), "/opt/amq-squad", "", "/home/owner")
	if c.Status != doctorWarn || !strings.Contains(c.Detail, "trust the two /<session-flags>/config.toml amq-squad activity entries") ||
		strings.Contains(c.Detail, ":stop:0:1") {
		t.Fatalf("two-entry activity trust row = %+v", c)
	}
}

func TestCodexActivityTrustUsesTheResolvedSeatExecutable(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "resolved-binary")
	link := filepath.Join(dir, "seat-link")
	if err := os.WriteFile(real, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "squad.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "squad.yaml"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	previousLookPath := upLookPath
	upLookPath = func(name string) (string, error) {
		if name == "amq" {
			return "/opt/amq", nil
		}
		return "", os.ErrNotExist
	}
	t.Cleanup(func() { upLookPath = previousLookPath })
	q, _, err := UpCompile(project, link)
	if err != nil || q.Wrapper != real {
		t.Fatalf("seat launch wrapper = %q, err %v, want resolved %q", q.Wrapper, err, real)
	}

	home, codexHome := t.TempDir(), t.TempDir()
	host := &doctorHost{Home: home, CodexHome: codexHome, Executable: link}
	det := registry.Detection{Tools: []registry.Tool{{Name: "codex", Found: true}, {Name: "amq", Found: true}}}
	var row *doctorCheck
	for _, check := range host.codexChecks(det, project) {
		if check.Name == "codex activity hook trust" {
			row = &check
		}
	}
	if row == nil {
		t.Fatal("doctor production wiring omitted the Codex activity row")
	}
	args := seatexec.CodexActivityHookArgsForHome(q.Wrapper, "", home)
	const argPrefix = `hooks.PostToolUse=[{hooks=[{type="command",command=`
	quoted, rest, ok := strings.Cut(strings.TrimPrefix(args[1], argPrefix), ",timeout=")
	if !ok || !strings.HasSuffix(rest, `}]}]`) {
		t.Fatalf("seat production hook argument = %q", args[1])
	}
	seatCommand, err := strconv.Unquote(quoted)
	if err != nil {
		t.Fatalf("seat command %q: %v", quoted, err)
	}
	const doctorPrefix = "/<session-flags>/config.toml:post_tool_use:0:0 = "
	_, doctorTail, ok := strings.Cut(row.Detail, doctorPrefix)
	if !ok {
		t.Fatalf("doctor row lacks PostToolUse command: %q", row.Detail)
	}
	doctorCommand, _, _ := strings.Cut(doctorTail, "; ")
	if doctorCommand != seatCommand || !strings.Contains(seatCommand, real) || strings.Contains(seatCommand, link) {
		t.Fatalf("doctor command %q, seat command %q; unresolved link %q, real %q", doctorCommand, seatCommand, link, real)
	}
}
