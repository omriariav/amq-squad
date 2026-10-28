//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/role"
)

// Brief 5-amq-A: `amq-squad pi shim` answers AMQ's pi adapter probes from
// the verified embedded runtime (node + the pi entry, no package, the
// probe's own agent dir, the forced environment) and refuses anything else.
func TestPiShimAnswersOnlyTheAdapterProbes(t *testing.T) {
	call, home := piFixture(t)
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
		for name, want := range map[string]string{"PI_OFFLINE": "1", "JITI_FS_CACHE": "false", "PI_CODING_AGENT_DIR": piruntime.AgentDirFor(home, piShimProbeSeat)} {
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
	if err := Run([]string{"pi", "run", "--seat", "scout", "--model", "ollama/qwen3.6:27b", "--thinking", "low", "--", "--session-id", "0b5e-id"}, "test"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(call.argv[len(call.argv)-2:], " "); got != "--session-id 0b5e-id" {
		t.Fatalf("argv %q does not end with the adapter's --session-id", call.argv)
	}
	tmp := filepath.Join(role.ExtensionDir(filepath.Join(root, "agents", "scout")), "tmp")
	for name, want := range map[string]string{"JITI_FS_CACHE": "false", "TMPDIR": tmp, "AMQ_SQUAD_AMQ": "/opt/amq/bin/amq", "AMQ_SQUAD_MODELS": "ollama/qwen3.6:27b", "PI_CODING_AGENT_DIR": piruntime.AgentDirFor(home, "scout")} {
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
	if err := piruntime.CheckShim(home, p, "/opt/b/amq-squad"); err == nil {
		t.Fatal("a shim for another binary passed")
	}
	if _, err := piruntime.EnsureShim(home, "/opt/b/amq-squad"); err != nil || piruntime.CheckShim(home, p, "/opt/b/amq-squad") != nil {
		t.Fatalf("rewrite for another binary: %v", err)
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
