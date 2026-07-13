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
}

type Binary struct {
	Models  []Entry `json:"models,omitempty"`
	Efforts []Entry `json:"efforts,omitempty"`
}

type Catalog struct {
	Binaries map[string]Binary `json:"binaries"`
}

// Builtins returns a fresh catalog in stable picker order.
func Builtins() Catalog {
	return Catalog{Binaries: map[string]Binary{
		"claude": {
			Models:  entries("fable", "opus", "sonnet", "haiku"),
			Efforts: entries("low", "medium", "high", "xhigh", "max"),
		},
		"codex": {
			Models:  entries("gpt-5.6-sol", "gpt-5.6-terra"),
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
