package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
)

func TestLoadAgentCatalogMergesGlobalThenProjectWithoutCacheBleed(t *testing.T) {
	home := t.TempDir()
	projectA := t.TempDir()
	projectB := t.TempDir()
	oldHome := agentCatalogUserHomeDir
	agentCatalogUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { agentCatalogUserHomeDir = oldHome })
	writeCatalogFixture(t, filepath.Join(home, ".amq-squad", "catalog.json"), `{
  "schema_version": 1,
  "binaries": {"claude": {"efforts": [{"value":"ultra","label":"Global ultra"}]}}
}`)
	writeCatalogFixture(t, filepath.Join(projectA, ".amq-squad", "catalog.json"), `{
  "schema_version": 1,
  "binaries": {"CLAUDE": {"efforts": [
    {"value":"HIGH","label":"Project high","enabled":false},
    {"value":"ULTRA","label":"Project ultra"}
  ]}}
}`)

	catA, warnings := loadAgentCatalog(projectA)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got, want := catalogValues(catA, "claude", agentcatalog.Efforts), []string{"low", "medium", "xhigh", "max", "ULTRA"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("project A values = %v, want %v", got, want)
	}
	catB, warnings := loadAgentCatalog(projectB)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got, want := catalogValues(catB, "claude", agentcatalog.Efforts), []string{"low", "medium", "high", "xhigh", "max", "ultra"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("project B values = %v, want %v", got, want)
	}
}

func TestLoadAgentCatalogBadLayersWarnAndFallBack(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	oldHome := agentCatalogUserHomeDir
	agentCatalogUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { agentCatalogUserHomeDir = oldHome })
	writeCatalogFixture(t, filepath.Join(home, ".amq-squad", "catalog.json"), `{bad`)
	writeCatalogFixture(t, filepath.Join(project, ".amq-squad", "catalog.json"), `{
  "schema_version": 1,
  "binaries": {"claude": {"efforts": [
    {"value":"custom"}, {"value":"future","enabled":true}
  ]}}
}`)
	cat, warnings := loadAgentCatalog(project)
	if len(warnings) != 2 || !strings.Contains(strings.Join(warnings, "\n"), "parse") || !strings.Contains(strings.Join(warnings, "\n"), "reserved") {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, ok := cat.Resolve("claude", agentcatalog.Efforts, "future"); !ok {
		t.Fatal("valid project entry should survive invalid sibling and malformed global layer")
	}
	if _, ok := cat.Resolve("claude", agentcatalog.Efforts, "custom"); ok {
		t.Fatal("reserved entry should be ignored")
	}
}

func TestLoadAgentCatalogUnreadableLayerWarnsAndContinues(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	oldHome := agentCatalogUserHomeDir
	agentCatalogUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { agentCatalogUserHomeDir = oldHome })
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad", "catalog.json"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeCatalogFixture(t, filepath.Join(project, ".amq-squad", "catalog.json"), `{
  "schema_version": 1,
  "binaries": {"claude": {"efforts": [{"value":"future"}]}}
}`)

	cat, warnings := loadAgentCatalog(project)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "read ") || !strings.Contains(warnings[0], "ignoring this layer") {
		t.Fatalf("warnings = %v", warnings)
	}
	if _, ok := cat.Resolve("claude", agentcatalog.Efforts, "future"); !ok {
		t.Fatal("read failure in global layer blocked valid project overlay")
	}
}

func TestLoadAgentCatalogMissingIsSilentAndUnknownSchemaFallsBack(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	oldHome := agentCatalogUserHomeDir
	agentCatalogUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { agentCatalogUserHomeDir = oldHome })

	cat, warnings := loadAgentCatalog(project)
	if len(warnings) != 0 {
		t.Fatalf("missing files warnings = %v", warnings)
	}
	if got, want := catalogValues(cat, "claude", agentcatalog.Efforts), []string{"low", "medium", "high", "xhigh", "max"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("built-in fallback = %v, want %v", got, want)
	}

	writeCatalogFixture(t, filepath.Join(project, ".amq-squad", "catalog.json"), `{
  "schema_version": 2,
  "binaries": {"claude": {"efforts": [{"value":"future"}]}}
}`)
	cat, warnings = loadAgentCatalog(project)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "unsupported schema_version 2") {
		t.Fatalf("schema warnings = %v", warnings)
	}
	if _, ok := cat.Resolve("claude", agentcatalog.Efforts, "future"); ok {
		t.Fatal("unsupported layer changed the effective catalog")
	}
}

func TestLoadAgentCatalogCaseVariantBinaryKeysMergeDeterministically(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	oldHome := agentCatalogUserHomeDir
	agentCatalogUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { agentCatalogUserHomeDir = oldHome })
	writeCatalogFixture(t, filepath.Join(project, ".amq-squad", "catalog.json"), `{
  "schema_version": 1,
  "binaries": {
    "CLAUDE": {"efforts": [{"value":"high","label":"Upper high","enabled":false}]},
    "claude": {"efforts": [{"value":"HIGH","label":"Lower high"},{"value":"future"}]}
  }
}`)

	cat, warnings := loadAgentCatalog(project)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if got, want := catalogValues(cat, "claude", agentcatalog.Efforts), []string{"low", "medium", "HIGH", "xhigh", "max", "future"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
	entry, ok := cat.Resolve("claude", agentcatalog.Efforts, "high")
	if !ok || entry.Value != "HIGH" || entry.Label != "Lower high" {
		t.Fatalf("resolved high = %+v, %t", entry, ok)
	}
}

func catalogValues(cat agentcatalog.Catalog, binary string, kind agentcatalog.Kind) []string {
	var out []string
	for _, entry := range cat.Entries(binary, kind) {
		out = append(out, entry.Value)
	}
	return out
}

func writeCatalogFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
