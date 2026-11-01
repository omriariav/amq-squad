package claudeseat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// expectedPluginVersion is the Claude plugin's version for this release. No
// Go-side constant carries it in production code (claudeseat reads the
// embedded manifest at run time, so the config dir's plugin cache path
// follows the manifest), so this test is the one place the expectation lives.
const expectedPluginVersion = "3.1.0-rc.1"

// Phase 5 release prep: the plugin manifest the seats install (embedded into
// the binary) names the release version, and the file on disk under
// plugins/claude agrees with the embedded copy, so the two cannot drift. The
// marketplace file carries no version of its own to fall out of step.
func TestClaudePluginManifestVersionIsTheReleaseVersion(t *testing.T) {
	embedded, err := pluginVersion()
	if err != nil {
		t.Fatal(err)
	}
	if embedded != expectedPluginVersion {
		t.Fatalf("the embedded plugin.json version is %q, want %q", embedded, expectedPluginVersion)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "plugins", "claude", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var disk struct{ Version string }
	if err := json.Unmarshal(raw, &disk); err != nil || disk.Version != expectedPluginVersion {
		t.Fatalf("plugins/claude plugin.json version is %q (%v), want %q", disk.Version, err, expectedPluginVersion)
	}
	market, err := os.ReadFile(filepath.Join("..", "..", ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Version string
		Plugins []struct{ Version string }
	}
	if err := json.Unmarshal(market, &m); err != nil {
		t.Fatal(err)
	}
	if m.Version != "" && m.Version != expectedPluginVersion {
		t.Fatalf("marketplace.json version is %q, want %q", m.Version, expectedPluginVersion)
	}
	for _, p := range m.Plugins {
		if p.Version != "" && p.Version != expectedPluginVersion {
			t.Fatalf("a marketplace plugin entry version is %q, want %q", p.Version, expectedPluginVersion)
		}
	}
}
