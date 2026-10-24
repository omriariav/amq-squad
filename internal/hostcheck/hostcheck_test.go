package hostcheck

import (
	"os"
	"path/filepath"
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
	for _, want := range []string{`-  "model": "opus"`, `+  "model": "sonnet"`, `+  "hooks": {}`, "-env.ANTHROPIC_API_KEY: <redacted>", "+env.ANTHROPIC_API_KEY: <redacted>"} {
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

// Review P1 (1): a nested secret never reaches the diff, in JSON or TOML,
// and a changed secret shows as a redacted pair under its key path.
func TestDiffOwnerConfigRedactsNestedSecrets(t *testing.T) {
	home := t.TempDir()
	cfg := OwnerConfigs(home)[0]
	write(t, cfg.Preflight, `{"env":{"GEMINI_API_KEY":"private-value"},"hooks":{"auth":{"headers":{"Authorization":"Bearer old-bearer"}}}}`, 0o600)
	write(t, cfg.Live, `{"env":{"GEMINI_API_KEY":"other-private"},"hooks":{"auth":{"headers":{"Authorization":"Bearer new-bearer"}}},"note":"sk-abcdefghijklmnop"}`, 0o600)
	d := DiffOwnerConfig(cfg)
	joined := strings.Join(d.Lines, "\n")
	for _, leak := range []string{"private-value", "other-private", "old-bearer", "new-bearer", "sk-abcdefghijklmnop"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("%q leaked:\n%s", leak, joined)
		}
	}
	if d.State != "changed" || !strings.Contains(joined, "-env.GEMINI_API_KEY: <redacted>") || !strings.Contains(joined, "+env.GEMINI_API_KEY: <redacted>") {
		t.Fatalf("changed secret not reported:\n%s", joined)
	}
	codex := OwnerConfigs(home)[1]
	write(t, codex.Preflight, "[mcp_servers.x]\nenv = { GEMINI_API_KEY = \"toml-old\" }\n[auth]\napi_key = \"toml-key-old\"\n", 0o600)
	write(t, codex.Live, "[mcp_servers.x]\nenv = { GEMINI_API_KEY = \"toml-new\" }\n[auth]\napi_key = \"toml-key-new\"\n", 0o600)
	joined = strings.Join(DiffOwnerConfig(codex).Lines, "\n")
	for _, leak := range []string{"toml-old", "toml-new", "toml-key-old", "toml-key-new"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("%q leaked:\n%s", leak, joined)
		}
	}
	if !strings.Contains(joined, "+auth.api_key: <redacted>") || !strings.Contains(joined, "+mcp_servers.x.env.GEMINI_API_KEY: <redacted>") {
		t.Fatalf("toml secret change not reported:\n%s", joined)
	}
}

// Second review pass, item 1: TOML is parsed, so a quoted inline key, a
// multiline string under a secret key and a secret parent table never leak.
func TestDiffOwnerConfigParsesTOML(t *testing.T) {
	home := t.TempDir()
	codex := OwnerConfigs(home)[1]
	doc := func(v string) string {
		return "[mcp_servers.x]\nenv = { 'api_key' = 'inline-" + v + "' }\n" +
			"[auth]\nprivate_key = \"\"\"\nmultiline-" + v + "\n\"\"\"\n" +
			"[[credentials]]\nvalue = \"table-" + v + "\"\n"
	}
	write(t, codex.Preflight, doc("old"), 0o600)
	write(t, codex.Live, doc("new"), 0o600)
	d := DiffOwnerConfig(codex)
	joined := strings.Join(d.Lines, "\n")
	for _, leak := range []string{"inline-", "multiline-", "table-"} {
		if strings.Contains(joined, leak) {
			t.Fatalf("%q leaked:\n%s", leak, joined)
		}
	}
	for _, want := range []string{"mcp_servers.x.env.api_key", "auth.private_key", "credentials[].value"} {
		if !strings.Contains(joined, "-"+want+": <redacted>") || !strings.Contains(joined, "+"+want+": <redacted>") {
			t.Fatalf("missing the redacted pair for %s:\n%s", want, joined)
		}
	}
	// An unparseable file prints nothing of its contents.
	write(t, codex.Live, "api_key = \"broken-secret\n", 0o600)
	if j := strings.Join(DiffOwnerConfig(codex).Lines, "\n"); strings.Contains(j, "broken-secret") || !strings.Contains(j, "withheld") {
		t.Fatalf("unparseable file:\n%s", j)
	}
}

// Second review pass, item N3: trailing content after the JSON value is a diff.
func TestDiffOwnerConfigTrailingJSONIsADiff(t *testing.T) {
	home := t.TempDir()
	cfg := OwnerConfigs(home)[0]
	write(t, cfg.Preflight, "{}", 0o600)
	write(t, cfg.Live, "{}\n{\"changed\":true}", 0o600)
	if d := DiffOwnerConfig(cfg); d.State != "changed" {
		t.Fatalf("trailing content compared equal: %+v", d)
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
