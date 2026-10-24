package registry

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeHost is a detection Env with a fixed PATH and canned command output.
type fakeHost struct {
	paths map[string]string
	out   map[string]string // "name arg..." -> output; a missing key is an error
	mu    sync.Mutex        // Detect runs commands from several goroutines
	ran   []string
}

func (f *fakeHost) env(home string) Env {
	return Env{
		Home: home, GOOS: "darwin",
		LookPath: func(name string) (string, error) {
			if p, ok := f.paths[name]; ok {
				return p, nil
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) (string, error) {
			key := strings.TrimSpace(filepath.Base(name) + " " + strings.Join(args, " "))
			f.mu.Lock()
			f.ran = append(f.ran, key)
			f.mu.Unlock()
			if out, ok := f.out[key]; ok {
				return out, nil
			}
			return "", errors.New("exit 1")
		},
		Getenv:     func(string) string { return "" },
		OllamaHost: "http://127.0.0.1:1", // unreachable unless a test overrides it
		HTTP:       &http.Client{Timeout: 200 * time.Millisecond},
	}
}

func TestDetectEmptyHostIsMissingToolsNotErrors(t *testing.T) {
	f := &fakeHost{}
	d := Detect(context.Background(), f.env(t.TempDir()))
	if len(d.Tools) != len(ToolOrder) {
		t.Fatalf("tools = %d, want one row per tool", len(d.Tools))
	}
	for _, tool := range d.Tools {
		if tool.Found || tool.Version != "" {
			t.Fatalf("%s found on an empty PATH", tool.Name)
		}
	}
	if d.OllamaErr == "" || len(d.Ollama) != 0 {
		t.Fatalf("ollama: %q %v", d.OllamaErr, d.Ollama)
	}
	r := Build(d, Registry{}, BuildOptions{})
	if len(r.Proposable()) != 0 {
		t.Fatalf("an empty host has proposable entries: %v", r.Proposable())
	}
	for _, e := range r.Entries {
		if e.State != StateMissingTool {
			t.Fatalf("%s state %s", e.ID(), e.State)
		}
	}
}

func TestDetectVersionsLoginAndHost(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"secret":"never read"}`), 0o600)
	f := &fakeHost{
		paths: map[string]string{"claude": "/bin/claude", "codex": "/bin/codex", "tmux": "/bin/tmux",
			"gh": "/bin/gh", "security": "/usr/bin/security", "sysctl": "/usr/sbin/sysctl"},
		out: map[string]string{
			"claude --version": "2.1.307 (Claude Code)\n",
			"codex --version":  "codex-cli 0.163.0\n",
			"tmux -V":          "tmux 3.7c\n",
			"gh --version":     "gh version 2.88.1 (2026-09-30)\n",
			"security find-generic-password -s Claude Code-credentials": "keychain: \"login.keychain-db\"\n",
			"sysctl -n hw.memsize":               "68719476736\n",
			"sysctl -n machdep.cpu.brand_string": "Apple M5 Max\n",
		},
	}
	d := Detect(context.Background(), f.env(home))
	claude, codex, tmux, gh := d.Tool("claude"), d.Tool("codex"), d.Tool("tmux"), d.Tool("gh")
	if claude.Version != "2.1.307" || claude.VerifiedVersion != "2.1.233" || claude.VersionCheck != "newer" {
		t.Fatalf("claude %+v", claude)
	}
	if claude.Login != "logged_in" || claude.LoginSource != "keychain" {
		t.Fatalf("claude login %+v", claude)
	}
	if codex.Version != "0.163.0" || codex.VersionCheck != "match" || codex.LoginSource != "file" {
		t.Fatalf("codex %+v", codex)
	}
	if tmux.Version != "3.7" || tmux.VersionCheck != "unverified" || gh.Login != "logged_out" {
		t.Fatalf("tmux %+v gh %+v", tmux, gh)
	}
	if d.Host.RAMGB != 64 || d.Host.GPU != "Apple M5 Max (unified memory)" {
		t.Fatalf("host %+v", d.Host)
	}
	for _, cmd := range f.ran {
		if strings.Contains(cmd, " -w") || strings.Contains(cmd, " -g") || strings.Contains(cmd, "login") && !strings.Contains(cmd, "Claude") {
			t.Fatalf("detection ran a login or secret read: %q", cmd)
		}
	}
}

func ollamaServer(t *testing.T, models map[string][]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			var list []map[string]string
			for name := range models {
				list = append(list, map[string]string{"name": name})
			}
			json.NewEncoder(w).Encode(map[string]any{"models": list})
		case "/api/show":
			var req struct{ Model string }
			json.NewDecoder(r.Body).Decode(&req)
			json.NewEncoder(w).Encode(map[string]any{"capabilities": models[req.Model]})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// localHost is a host with ollama on PATH and an installed pi runtime.
func localHost(t *testing.T, models map[string][]string) (Env, string) {
	t.Helper()
	home := t.TempDir()
	rt := filepath.Join(home, ".amq-squad", "runtime", "1.3.0-abc")
	os.MkdirAll(rt, 0o700)
	os.WriteFile(filepath.Join(rt, RuntimeManifestName), []byte(`{"version":"1.3.0","files":[]}`), 0o444)
	f := &fakeHost{paths: map[string]string{"ollama": "/bin/ollama"}, out: map[string]string{"ollama --version": "ollama version is 0.34.4"}}
	env := f.env(home)
	env.OllamaHost = ollamaServer(t, models).URL
	return env, home
}

func TestLocalEntriesFollowR12(t *testing.T) {
	env, _ := localHost(t, map[string][]string{
		"qwen3.6:27b": {"completion", "tools", "thinking"},
		"qwen2.5:32b": {"completion", "tools"},
		"gemma3:4b":   {"completion"},
	})
	d := Detect(context.Background(), env)
	if !d.Tool("pi").Found || d.Tool("pi").Version != "" || len(d.Ollama) != 3 {
		t.Fatalf("pi %+v ollama %+v (%s)", d.Tool("pi"), d.Ollama, d.OllamaErr)
	}
	r := Build(d, Registry{}, BuildOptions{Probes: map[string]ProbeRecord{
		"ollama/qwen3.6:27b": {Runs: 5, Passes: 5, Predicate: ProbePredicate, Version: "pi 1.3.0"},
		"ollama/qwen2.5:32b": {Runs: 7, Passes: 6, ToolErrors: 6, Predicate: ProbePredicate},
	}})
	want := map[string]string{
		"pi/ollama/qwen3.6:27b/low": StateReady,
		"pi/ollama/qwen2.5:32b/low": StateProbeFailed,
		"pi/ollama/gemma3:4b/low":   StateNoToolCalls,
	}
	for id, state := range want {
		parts := strings.SplitN(id, "/", 2)
		model := parts[1][:strings.LastIndex(parts[1], "/")]
		e, ok := r.Lookup("pi", model, "low")
		if !ok || e.State != state {
			t.Fatalf("%s: %+v", id, e)
		}
		if e.ConcurrencyPool != "ollama-gpu" || e.PrivacyClass != "local" || e.ConcurrencyCap != 1 {
			t.Fatalf("%s pool %+v", id, e)
		}
	}
	q25, _ := r.Lookup("pi", "ollama/qwen2.5:32b", "high")
	if !q25.Probation || q25.Verified {
		t.Fatalf("qwen2.5 must be unverified and on probation: %+v", q25)
	}
	gemma, _ := r.Lookup("pi", "ollama/gemma3:4b", "low")
	for _, c := range gemma.WorkClasses {
		if c == "implement" || c == "script" {
			t.Fatalf("no-tool-calls entry takes %s", c)
		}
	}
	// 3 of 3 is the spike count, not the ship count.
	r = Build(d, Registry{}, BuildOptions{Probes: map[string]ProbeRecord{"ollama/qwen3.6:27b": {Runs: 3, Passes: 3, Predicate: ProbePredicate}}})
	if e, _ := r.Lookup("pi", "ollama/qwen3.6:27b", "low"); e.Verified {
		t.Fatalf("3 of 3 verified an entry: %+v", e)
	}
}

// Acceptance 2: two runs on one host give the same file except latency.
func TestRegistryWriteIdempotentAcrossRuns(t *testing.T) {
	env, home := localHost(t, map[string][]string{"qwen3.6:27b": {"completion", "tools"}})
	path := Path(home)
	first := Build(Detect(context.Background(), env), Registry{}, BuildOptions{
		Probes:  map[string]ProbeRecord{"ollama/qwen3.6:27b": {Runs: 5, Passes: 5, Predicate: ProbePredicate}},
		Latency: map[string]Latency{"ollama/qwen3.6:27b": {TextMS: 6827, ReasoningMS: 106}},
	})
	if err := Write(path, first); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(path)
	prev, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	second := Build(Detect(context.Background(), env), prev, BuildOptions{
		Latency: map[string]Latency{"ollama/qwen3.6:27b": {TextMS: 7001, ReasoningMS: 110}},
	})
	if err := Write(path, second); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	lat := regexp.MustCompile(`(?m)^    latency(_reasoning)?_ms: \d+$`)
	if lat.ReplaceAllString(string(a), "") != lat.ReplaceAllString(string(b), "") {
		t.Fatalf("second run changed more than latency:\n%s\n----\n%s", a, b)
	}
	if string(a) == string(b) {
		t.Fatal("latency was not refreshed")
	}
	third := Build(Detect(context.Background(), env), second, BuildOptions{})
	if Format(third) != Format(second) {
		t.Fatal("a run without new measurements changed the file")
	}
	if e, _ := third.Lookup("pi", "ollama/qwen3.6:27b", "medium"); !e.Verified {
		t.Fatalf("probe result lost across runs: %+v", e)
	}
}

func TestPinnedAMQModuleMatchesGoMod(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`github.com/avivsinai/agent-message-queue (v\S+)`).FindStringSubmatch(string(raw))
	if m == nil || m[1] != PinnedAMQModule {
		t.Fatalf("go.mod pins %v, registry.PinnedAMQModule is %s", m, PinnedAMQModule)
	}
	if LaunchVerifiedVersion(PinnedAMQModule, "codex") == "" || LaunchVerifiedVersion(PinnedAMQModule, "claude") == "" {
		t.Fatalf("no launch-verified versions for %s", PinnedAMQModule)
	}
}

// TestRehashRuntimeTiming measures a full re-hash of a synthetic tree the
// size of the S3 runtime (6,432 files, 179 MB) when AMQ_SQUAD_REHASH_TIMING=1.
func TestRehashRuntimeTiming(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_REHASH_TIMING") != "1" {
		t.Skip("set AMQ_SQUAD_REHASH_TIMING=1")
	}
	dir := t.TempDir()
	var entries []runtimeFile
	big := make([]byte, 112<<20) // the Node binary
	small := make([]byte, (179-112)<<20/6431)
	for i := 0; i < 6432; i++ {
		body := small
		rel := filepath.ToSlash(filepath.Join("lib", strconv.Itoa(i%64), strconv.Itoa(i)+".js"))
		if i == 0 {
			body, rel = big, "node/bin/node"
		}
		body[0] = byte(i)
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, body, 0o444)
		sum := sha256.Sum256(body)
		entries = append(entries, runtimeFile{Path: rel, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Mode: 0o444})
	}
	raw, _ := json.Marshal(map[string]any{"files": entries})
	os.WriteFile(filepath.Join(dir, RuntimeManifestName), raw, 0o444)
	for i := 0; i < 3; i++ {
		start := time.Now()
		sum := sha256.Sum256(raw)
		res, err := rehashDir(dir, hex.EncodeToString(sum[:]))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("full re-hash: %d files, %d MB, %v", res.Files, res.Bytes>>20, time.Since(start).Round(time.Millisecond))
	}
}

func TestRehashRuntime(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{"bin/node": "#!node", "lib/cli.js": "console.log(1)"}
	var entries []runtimeFile
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o555)
		os.Chmod(p, 0o555)
		sum := sha256.Sum256([]byte(body))
		entries = append(entries, runtimeFile{Path: rel, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Mode: 0o555})
	}
	raw, _ := json.Marshal(map[string]any{"version": "1.3.0", "files": entries})
	os.WriteFile(filepath.Join(dir, RuntimeManifestName), raw, 0o444)
	os.WriteFile(filepath.Join(dir, RuntimeMarkerName), []byte("x\n"), 0o444)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	res, err := rehashDir(dir, digest)
	if err != nil || res.Files != 2 {
		t.Fatalf("clean runtime: %+v %v", res, err)
	}
	if _, err := rehashDir(dir, strings.Repeat("0", 64)); err == nil {
		t.Fatal("foreign manifest accepted")
	}
	// A same-size edit to a non-executable file: the launch subset of S3
	// misses it, the full re-hash does not.
	js := filepath.Join(dir, "lib", "cli.js")
	os.Chmod(js, 0o755)
	os.WriteFile(js, []byte("console.log(2)"), 0o555)
	os.Chmod(js, 0o555)
	if _, err := rehashDir(dir, digest); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("same-size edit: %v", err)
	}
	os.Chmod(js, 0o755)
	os.WriteFile(js, []byte("console.log(1)"), 0o555)
	os.Chmod(js, 0o555)
	os.WriteFile(filepath.Join(dir, "lib", "extra.js"), []byte("x"), 0o444)
	if _, err := rehashDir(dir, digest); err == nil || !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("extra file: %v", err)
	}
}
