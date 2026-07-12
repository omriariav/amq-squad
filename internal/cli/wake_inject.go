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

func validateWakeInjectConfig(mode, via string, args []string, injectCmd string) error {
	if len(args) > 0 && strings.TrimSpace(via) == "" {
		return usageErrorf("--wake-inject-arg requires --wake-inject-via")
	}
	if mode == "none" && (strings.TrimSpace(via) != "" || len(args) > 0 || strings.TrimSpace(injectCmd) != "") {
		return usageErrorf("--wake-inject-mode none cannot be combined with injector flags")
	}
	return nil
}
