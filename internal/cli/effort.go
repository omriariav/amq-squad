package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/agentcatalog"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

const effortAutomatic = "automatic"

// parseEffortOverrides parses the wizard-facing role=effort flag. Effort is
// deliberately not a persisted field: callers normalize each value into the
// member's existing CodexArgs or ClaudeArgs field.
func parseEffortOverrides(raw string) (map[string]string, error) {
	overrides, err := parseKV(raw)
	if err != nil {
		return nil, fmt.Errorf("parse --effort: %w", err)
	}
	overrides = lowercaseKeys(overrides)
	for role, value := range overrides {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("parse --effort: role %q has an empty effort", role)
		}
		overrides[role] = value
	}
	return overrides, nil
}

func validateEffortOverrideKeys(overrides map[string]string, selected map[string]bool, selectionSource string) error {
	var unknown []string
	for role := range overrides {
		if !selected[role] {
			unknown = append(unknown, role)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	// The message names the ACTUAL selection source (#597 guard 4). Under
	// --from-profile the roster comes from the clone and no --roles flag was
	// ever passed, so naming --roles sent the operator to a flag they had not
	// used. Where --roles genuinely IS the source, saying so is more useful
	// than a generic phrase, which is why this is parameterized rather than
	// flattened to one wording.
	return fmt.Errorf("--effort names role(s) not selected by %s: %s", selectionSource, strings.Join(unknown, ", "))
}

func effortArgsForBinary(binary, effort string) ([]string, error) {
	args, _, err := effortArgsForBinaryCatalog(binary, effort, agentcatalog.Builtins())
	return args, err
}

// effortArgsForBinaryCatalog translates any explicit tier for a supported
// binary. The bool reports catalog membership; an unknown tier is advisory,
// not an error, and retains its exact trimmed spelling.
func effortArgsForBinaryCatalog(binary, effort string, catalog agentcatalog.Catalog) ([]string, bool, error) {
	binary = normalizedAgentBinary(binary)
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return nil, false, fmt.Errorf("--effort cannot be empty; use %q to clear native effort args", effortAutomatic)
	}
	if strings.EqualFold(effort, effortAutomatic) {
		return nil, true, nil
	}
	if binary != "codex" && binary != "claude" {
		return nil, false, fmt.Errorf("--effort cannot target binary %q; choose codex or claude", binary)
	}
	known := false
	if entry, ok := catalog.Resolve(binary, agentcatalog.Efforts, effort); ok {
		effort = entry.Value
		known = true
	}
	if binary == "codex" {
		return []string{"-c", "model_reasoning_effort=" + effort}, known, nil
	}
	return []string{"--effort", effort}, known, nil
}

func applyMemberEffort(member *team.Member, effort string) error {
	return applyMemberEffortCatalog(member, effort, agentcatalog.Builtins())
}

func applyMemberEffortCatalog(member *team.Member, effort string, catalog agentcatalog.Catalog) error {
	return applyMemberEffortCatalogMode(member, effort, catalog, true)
}

func applyMemberEffortCatalogMode(member *team.Member, effort string, catalog agentcatalog.Catalog, warnUnknown bool) error {
	args, known, err := effortArgsForBinaryCatalog(member.Binary, effort, catalog)
	if err != nil {
		return fmt.Errorf("--effort %s=%s: %w", member.Role, effort, err)
	}
	if len(args) > 0 && !known && warnUnknown {
		fmt.Fprintf(os.Stderr, "warning: effort %s=%s is not in the merged catalog for %s; passing the exact value through, and the underlying binary may reject it\n", member.Role, strings.TrimSpace(effort), normalizedAgentBinary(member.Binary))
	}
	if len(args) == 0 {
		return nil
	}
	switch normalizedAgentBinary(member.Binary) {
	case "codex":
		member.CodexArgs = append(member.CodexArgs, args...)
	case "claude":
		member.ClaudeArgs = append(member.ClaudeArgs, args...)
	}
	return nil
}

