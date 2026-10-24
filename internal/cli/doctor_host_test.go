package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// hostWith is a doctorHost on a temp HOME whose detection finds the named tools.
func hostWith(t *testing.T, home string, found ...string) *doctorHost {
	t.Helper()
	tools := make([]registry.Tool, 0, len(registry.ToolOrder))
	for _, name := range registry.ToolOrder {
		tool := registry.Tool{Name: name}
		for _, f := range found {
			if f == name {
				tool = registry.Tool{Name: name, Found: true, Path: "/bin/" + name, Version: "1.0.0", Login: "n/a"}
			}
		}
		tools = append(tools, tool)
	}
	return &doctorHost{
		Home: home, GOOS: "darwin",
		Detect: func(context.Context, registry.Env) registry.Detection {
			return registry.Detection{Tools: tools, Host: registry.Host{RAMGB: 64, GPU: "test"}, OllamaErr: "ollama not on PATH"}
		},
		Pmset: func(context.Context) (string, error) { return " sleep                0\n", nil },
	}
}

func runHostDoctor(t *testing.T, h *doctorHost, project string) (doctorEnvelopeData, error) {
	t.Helper()
	d := newDoctorExec(t, project)
	d.Host = h
	d.JSON = true
	var buf bytes.Buffer
	d.Out = &buf
	err := executeDoctor(d)
	return decodeDoctorJSON(t, &buf), err
}

// Acceptance 1: a host with none of the optional tools passes, one line per tool.
func TestDoctorHostWithoutOptionalToolsPasses(t *testing.T) {
	home := t.TempDir()
	data, err := runHostDoctor(t, hostWith(t, home, "amq"), t.TempDir())
	if err != nil {
		t.Fatalf("doctor failed on a host with only amq: %v\n%+v", err, data.Checks)
	}
	for _, name := range registry.ToolOrder {
		rows := 0
		for _, c := range data.Checks {
			if c.Name == "tool/"+name {
				rows++
				if name != "amq" && (c.Status != doctorOK || !strings.Contains(c.Detail, "not on PATH")) {
					t.Fatalf("missing %s: %+v", name, c)
				}
			}
		}
		if rows != 1 {
			t.Fatalf("%s has %d rows, want 1", name, rows)
		}
	}
	if c := findCheck(data.Checks, "codex trust"); c != nil {
		t.Fatalf("codex trust checked without codex: %+v", c)
	}
	if _, err := os.Stat(registry.Path(home)); err == nil {
		t.Fatal("doctor without --write-registry wrote the registry")
	}
	data, err = runHostDoctor(t, hostWith(t, home), t.TempDir())
	if err == nil || findCheck(data.Checks, "tool/amq").Status != doctorFail {
		t.Fatalf("missing amq must fail: %v", err)
	}
}

func TestDoctorWriteRegistry(t *testing.T) {
	home := t.TempDir()
	h := hostWith(t, home, "amq", "claude")
	h.WriteRegistry = true
	data, err := runHostDoctor(t, h, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(data.Checks, "registry"); c == nil || !strings.Contains(c.Detail, "written") {
		t.Fatalf("registry row %+v", c)
	}
	reg, err := registry.Read(registry.Path(home))
	if err != nil || len(reg.Proposable()) == 0 {
		t.Fatalf("registry: %v %d", err, len(reg.Proposable()))
	}
	h.WriteRegistry = false
	data, _ = runHostDoctor(t, h, t.TempDir())
	if c := findCheck(data.Checks, "registry"); c == nil || c.Status != doctorOK || !strings.Contains(c.Detail, "current") {
		t.Fatalf("second run: %+v", c)
	}
}

// Acceptance 5, 6 and 7 through doctor.
func TestDoctorHostFileChecks(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	hostWrite(t, filepath.Join(home, ".zshrc"), "export GEMINI_API_KEY=AIzaSecretValue\n")
	hostWrite(t, filepath.Join(home, ".amq-squad", "preflight", "claude-settings.json"), "{\n  \"model\": \"opus\"\n}\n")
	hostWrite(t, filepath.Join(home, ".claude", "settings.json"), "{\n  \"model\": \"sonnet\"\n}\n")
	hostWrite(t, filepath.Join(home, ".codex", "config.toml"), "[projects.\"/elsewhere\"]\ntrust_level = \"trusted\"\n")
	h := hostWith(t, home, "amq", "codex")
	data, err := runHostDoctor(t, h, project)
	if err != nil {
		t.Fatalf("warnings must not fail doctor: %v", err)
	}
	profile := findCheck(data.Checks, "shell profile")
	if profile.Status != doctorWarn || !strings.Contains(profile.Detail, "GEMINI_API_KEY") || strings.Contains(profile.Detail, "AIza") {
		t.Fatalf("shell profile: %+v", profile)
	}
	diff := findCheck(data.Checks, "owner config/claude-settings.json")
	if diff.Status != doctorWarn || !strings.Contains(diff.Detail, `-  "model": "opus"`) || !strings.Contains(diff.Detail, `+  "model": "sonnet"`) {
		t.Fatalf("config diff: %+v", diff)
	}
	if c := findCheck(data.Checks, "owner config/codex-config.toml"); c.Status != doctorOK {
		t.Fatalf("codex config without preflight: %+v", c)
	}
	trust := findCheck(data.Checks, "codex trust")
	if trust.Status != doctorWarn || !strings.Contains(trust.Detail, canonicalFilesystemPath(project)) || !strings.Contains(trust.Detail, "amq-squad trust") {
		t.Fatalf("codex trust: %+v", trust)
	}
	if c := findCheck(data.Checks, "codex notify fan-out"); c.Status != doctorOK || !strings.Contains(c.Detail, "absent") {
		t.Fatalf("notify: %+v", c)
	}
}

// Acceptance 7 (writer side): trust prints the steps and changes no file under HOME.
func TestTrustPrintsStepsAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	hostWrite(t, filepath.Join(home, ".codex", "config.toml"), "model = \"gpt-5.6-sol\"\n")
	before := treeDigest(t, home)
	var out bytes.Buffer
	if err := executeTrust(&out, home, "", repo); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[projects." + `"` + canonicalFilesystemPath(repo) + `"]`, `trust_level = "trusted"`, "never writes", "notify = ["} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("trust output missing %q:\n%s", want, out.String())
		}
	}
	if after := treeDigest(t, home); after != before {
		t.Fatalf("trust changed files under HOME:\n%s\n---\n%s", before, after)
	}
	if err := executeTrust(&out, home, "", filepath.Join(repo, "missing")); err == nil {
		t.Fatal("trust accepted a missing directory")
	}
	if _, _, err := captureOutput(t, func() error { return runTrust(nil) }); err == nil {
		t.Fatal("trust without a repository")
	}
}

func TestDoctorGrantsFileMode(t *testing.T) {
	home := t.TempDir()
	h := hostWith(t, home, "amq")
	data, _ := runHostDoctor(t, h, t.TempDir())
	if c := findCheck(data.Checks, "grants"); c.Status != doctorOK || !strings.Contains(c.Detail, "seat cap 12") {
		t.Fatalf("absent grants: %+v", c)
	}
	path := filepath.Join(home, ".amq-squad", "grants.yaml")
	hostWrite(t, path, "schema: 1\nmax_seats: 8\n")
	os.Chmod(filepath.Dir(path), 0o700)
	os.Chmod(path, 0o600)
	data, _ = runHostDoctor(t, h, t.TempDir())
	if c := findCheck(data.Checks, "grants"); c.Status != doctorOK || !strings.Contains(c.Detail, "0600") || !strings.Contains(c.Detail, "seat cap 8") {
		t.Fatalf("private grants: %+v", c)
	}
	if filepath.Separator != '/' {
		return
	}
	os.Chmod(path, 0o666)
	data, err := runHostDoctor(t, h, t.TempDir())
	c := findCheck(data.Checks, "grants")
	if err == nil || c.Status != doctorFail || c.Kind != "owner_grants_insecure" || !strings.Contains(c.Detail, "0666") {
		t.Fatalf("insecure grants: %v %+v", err, c)
	}
}

func TestDoctorSleepAndLoginService(t *testing.T) {
	home := t.TempDir()
	h := hostWith(t, home, "amq", "amq-remote")
	h.Pmset = func(context.Context) (string, error) { return " sleep                10\n", nil }
	data, _ := runHostDoctor(t, h, t.TempDir())
	if c := findCheck(data.Checks, "sleep"); c.Status != doctorWarn {
		t.Fatalf("sleep: %+v", c)
	}
	if c := findCheck(data.Checks, "amq-remote login service"); c.Status != doctorWarn {
		t.Fatalf("login service: %+v", c)
	}
	h.GOOS = "linux"
	data, _ = runHostDoctor(t, h, t.TempDir())
	if findCheck(data.Checks, "sleep") != nil {
		t.Fatal("sleep checked off macOS")
	}
}

func hostWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// treeDigest lists every path under root with its mode and content hash.
func treeDigest(t *testing.T, root string) string {
	t.Helper()
	var rows []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, _ := d.Info()
		row := p + " " + info.Mode().String()
		if !d.IsDir() {
			raw, _ := os.ReadFile(p)
			sum := sha256.Sum256(raw)
			row += " " + hex.EncodeToString(sum[:])
		}
		rows = append(rows, row)
		return nil
	})
	sort.Strings(rows)
	return strings.Join(rows, "\n")
}
