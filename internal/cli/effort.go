package cli

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/agentcatalog"
	"github.com/omriariav/amq-squad/v2/internal/team"
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

func validateEffortOverrideKeys(overrides map[string]string, selected map[string]bool) error {
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
	return fmt.Errorf("--effort names role(s) not selected by --roles: %s", strings.Join(unknown, ", "))
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

func memberEffort(member team.Member) string {
	binary := normalizedAgentBinary(member.Binary)
	args := member.ExtraArgs()
	for i := 0; i < len(args); i++ {
		if binary == "codex" {
			if value, consumed, ok := codexEffortArg(args, i); ok {
				return value
			} else if consumed {
				i++
			}
		}
		if binary == "claude" && args[i] == "--effort" && i+1 < len(args) {
			return args[i+1]
		}
		if binary == "claude" && strings.HasPrefix(args[i], "--effort=") {
			return strings.TrimPrefix(args[i], "--effort=")
		}
	}
	return effortAutomatic
}

// applyLaunchEffortOverrides returns an ephemeral roster copy with only the
// native effort arguments replaced. It never writes the profile and preserves
// all unrelated per-member args.
func applyLaunchEffortOverrides(members []team.Member, overrides map[string]string) ([]team.Member, error) {
	return applyLaunchEffortOverridesCatalog(members, overrides, agentcatalog.Builtins())
}

func applyLaunchEffortOverridesCatalog(members []team.Member, overrides map[string]string, catalog agentcatalog.Catalog) ([]team.Member, error) {
	return applyLaunchEffortOverridesCatalogMode(members, overrides, catalog, true)
}

func applyLaunchEffortOverridesCatalogMode(members []team.Member, overrides map[string]string, catalog agentcatalog.Catalog, warnUnknown bool) ([]team.Member, error) {
	if len(overrides) == 0 {
		return append([]team.Member(nil), members...), nil
	}
	known := make(map[string]bool, len(members))
	for _, member := range members {
		known[strings.ToLower(strings.TrimSpace(member.Role))] = true
	}
	var unknown []string
	for role := range overrides {
		if !known[strings.ToLower(strings.TrimSpace(role))] {
			unknown = append(unknown, role)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("--effort names role(s) not present in the selected profile: %s", strings.Join(unknown, ", "))
	}
	out := append([]team.Member(nil), members...)
	for i := range out {
		effort, ok := overrides[strings.ToLower(strings.TrimSpace(out[i].Role))]
		if !ok {
			continue
		}
		switch normalizedAgentBinary(out[i].Binary) {
		case "codex":
			out[i].CodexArgs = stripNativeEffortArgs(out[i].CodexArgs, "codex")
		case "claude":
			out[i].ClaudeArgs = stripNativeEffortArgs(out[i].ClaudeArgs, "claude")
		}
		if err := applyMemberEffortCatalogMode(&out[i], effort, catalog, warnUnknown); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func stripNativeEffortArgs(args []string, binary string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if binary == "codex" {
			if _, consumed, ok := codexEffortArg(args, i); ok {
				if consumed {
					i++
				}
				continue
			}
		}
		if binary == "claude" {
			if arg == "--effort" && i+1 < len(args) {
				i++
				continue
			}
			if strings.HasPrefix(arg, "--effort=") {
				continue
			}
		}
		out = append(out, arg)
	}
	return out
}

// codexEffortArg recognizes every supported split and inline spelling of the
// Codex config option. consumed reports whether the next argv token belongs to
// the current option, even when it names a different config key.
func codexEffortArg(args []string, index int) (value string, consumed bool, ok bool) {
	arg := args[index]
	spec, inline, known := nativeValueSpecForArg("codex", arg)
	if !known || spec.Canonical != "--config" {
		if strings.HasPrefix(arg, "model_reasoning_effort=") {
			return strings.TrimPrefix(arg, "model_reasoning_effort="), false, true
		}
		return "", false, false
	}
	raw := ""
	if inline {
		raw = compactNativeValue(arg)
	} else if index+1 < len(args) {
		raw = args[index+1]
		consumed = true
	}
	key, value, found := strings.Cut(raw, "=")
	if !found || strings.TrimSpace(key) != "model_reasoning_effort" {
		return "", consumed, false
	}
	return value, consumed, true
}
