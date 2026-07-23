package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
)

const agentCatalogSchemaVersion = 1

var agentCatalogUserHomeDir = os.UserHomeDir

type agentCatalogFile struct {
	SchemaVersion int                               `json:"schema_version"`
	Binaries      map[string]agentCatalogFileBinary `json:"binaries"`
}

type agentCatalogFileBinary struct {
	Models  []agentCatalogFileEntry `json:"models"`
	Efforts []agentCatalogFileEntry `json:"efforts"`
}

type agentCatalogFileEntry struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Enabled *bool  `json:"enabled"`
	// Routing metadata (#496): optional; absent on older/plain overlays.
	CapabilityTier string   `json:"capability_tier,omitempty"`
	CostIndex      float64  `json:"cost_index,omitempty"`
	LatencyIndex   float64  `json:"latency_index,omitempty"`
	Strengths      []string `json:"strengths,omitempty"`
	WorkClasses    []string `json:"work_classes,omitempty"`
}

func loadAgentCatalog(teamHome string) (agentcatalog.Catalog, []string) {
	merged := agentcatalog.Builtins()
	var warnings []string
	var paths []string
	if home, err := agentCatalogUserHomeDir(); err != nil {
		warnings = append(warnings, fmt.Sprintf("resolve home directory: %v; using built-in catalog", err))
	} else {
		paths = append(paths, filepath.Join(home, ".amq-squad", "catalog.json"))
	}
	if strings.TrimSpace(teamHome) != "" {
		projectPath := filepath.Join(filepath.Clean(teamHome), ".amq-squad", "catalog.json")
		if len(paths) == 0 || filepath.Clean(paths[len(paths)-1]) != filepath.Clean(projectPath) {
			paths = append(paths, projectPath)
		}
	}
	for _, path := range paths {
		layer, layerWarnings, ok := readAgentCatalogLayer(path)
		warnings = append(warnings, layerWarnings...)
		if ok {
			merged = agentcatalog.Merge(merged, layer)
		}
	}
	return merged, warnings
}

func readAgentCatalogLayer(path string) (agentcatalog.Catalog, []string, bool) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return agentcatalog.Catalog{}, nil, false
	}
	if err != nil {
		return agentcatalog.Catalog{}, []string{fmt.Sprintf("read %s: %v; ignoring this layer", path, err)}, false
	}
	var raw agentCatalogFile
	if err := json.Unmarshal(body, &raw); err != nil {
		return agentcatalog.Catalog{}, []string{fmt.Sprintf("parse %s: %v; ignoring this layer", path, err)}, false
	}
	if raw.SchemaVersion != agentCatalogSchemaVersion {
		return agentcatalog.Catalog{}, []string{fmt.Sprintf("%s uses unsupported schema_version %d (want %d); ignoring this layer", path, raw.SchemaVersion, agentCatalogSchemaVersion)}, false
	}
	layer := agentcatalog.Catalog{Binaries: map[string]agentcatalog.Binary{}}
	var warnings []string
	keys := make([]string, 0, len(raw.Binaries))
	for key := range raw.Binaries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if left == right {
			return keys[i] < keys[j]
		}
		return left < right
	})
	for _, rawBinary := range keys {
		binary := strings.ToLower(strings.TrimSpace(rawBinary))
		if binary == "" || containsControl(binary) {
			warnings = append(warnings, fmt.Sprintf("%s has invalid binary key %q; ignoring it", path, rawBinary))
			continue
		}
		rawValues := raw.Binaries[rawBinary]
		models, modelWarnings := convertAgentCatalogEntries(path, binary, "models", rawValues.Models)
		efforts, effortWarnings := convertAgentCatalogEntries(path, binary, "efforts", rawValues.Efforts)
		warnings = append(warnings, modelWarnings...)
		warnings = append(warnings, effortWarnings...)
		current := layer.Binaries[binary]
		current.Models = append(current.Models, models...)
		current.Efforts = append(current.Efforts, efforts...)
		layer.Binaries[binary] = current
	}
	return layer, warnings, true
}

func convertAgentCatalogEntries(path, binary, kind string, raw []agentCatalogFileEntry) ([]agentcatalog.Entry, []string) {
	entries := make([]agentcatalog.Entry, 0, len(raw))
	var warnings []string
	for i, item := range raw {
		value := strings.TrimSpace(item.Value)
		label := strings.TrimSpace(item.Label)
		if label == "" {
			label = value
		}
		normalized := strings.ToLower(value)
		if value == "" || containsControl(value) || containsControl(label) || normalized == "automatic" || normalized == "custom" || normalized == "keep" {
			warnings = append(warnings, fmt.Sprintf("%s %s.%s[%d] has invalid or reserved value %q; ignoring it", path, binary, kind, i, item.Value))
			continue
		}
		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}
		entries = append(entries, agentcatalog.Entry{
			Value: value, Label: label, Enabled: enabled,
			CapabilityTier: strings.TrimSpace(item.CapabilityTier),
			CostIndex:      item.CostIndex,
			LatencyIndex:   item.LatencyIndex,
			Strengths:      append([]string(nil), item.Strengths...),
			WorkClasses:    append([]string(nil), item.WorkClasses...),
		})
	}
	return entries, warnings
}

func containsControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func emitAgentCatalogWarnings(warnings []string) {
	for _, warning := range warnings {
		fmt.Fprintf(os.Stderr, "warning: catalog: %s\n", warning)
	}
}

func loadAgentCatalogAndWarn(teamHome string) agentcatalog.Catalog {
	catalog, warnings := loadAgentCatalog(teamHome)
	emitAgentCatalogWarnings(warnings)
	return catalog
}
