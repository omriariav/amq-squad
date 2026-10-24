package registry

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/agentcatalog"
)

func readFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// Acceptance 4: the S4 closure output of the 0.147.0 capture pin.
func TestClassifyCodexOutputS4Closure(t *testing.T) {
	astra := ClassifyCodexOutput("gpt-6-astra", "0.147.0", readFixture(t, "s4c-codex-0.147.0-gpt-6-astra.txt"))
	if astra.Usable || !astra.Known || !strings.Contains(astra.Reason, "newer version") {
		t.Fatalf("gpt-6-astra: %+v", astra)
	}
	gpt55 := ClassifyCodexOutput("gpt-5.5", "0.147.0", readFixture(t, "s4d-codex-0.147.0-gpt-5.5.txt"))
	if gpt55.Usable || !gpt55.Known || !strings.Contains(gpt55.Reason, "ChatGPT account") {
		t.Fatalf("gpt-5.5: %+v", gpt55)
	}
	// The usage-limit reset banner in the same panes is not a refusal.
	ok := ClassifyCodexOutput("gpt-5.6-sol", "0.147.0", "• You have 3 usage limit resets available. Run /usage to use one.\nOK\n")
	if !ok.Usable {
		t.Fatalf("plain OK: %+v", ok)
	}
}

func TestCheckCodexModelBeforeLaunch(t *testing.T) {
	if v := CheckCodexModel("0.147.0", "gpt-6-astra", nil); v.Usable || !v.Known {
		t.Fatalf("astra on 0.147.0: %+v", v)
	}
	if v := CheckCodexModel("0.147.0", "gpt-5.6-sol", nil); !v.Usable {
		t.Fatalf("sol on 0.147.0: %+v", v)
	}
	if v := CheckCodexModel("0.163.0", "gpt-5.6-sol", nil); v.Usable || v.Known {
		t.Fatalf("an unlisted version must leave the id unchecked: %+v", v)
	}
	probed := map[string]CodexVerdict{"gpt-5.6-sol": {Model: "gpt-5.6-sol", Version: "0.163.0", Known: true, Usable: true}}
	if v := CheckCodexModel("0.163.0", "gpt-5.6-sol", probed); !v.Usable {
		t.Fatalf("probed id: %+v", v)
	}
}

// The registry marks the refused id unusable and CheckLaunch refuses it.
func TestRefusedCodexModelIsUnusableBeforeLaunch(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".codex"), 0o700)
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte("{}"), 0o600)
	f := &fakeHost{
		paths: map[string]string{"codex": "/bin/codex"},
		out: map[string]string{
			"codex --version": "codex-cli 0.147.0",
			"codex exec --skip-git-repo-check --sandbox read-only --model gpt-6-astra Reply with the single word OK and nothing else.": readFixture(t, "s4c-codex-0.147.0-gpt-6-astra.txt"),
		},
	}
	env := f.env(home)
	det := Detect(context.Background(), env)
	verdict := ProbeCodexModel(context.Background(), env, "/bin/codex", "0.147.0", "gpt-6-astra")
	if verdict.Usable {
		t.Fatalf("probe verdict %+v", verdict)
	}
	cat := catalogWith("codex", "gpt-6-astra", "gpt-5.6-sol")
	r := Build(det, Registry{}, BuildOptions{Catalog: cat, CodexProbes: map[string]CodexVerdict{"gpt-6-astra": verdict}})
	astra, ok := r.Lookup("codex", "gpt-6-astra", "medium")
	if !ok || astra.Verified || astra.State != StateRefused {
		t.Fatalf("astra entry %+v", astra)
	}
	if err := CheckLaunch(r, "codex", "gpt-6-astra", "medium", "0.147.0"); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("launch check: %v", err)
	}
	if err := CheckLaunch(r, "codex", "gpt-5.6-sol", "medium", "0.147.0"); err != nil {
		t.Fatalf("sol launch check: %v", err)
	}
	if err := CheckLaunch(r, "codex", "gpt-5.6-sol", "medium", "0.163.0"); err == nil {
		t.Fatal("a codex upgrade after the check passed the launch check")
	}
	// The refusal survives a later run without a probe.
	again := Build(det, r, BuildOptions{Catalog: cat})
	if e, _ := again.Lookup("codex", "gpt-6-astra", "low"); e.State != StateRefused {
		t.Fatalf("refusal lost: %+v", e)
	}
}

func catalogWith(binary string, models ...string) agentcatalog.Catalog {
	var entries []agentcatalog.Entry
	for _, m := range models {
		entries = append(entries, agentcatalog.Entry{Value: m, Enabled: true, CapabilityTier: agentcatalog.TierFrontier})
	}
	return agentcatalog.Catalog{Binaries: map[string]agentcatalog.Binary{
		binary: {Models: entries, Efforts: []agentcatalog.Entry{{Value: "low", Enabled: true}, {Value: "medium", Enabled: true}}},
	}}
}
