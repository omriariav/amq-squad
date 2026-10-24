package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// Rebase onto 2b-3: the lead's model comes from the registry's model row
// (the first verified pi entry, effort medium preferred); the constant is
// the fallback when the registry has no usable entry or is absent.
func TestLeadModelFromRegistry(t *testing.T) {
	call, home := piFixture(t)
	chdir(t, t.TempDir())
	if m, _, src := leadModel(home); m != defaultLeadModel || !strings.Contains(src, "no registry") {
		t.Fatalf("no registry: %s (%s)", m, src)
	}
	reg := registry.Registry{Schema: registry.Schema, Entries: []registry.Entry{
		{Runtime: "claude", Provider: "anthropic", Model: "sonnet", Effort: "medium", Verified: true},
		{Runtime: "pi", Provider: "ollama", Model: "ollama/qwen2.5:32b", Effort: "low", Verified: false},
		{Runtime: "pi", Provider: "ollama", Model: "ollama/gemma4:31b", Effort: "low", Verified: true},
		{Runtime: "pi", Provider: "ollama", Model: "ollama/gemma4:31b", Effort: "medium", Verified: true},
	}}
	if err := registry.Write(registry.Path(home), reg); err != nil {
		t.Fatal(err)
	}
	m, eff, src := leadModel(home)
	if m != "ollama/gemma4:31b" || eff != "medium" || !strings.Contains(src, "registry entry pi/ollama/gemma4:31b/medium") {
		t.Fatalf("registry pick: %s %s (%s)", m, eff, src)
	}
	if _, stderr, err := captureOutput(t, func() error { return Run([]string{"lead"}, "test") }); err != nil {
		t.Fatal(err)
	} else if !strings.Contains(stderr, "registry entry") {
		t.Fatalf("lead did not say where its model came from:\n%s", stderr)
	}
	if call.getenv("AMQ_SQUAD_MODELS") != "ollama/gemma4:31b" {
		t.Fatalf("lead models %q", call.getenv("AMQ_SQUAD_MODELS"))
	}
	// No verified pi entry: the fallback, said so.
	reg.Entries = reg.Entries[:2]
	if err := registry.Write(registry.Path(home), reg); err != nil {
		t.Fatal(err)
	}
	if m, _, src := leadModel(home); m != defaultLeadModel || !strings.Contains(src, "no verified pi entry") {
		t.Fatalf("no verified entry: %s (%s)", m, src)
	}
	_ = os.Remove(registry.Path(home))
}

// Rebase onto 2b-3: with the payload embedded, doctor's pi runtime row is
// piruntime's (opened from HOME, the embedded digest), the verified Runtime
// is kept for the R12 probe (its PiArgv), and the registry learns the
// embedded runtime's digest and directory.
func TestDoctorHostUsesEmbeddedRuntime(t *testing.T) {
	_, home := piFixture(t)
	chdir(t, t.TempDir())
	h := &doctorHost{Home: home}
	if c, _, ok := h.runtimeCheck(registry.Detection{}); ok || !strings.Contains(c.Detail, "not unpacked") {
		t.Fatalf("before unpack: %+v %v", c, ok)
	}
	if err := Run([]string{"pi", "run", "--no-package"}, "test"); err != nil {
		t.Fatal(err)
	}
	c, res, ok := h.runtimeCheck(registry.Detection{})
	if !ok || c.Status != doctorOK || h.piRuntime == nil || res.Dir != h.piRuntime.Dir {
		t.Fatalf("verified row: %+v ok=%v rt=%v", c, ok, h.piRuntime)
	}
	argv := h.piRuntime.PiArgv()
	if len(argv) < 2 || argv[0] != h.piRuntime.Exe() {
		t.Fatalf("probe argv %v", argv)
	}
	prevSHA, prevDir := registry.EmbeddedRuntimeManifestSHA256, registry.EmbeddedRuntimeDirName
	t.Cleanup(func() { registry.EmbeddedRuntimeManifestSHA256, registry.EmbeddedRuntimeDirName = prevSHA, prevDir })
	syncRegistryRuntimePin()
	if registry.EmbeddedRuntimeManifestSHA256 == "" || registry.PiRuntimeDir(home) != h.piRuntime.Dir {
		t.Fatalf("registry pin: %q %q", registry.EmbeddedRuntimeManifestSHA256, registry.PiRuntimeDir(home))
	}
}
