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
