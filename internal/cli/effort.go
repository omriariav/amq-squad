package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

const effortAutomatic = "automatic"

var effortOptionsByBinary = map[string]map[string]bool{
	"codex": {
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
	},
	"claude": {
		"low":    true,
		"medium": true,
		"high":   true,
	},
}

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
		value = strings.ToLower(strings.TrimSpace(value))
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
	binary = normalizedAgentBinary(binary)
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" || effort == effortAutomatic {
		return nil, nil
	}
	options, ok := effortOptionsByBinary[binary]
	if !ok {
		return nil, fmt.Errorf("--effort cannot target binary %q; choose codex or claude", binary)
	}
	if !options[effort] {
		allowed := make([]string, 0, len(options)+1)
		allowed = append(allowed, effortAutomatic)
		for option := range options {
			allowed = append(allowed, option)
		}
		sort.Strings(allowed)
		return nil, fmt.Errorf("unsupported %s effort %q (want %s)", binary, effort, strings.Join(allowed, ", "))
	}
	if binary == "codex" {
		return []string{"-c", "model_reasoning_effort=" + effort}, nil
	}
	return []string{"--effort", effort}, nil
}

func applyMemberEffort(member *team.Member, effort string) error {
	args, err := effortArgsForBinary(member.Binary, effort)
	if err != nil {
		return fmt.Errorf("--effort %s=%s: %w", member.Role, effort, err)
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
	args := member.ExtraArgs()
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "model_reasoning_effort=") {
			return strings.TrimPrefix(args[i+1], "model_reasoning_effort=")
		}
		if strings.HasPrefix(args[i], "model_reasoning_effort=") {
			return strings.TrimPrefix(args[i], "model_reasoning_effort=")
		}
		if args[i] == "--effort" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], "--effort=") {
			return strings.TrimPrefix(args[i], "--effort=")
		}
	}
	return effortAutomatic
}
