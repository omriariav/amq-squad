package registry

// One test row per defect of the Codex review of 33a3d72d (registry side).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// writeRuntime writes a small runtime with an on-disk manifest and returns
// the manifest's digest.
func writeRuntime(t *testing.T, dir string) string {
	t.Helper()
	files := map[string]string{"node/bin/node": "#!node", "lib/cli.js": "console.log(1)"}
	var entries []runtimeFile
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o444)
		os.Chmod(p, 0o444)
		sum := sha256.Sum256([]byte(body))
		entries = append(entries, runtimeFile{Path: rel, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body)), Mode: 0o444})
	}
	raw, _ := json.Marshal(map[string]any{"version": "1.3.0", "executable": "node/bin/node", "args": []string{"lib/cli.js"}, "files": entries})
	os.WriteFile(filepath.Join(dir, RuntimeManifestName), raw, 0o444)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Review P1 (4): without the binary's trusted digest no runtime passes, and
// an edit of a file plus its hash in the on-disk manifest is refused.
func TestRehashRequiresTrustedDigest(t *testing.T) {
	dir := t.TempDir()
	digest := writeRuntime(t, dir)
	if _, err := rehashDir(dir, digest); err != nil {
		t.Fatalf("clean runtime: %v", err)
	}
	if _, err := rehashDir(dir, ""); err == nil {
		t.Fatal("an empty trusted digest passed")
	}
	// The attacker edits lib/cli.js and rewrites its hash in the manifest.
	js := filepath.Join(dir, "lib", "cli.js")
	os.Chmod(js, 0o644)
	os.WriteFile(js, []byte("evil()"), 0o444)
	os.Chmod(js, 0o444)
	mf := filepath.Join(dir, RuntimeManifestName)
	raw, _ := os.ReadFile(mf)
	var m map[string]any
	json.Unmarshal(raw, &m)
	sum := sha256.Sum256([]byte("evil()"))
	for _, f := range m["files"].([]any) {
		if e := f.(map[string]any); e["path"] == "lib/cli.js" {
			e["sha256"], e["size"] = hex.EncodeToString(sum[:]), 6
		}
	}
	raw, _ = json.Marshal(m)
	os.Chmod(mf, 0o644)
	os.WriteFile(mf, raw, 0o444)
	if _, err := rehashDir(dir, digest); err == nil || !strings.Contains(err.Error(), "differs from the embedded") {
		t.Fatalf("rewritten manifest: %v", err)
	}
}

// Review P1 (5): a symlinked manifest with identical bytes and a symlinked
// directory component are both refused.
func TestRehashRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir := t.TempDir()
	digest := writeRuntime(t, dir)
	mf := filepath.Join(dir, RuntimeManifestName)
	elsewhere := filepath.Join(t.TempDir(), "manifest.json")
	raw, _ := os.ReadFile(mf)
	os.WriteFile(elsewhere, raw, 0o444)
	os.Chmod(mf, 0o644)
	os.Remove(mf)
	if err := os.Symlink(elsewhere, mf); err != nil {
		t.Fatal(err)
	}
	if _, err := rehashDir(dir, digest); err == nil {
		t.Fatal("a symlinked manifest with identical bytes passed")
	}
	os.Remove(mf)
	os.WriteFile(mf, raw, 0o444)
	// lib -> a copy of lib outside the runtime.
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "cli.js"), []byte("console.log(1)"), 0o444)
	lib := filepath.Join(dir, "lib")
	os.Chmod(filepath.Join(lib, "cli.js"), 0o644)
	os.RemoveAll(lib)
	if err := os.Symlink(outside, lib); err != nil {
		t.Fatal(err)
	}
	if _, err := rehashDir(dir, digest); err == nil {
		t.Fatal("a symlinked directory component passed")
	}
	rootLink := filepath.Join(t.TempDir(), "rt")
	os.Symlink(dir, rootLink)
	if _, err := rehashDir(rootLink, digest); err == nil {
		t.Fatal("a symlinked runtime root passed")
	}
}

// Review P1 (6): a run that hides a test edit from git, skips the failing
// test, changes calc.go by a comment and reports PASS is a false success.
func TestRunProbeRejectsTamperedFixture(t *testing.T) {
	work := t.TempDir()
	env := []string{"GOCACHE=" + filepath.Join(t.TempDir(), "gocache"), "HOME=" + t.TempDir()}
	run, err := RunProbe(context.Background(), ProbeConfig{Pi: []string{fakePi(t, "tamper")}, Env: env, Model: "ollama/fake", WorkDir: work})
	if err != nil {
		t.Fatal(err)
	}
	if run.Pass || !run.FalseSuccess || run.Checks["worktree_exact"] || run.Checks["independent_go_test"] {
		t.Fatalf("tampered run %+v", run)
	}
}

// Review P1 (7): a symlinked models.json or pi-agent directory is never
// followed; the outside file stays intact.
func TestPiProbeEnvRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	home := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim.txt")
	os.WriteFile(victim, []byte("keep me"), 0o600)
	agent := filepath.Join(home, ".amq-squad", "probe", "pi-agent")
	os.MkdirAll(agent, 0o700)
	os.Symlink(victim, filepath.Join(agent, "models.json"))
	env := Env{OllamaHost: "http://127.0.0.1:11434", Getenv: func(string) string { return "" }}
	if _, err := PiProbeEnv(home, env, []OllamaModel{{Name: "m"}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(victim); string(got) != "keep me" {
		t.Fatalf("the symlink target was written: %q", got)
	}
	if st, _ := os.Lstat(filepath.Join(agent, "models.json")); st.Mode()&os.ModeSymlink != 0 {
		t.Fatal("models.json is still a symlink")
	}
	home2 := t.TempDir()
	os.MkdirAll(filepath.Join(home2, ".amq-squad", "probe"), 0o700)
	os.Symlink(t.TempDir(), filepath.Join(home2, ".amq-squad", "probe", "pi-agent"))
	if _, err := PiProbeEnv(home2, env, nil); err == nil {
		t.Fatal("a symlinked pi-agent directory was used")
	}
}

type countingTransport struct{ calls atomic.Int32 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, errors.New("no network in tests")
}

// Review P2 (8): detection contacts only a loopback Ollama, and only with
// ollama on PATH.
func TestDetectContactsOnlyLoopbackOllama(t *testing.T) {
	for _, tc := range []struct {
		host   string
		onPath bool
		want   string
	}{
		{"https://remote.example", true, ErrOllamaNotLocal},
		{"http://10.0.0.5:11434", true, ErrOllamaNotLocal},
		{"http://127.0.0.1:11434", false, "ollama not on PATH"},
	} {
		tr := &countingTransport{}
		f := &fakeHost{paths: map[string]string{}}
		if tc.onPath {
			f.paths["ollama"] = "/bin/ollama"
		}
		env := f.env(t.TempDir())
		env.OllamaHost, env.HTTP = tc.host, &http.Client{Transport: tr}
		d := Detect(context.Background(), env)
		if tr.calls.Load() != 0 || d.OllamaErr != tc.want {
			t.Fatalf("%s on PATH=%v: %d calls, %q", tc.host, tc.onPath, tr.calls.Load(), d.OllamaErr)
		}
		if _, err := MeasureTTFT(context.Background(), env, "m", 8); tc.host != "http://127.0.0.1:11434" && (err == nil || tr.calls.Load() != 0) {
			t.Fatalf("TTFT to %s: %v, %d calls", tc.host, err, tr.calls.Load())
		}
	}
	for host, local := range map[string]bool{"http://localhost:11434": true, "http://[::1]:11434": true, "http://127.0.0.2": true, "http://localhost.example": false, "file:///tmp/x": false} {
		if LoopbackURL(host) != local {
			t.Fatalf("LoopbackURL(%s) = %v", host, !local)
		}
	}
}

// rehashDir re-hashes dir anchored at its parent.
func rehashDir(dir, want string) (RehashResult, error) {
	return RehashRuntime(filepath.Dir(dir), filepath.Base(dir), want)
}
