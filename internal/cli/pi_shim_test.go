//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/role"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
)

// Brief 5-amq-A: `amq-squad pi shim` answers AMQ's pi adapter probes from
// the verified embedded runtime (node + the pi entry, no package, the
// probe's own agent dir, the forced environment) and refuses anything else.
func TestPiShimAnswersOnlyTheAdapterProbes(t *testing.T) {
	call, home := piFixture(t)
	old := piProbe
	t.Cleanup(func() { piProbe = old })
	piProbe = piRun
	// A timing request from the caller's environment never reaches the
	// probe: AMQ parses its combined output (the live row's finding).
	t.Setenv("AMQ_SQUAD_PI_TIMING", "1")
	for _, probe := range []string{"--version", "--help"} {
		if err := Run([]string{"pi", "shim", probe}, "test"); err != nil {
			t.Fatalf("%s: %v", probe, err)
		}
		if !strings.HasPrefix(call.path, filepath.Join(home, ".amq-squad", "runtime")+string(filepath.Separator)) || call.argv[len(call.argv)-1] != probe {
			t.Fatalf("%s: exec %s %q, want the unpacked node with the probe", probe, call.path, call.argv)
		}
		for _, a := range call.argv {
			if a == "-e" {
				t.Fatalf("%s: the probe loaded the package: %q", probe, call.argv)
			}
		}
		for name, want := range map[string]string{"PI_OFFLINE": "1", "JITI_FS_CACHE": "false", "PI_CODING_AGENT_DIR": piruntime.AgentDirFor(home, piShimProbeSeat), "AMQ_SQUAD_PI_TIMING": ""} {
			if got := call.getenv(name); got != want {
				t.Fatalf("%s: %s=%q, want %q", probe, name, got, want)
			}
		}
	}
	call.path = ""
	for _, args := range [][]string{{}, {"--session-id", "x"}, {"--version", "--help"}, {"-p", "hi"}} {
		if err := Run(append([]string{"pi", "shim"}, args...), "test"); err == nil || call.path != "" {
			t.Fatalf("pi shim %q ran pi: %v", args, err)
		}
	}
}

// Brief 5-amq-A acceptance 2 (unit): the exec seat-exec performs for a pi
// seat, `amq-squad pi run --seat H -- <adapter argv>`, starts the embedded
// runtime with the package, the adapter's --session-id, the forced
// environment, the seat's private TMPDIR under its extension directory, and
// the launch record's amq.
func TestPiRunAsSeatExecStartsIt(t *testing.T) {
	call, home := piFixture(t)
	chdir(t, t.TempDir())
	root := filepath.Join(home, "proj", ".agent-mail", "s1")
	t.Setenv("AM_ME", "scout")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AMQ_SQUAD_AMQ", "/opt/amq/bin/amq")
	t.Setenv("JITI_FS_CACHE", "true")
	t.Setenv("TMPDIR", "/tmp")
	// Fix round: the seat's pi process gets no NODE_PATH and an empty
	// NODE_OPTIONS whatever the parent says (either would load code from
	// outside the digest), and the compile cache off.
	t.Setenv("NODE_OPTIONS", "--require=/tmp/evil.cjs")
	t.Setenv("NODE_PATH", "/tmp/mods")
	t.Setenv("NODE_DISABLE_COMPILE_CACHE", "0")
	if err := Run([]string{"pi", "run", "--seat", "scout", "--model", "ollama/qwen3.6:27b", "--thinking", "low", "--", "--session-id", "0b5e-id"}, "test"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(call.argv[len(call.argv)-2:], " "); got != "--session-id 0b5e-id" {
		t.Fatalf("argv %q does not end with the adapter's --session-id", call.argv)
	}
	tmp := filepath.Join(role.ExtensionDir(filepath.Join(root, "agents", "scout")), "tmp")
	nodeOptions := 0
	for _, kv := range call.env {
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case name == "NODE_PATH":
			t.Error("NODE_PATH reached the seat's pi")
		case name == "NODE_OPTIONS" && value != "":
			t.Error("a non-empty NODE_OPTIONS reached the seat's pi")
		case name == "NODE_OPTIONS":
			nodeOptions++
		}
	}
	if nodeOptions != 1 {
		t.Errorf("NODE_OPTIONS is set empty %d times in the seat's pi environment, want exactly once (forced empty)", nodeOptions)
	}
	for name, want := range map[string]string{"NODE_DISABLE_COMPILE_CACHE": "1", "JITI_FS_CACHE": "false", "TMPDIR": tmp, "AMQ_SQUAD_AMQ": "/opt/amq/bin/amq", "AMQ_SQUAD_MODELS": "ollama/qwen3.6:27b", "PI_CODING_AGENT_DIR": piruntime.AgentDirFor(home, "scout")} {
		if got := call.getenv(name); got != want {
			t.Errorf("%s=%q, want %q", name, got, want)
		}
	}
	if fi, err := os.Stat(tmp); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("seat TMPDIR %s: %v %v", tmp, fi, err)
	}
}

// The shim file: written once for a binary and left alone after (its
// identity, which AMQ's trust subject binds, is stable across launches),
// rewritten for another binary, refused in place of a symlink.
func TestEnsureShimIsStableAndRefusesASymlink(t *testing.T) {
	home := t.TempDir()
	p, err := piruntime.EnsureShim(home, "/opt/a/amq-squad")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(p)
	if _, err := piruntime.EnsureShim(home, "/opt/a/amq-squad"); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("an unchanged shim was rewritten")
	}
	if err := piruntime.CheckShim(p, "/opt/b/amq-squad"); err == nil {
		t.Fatal("a shim for another binary passed")
	}
	if _, err := piruntime.EnsureShim(home, "/opt/b/amq-squad"); err != nil || piruntime.CheckShim(p, "/opt/b/amq-squad") != nil {
		t.Fatalf("rewrite for another binary: %v", err)
	}
	// Codex A-P3: a shim of another mode (0600: AMQ's Apply could not
	// execute it) is not accepted and EnsureShim repairs it to 0700.
	os.Chmod(p, 0o600)
	if err := piruntime.CheckShim(p, "/opt/b/amq-squad"); err == nil {
		t.Fatal("a 0600 shim passed the check")
	}
	if _, err := piruntime.EnsureShim(home, "/opt/b/amq-squad"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode().Perm() != 0o700 {
		t.Fatalf("EnsureShim left mode %s", fi.Mode())
	}
	// Codex A-P2: the write is atomicfile's (a random O_EXCL temp): a
	// symlink planted at the old predictable temp name is never followed.
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("keep me\n"), 0o600)
	os.Symlink(victim, p+".tmp-"+strconv.Itoa(os.Getpid()))
	os.Remove(p)
	if _, err := piruntime.EnsureShim(home, "/opt/b/amq-squad"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "keep me\n" {
		t.Fatal("the shim write followed a planted temp-name symlink")
	}
	if err := piruntime.CheckShim(p, "/opt/b/amq-squad"); err != nil {
		t.Fatalf("after the write: %v", err)
	}
	os.Remove(p)
	os.Symlink("/bin/sh", p)
	if _, err := piruntime.EnsureShim(home, "/opt/b/amq-squad"); err == nil {
		t.Fatal("a symlink in the shim's place was followed or replaced")
	}
	if _, err := piruntime.ShimScript("relative/amq-squad"); err == nil {
		t.Fatal("a relative binary path was named")
	}
	if _, err := piruntime.ShimScript("/opt/it's/amq-squad"); err == nil {
		t.Fatal("a quote in the binary path was named")
	}
}

// Codex A-P1, end to end: the launcher argv seat-exec composes for a pi seat
// whose record declares ANTHROPIC_API_KEY (credential_needs) starts pi with
// that credential and no other of the kind, in the record's agent dir.
func TestPiSeatKeepsItsDeclaredCredential(t *testing.T) {
	call, home := piFixture(t)
	chdir(t, t.TempDir())
	root := filepath.Join(home, "proj", ".agent-mail", "s1")
	t.Setenv("AM_ME", "scout")
	t.Setenv("AM_ROOT", root)
	t.Setenv("ANTHROPIC_API_KEY", "declared")
	t.Setenv("OPENAI_API_KEY", "undeclared")
	t.Setenv("GEMINI_API_KEY", "undeclared")
	agentDir := filepath.Join(home, "recorded-agent-dir")
	rec := launch.Record{Handle: "scout", Runtime: "pi", CredNeeds: []string{"ANTHROPIC_API_KEY"}, PiAgentDir: agentDir}
	argv := seatexec.PiLauncherArgv("/opt/amq-squad", rec, []string{"/shim/pi", "--session-id", "id-1"})
	if err := Run(argv[1:], "test"); err != nil {
		t.Fatal(err)
	}
	if call.getenv("ANTHROPIC_API_KEY") != "declared" {
		t.Error("the declared credential did not reach the seat's pi")
	}
	for _, name := range []string{"OPENAI_API_KEY", "GEMINI_API_KEY"} {
		if call.getenv(name) != "" {
			t.Errorf("%s reached the seat's pi", name)
		}
	}
	if call.getenv("PI_CODING_AGENT_DIR") != agentDir {
		t.Errorf("PI_CODING_AGENT_DIR is not the record's agent dir")
	}
}

// Re-pass item 1: a FIFO named pi in the shim's place (no writer) is
// refused at once, not waited on: the open does not block and the fstat
// refuses a non-regular file. Re-pass item 2: the full mode must be 0700, so
// an owned, byte-correct shim with the setuid bit fails the check and is
// repaired to exactly 0700.
func TestShimRefusesAFIFOAndRepairsSpecialBits(t *testing.T) {
	home := t.TempDir()
	p, err := piruntime.EnsureShim(home, "/opt/a/amq-squad")
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o700|os.ModeSetuid)
	if fi, _ := os.Stat(p); fi.Mode()&os.ModeSetuid == 0 {
		t.Skip("this filesystem does not keep the setuid bit")
	}
	if err := piruntime.CheckShim(p, "/opt/a/amq-squad"); err == nil {
		t.Fatal("a setuid shim passed the check")
	}
	if _, err := piruntime.EnsureShim(home, "/opt/a/amq-squad"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(p); fi.Mode() != 0o700 {
		t.Fatalf("EnsureShim left mode %s, want exactly 0700", fi.Mode())
	}

	os.Remove(p)
	if err := syscall.Mkfifo(p, 0o700); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- piruntime.CheckShim(p, "/opt/a/amq-squad") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO passed the shim check")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the shim check blocked on a FIFO with no writer")
	}
}
