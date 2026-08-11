package drafter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const outputLimit = 4 << 20

type Request struct {
	Prompt           string
	WorkingDirectory string
}

// Evidence records the exact argv submitted to the operating system. Prompt
// content is kept in a private file or stdin and therefore does not leak into
// command evidence.
type Evidence struct {
	Backend        string    `json:"backend"`
	Command        []string  `json:"command,omitempty"`
	CommandDisplay string    `json:"command_display,omitempty"`
	Model          string    `json:"model,omitempty"`
	Effort         string    `json:"effort,omitempty"`
	TimeoutSeconds int       `json:"timeout_seconds,omitempty"`
	StartedAt      time.Time `json:"started_at,omitempty"`
	DurationMillis int64     `json:"duration_millis,omitempty"`
	ExitCode       int       `json:"exit_code"`
	Stderr         string    `json:"stderr,omitempty"`
	Failure        string    `json:"failure,omitempty"`
}

type Result struct {
	Text         string     `json:"text,omitempty"`
	UseInSession bool       `json:"use_in_session,omitempty"`
	Fallback     bool       `json:"fallback,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	Remedy       string     `json:"remedy,omitempty"`
	Evidence     Evidence   `json:"evidence"`
	Attempts     []Evidence `json:"attempts,omitempty"`
}

// RunError is returned only when on_failure=error. Result still carries the
// command evidence so callers can report the failed exact invocation.
type RunError struct {
	Cause    error
	Evidence Evidence
	Attempts []Evidence
	Remedy   string
}

func (e *RunError) Error() string {
	return fmt.Sprintf("drafter backend failed: %v; remedy: %s", e.Cause, e.Remedy)
}

func (e *RunError) Unwrap() error { return e.Cause }

func Run(ctx context.Context, config *Config, request Request) (Result, error) {
	if err := Validate(config); err != nil {
		return Result{}, fmt.Errorf("invalid drafter config: %w", err)
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return Result{}, fmt.Errorf("drafter prompt cannot be empty")
	}
	if config == nil || config.EffectiveBackend() == BackendInSession {
		return Result{
			UseInSession: true,
			Reason:       "the profile uses the in-session drafter",
			Remedy:       "complete the generated prompt in the active session, or configure the profile drafter block",
			Evidence:     Evidence{Backend: BackendInSession, ExitCode: 0},
		}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	tempDir, err := os.MkdirTemp("", "amq-squad-drafter-")
	if err != nil {
		return Result{}, fmt.Errorf("create drafter temp directory: %w", err)
	}
	promptPath := tempDir + string(os.PathSeparator) + "prompt.md"
	cleanupPaths := []string{promptPath}
	defer func() { cleanupTempFiles(append(cleanupPaths, tempDir)...) }()
	if err := os.WriteFile(promptPath, []byte(request.Prompt), 0o600); err != nil {
		return Result{}, fmt.Errorf("write drafter prompt: %w", err)
	}

	backends := config.EffectiveBackends()
	attempts := make([]Evidence, 0, len(backends))
	failures := make([]error, 0, len(backends))
	for i, backend := range backends {
		outputPath := tempDir + string(os.PathSeparator) + fmt.Sprintf("draft-%d.md", i)
		cleanupPaths = append(cleanupPaths, outputPath)
		attempt := config.forBackend(backend)
		text, evidence, cause := runAttempt(ctx, attempt, request, promptPath, outputPath)
		if cause != nil {
			evidence.Failure = cause.Error()
			attempts = append(attempts, evidence)
			failures = append(failures, fmt.Errorf("%s: %w", backend, cause))
			continue
		}
		attempts = append(attempts, evidence)
		return Result{Text: text, Evidence: evidence, Attempts: attempts}, nil
	}
	return failureResult(*config, attempts, failures)
}

func runAttempt(ctx context.Context, config Config, request Request, promptPath, outputPath string) (string, Evidence, error) {
	argv, promptFile, outputFile, err := buildCommand(config, promptPath, outputPath)
	if err != nil {
		return "", Evidence{Backend: config.EffectiveBackend(), ExitCode: -1}, err
	}
	timeout := config.EffectiveTimeout()
	started := time.Now()
	evidence := Evidence{
		Backend:        config.EffectiveBackend(),
		Command:        append([]string(nil), argv...),
		CommandDisplay: commandDisplay(argv),
		Model:          strings.TrimSpace(config.Model),
		Effort:         strings.TrimSpace(config.Effort),
		TimeoutSeconds: int(timeout / time.Second),
		ExitCode:       -1,
		StartedAt:      started.UTC(),
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = strings.TrimSpace(request.WorkingDirectory)
	if !promptFile {
		cmd.Stdin = strings.NewReader(request.Prompt)
	}
	var stdout, stderr limitedBuffer
	stdout.limit = outputLimit
	stderr.limit = outputLimit
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	evidence.DurationMillis = time.Since(started).Milliseconds()
	evidence.Stderr = strings.TrimSpace(stderr.String())
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			evidence.ExitCode = exitErr.ExitCode()
		}
		return "", evidence, commandFailure(runCtx, runErr, evidence.Stderr)
	}
	evidence.ExitCode = 0

	var output []byte
	if outputFile {
		output, err = readFileLimited(outputPath, outputLimit)
		if err != nil {
			return "", evidence, fmt.Errorf("read configured {out} file: %w", err)
		}
	} else {
		output = stdout.Bytes()
	}
	text := strings.TrimSpace(string(output))
	if text == "" {
		return "", evidence, fmt.Errorf("command produced an empty draft")
	}
	return text, evidence, nil
}

func readFileLimited(path string, limit int) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	output, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(output) > limit {
		return nil, fmt.Errorf("output exceeds %d-byte limit", limit)
	}
	return output, nil
}

func buildCommand(config Config, promptPath, outputPath string) ([]string, bool, bool, error) {
	if err := Validate(&config); err != nil {
		return nil, false, false, fmt.Errorf("invalid drafter config: %w", err)
	}
	var command []string
	if len(config.Command) > 0 {
		command = append([]string(nil), config.Command...)
	} else {
		switch config.EffectiveBackend() {
		case BackendYoetz:
			command = []string{"yoetz", "ask", "--prompt-file", "{prompt}", "--output-final", "{out}", "--response-format", "text", "--no-notify"}
			if model := strings.TrimSpace(config.Model); model != "" {
				command = append(command, "--model", model)
			}
		case BackendClaude:
			command = []string{"claude", "-p", "--output-format", "text", "--no-session-persistence"}
			if model := strings.TrimSpace(config.Model); model != "" {
				command = append(command, "--model", model)
			}
			if effort := strings.TrimSpace(config.Effort); effort != "" {
				command = append(command, "--effort", effort)
			}
		case BackendCodex:
			command = []string{"codex", "exec", "--ephemeral", "--color", "never"}
			if model := strings.TrimSpace(config.Model); model != "" {
				command = append(command, "--model", model)
			}
			if effort := strings.TrimSpace(config.Effort); effort != "" {
				command = append(command, "--config", "model_reasoning_effort="+effort)
			}
			command = append(command, "-")
		default:
			return nil, false, false, fmt.Errorf("drafter backend %q has no command preset", config.EffectiveBackend())
		}
	}

	promptFile := templateContains(command, "{prompt}")
	outputFile := templateContains(command, "{out}")
	values := map[string]string{
		"{prompt}": promptPath,
		"{out}":    outputPath,
		"{model}":  strings.TrimSpace(config.Model),
		"{effort}": strings.TrimSpace(config.Effort),
	}
	for i, arg := range command {
		for token, value := range values {
			arg = strings.ReplaceAll(arg, token, value)
		}
		command[i] = arg
	}
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return nil, false, false, fmt.Errorf("drafter command cannot be empty")
	}
	return command, promptFile, outputFile, nil
}

func failureResult(config Config, attempts []Evidence, failures []error) (Result, error) {
	evidence := Evidence{Backend: BackendInSession, ExitCode: 0}
	if len(attempts) > 0 {
		evidence = attempts[len(attempts)-1]
	}
	cause := errors.Join(failures...)
	if cause == nil {
		cause = fmt.Errorf("configured drafter chain had no external attempts")
	}
	remedy := fmt.Sprintf("check the configured %s commands and provider credentials", strings.Join(config.EffectiveBackends(), ", "))
	if config.EffectiveFailureMode() == FailureInSession {
		remedy += ", then retry; this run may be completed from the generated prompt in the active session"
		return Result{
			UseInSession: true,
			Fallback:     true,
			Reason:       cause.Error(),
			Remedy:       remedy,
			Evidence:     evidence,
			Attempts:     append([]Evidence(nil), attempts...),
		}, nil
	}
	remedy += " or set drafter.on_failure to in_session"
	return Result{Reason: cause.Error(), Remedy: remedy, Evidence: evidence, Attempts: append([]Evidence(nil), attempts...)}, &RunError{Cause: cause, Evidence: evidence, Attempts: append([]Evidence(nil), attempts...), Remedy: remedy}
}

func commandFailure(ctx context.Context, runErr error, stderr string) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("command timed out: %w", ctx.Err())
	}
	if stderr != "" {
		return fmt.Errorf("command failed: %w: %s", runErr, stderr)
	}
	return fmt.Errorf("command failed: %w", runErr)
}

func cleanupTempFiles(paths ...string) {
	for _, path := range paths {
		_ = os.Remove(path)
	}
}

func commandDisplay(argv []string) string {
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		if arg != "" && !strings.ContainsAny(arg, " \t\r\n'\"\\$`!&|;()<>*?[]{}") {
			quoted[i] = arg
			continue
		}
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}

type limitedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.limit - b.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.Buffer.Write(p)
	}
	return original, nil
}

func (b *limitedBuffer) Bytes() []byte { return append([]byte(nil), b.Buffer.Bytes()...) }
