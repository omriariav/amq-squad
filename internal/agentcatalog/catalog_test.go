package agentcatalog

import (
	"reflect"
	"testing"
)

func TestBuiltinsIncludeCurrentClaudeAndCodexEffortsInPickerOrder(t *testing.T) {
	cat := Builtins()
	values := func(binary string) []string {
		var out []string
		for _, entry := range cat.Entries(binary, Efforts) {
			out = append(out, entry.Value)
		}
		return out
	}
	if got, want := values("claude"), []string{"low", "medium", "high", "xhigh", "max"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claude efforts = %v, want %v", got, want)
	}
	if got, want := values("codex"), []string{"minimal", "low", "medium", "high", "xhigh"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codex efforts = %v, want %v", got, want)
	}
}

func TestBuiltinsCarryCapabilityTiersForRouting(t *testing.T) {
	cat := Builtins()
	tier := func(binary, model string) string {
		entry, ok := cat.Resolve(binary, Models, model)
		if !ok {
			t.Fatalf("model %s/%s not found", binary, model)
		}
		return entry.CapabilityTier
	}
	if got := tier("claude", "opus"); got != TierFrontier {
		t.Fatalf("claude/opus tier = %q, want %q", got, TierFrontier)
	}
	if got := tier("claude", "haiku"); got != TierFast {
		t.Fatalf("claude/haiku tier = %q, want %q", got, TierFast)
	}
	if got := tier("codex", "gpt-5.6-sol"); got != TierFrontier {
		t.Fatalf("codex/gpt-5.6-sol tier = %q, want %q", got, TierFrontier)
	}
	// Efforts are not model-tiered; routing metadata stays zero on them.
	for _, entry := range cat.Entries("claude", Efforts) {
		if entry.CapabilityTier != "" {
			t.Fatalf("effort entry %q unexpectedly carries a capability tier", entry.Value)
		}
	}
}

func TestMergePreservesRoutingMetadataOnWinningEntry(t *testing.T) {
	base := Builtins()
	overlay := Catalog{Binaries: map[string]Binary{"claude": {Models: []Entry{
		{Value: "opus", Label: "Opus (project)", Enabled: true, CapabilityTier: TierBalanced, CostIndex: 3, Strengths: []string{"taste"}},
	}}}}
	got := Merge(base, overlay)
	entry, ok := got.Resolve("claude", Models, "opus")
	if !ok {
		t.Fatal("expected opus to resolve")
	}
	if entry.CapabilityTier != TierBalanced || entry.CostIndex != 3 || len(entry.Strengths) != 1 || entry.Strengths[0] != "taste" {
		t.Fatalf("merge did not carry overlay routing metadata onto the winning entry: %+v", entry)
	}
}

func TestMergePreservesPositionAndCanonicalWinningEntry(t *testing.T) {
	base := Builtins()
	global := Catalog{Binaries: map[string]Binary{"Claude": {Efforts: []Entry{
		{Value: "HIGH", Label: "High global", Enabled: true},
		{Value: "ultra", Label: "Ultra", Enabled: true},
	}}}}
	project := Catalog{Binaries: map[string]Binary{"claude": {Efforts: []Entry{
		{Value: "high", Label: "High project", Enabled: false},
		{Value: "ULTRA", Label: "Project ultra", Enabled: true},
	}}}}
	got := Merge(Merge(base, global), project)
	var values []string
	for _, entry := range got.Entries("CLAUDE", Efforts) {
		values = append(values, entry.Value)
	}
	want := []string{"low", "medium", "xhigh", "max", "ULTRA"}
	if got := values; !reflect.DeepEqual(got, want) {
		t.Fatalf("enabled values = %v, want %v", got, want)
	}
	entry, ok := got.Resolve("claude", Efforts, "ultra")
	if !ok || entry.Value != "ULTRA" || entry.Label != "Project ultra" {
		t.Fatalf("resolved ultra = %+v, %t", entry, ok)
	}
}
