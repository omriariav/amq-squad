package cli

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

// piFixture installs a tiny packed runtime as the embedded payload and an exec
// seam that records the exec instead of performing it.
func piFixture(t *testing.T) (*piExecCall, string) {
	t.Helper()
	tree := t.TempDir()
	for rel, body := range map[string]string{
		"node/bin/node":              "#!/bin/sh\n",
		"pi/node_modules/pi/cli.js":  "x\n",
		"pi/package.json":            `{"name":"@amq-squad/pi"}`,
		"pi/extensions/amq-squad.ts": "export default function () {}\n",
		"tools/rg":                   "#!/bin/sh\n",
	} {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasPrefix(body, "#!") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	packed, err := piruntime.Pack(tree, piruntime.PackSpec{
		Version: "pi-test", Platform: runtime.GOOS + "-" + runtime.GOARCH, PiVersion: "1.3.0", NodeVersion: "22.21.1",
		Executable: "node/bin/node", Args: []string{"pi/node_modules/pi/cli.js"}, Package: "pi", Tools: map[string]string{"rg": "tools/rg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	oldLoad, oldExec := piLoad, piExec
	t.Cleanup(func() { piLoad, piExec = oldLoad, oldExec })
	piLoad = func() (piruntime.Embedded, error) {
		return piruntime.ParseEmbedded(packed.Manifest, packed.Digest, func() ([]byte, error) { return packed.Payload, nil })
	}
	call := &piExecCall{}
	piExec = func(path string, argv, env []string) error {
		call.path, call.argv, call.env = path, argv, env
		return nil
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	return call, home
}

type piExecCall struct {
	path      string
	argv, env []string
}

func (c *piExecCall) getenv(name string) string {
	for _, kv := range c.env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// Acceptance 1 (shape): the lead execs Node from the unpacked runtime with
// absolute paths; no pi or node is looked up on PATH.
func TestLeadExecsTheUnpackedRuntime(t *testing.T) {
	call, home := piFixture(t)
	chdir(t, t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("GEMINI_API_KEY", "leak")
	if err := Run([]string{"lead", "--model", "ollama/qwen3.6:27b", "--", "-p", "hi"}, "test"); err != nil {
		t.Fatal(err)
	}
	runtimeBase := filepath.Join(home, ".amq-squad", "runtime")
	if !strings.HasPrefix(call.path, runtimeBase+string(filepath.Separator)) || !strings.HasSuffix(call.path, "node/bin/node") {
		t.Fatalf("exec path %q is not the unpacked Node", call.path)
	}
	if call.argv[0] != call.path || !strings.HasSuffix(call.argv[1], "pi/node_modules/pi/cli.js") || call.argv[2] != "-e" || !strings.HasSuffix(call.argv[3], "/pi") {
		t.Fatalf("argv %v", call.argv)
	}
	if got := call.argv[len(call.argv)-2:]; got[0] != "-p" || got[1] != "hi" {
		t.Fatalf("pi args not passed: %v", call.argv)
	}
	if call.getenv("GEMINI_API_KEY") != "" {
		t.Fatal("GEMINI_API_KEY reached the lead")
	}
	for name, want := range map[string]string{"AM_ME": "lead", "AM_SESSION": "solo", "PI_OFFLINE": "1", "PI_TELEMETRY": "0", "AMQ_SQUAD_MODELS": "ollama/qwen3.6:27b"} {
		if got := call.getenv(name); got != want {
			t.Fatalf("%s=%q, want %q", name, got, want)
		}
	}
	if !filepath.IsAbs(call.getenv("AMQ_SQUAD_BIN")) {
		t.Fatalf("AMQ_SQUAD_BIN %q is not absolute", call.getenv("AMQ_SQUAD_BIN"))
	}
}

// Acceptance 7 (R7): a first run in a fresh git repository leaves git status
// clean and writes only under ~/.amq-squad/.
func TestLeadSoloFirstRunWritesOnlyUnderHome(t *testing.T) {
	call, home := piFixture(t)
	repo := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "init")
	sub := filepath.Join(repo, "pkg")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, sub)
	before := snapshot(t, repo)
	if err := Run([]string{"lead"}, "test"); err != nil {
		t.Fatal(err)
	}
	if call.path == "" {
		t.Fatal("lead did not exec")
	}
	if out := git("status", "--porcelain", "--untracked-files=all", "--ignored"); strings.TrimSpace(out) != "" {
		t.Fatalf("git status not clean after the first run:\n%s", out)
	}
	if after := snapshot(t, repo); !equalSets(before, after) {
		t.Fatalf("the first run wrote inside the repository: %v", diffSets(before, after))
	}
	// Every write is under ~/.amq-squad.
	var outside []string
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if rel, _ := filepath.Rel(home, p); !strings.HasPrefix(rel, ".amq-squad"+string(filepath.Separator)) {
				outside = append(outside, rel)
			}
		}
		return nil
	})
	if len(outside) > 0 {
		t.Fatalf("writes outside ~/.amq-squad: %v", outside)
	}
	root := call.getenv("AM_ROOT")
	if !strings.HasPrefix(root, filepath.Join(home, ".amq-squad", "solo")) {
		t.Fatalf("solo root %q is not under ~/.amq-squad/solo", root)
	}
	if realRepo, _ := filepath.EvalSymlinks(repo); call.getenv("AMQ_SQUAD_PROJECT") != realRepo {
		t.Fatalf("project %q, want the repository top level %q", call.getenv("AMQ_SQUAD_PROJECT"), realRepo)
	}
	tmp := call.getenv("TMPDIR")
	if want := filepath.Join(root, "agents", "lead", "extensions", "io.github.omriariav.amq-squad", "tmp"); tmp != want {
		t.Fatalf("lead TMPDIR %q, want the seat's extension dir %q", tmp, want)
	}
	if fi, err := os.Stat(tmp); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("lead TMPDIR not 0700: %v %v", fi, err)
	}
	if call.getenv("JITI_FS_CACHE") != "false" {
		t.Fatal("JITI_FS_CACHE=false not set for the lead")
	}
	role, err := os.ReadFile(call.getenv("AMQ_SQUAD_ROLE_PATH"))
	if err != nil || !strings.Contains(string(role), "solo mode") {
		t.Fatalf("solo role not written: %v", err)
	}
}

func TestLeadTeamHasNoWriteTools(t *testing.T) {
	call, _ := piFixture(t)
	chdir(t, t.TempDir())
	if err := Run([]string{"lead", "--team"}, "test"); err != nil {
		t.Fatal(err)
	}
	// Amendment 26: the team lead's tools are exactly the native AMQ and
	// task tools, the repository-scoped read and list, and status; no bash,
	// read, edit or write.
	tools := ""
	for i, a := range call.argv {
		if a == "--tools" && i+1 < len(call.argv) {
			tools = call.argv[i+1]
		}
	}
	want := "amq_send,amq_drain,amq_reply,amq_task_list,amq_task_add,amq_task_reset,amq_repo_read,amq_repo_list,amq_status"
	if tools != want || call.getenv("AMQ_SQUAD_LEAD") != "1" {
		t.Fatalf("team lead --tools %q (want %q), lead=%q", tools, want, call.getenv("AMQ_SQUAD_LEAD"))
	}
	for _, builtin := range []string{"bash", "read", "edit", "write", "grep", "find", "ls"} {
		for _, tl := range strings.Split(tools, ",") {
			if tl == builtin {
				t.Fatalf("team lead has the %s tool", builtin)
			}
		}
	}
	// The solo lead stays a coding agent (plan 3.2): no tool restriction.
	solo, _ := piFixture(t)
	if err := Run([]string{"lead"}, "test"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(solo.argv, " "), "--tools") {
		t.Fatalf("solo lead restricted: %v", solo.argv)
	}
	role, _ := os.ReadFile(call.getenv("AMQ_SQUAD_ROLE_PATH"))
	if !strings.Contains(string(role), "never write or edit code") {
		t.Fatal("team lead role not delivered")
	}
}

// The resume refusal of amendment 23 on the pi side.
func TestPiRunResumeRefusal(t *testing.T) {
	_, home := piFixture(t)
	cwd := t.TempDir()
	chdir(t, cwd)
	root := t.TempDir()
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "pi-1")
	ledger := piruntime.LedgerPath(root, "pi-1")
	if err := os.MkdirAll(filepath.Dir(ledger), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte(`{"event":"tool_result","tool":"bash","outcome":"ok"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := Run([]string{"pi", "run", "--resume", "--", "--session-id", "nonce-1"}, "test")
	var rr *piruntime.ResumeRefusal
	if !errors.As(err, &rr) || rr.Code != "pi_resume_context_lost" {
		t.Fatalf("resume with a lost session not refused: %v", err)
	}
	agent := piruntime.AgentDirFor(home, "pi-1")
	real, _ := filepath.EvalSymlinks(cwd)
	for _, dir := range []string{cwd, real} {
		f := filepath.Join(piruntime.SessionDir(agent, dir), "2026-10-24T10-00-00-000Z_nonce-1.jsonl")
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Run([]string{"pi", "run", "--resume", "--", "--session-id", "nonce-1"}, "test"); err != nil {
		t.Fatalf("resume with its session refused: %v", err)
	}
}

func TestPiRunRefusesTamperedRuntime(t *testing.T) {
	call, home := piFixture(t)
	chdir(t, t.TempDir())
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil {
		t.Fatal(err)
	}
	node := call.path
	_ = os.Chmod(node, 0o755)
	if err := os.WriteFile(node, []byte("#!/bin/sh\necho evil\n"), 0o555); err != nil {
		t.Fatal(err)
	}
	call.path = ""
	err := Run([]string{"pi", "run", "--no-package"}, "test")
	var rej *piruntime.Reject
	if !errors.As(err, &rej) || call.path != "" {
		t.Fatalf("tampered runtime launched: %v", err)
	}
	_ = home
}

func TestPiWithoutPayload(t *testing.T) {
	old := piLoad
	t.Cleanup(func() { piLoad = old })
	piLoad = func() (piruntime.Embedded, error) { return piruntime.Embedded{}, piruntime.ErrNotEmbedded }
	if err := Run([]string{"lead"}, "test"); !errors.Is(err, piruntime.ErrNotEmbedded) {
		t.Fatalf("lead without a payload: %v", err)
	}
}

func snapshot(t *testing.T, dir string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(dir, p)
			if !strings.HasPrefix(rel, ".git") {
				out[rel] = true
			}
		}
		return nil
	})
	return out
}

func equalSets(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func diffSets(a, b map[string]bool) []string {
	var d []string
	for k := range b {
		if !a[k] {
			d = append(d, "+"+k)
		}
	}
	for k := range a {
		if !b[k] {
			d = append(d, "-"+k)
		}
	}
	return d
}
