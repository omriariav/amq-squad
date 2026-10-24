package hostcheck

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// Acceptance 5: the warning names the variable, never its value.
func TestShellProfileExportsNamesOnly(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".zshrc"), strings.Join([]string{
		"# export COMMENTED_API_KEY=x",
		"export GEMINI_API_KEY=AIzaSyDUMMYVALUE123",
		"export PATH=$PATH:/opt/bin",
		"export GH_TOKEN='ghp_abcdefabcdefabcdef' OTHER=1",
		"typeset -x MY_SECRET=\"s3cr3t value\"",
		"alias k=kubectl; export NOT_A_KEY=1",
	}, "\n"), 0o600)
	write(t, filepath.Join(home, ".config", "fish", "config.fish"), "set -gx OPENAI_API_KEY sk-fishvalue\n", 0o600)
	got := ShellProfileExports(home)
	var names []string
	for _, e := range got {
		names = append(names, e.Name)
		if strings.Contains(e.Name, "AIza") || strings.Contains(e.Name, "ghp_") || strings.Contains(e.Name, "s3cr3t") {
			t.Fatalf("value leaked: %+v", e)
		}
	}
	if strings.Join(names, ",") != "GEMINI_API_KEY,GH_TOKEN,MY_SECRET,OPENAI_API_KEY" {
		t.Fatalf("names = %v", names)
	}
	if got[0].Line != 2 || !strings.HasSuffix(got[0].File, ".zshrc") {
		t.Fatalf("position %+v", got[0])
	}
}

// Acceptance 6: a changed settings.json shows the diff against the preflight copy.
func TestDiffOwnerConfig(t *testing.T) {
	home := t.TempDir()
	cfg := OwnerConfigs(home)[0]
	write(t, cfg.Preflight, "{\n  \"model\": \"opus\",\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-ant-old-value-123\"\n  }\n}\n", 0o600)
	write(t, cfg.Live, "{\n  \"model\": \"sonnet\",\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-ant-new-value-456\"\n  },\n  \"hooks\": {}\n}\n", 0o600)
	d := DiffOwnerConfig(cfg)
	if d.State != "changed" {
		t.Fatalf("state %s", d.State)
	}
	joined := strings.Join(d.Lines, "\n")
	for _, want := range []string{`-  "model": "opus",`, `+  "model": "sonnet",`, `+  "hooks": {}`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diff missing %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "sk-ant") {
		t.Fatalf("secret value in diff:\n%s", joined)
	}
	write(t, cfg.Live, "{\n  \"model\": \"opus\",\n  \"env\": {\n    \"ANTHROPIC_API_KEY\": \"sk-ant-old-value-123\"\n  }\n}\n", 0o600)
	if d := DiffOwnerConfig(cfg); d.State != "same" || len(d.Lines) != 0 {
		t.Fatalf("same file: %+v", d)
	}
	codex := OwnerConfigs(home)[1]
	if d := DiffOwnerConfig(codex); d.State != "no_preflight" {
		t.Fatalf("no preflight: %+v", d)
	}
	write(t, codex.Preflight, "model = \"gpt-5.6-sol\"\n[marketplaces.x]\nlast_updated = \"2026-10-21\"\n", 0o600)
	write(t, codex.Live, "model = \"gpt-5.6-sol\"\n[marketplaces.x]\nlast_updated = \"2026-10-24\"\n", 0o600)
	if d := DiffOwnerConfig(codex); d.State != "same" || d.Ignored != 2 {
		t.Fatalf("codex bookkeeping counted as a change: %+v", d)
	}
}

// Acceptance 7 (reader side): a config without the repo lists the repo.
func TestUntrustedRootsAndNotify(t *testing.T) {
	ct := ParseCodexTrust(strings.Join([]string{
		`model = "gpt-5.6-sol"`,
		`notify = ["/usr/local/bin/amq", "__codex-notify"]`,
		`[projects."/Users/o/code/trusted"]`,
		`trust_level = "trusted"`,
		`[projects.'/Users/o/code/untrusted']`,
		`trust_level = "untrusted"`,
		`[tui]`,
		`notify = ["not-top-level"]`,
	}, "\n"))
	got := ct.UntrustedRoots([]string{"/Users/o/code/trusted", "/Users/o/code/untrusted", "/Users/o/code/new", "/Users/o/code/trusted/sub"})
	if strings.Join(got, ",") != "/Users/o/code/untrusted,/Users/o/code/new,/Users/o/code/trusted/sub" {
		t.Fatalf("untrusted = %v", got)
	}
	if ct.Notify != `["/usr/local/bin/amq", "__codex-notify"]` || ct.HasNotifyFanout() {
		t.Fatalf("notify %q", ct.Notify)
	}
	steps := TrustSteps("/Users/o/code/new", "/h/.codex/config.toml", "/h/.amq-squad/bin/"+NotifyFanoutName)
	for _, want := range []string{`[projects."/Users/o/code/new"]`, `trust_level = "trusted"`, "never writes", "Candidate", NotifyFanoutName} {
		if !strings.Contains(steps, want) {
			t.Fatalf("steps missing %q:\n%s", want, steps)
		}
	}
	withFanout := ParseCodexTrust(`notify = ["/h/.amq-squad/bin/` + NotifyFanoutName + `"]`)
	if !withFanout.HasNotifyFanout() {
		t.Fatal("fan-out line not seen")
	}
}

func TestSleepAndLoginService(t *testing.T) {
	cases := []struct {
		out       string
		permitted bool
	}{
		{" sleep                1 (sleep prevented by powerd, caffeinate)\n", false},
		{" sleep                10\n displaysleep         10\n", true},
		{" sleep                0\n", false},
	}
	for _, c := range cases {
		got, _, ok := SleepPermitted(c.out)
		if !ok || got != c.permitted {
			t.Fatalf("%q: permitted=%v ok=%v", c.out, got, ok)
		}
	}
	if _, _, ok := SleepPermitted("nothing"); ok {
		t.Fatal("no sleep line parsed")
	}
	home := t.TempDir()
	if len(LoginServices(home)) != 0 {
		t.Fatal("service found in an empty home")
	}
	write(t, filepath.Join(home, "Library", "LaunchAgents", "io.github.avivsinai.amq-remote.plist"), "<plist/>", 0o644)
	if len(LoginServices(home)) != 1 {
		t.Fatal("service not found")
	}
}

func TestGrantsMode(t *testing.T) {
	home := t.TempDir()
	path := GrantsPath(home)
	if _, present, _ := GrantsMode(path); present {
		t.Fatal("absent file present")
	}
	write(t, path, "schema: 1\n", 0o600)
	os.Chmod(filepath.Dir(path), 0o700)
	if mode, present, reason := GrantsMode(path); !present || reason != "" || mode != "0600" {
		t.Fatalf("private file: %s %v %q", mode, present, reason)
	}
	if runtime.GOOS == "windows" {
		return
	}
	os.Chmod(path, 0o666)
	if _, _, reason := GrantsMode(path); !strings.Contains(reason, "world-writable") {
		t.Fatalf("0666 reason %q", reason)
	}
}
