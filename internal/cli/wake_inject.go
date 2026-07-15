package cli

import (
	"strings"
)

func normalizeWakeInjectMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", "auto", "raw", "paste", "none":
		return mode, nil
	default:
		return "", usageErrorf("--wake-inject-mode must be auto, raw, paste, or none")
	}
}

// resolveWakeInjectModeForBinary makes managed interactive agents explicit:
// Codex and Claude use raw injection when no mode (or auto) was selected.
// Unknown/custom agent binaries retain AMQ's prior unspecified/auto behavior.
// The binary is the underlying member binary, not an optional launcher.
func resolveWakeInjectModeForBinary(mode, binary string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "" && mode != "auto" {
		return mode
	}
	switch normalizedAgentBinary(binary) {
	case "codex", "claude":
		return "raw"
	default:
		return mode
	}
}

func validateWakeInjectConfig(mode, via string, args []string, injectCmd string) error {
	if len(args) > 0 && strings.TrimSpace(via) == "" {
		return usageErrorf("--wake-inject-arg requires --wake-inject-via")
	}
	if mode == "none" && (strings.TrimSpace(via) != "" || len(args) > 0 || strings.TrimSpace(injectCmd) != "") {
		return usageErrorf("--wake-inject-mode none cannot be combined with injector flags")
	}
	return nil
}
