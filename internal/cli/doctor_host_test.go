package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

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

// probeHost is a host with a verified embedded pi runtime (the trusted
// digest set for the test) and two local models.
func probeHost(t *testing.T, home string) *doctorHost {
	t.Helper()
	rt := filepath.Join(home, ".amq-squad", "runtime", "1.3.0-abc")
	body := "console.log(1)"
	sum := sha256.Sum256([]byte(body))
	hostWrite(t, filepath.Join(rt, "lib", "cli.js"), body)
	os.Chmod(filepath.Join(rt, "lib", "cli.js"), 0o444)
	manifest := `{"version":"1.3.0","executable":"lib/cli.js","files":[{"path":"lib/cli.js","sha256":"` + hex.EncodeToString(sum[:]) + `","size":14,"mode":292}]}`
	hostWrite(t, filepath.Join(rt, registry.RuntimeManifestName), manifest)
	msum := sha256.Sum256([]byte(manifest))
	prev := registry.EmbeddedRuntimeManifestSHA256
	registry.EmbeddedRuntimeManifestSHA256 = hex.EncodeToString(msum[:])
	t.Cleanup(func() { registry.EmbeddedRuntimeManifestSHA256 = prev })
	h := hostWith(t, home, "amq", "pi", "ollama", "codex")
	detect := h.Detect
	h.Detect = func(ctx context.Context, env registry.Env) registry.Detection {
		d := detect(ctx, env)
		d.Ollama = []registry.OllamaModel{{Name: "qwen3.6:27b", CapsKnown: true, Tools: true}, {Name: "gemma3:4b", CapsKnown: true}}
		d.OllamaErr, d.PiRuntimeDir = "", rt
		for i := range d.Tools {
			if d.Tools[i].Name == "pi" {
				d.Tools[i].Path = rt
			}
		}
		return d
	}
	return h
}

// Review P1 (3): with a runtime that fails its re-hash, no probe command
// is built or run, and doctor fails.
func TestDoctorProbeVerifiesRuntimeFirst(t *testing.T) {
	home := t.TempDir()
	h := probeHost(t, home)
	h.Probe, h.ProbeRuns = true, 5
	ran := 0
	h.ProbeLocal = func(context.Context, *doctorHost, registry.Detection, string) (registry.ProbeRecord, error) {
		ran++
		return registry.ProbeRecord{Runs: 5, Passes: 5}, nil
	}
	js := filepath.Join(home, ".amq-squad", "runtime", "1.3.0-abc", "lib", "cli.js")
	os.Chmod(js, 0o644)
	os.WriteFile(js, []byte("console.log(2)"), 0o444)
	os.Chmod(js, 0o444)
	data, err := runHostDoctor(t, h, t.TempDir())
	if ran != 0 {
		t.Fatalf("a probe ran against an unverified runtime (%d runs)", ran)
	}
	if err == nil || findCheck(data.Checks, "pi runtime").Status != doctorFail || findCheck(data.Checks, "probe/pi").Status != doctorFail {
		t.Fatalf("unverified runtime: %v", err)
	}
	// A PATH pi is never probed either (R2: the built-in pi only).
	h2 := hostWith(t, t.TempDir(), "amq", "pi", "ollama")
	h2.Probe, h2.ProbeLocal = true, h.ProbeLocal
	runHostDoctor(t, h2, t.TempDir())
	if ran != 0 {
		t.Fatal("a PATH pi was probed")
	}
}

// Review P2 (9): a failed tally and a probe that fails to run are failures.
func TestDoctorFailedProbeFails(t *testing.T) {
	for _, tc := range []struct {
		rec registry.ProbeRecord
		err error
	}{
		{registry.ProbeRecord{Runs: 5, Passes: 0, FalseSuccesses: 5}, nil},
		{registry.ProbeRecord{}, errors.New("pi exited 1")},
	} {
		h := probeHost(t, t.TempDir())
		h.Probe, h.ProbeRuns = true, 5
		h.ProbeLocal = func(context.Context, *doctorHost, registry.Detection, string) (registry.ProbeRecord, error) {
			return tc.rec, tc.err
		}
		data, err := runHostDoctor(t, h, t.TempDir())
		c := findCheck(data.Checks, "probe/ollama/qwen3.6:27b")
		if err == nil || c == nil || c.Status != doctorFail {
			t.Fatalf("%+v %v: doctor err %v, row %+v", tc.rec, tc.err, err, c)
		}
	}
}

func TestDoctorProbeWritesProbeResults(t *testing.T) {
	home := t.TempDir()
	h := probeHost(t, home)
	h.Probe, h.WriteRegistry, h.ProbeRuns = true, true, 5
	var probed []string
	h.ProbeLocal = func(_ context.Context, _ *doctorHost, _ registry.Detection, model string) (registry.ProbeRecord, error) {
		probed = append(probed, model)
		return registry.ProbeRecord{Runs: 5, Passes: 5, Predicate: registry.ProbePredicate}, nil
	}
	h.TTFT = func(context.Context, registry.Env, string) (registry.Latency, error) {
		return registry.Latency{TextMS: 6827, ReasoningMS: 106}, nil
	}
	h.ProbeCodex = func(_ context.Context, _ registry.Env, _, version, model string) registry.CodexVerdict {
		return registry.CodexVerdict{Model: model, Version: version, Known: true, Usable: model == "gpt-5.6-sol", Reason: "refused in test"}
	}
	data, err := runHostDoctor(t, h, t.TempDir())
	if err != nil {
		t.Fatalf("%v %+v", err, data.Checks)
	}
	if strings.Join(probed, ",") != "ollama/qwen3.6:27b" {
		t.Fatalf("probed %v (a no-tool-calls model must be skipped)", probed)
	}
	if c := findCheck(data.Checks, "probe/codex/gpt-5.6-terra"); c == nil || c.Status != doctorWarn {
		t.Fatalf("codex probe row %+v", c)
	}
	reg, err := registry.Read(registry.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := reg.Lookup("pi", "ollama/qwen3.6:27b", "medium"); !e.Verified || e.LatencyMS != 6827 {
		t.Fatalf("local entry %+v", e)
	}
	if e, _ := reg.Lookup("codex", "gpt-5.6-terra", "high"); e.Verified || e.State != registry.StateRefused {
		t.Fatalf("codex entry %+v", e)
	}
	if e, _ := reg.Lookup("codex", "gpt-5.6-sol", "high"); !e.Verified {
		t.Fatalf("sol entry %+v", e)
	}
}

// The owner-run measurement: doctor --usage-scan names the files that
// carried a signal and reads nothing outside HOME.
func TestDoctorUsageScan(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 10, 21, 3, 0, 0, 0, time.UTC)
	claudeFile := filepath.Join(home, ".claude", "projects", "-repo", "s1.jsonl")
	hostWrite(t, claudeFile, `{"type":"assistant","timestamp":"2026-10-21T02:10:04.000Z","isApiErrorMessage":true,"error":"rate_limit","apiErrorStatus":429,"cwd":"/repo","message":{"content":[{"type":"text","text":"You've hit your session limit · resets 5:00am (Asia/Jerusalem)"}]}}`+"\n")
	codexFile := filepath.Join(home, ".codex", "sessions", "2026", "10", "21", "rollout-x-1.jsonl")
	hostWrite(t, codexFile, `{"timestamp":"2026-10-21T02:00:00.000Z","type":"session_meta","payload":{"cwd":"/repo"}}`+"\n"+
		`{"timestamp":"2026-10-21T02:30:00.000Z","type":"event_msg","payload":{"type":"background_event","message":"You have 3 usage limit resets available. Run /usage to use one."}}`+"\n")
	h := hostWith(t, home, "amq")
	h.UsageScan, h.Now = 24*time.Hour, func() time.Time { return now }
	data, err := runHostDoctor(t, h, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(data.Checks, "usage scan"); c == nil || c.Status != doctorWarn || !strings.Contains(c.Detail, "1 usage-limit event") {
		t.Fatalf("summary %+v", c)
	}
	if c := findCheck(data.Checks, "usage/claude"); c == nil || !strings.Contains(c.Detail, claudeFile) || !strings.Contains(c.Detail, "usage_limit=1") {
		t.Fatalf("claude file row %+v", c)
	}
	if c := findCheck(data.Checks, "usage/codex"); c != nil {
		t.Fatalf("the reset banner counted: %+v", c)
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
