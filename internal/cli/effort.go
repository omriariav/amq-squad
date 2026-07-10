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

// applyLaunchEffortOverrides returns an ephemeral roster copy with only the
// native effort arguments replaced. It never writes the profile and preserves
// all unrelated per-member args.
func applyLaunchEffortOverrides(members []team.Member, overrides map[string]string) ([]team.Member, error) {
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
		if err := applyMemberEffort(&out[i], effort); err != nil {
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
			if arg == "-c" && i+1 < len(args) && strings.HasPrefix(args[i+1], "model_reasoning_effort=") {
				i++
				continue
			}
			if strings.HasPrefix(arg, "model_reasoning_effort=") {
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
