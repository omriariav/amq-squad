// Package userconfig owns the machine-scoped amq-squad configuration file.
package userconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
)

const ConfigEnv = "AMQ_SQUAD_CONFIG"

// Config is the user-level machine configuration. Keep unrelated settings in
// this shared top-level shape so setup can update drafter policy without
// discarding model defaults used by existing installations.
type Config struct {
	Model       string            `json:"model,omitempty"`
	CodexModel  string            `json:"codex_model,omitempty"`
	ClaudeModel string            `json:"claude_model,omitempty"`
	Models      map[string]string `json:"models,omitempty"`
	Drafter     *drafter.Config   `json:"drafter,omitempty"`
}

// Path returns the canonical ~/.config/amq-squad/config.json path.
// AMQ_SQUAD_CONFIG is an explicit whole-file override for non-interactive/CI
// provisioning and tests; it never triggers path or backend auto-discovery.
func Path() (string, error) {
	if override := strings.TrimSpace(os.Getenv(ConfigEnv)); override != "" {
		return filepath.Clean(override), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for amq-squad user config: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", fmt.Errorf("resolve home directory for amq-squad user config: empty path")
	}
	return filepath.Join(home, ".config", "amq-squad", "config.json"), nil
}

// Read loads the canonical user config. A missing file is the empty config;
// malformed or invalid policy fails closed instead of silently selecting a
// different backend.
func Read() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	cfg, err := ReadFile(path)
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	return cfg, err
}

// ReadFile strictly decodes one explicit user-config path. It is exported for
// setup, deterministic tests, and callers that already resolved the path.
func ReadFile(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse user config %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("parse user config %s: multiple JSON values", path)
		}
		return Config{}, fmt.Errorf("parse user config %s: %w", path, err)
	}
	if err := drafter.ValidateGlobal(cfg.Drafter); err != nil {
		return Config{}, fmt.Errorf("validate user config %s: drafter: %w", path, err)
	}
	return cfg, nil
}

// Write validates and atomically persists the user-level config at Path.
// The containing directory and file stay private because trusted global
// drafter policy may include a custom argv template.
func Write(cfg Config) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}
	if err := WriteFile(path, cfg); err != nil {
		return "", err
	}
	return path, nil
}

// WriteFile validates and atomically persists one explicit config path. It is
// exported for setup tests and callers that already resolved the destination.
func WriteFile(path string, cfg Config) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("write user config: path cannot be empty")
	}
	if err := drafter.ValidateGlobal(cfg.Drafter); err != nil {
		return fmt.Errorf("write user config %s: drafter: %w", path, err)
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode user config %s: %w", path, err)
	}
	body = append(body, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create user config directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary user config in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary user config %s: %w", tmpPath, err)
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temporary user config %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temporary user config %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary user config %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install user config %s: %w", path, err)
	}
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
