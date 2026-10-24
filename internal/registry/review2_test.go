package registry

// One test row per item of the second Codex review pass on 68a6d347
// (registry side).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Item 3: the pi command comes from the verified manifest bytes; a manifest
// swapped after verification changes nothing, and detection never runs a
// PATH pi.
func TestPiCommandUsesVerifiedManifest(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "rt")
	os.MkdirAll(dir, 0o755)
	digest := writeRuntime(t, dir)
	res, err := RehashRuntime(base, "rt", digest)
	if err != nil {
		t.Fatal(err)
	}
	mf := filepath.Join(dir, RuntimeManifestName)
	os.Chmod(mf, 0o644)
	os.WriteFile(mf, []byte(`{"executable":"/tmp/attacker","files":[]}`), 0o444)
	cmd, err := PiCommand(res)
	if err != nil || cmd[0] != filepath.Join(dir, "node", "bin", "node") {
		t.Fatalf("command after a manifest swap: %v %v", cmd, err)
	}
	f := &fakeHost{paths: map[string]string{"pi": "/usr/local/bin/pi"}, out: map[string]string{"pi --version": "1.3.0"}}
	d := Detect(context.Background(), f.env(t.TempDir()))
	for _, c := range f.ran {
		if c == "pi" || strings.HasPrefix(c, "pi ") {
			t.Fatalf("detection executed a PATH pi: %q", c)
		}
	}
	if pi := d.Tool("pi"); !pi.Found || pi.Version != "" {
		t.Fatalf("PATH pi row %+v", pi)
	}
	if e, ok := Build(Detection{Tools: d.Tools, Ollama: []OllamaModel{{Name: "m", CapsKnown: true, Tools: true}}}, Registry{}, BuildOptions{
		Probes: map[string]ProbeRecord{"ollama/m": {Runs: 5, Passes: 5, Predicate: ProbePredicate}},
	}).Lookup("pi", "ollama/m", "low"); !ok || e.Verified {
		t.Fatalf("a PATH pi made a local entry verified: %+v", e)
	}
}

// Item 5: a symlink above the runtime but below the anchor is refused.
func TestRehashRefusesAncestorSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	base := t.TempDir()
	real := filepath.Join(t.TempDir(), "real")
	os.MkdirAll(filepath.Join(real, "rt"), 0o755)
	digest := writeRuntime(t, filepath.Join(real, "rt"))
	if err := os.Symlink(real, filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := RehashRuntime(base, "link/rt", digest); err == nil {
		t.Fatal("an ancestor symlink below the anchor was followed")
	}
	if _, err := RehashRuntime(filepath.Dir(real), "real/rt", digest); err != nil {
		t.Fatalf("the real path: %v", err)
	}
}

// Item N1: a FIFO at a listed path is refused, without hanging.
func TestRehashRefusesFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fifo")
	}
	base := t.TempDir()
	dir := filepath.Join(base, "rt")
	os.MkdirAll(dir, 0o755)
	digest := writeRuntime(t, dir)
	js := filepath.Join(dir, "lib", "cli.js")
	os.Chmod(js, 0o644)
	os.Remove(js)
	if err := mkfifo(js); err != nil {
		t.Skip("mkfifo: " + err.Error())
	}
	done := make(chan error, 1)
	go func() { _, err := RehashRuntime(base, "rt", digest); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO passed the re-hash")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the re-hash hung on a FIFO")
	}
}

// Item 6: exactly one RESULT line, and "nothing else" covers directories.
func TestProbePredicateV3(t *testing.T) {
	twice := append(honestEvents(t, "RESULT: FAIL"), final(t, "RESULT: PASS"))
	r := Evaluate(twice, cleanWorktree)
	if r.Pass || !r.FalseSuccess {
		t.Fatalf("FAIL then PASS: %+v", r)
	}
	inOne := Evaluate(honestEvents(t, "RESULT: FAIL\nRESULT: PASS"), cleanWorktree)
	if inOne.Pass || !inOne.FalseSuccess {
		t.Fatalf("two RESULT lines in one message: %+v", inOne)
	}
	dir := t.TempDir()
	for name, body := range expectedFiles() {
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	if bad := checkFiles(dir); len(bad) != 0 {
		t.Fatalf("the expected tree: %v", bad)
	}
	os.Mkdir(filepath.Join(dir, "empty"), 0o755)
	if bad := checkFiles(dir); len(bad) != 1 || !strings.Contains(bad[0], "directory empty") {
		t.Fatalf("an extra empty directory: %v", bad)
	}
}

// Item 7: the write goes to the directory that was opened, even when the
// path is swapped for a symlink afterwards, and nothing outside changes.
func TestWriteAnchoredSurvivesSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("descriptor-relative writes")
	}
	home := t.TempDir()
	parts := []string{".amq-squad", "probe", "pi-agent"}
	d, err := openAnchored(home, parts, true)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	agent := filepath.Join(home, ".amq-squad", "probe", "pi-agent")
	moved := filepath.Join(home, ".amq-squad", "probe", "moved")
	victim := t.TempDir()
	os.Rename(agent, moved)
	os.Symlink(victim, agent)
	if err := d.writeAtomic("models.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(victim, "models.json")); err == nil {
		t.Fatal("the write followed the swapped-in symlink")
	}
	if got, _ := os.ReadFile(filepath.Join(moved, "models.json")); string(got) != "{}" {
		t.Fatalf("the opened directory did not get the file: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(moved, ".models.json-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

type hostRecorder struct {
	mu    sync.Mutex
	hosts []string
}

func (h *hostRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	h.mu.Lock()
	h.hosts = append(h.hosts, r.URL.Host)
	h.mu.Unlock()
	if !LoopbackURL("http://" + r.URL.Host) {
		return nil, http.ErrUseLastResponse
	}
	return http.DefaultTransport.RoundTrip(r)
}

// Item 8: a supplied client follows no redirect, even a loopback 302 to a
// remote host.
func TestSuppliedClientFollowsNoRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://remote.example"+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	rec := &hostRecorder{}
	f := &fakeHost{paths: map[string]string{"ollama": "/bin/ollama"}}
	env := f.env(t.TempDir())
	env.OllamaHost, env.HTTP = srv.URL, &http.Client{Transport: rec}
	d := Detect(context.Background(), env)
	MeasureTTFT(context.Background(), env, "m", 8)
	for _, h := range rec.hosts {
		if strings.Contains(h, "remote.example") {
			t.Fatalf("a redirect left loopback: %v", rec.hosts)
		}
	}
	if d.OllamaErr == "" {
		t.Fatal("a redirect answer was taken as a model list")
	}
}

// Item N2: a probe record from an older predicate is discarded.
func TestOlderPredicateRecordsAreDiscarded(t *testing.T) {
	det := Detection{Tools: []Tool{{Name: "pi", Found: true, Path: "/rt"}}, PiRuntimeDir: "/rt",
		Ollama: []OllamaModel{{Name: "m", CapsKnown: true, Tools: true}}}
	prev := Registry{Entries: []Entry{{Runtime: "pi", Model: "ollama/m", Effort: "low", ProbeRuns: 5, ProbePasses: 5, ProbePredicate: "r12-v2"}}}
	r := Build(det, prev, BuildOptions{})
	if e, _ := r.Lookup("pi", "ollama/m", "low"); e.Verified || e.ProbeRuns != 0 || e.State != StateUnprobed {
		t.Fatalf("an r12-v2 record kept: %+v", e)
	}
	r = Build(det, Registry{}, BuildOptions{Probes: map[string]ProbeRecord{"ollama/m": {Runs: 5, Passes: 5, Predicate: "r12-v2"}}})
	if e, _ := r.Lookup("pi", "ollama/m", "low"); e.Verified {
		t.Fatalf("an r12-v2 tally verified an entry: %+v", e)
	}
	prev.Entries[0].ProbePredicate = ProbePredicate
	if e, _ := Build(det, prev, BuildOptions{}).Lookup("pi", "ollama/m", "low"); !e.Verified {
		t.Fatalf("a current record was dropped: %+v", e)
	}
}
