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
)

var supportedTemplateTokens = []string{"{prompt}", "{out}", "{model}", "{effort}"}
var templateTokenPattern = regexp.MustCompile(`\{[A-Za-z_][A-Za-z0-9_]*\}`)

// Config is the profile-scoped external drafting policy. Command is an argv
// template, never a shell string. The optional {prompt} and {out} tokens expand
// to private temporary files; without them prompt input is sent on stdin and
// the draft is read from stdout. {model} and {effort} expose the corresponding
// knobs to custom templates.
type Config struct {
	Backend        string   `json:"backend,omitempty"`
	Command        []string `json:"command,omitempty"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	OnFailure      string   `json:"on_failure,omitempty"`
}

func (c Config) EffectiveBackend() string {
	if backend := strings.TrimSpace(c.Backend); backend != "" {
		return backend
	}
	return BackendInSession
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

// Validate checks the persisted policy before any subprocess or temporary
// file is created.
func Validate(c *Config) error {
	if c == nil {
		return nil
	}
	backend := c.EffectiveBackend()
	switch backend {
	case BackendInSession, BackendYoetz, BackendClaude, BackendCodex, BackendCustom:
	default:
		return fmt.Errorf("backend: unsupported drafter backend %q: use %s, %s, %s, %s, or %s",
			backend, BackendInSession, BackendYoetz, BackendClaude, BackendCodex, BackendCustom)
	}
	if c.TimeoutSeconds < 0 || c.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("timeout_seconds: must be between 1 and %d when set", MaxTimeoutSeconds)
	}
	switch c.EffectiveFailureMode() {
	case FailureInSession, FailureError:
	default:
		return fmt.Errorf("on_failure: invalid mode %q: use %s or %s", c.OnFailure, FailureInSession, FailureError)
	}

	if backend == BackendInSession {
		if len(c.Command) > 0 || strings.TrimSpace(c.Model) != "" || strings.TrimSpace(c.Effort) != "" || c.TimeoutSeconds != 0 || strings.TrimSpace(c.OnFailure) != "" {
			return fmt.Errorf("backend %s cannot set command, model, effort, timeout_seconds, or on_failure", BackendInSession)
		}
		return nil
	}
	if backend == BackendCustom && len(c.Command) == 0 {
		return fmt.Errorf("command: required for custom drafter backend")
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
	if len(c.Command) == 0 && backend == BackendYoetz && strings.TrimSpace(c.Effort) != "" {
		return fmt.Errorf("effort: the yoetz preset has no effort flag; use a custom command template with {effort}")
	}
	return nil
}

func templateContains(command []string, token string) bool {
	for _, arg := range command {
		if strings.Contains(arg, token) {
			return true
		}
	}
	return false
}
