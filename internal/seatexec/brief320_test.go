package seatexec

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Brief 3.2.0 rows 1 and 4: Codex 0.166.0 has an in-turn PostToolUse
// catalogue event, so the seat receives activity hooks for it and Stop. A
// prompt submission or permission wait alone is never configured as work.
func TestCodexActivityHooksUseOnlyWorkEvents(t *testing.T) {
	args := CodexActivityHookArgs("/opt/amq squad/bin", "/opt/amq/bin")
	joined := strings.Join(args, "\n")
	for _, want := range []string{"hooks.PostToolUse", "hooks.Stop", "evidence ingest", "wake dialog"} {
		if !strings.Contains(joined, want) {
			t.Errorf("activity hook args omit %q: %s", want, joined)
		}
	}
	for _, absent := range []string{"hooks.PermissionRequest", "hooks.UserPromptSubmit"} {
		if strings.Contains(joined, absent) {
			t.Errorf("non-work event configured as activity: %s", joined)
		}
	}
	if strings.Join(CodexActivityHookEvents, ",") != "PostToolUse,Stop" {
		t.Fatalf("catalogue selection = %v", CodexActivityHookEvents)
	}
}

func TestCodexStopOverlayUsesAMQsInstalledDialogCloseCommand(t *testing.T) {
	amq, err := exec.LookPath("amq")
	if err != nil {
		t.Skip("installed amq is required for the source comparison")
	}
	home, codexHome := t.TempDir(), t.TempDir()
	cmd := exec.Command(amq, "wake", "install-dialog-hook", "--dry-run", "--harness", "codex")
	cmd.Env = []string{"HOME=" + home, "CODEX_HOME=" + codexHome, "AMQ_NO_UPDATE_CHECK=1"}
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	first, _, _ := strings.Cut(string(out), "\n")
	fields := strings.Split(first, "\t")
	if len(fields) != 4 || fields[0] != "Stop" {
		t.Fatalf("AMQ dry-run Stop row: %q", first)
	}
	joined := strings.Join(CodexActivityHookArgsForHome("/opt/amq-squad", amq, home), "\n")
	if !strings.Contains(joined, strconv.Quote(fields[3])) {
		t.Fatalf("Stop overlay drifted from AMQ installer\noverlay: %s\nwant command: %s", joined, fields[3])
	}
}

func TestCodexActivityTrustMatchesCodex0166HashAndState(t *testing.T) {
	if got, want := CodexActivityHookHash("post_tool_use", "echo hi", 5), "sha256:ffc93b7ed01e5d85ae11c6ef9b2a9f3d795165d49f717975ba86d4e38d026710"; got != want {
		t.Fatalf("Codex 0.166 hash = %s, want %s", got, want)
	}
	dir := t.TempDir()
	entries := codexActivityEntries("/opt/amq-squad", "/opt/amq", "/home/owner")
	var cfg strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&cfg, "[hooks.state.%q]\ntrusted_hash = %q\n", e.key, CodexActivityHookHash(e.event, e.command, e.timeout))
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := ReadCodexActivityTrust(dir, "/opt/amq-squad", "/opt/amq", "/home/owner"); !got.Ready() {
		t.Fatalf("trusted overlay not ready: %+v", got)
	}
	if got := ReadCodexActivityTrust(dir, "/different/amq-squad", "/opt/amq", "/home/owner"); got.Ready() || len(got.Missing) != 2 {
		t.Fatalf("changed command path retained trust: %+v", got)
	}
}

func TestCodexActivityHashesArePinnedFromCodex0166(t *testing.T) {
	var fixture struct {
		CodexVersion string `json:"codex_version"`
		Entries      []struct {
			Key         string `json:"key"`
			Event       string `json:"event_name"`
			Command     string `json:"command"`
			Timeout     int    `json:"timeout_sec"`
			CurrentHash string `json:"current_hash"`
		} `json:"entries"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-0.166-hooks-list.json"))
	if err != nil || json.Unmarshal(raw, &fixture) != nil {
		t.Fatalf("Codex hooks/list fixture: %v", err)
	}
	if fixture.CodexVersion != "0.166.0" || len(fixture.Entries) != 4 {
		t.Fatalf("Codex fixture = version %q, entries %d", fixture.CodexVersion, len(fixture.Entries))
	}
	live := codexActivityEntries(
		"/private/tmp/amq-squad-t79-live/amq-squad",
		"/opt/homebrew/bin/amq",
		"/private/tmp/amq-squad-t79-live/home",
	)
	if len(live) != 3 {
		t.Fatalf("live entries = %d, want 3", len(live))
	}
	for i, pin := range fixture.Entries {
		if i < len(live) && (pin.Key != live[i].key || pin.Event != live[i].event || pin.Command != live[i].command || pin.Timeout != live[i].timeout) {
			t.Errorf("Codex live fixture entry %d drifted: fixture %+v, production %+v", i, pin, live[i])
		}
		if got := CodexActivityHookHash(pin.Event, pin.Command, pin.Timeout); got != pin.CurrentHash {
			t.Errorf("Codex 0.166 entry %s hash = %s, want %s", pin.Key, got, pin.CurrentHash)
		}
	}
}
