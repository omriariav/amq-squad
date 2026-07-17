package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

var (
	modelUserConfigDir = os.UserConfigDir
	modelUserHomeDir   = os.UserHomeDir
	modelGetenv        = os.Getenv
	modelReadFile      = os.ReadFile
)

type amqSquadConfig struct {
	Model       string            `json:"model,omitempty"`
	CodexModel  string            `json:"codex_model,omitempty"`
	ClaudeModel string            `json:"claude_model,omitempty"`
	Models      map[string]string `json:"models,omitempty"`
}

func resolveModelForLaunch(binary, requested string, nativeArgs []string) string {
	if model := nativeModelArg(binary, nativeArgs); model != "" {
		return model
	}
	if model := strings.TrimSpace(requested); model != "" {
		return model
	}
	if model := amqSquadDefaultModel(binary); model != "" {
		return model
	}
	if normalizedAgentBinary(binary) == "codex" {
		return codexLocalDefaultModel(nativeArgs)
	}
	return ""
}

func nativeModelArg(binary string, args []string) string {
	binary = normalizedAgentBinary(binary)
	model := ""
	for i := 0; i < len(args); i++ {
		spec, inline, ok := nativeValueSpecForArg(binary, args[i])
		if !ok || spec.Canonical != "--model" {
			continue
		}
		if inline {
			model = strings.TrimSpace(compactNativeValue(args[i]))
			continue
		}
		if i+1 < len(args) {
			i++
			model = strings.TrimSpace(args[i])
		}
	}
	return model
}

func memberResolvedModel(m team.Member, overrides map[string]string, binaryArgs map[string][]string) string {
	requested := memberEffectiveModel(m, overrides)
	nativeArgs := composeBinaryArgs(m.Binary, binaryArgsFor(m.Binary, binaryArgs), m.ExtraArgs())
	return resolveModelForLaunch(m.Binary, requested, nativeArgs)
}

func amqSquadDefaultModel(binary string) string {
	cfg, ok := readAMQSquadConfig()
	if !ok {
		return ""
	}
	key := normalizedAgentBinary(binary)
	if cfg.Models != nil {
		if model := strings.TrimSpace(cfg.Models[key]); model != "" {
			return model
		}
	}
	switch key {
	case "codex":
		return strings.TrimSpace(cfg.CodexModel)
	case "claude":
		return strings.TrimSpace(cfg.ClaudeModel)
	default:
		return strings.TrimSpace(cfg.Model)
	}
}

func readAMQSquadConfig() (amqSquadConfig, bool) {
	for _, path := range amqSquadConfigPaths() {
		b, err := modelReadFile(path)
		if err != nil {
			continue
		}
		var cfg amqSquadConfig
		if err := json.Unmarshal(b, &cfg); err != nil {
			continue
		}
		return cfg, true
	}
	return amqSquadConfig{}, false
}

func amqSquadConfigPaths() []string {
	var paths []string
	if p := strings.TrimSpace(modelGetenv("AMQ_SQUAD_CONFIG")); p != "" {
		paths = append(paths, p)
	}
	if dir, err := modelUserConfigDir(); err == nil && strings.TrimSpace(dir) != "" {
		paths = append(paths, filepath.Join(dir, "amq-squad", "config.json"))
	}
	if home, err := modelUserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		paths = append(paths, filepath.Join(home, ".amq-squad", "config.json"))
	}
	return paths
}

func codexLocalDefaultModel(nativeArgs []string) string {
	for _, path := range codexConfigPaths(nativeArgs) {
		b, err := modelReadFile(path)
		if err != nil {
			continue
		}
		if model := parseCodexModelTOML(string(b)); model != "" {
			return model
		}
	}
	return ""
}

func codexConfigPaths(nativeArgs []string) []string {
	root := strings.TrimSpace(modelGetenv("CODEX_HOME"))
	if root == "" {
		if home, err := modelUserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			root = filepath.Join(home, ".codex")
		}
	}
	if root == "" {
		return nil
	}
	var paths []string
	if profile := codexProfileArg(nativeArgs); profile != "" {
		paths = append(paths, filepath.Join(root, profile+".config.toml"))
	}
	paths = append(paths, filepath.Join(root, "config.toml"))
	return paths
}

func codexProfileArg(args []string) string {
	for i, arg := range args {
		switch {
		case arg == "--profile" && i+1 < len(args):
			return strings.TrimSpace(args[i+1])
		case strings.HasPrefix(arg, "--profile="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "--profile="))
		}
	}
	return ""
}

var codexModelLine = regexp.MustCompile(`(?m)^\s*model\s*=\s*["']([^"']+)["']`)

func parseCodexModelTOML(body string) string {
	m := codexModelLine.FindStringSubmatch(body)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}
