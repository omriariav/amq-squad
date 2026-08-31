// Package drafter runs bounded, headless LLM drafting commands configured by
// an amq-squad team profile. It deliberately owns only prose generation: the
// calling CLI keeps deterministic validation, staging, and lifecycle policy.
package drafter

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	BackendInSession = "in_session"
	BackendYoetz     = "yoetz"
	BackendClaude    = "claude"
	BackendCodex     = "codex"
	BackendCustom    = "custom"

	FailureInSession = "in_session"
	FailureError     = "error"

	DefaultTimeoutSeconds = 180
	MaxTimeoutSeconds     = 3600

	SourceProfile   = "profile"
	SourceGlobal    = "global"
	SourceInSession = "in_session"
)

var supportedTemplateTokens = []string{"{prompt}", "{out}", "{model}", "{effort}"}
var templateTokenPattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// Config is an external drafting policy. A profile may select preset backends
// and knobs, while Command is trusted only when this config came from the
// user-level global config. Command is an argv template, never a shell string.
// The optional {prompt} and {out} tokens expand to private temporary files;
// without them prompt input is sent on stdin and the draft is read from stdout.
// {model} and {effort} expose the corresponding knobs to custom templates.
type Config struct {
	Backend        string   `json:"backend,omitempty"`
	Chain          []string `json:"chain,omitempty"`
	Command        []string `json:"command,omitempty"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	OnFailure      string   `json:"on_failure,omitempty"`
}

// Resolution names which complete drafter block won precedence. Config is nil
// only for the implicit in-session default.
type Resolution struct {
	Config *Config
	Source string
}

func (c Config) EffectiveBackend() string {
	if len(c.Chain) > 0 {
		return strings.TrimSpace(c.Chain[0])
	}
	if backend := strings.TrimSpace(c.Backend); backend != "" {
		return backend
	}
	return BackendInSession
}

// EffectiveBackends returns the configured external backends in attempt order.
// The implicit in-session default is returned only when no external backend is
// configured; it is never appended to an explicit chain.
func (c Config) EffectiveBackends() []string {
	if len(c.Chain) > 0 {
		backends := make([]string, len(c.Chain))
		for i, backend := range c.Chain {
			backends[i] = strings.TrimSpace(backend)
		}
		return backends
	}
	return []string{c.EffectiveBackend()}
}

func (c Config) EffectiveTimeout() time.Duration {
	seconds := c.TimeoutSeconds
	if seconds == 0 {
		seconds = DefaultTimeoutSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (c Config) EffectiveFailureMode() string {
	if mode := strings.TrimSpace(c.OnFailure); mode != "" {
		return mode
	}
	return FailureInSession
}

// Resolve applies whole-block precedence. Profile fields are never merged with
// the global block because doing so could accidentally import a user command
// template into project-controlled policy.
func Resolve(profile, global *Config) (Resolution, error) {
	if profile != nil {
		if err := ValidateProfile(profile); err != nil {
			return Resolution{}, fmt.Errorf("profile drafter: %w", err)
		}
		return Resolution{Config: cloneConfig(profile), Source: SourceProfile}, nil
	}
	if global != nil {
		if err := ValidateGlobal(global); err != nil {
			return Resolution{}, fmt.Errorf("global drafter: %w", err)
		}
		return Resolution{Config: cloneConfig(global), Source: SourceGlobal}, nil
	}
	return Resolution{Source: SourceInSession}, nil
}

// ValidateProfile rejects executable argv from project-controlled team files.
func ValidateProfile(c *Config) error {
	if c == nil {
		return nil
	}
	if len(c.Command) > 0 {
		return fmt.Errorf("command: custom argv templates are allowed only in the user-level global config")
	}
	for _, backend := range c.EffectiveBackends() {
		if backend == BackendCustom {
			return fmt.Errorf("backend: custom is allowed only in the user-level global config")
		}
	}
	return Validate(c)
}

// ValidateGlobal validates trusted user-level drafter policy, including custom
// argv templates.
func ValidateGlobal(c *Config) error { return Validate(c) }

// Validate checks the persisted policy before any subprocess or temporary
// file is created.
func Validate(c *Config) error {
	if c == nil {
		return nil
	}
	if strings.TrimSpace(c.Backend) != "" && len(c.Chain) > 0 {
		return fmt.Errorf("backend and chain are mutually exclusive")
	}
	backends := c.EffectiveBackends()
	seen := make(map[string]struct{}, len(backends))
	for i, backend := range backends {
		if backend == "" {
			return fmt.Errorf("chain[%d]: backend cannot be empty", i)
		}
		switch backend {
		case BackendInSession, BackendYoetz, BackendClaude, BackendCodex, BackendCustom:
		default:
			return fmt.Errorf("backend: unsupported drafter backend %q: use %s, %s, %s, %s, or %s",
				backend, BackendInSession, BackendYoetz, BackendClaude, BackendCodex, BackendCustom)
		}
		if len(c.Chain) > 0 && backend == BackendInSession {
			return fmt.Errorf("chain[%d]: %s is implicit after chain exhaustion and cannot be configured as a hop", i, BackendInSession)
		}
		if _, ok := seen[backend]; ok {
			return fmt.Errorf("chain[%d]: duplicate backend %q", i, backend)
		}
		seen[backend] = struct{}{}
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("timeout_seconds: must be between 1 and %d when set", MaxTimeoutSeconds)
	}
	switch c.EffectiveFailureMode() {
	case FailureInSession, FailureError:
	default:
		return fmt.Errorf("on_failure: invalid mode %q: use %s or %s", c.OnFailure, FailureInSession, FailureError)
	}

	if len(c.Chain) == 0 && backends[0] == BackendInSession {
		if len(c.Command) > 0 || strings.TrimSpace(c.Model) != "" || strings.TrimSpace(c.Effort) != "" || c.TimeoutSeconds != 0 || strings.TrimSpace(c.OnFailure) != "" {
			return fmt.Errorf("backend %s cannot set chain, command, model, effort, timeout_seconds, or on_failure", BackendInSession)
		}
		return nil
	}
	_, hasCustom := seen[BackendCustom]
	if hasCustom && len(c.Command) == 0 {
		return fmt.Errorf("command: required for custom drafter backend")
	}
	if len(c.Chain) > 0 && len(c.Command) > 0 && !hasCustom {
		return fmt.Errorf("command: with chain it applies only to a custom backend, but chain has no custom hop")
	}
	for i, arg := range c.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("command[%d]: cannot be empty", i)
		}
		if strings.ContainsAny(arg, "\x00\r\n") {
			return fmt.Errorf("command[%d]: cannot contain NUL or newlines", i)
		}
		remaining := arg
		for _, token := range supportedTemplateTokens {
			remaining = strings.ReplaceAll(remaining, token, "")
		}
		if templateTokenPattern.MatchString(remaining) {
			return fmt.Errorf("command[%d]: unsupported template token in %q", i, arg)
		}
	}
	if len(c.Command) > 0 {
		if strings.TrimSpace(c.Model) != "" && !templateContains(c.Command, "{model}") {
			return fmt.Errorf("model: custom command must include {model}")
		}
		if strings.TrimSpace(c.Effort) != "" && !templateContains(c.Command, "{effort}") {
			return fmt.Errorf("effort: custom command must include {effort}")
		}
	}
	if templateContains(c.Command, "{model}") && strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("model: required when command contains {model}")
	}
	if templateContains(c.Command, "{effort}") && strings.TrimSpace(c.Effort) == "" {
		return fmt.Errorf("effort: required when command contains {effort}")
	}
	if len(c.Command) == 0 && len(backends) == 1 && backends[0] == BackendYoetz && strings.TrimSpace(c.Effort) != "" {
		return fmt.Errorf("effort: the yoetz preset has no effort flag; use a custom command template with {effort}")
	}
	// gh#760: the yoetz preset silently omits --model when Config.Model is
	// empty (buildCommand), which yoetz itself only rejects at invocation
	// time with "Error: provider is required". Refuse it up front instead,
	// for a lone yoetz backend or yoetz as any hop in a chain.
	if len(c.Command) == 0 && strings.TrimSpace(c.Model) == "" {
		for _, backend := range backends {
			if backend == BackendYoetz {
				return fmt.Errorf("model: required for the yoetz preset backend")
			}
		}
	}
	return nil
}

func (c Config) forBackend(backend string) Config {
	attempt := c
	attempt.Backend = backend
	attempt.Chain = nil
	if len(c.Chain) > 0 && backend != BackendCustom {
		attempt.Command = nil
	}
	if backend == BackendYoetz && len(attempt.Command) == 0 {
		attempt.Effort = ""
	}
	return attempt
}

func cloneConfig(c *Config) *Config {
	if c == nil {
		return nil
	}
	clone := *c
	clone.Chain = append([]string(nil), c.Chain...)
	clone.Command = append([]string(nil), c.Command...)
	return &clone
}

func templateContains(command []string, token string) bool {
	for _, arg := range command {
		if strings.Contains(arg, token) {
			return true
		}
	}
	return false
}
