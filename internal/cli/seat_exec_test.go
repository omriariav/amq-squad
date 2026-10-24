package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

func TestSeatExecTakesOnlySeat(t *testing.T) {
	for _, args := range [][]string{
		{"--root", "/r", "--seat", "cto", "claude"},
		{"--seat", "cto", "--root", "/r", "claude"},
		{"--seat", "cto"},
		{"claude"},
	} {
		if _, _, err := parseSeatExecArgs(args); ExitCode(err) != ExitUser {
			t.Errorf("parseSeatExecArgs(%q) = %v, want a usage error", args, err)
		}
	}
	for _, args := range [][]string{{"--seat", "cto", "/bin/claude", "--model", "m"}, {"--seat=cto", "--", "/bin/claude", "--model", "m"}} {
		seat, argv, err := parseSeatExecArgs(args)
		if err != nil || seat != "cto" || strings.Join(argv, " ") != "/bin/claude --model m" {
			t.Errorf("parseSeatExecArgs(%q) = %q %q %v", args, seat, argv, err)
		}
	}
}

func TestSeatExecRefusalExitsOne(t *testing.T) {
	t.Setenv("AM_ME", "qa")
	t.Setenv("AM_ROOT", t.TempDir())
	_, _, err := captureOutput(t, func() error { return Run([]string{"seat-exec", "--seat", "cto", "/bin/true"}, "test") })
	if ExitCode(err) != ExitUser || !strings.Contains(err.Error(), "identity_mismatch") {
		t.Fatalf("seat-exec with AM_ME=qa --seat cto: %v", err)
	}
}

// 2a-3 open question 3: context reports the rules digest recorded at launch
// and flags drift.
func TestContextFlagsRulesDriftFromLaunch(t *testing.T) {
	f := newHookFixture(t)
	rulesPath := filepath.Join(f.project, "team-rules.md")
	mustWrite(t, rulesPath, "# rules\n")
	_, sum, _ := readHookFile(rulesPath)
	t.Setenv("AMQ_SQUAD_RULES_PATH", rulesPath)
	t.Setenv("AMQ_SQUAD_RULES_SHA256", sum)
	drift := func(ws []string) (n int) {
		for _, w := range ws {
			if strings.Contains(w, "rules changed since launch") {
				n++
			}
		}
		return n
	}
	if d := buildHookContext(f.id(), "", f.now); d.Rules.SHA256 != sum || drift(d.Warnings) != 0 {
		t.Fatalf("unchanged rules: %+v %v", d.Rules, d.Warnings)
	}
	mustWrite(t, rulesPath, "# rules, edited\n")
	d := buildHookContext(f.id(), "", f.now)
	if drift(d.Warnings) != 1 {
		t.Fatalf("drift not flagged: %v", d.Warnings)
	}
}

// Acceptance 10: a seat write to the owner's global agent config under a
// test HOME is denied, and the preflight copy exists.
func TestSeatWriteToGlobalAgentConfigIsDenied(t *testing.T) {
	f := newHookFixture(t)
	mustMkdir(t, filepath.Join(f.home, ".claude"))
	mustMkdir(t, filepath.Join(f.home, ".codex"))
	mustWrite(t, filepath.Join(f.home, ".claude", "settings.json"), `{"owner":true}`)
	mustWrite(t, filepath.Join(f.home, ".codex", "config.toml"), "model = \"x\"\n")
	if err := seatexec.WritePreflight(f.home, true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude-settings.json", "codex-config.toml"} {
		if _, err := os.Stat(filepath.Join(seatexec.PreflightDir(f.home), name)); err != nil {
			t.Fatalf("preflight copy %s: %v", name, err)
		}
	}
	for _, payload := range []string{
		fmt.Sprintf(`{"tool_name":"Write","tool_input":{"file_path":%q,"content":"{}"},"cwd":%q}`, filepath.Join(f.home, ".claude", "settings.json"), f.repo),
		fmt.Sprintf(`{"tool_name":"Edit","tool_input":{"file_path":"~/.codex/config.toml"},"cwd":%q}`, f.repo),
		fmt.Sprintf(`{"tool_name":"Bash","tool_input":{"command":"echo x > ~/.claude/settings.json"},"cwd":%q}`, f.repo),
		fmt.Sprintf(`{"toolName":"write","input":{"path":%q},"cwd":%q}`, filepath.Join(seatexec.PreflightDir(f.home), "claude-settings.json"), f.repo),
	} {
		withHookStdin(t, []byte(payload))
		out, _, err := captureOutput(t, func() error { return Run([]string{"guard", "--me", "cto", "--stdin"}, "test") })
		var v map[string]any
		if err != nil || json.Unmarshal([]byte(out), &v) != nil || v["verdict"] != "deny" || v["reason_code"] != "protected_path" {
			t.Fatalf("guard %s = %s %v; want deny protected_path", payload, out, err)
		}
	}
}
