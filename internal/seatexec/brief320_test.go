package seatexec

import (
	"bytes"
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
		Captures []struct {
			Name           string          `json:"name"`
			CodexVersion   string          `json:"codex_version"`
			CaptureCommand string          `json:"capture_command"`
			Request        json.RawMessage `json:"request"`
			Response       json.RawMessage `json:"response"`
		} `json:"captures"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-0.166-hooks-list.json"))
	if err != nil {
		t.Fatalf("Codex hooks/list fixture: %v", err)
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatalf("parse Codex hooks/list fixture: %v", err)
	}
	if len(fixture.Captures) != 2 {
		t.Fatalf("Codex fixture captures = %d, want live and synthetic", len(fixture.Captures))
	}
	live := codexActivityEntries(
		"/private/tmp/amq-squad-t79-live/amq-squad",
		"/opt/homebrew/bin/amq",
		"/private/tmp/amq-squad-t79-live/home",
	)
	if len(live) != 3 {
		t.Fatalf("live entries = %d, want 3", len(live))
	}
	for _, capture := range fixture.Captures {
		if capture.CodexVersion != "0.166.0" || capture.CaptureCommand == "" {
			t.Fatalf("Codex capture %q metadata = version %q command %q", capture.Name, capture.CodexVersion, capture.CaptureCommand)
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(capture.Request, &request); err != nil || request.Method != "hooks/list" {
			t.Fatalf("Codex capture %q request: method %q err %v", capture.Name, request.Method, err)
		}
		var response struct {
			ID     int `json:"id"`
			Result struct {
				Data []struct {
					Hooks []struct {
						Key         string `json:"key"`
						Event       string `json:"eventName"`
						Command     string `json:"command"`
						Timeout     int    `json:"timeoutSec"`
						CurrentHash string `json:"currentHash"`
					} `json:"hooks"`
				} `json:"data"`
			} `json:"result"`
		}
		if err := json.Unmarshal(capture.Response, &response); err != nil || response.ID != 2 || len(response.Result.Data) != 1 {
			t.Fatalf("Codex capture %q response: id %d data %d err %v", capture.Name, response.ID, len(response.Result.Data), err)
		}
		hooks := response.Result.Data[0].Hooks
		if capture.Name == "live" && len(hooks) != len(live) {
			t.Fatalf("Codex live response hooks = %d, want %d", len(hooks), len(live))
		}
		if capture.Name == "synthetic-unicode" {
			if len(hooks) != 2 || !bytes.Contains(capture.Response, []byte("\u2028")) || !bytes.Contains(capture.Response, []byte("\u2029")) ||
				bytes.Contains(capture.Response, []byte(`\u2028`)) || bytes.Contains(capture.Response, []byte(`\u2029`)) {
				t.Fatalf("Codex Unicode response is not the raw UTF-8 two-pin capture: %s", capture.Response)
			}
		}
		for i, pin := range hooks {
			event := map[string]string{"postToolUse": "post_tool_use", "stop": "stop"}[pin.Event]
			if event == "" {
				t.Fatalf("Codex capture %q entry %d event %q", capture.Name, i, pin.Event)
			}
			if capture.Name == "live" && (pin.Key != live[i].key || event != live[i].event || pin.Command != live[i].command || pin.Timeout != live[i].timeout) {
				t.Errorf("Codex live fixture entry %d drifted: key %q event %q command %q timeout %d, production %+v", i, pin.Key, event, pin.Command, pin.Timeout, live[i])
			}
			if got := CodexActivityHookHash(event, pin.Command, pin.Timeout); got != pin.CurrentHash {
				t.Errorf("Codex 0.166 capture %q entry %s hash = %s, want Codex's %s", capture.Name, pin.Key, got, pin.CurrentHash)
			}
		}
	}
}
