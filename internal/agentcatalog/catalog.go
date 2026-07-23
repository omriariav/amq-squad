// Package agentcatalog defines the advisory per-binary model and effort
// catalog shared by CLI validation and wizard pickers. Catalog membership is
// never launch authority: explicit values may pass through to a supported
// binary even when they are not listed here.
package agentcatalog

import "strings"

type Kind string

const (
	Models  Kind = "models"
	Efforts Kind = "efforts"
)

type Entry struct {
	Value   string `json:"value"`
	Label   string `json:"label,omitempty"`
	Enabled bool   `json:"enabled"`
	// Routing metadata (#496): optional, advisory-only fields the wizard's
	// recommendation engine (internal/wizard/routing.go) may consult. They
	// are meaningful on Models entries; Efforts entries leave them zero.
	// Absent on older catalogs and overlays, which keep working unchanged --
	// never required, never launch authority, never a hardcoded price.
	CapabilityTier string   `json:"capability_tier,omitempty"`
	CostIndex      float64  `json:"cost_index,omitempty"`
	LatencyIndex   float64  `json:"latency_index,omitempty"`
	Strengths      []string `json:"strengths,omitempty"`
	WorkClasses    []string `json:"work_classes,omitempty"`
}

// Capability tiers for Entry.CapabilityTier. Relative, not a universal
// ranking: a project overlay may retier or omit entirely.
const (
	TierFast     = "fast"
	TierBalanced = "balanced"
	TierFrontier = "frontier"
)

type Binary struct {
	Models  []Entry `json:"models,omitempty"`
	Efforts []Entry `json:"efforts,omitempty"`
}

type Catalog struct {
	Binaries map[string]Binary `json:"binaries"`
}

// Builtins returns a fresh catalog in stable picker order. Capability tiers
// below are this project's own advisory read of its default models, not a
// universal ranking; a project overlay can retier or omit them freely.
func Builtins() Catalog {
	return Catalog{Binaries: map[string]Binary{
		"claude": {
			Models: []Entry{
				modelEntry("fable", TierFrontier, []string{"planning", "review"}),
				modelEntry("opus", TierFrontier, []string{"planning", "implementation", "review"}),
				modelEntry("sonnet", TierBalanced, []string{"implementation"}),
				modelEntry("haiku", TierFast, []string{"implementation"}),
			},
			Efforts: entries("low", "medium", "high", "xhigh", "max"),
		},
		"codex": {
			Models: []Entry{
				modelEntry("gpt-5.6-sol", TierFrontier, []string{"planning", "implementation", "review", "security"}),
				modelEntry("gpt-5.6-terra", TierBalanced, []string{"implementation"}),
			},
			Efforts: entries("minimal", "low", "medium", "high", "xhigh"),
		},
	}}
}

func entries(values ...string) []Entry {
	out := make([]Entry, 0, len(values))
	for _, value := range values {
		out = append(out, Entry{Value: value, Label: value, Enabled: true})
	}
	return out
}

func modelEntry(value, tier string, strengths []string) Entry {
	return Entry{Value: value, Label: value, Enabled: true, CapabilityTier: tier, Strengths: strengths}
}

// Effective turns a zero catalog into the shipped defaults. This keeps older
// injected ProjectContext fixtures source-compatible while making production
// callers explicit about overlays.
func (c Catalog) Effective() Catalog {
	if len(c.Binaries) == 0 {
		return Builtins()
	}
	return c
}

func (c Catalog) Entries(binary string, kind Kind) []Entry {
	c = c.Effective()
	b, ok := c.Binaries[normalize(binary)]
	if !ok {
		return nil
	}
	entries := b.Models
	if kind == Efforts {
		entries = b.Efforts
	}
	out := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Enabled {
			out = append(out, entry)
		}
	}
	return out
}

// Resolve performs case-insensitive membership lookup and returns the winning
// entry's canonical spelling.
func (c Catalog) Resolve(binary string, kind Kind, value string) (Entry, bool) {
	want := normalize(value)
	for _, entry := range c.Entries(binary, kind) {
		if normalize(entry.Value) == want {
			return entry, true
		}
	}
	return Entry{}, false
}

// Merge overlays later entries without reordering an existing normalized
// value. Disabled entries stay in the internal sequence so a higher layer can
// re-enable them at their original position.
func Merge(base, overlay Catalog) Catalog {
	out := clone(base.Effective())
	for binary, layer := range overlay.Binaries {
		key := normalize(binary)
		current := out.Binaries[key]
		current.Models = mergeEntries(current.Models, layer.Models)
		current.Efforts = mergeEntries(current.Efforts, layer.Efforts)
		out.Binaries[key] = current
	}
	return out
}

func mergeEntries(base, overlay []Entry) []Entry {
	out := append([]Entry(nil), base...)
	positions := make(map[string]int, len(out))
	for i, entry := range out {
		positions[normalize(entry.Value)] = i
	}
	for _, entry := range overlay {
		if entry.Label == "" {
			entry.Label = entry.Value
		}
		key := normalize(entry.Value)
		if i, ok := positions[key]; ok {
			out[i] = entry
			continue
		}
		positions[key] = len(out)
		out = append(out, entry)
	}
	return out
}

func clone(in Catalog) Catalog {
	out := Catalog{Binaries: make(map[string]Binary, len(in.Binaries))}
	for binary, values := range in.Binaries {
		values.Models = append([]Entry(nil), values.Models...)
		values.Efforts = append([]Entry(nil), values.Efforts...)
		out.Binaries[normalize(binary)] = values
	}
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
