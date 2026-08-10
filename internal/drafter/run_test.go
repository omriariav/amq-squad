package drafter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRunInSessionDoesNotInvokeACommand(t *testing.T) {
	result, err := Run(context.Background(), nil, Request{Prompt: "Draft this."})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.UseInSession || result.Fallback || result.Evidence.Backend != BackendInSession || len(result.Evidence.Command) != 0 {
		t.Fatalf("result = %+v, want direct in-session outcome", result)
	}
}

func TestRunCustomFileTemplateCapturesExactCommandEvidence(t *testing.T) {
	script := writeExecutable(t, `#!/bin/sh
prompt=$1
out=$2
model=$3
effort=$4
first=$(sed -n '1p' "$prompt")
printf '%s|%s|%s\n' "$first" "$model" "$effort" > "$out"
`)
	cfg := &Config{
		Backend: BackendCustom,
		Command: []string{script, "{prompt}", "{out}", "{model}", "{effort}"},
		Model:   "fast-model",
		Effort:  "low",
	}
	result, err := Run(context.Background(), cfg, Request{Prompt: "first line\nsecond line", WorkingDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Text != "first line|fast-model|low" {
		t.Fatalf("Text = %q", result.Text)
	}
	if result.UseInSession || result.Fallback || result.Evidence.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Evidence.Command) != 5 || result.Evidence.Command[0] != script || result.Evidence.Command[3] != "fast-model" || result.Evidence.Command[4] != "low" {
		t.Fatalf("evidence command = %v", result.Evidence.Command)
	}
	if strings.Contains(result.Evidence.CommandDisplay, "first line") || result.Evidence.CommandDisplay == "" {
		t.Fatalf("command display leaked prompt or was empty: %q", result.Evidence.CommandDisplay)
	}
}

func TestRunCustomDefaultsToStdinAndStdout(t *testing.T) {
	script := writeExecutable(t, `#!/bin/sh
IFS= read -r line
printf 'draft:%s\n' "$line"
`)
	cfg := &Config{Backend: BackendCustom, Command: []string{script}}
	result, err := Run(context.Background(), cfg, Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Text != "draft:hello" {
		t.Fatalf("Text = %q", result.Text)
	}
}

func TestRunMissingProviderKeyFallsBackInSession(t *testing.T) {
	script := writeExecutable(t, `#!/bin/sh
printf 'missing provider API key\n' >&2
exit 17
`)
	cfg := &Config{Backend: BackendYoetz, Command: []string{script}}
	result, err := Run(context.Background(), cfg, Request{Prompt: "draft"})
	if err != nil {
		t.Fatalf("Run fallback: %v", err)
	}
	if !result.UseInSession || !result.Fallback || result.Evidence.ExitCode != 17 {
		t.Fatalf("result = %+v, want in-session fallback with exit 17", result)
	}
	if !strings.Contains(result.Reason, "missing provider API key") || !strings.Contains(result.Remedy, "provider credentials") {
		t.Fatalf("fallback lacks explicit key/remedy evidence: %+v", result)
	}
}

func TestRunFailureModeErrorFailsClosedWithEvidence(t *testing.T) {
	script := writeExecutable(t, "#!/bin/sh\nexit 23\n")
	cfg := &Config{Backend: BackendCustom, Command: []string{script}, OnFailure: FailureError}
	result, err := Run(context.Background(), cfg, Request{Prompt: "draft"})
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run error = %v, want RunError", err)
	}
	if result.UseInSession || result.Evidence.ExitCode != 23 || runErr.Evidence.ExitCode != 23 {
		t.Fatalf("result=%+v runErr=%+v", result, runErr)
	}
	if !strings.Contains(err.Error(), "set drafter.on_failure to in_session") {
		t.Fatalf("error lacks remedy: %v", err)
	}
}

func TestRunTimeoutUsesFallbackPolicy(t *testing.T) {
	script := writeExecutable(t, "#!/bin/sh\nexec sleep 5\n")
	cfg := &Config{Backend: BackendCustom, Command: []string{script}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result, err := Run(ctx, cfg, Request{Prompt: "draft"})
	if err != nil {
		t.Fatalf("Run timeout fallback: %v", err)
	}
	if !result.UseInSession || !result.Fallback || !strings.Contains(result.Reason, "timed out") {
		t.Fatalf("timeout result = %+v", result)
	}
}

func TestRunOutputFileLimitUsesFailurePolicy(t *testing.T) {
	script := writeExecutable(t, `#!/bin/sh
dd if=/dev/zero of="$1" bs=1048576 count=4 2>/dev/null
printf x >> "$1"
`)
	tests := []struct {
		name      string
		mode      string
		wantError bool
	}{
		{name: "fallback", mode: FailureInSession},
		{name: "fail-closed", mode: FailureError, wantError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Backend:   BackendCustom,
				Command:   []string{script, "{out}"},
				OnFailure: tc.mode,
			}
			result, err := Run(context.Background(), cfg, Request{Prompt: "draft"})
			if tc.wantError {
				var runErr *RunError
				if !errors.As(err, &runErr) {
					t.Fatalf("Run error = %v, want RunError", err)
				}
				if result.UseInSession || result.Fallback {
					t.Fatalf("result = %+v, want fail-closed result", result)
				}
			} else {
				if err != nil {
					t.Fatalf("Run fallback: %v", err)
				}
				if !result.UseInSession || !result.Fallback {
					t.Fatalf("result = %+v, want in-session fallback", result)
				}
			}
			if result.Evidence.ExitCode != 0 {
				t.Fatalf("exit code = %d, want successful command evidence", result.Evidence.ExitCode)
			}
			if !strings.Contains(result.Reason, "read configured {out} file: output exceeds 4194304-byte limit") {
				t.Fatalf("reason = %q, want explicit output limit failure", result.Reason)
			}
		})
	}
}

func TestBuildCommandPresets(t *testing.T) {
	tests := []struct {
		name       string
		cfg        Config
		want       []string
		promptFile bool
		outputFile bool
	}{
		{
			name:       "yoetz",
			cfg:        Config{Backend: BackendYoetz, Model: "gemini/flash"},
			want:       []string{"yoetz", "ask", "--prompt-file", "/tmp/p", "--output-final", "/tmp/o", "--response-format", "text", "--no-notify", "--model", "gemini/flash"},
			promptFile: true,
			outputFile: true,
		},
		{
			name: "claude",
			cfg:  Config{Backend: BackendClaude, Model: "fable", Effort: "low"},
			want: []string{"claude", "-p", "--output-format", "text", "--no-session-persistence", "--model", "fable", "--effort", "low"},
		},
		{
			name: "codex",
			cfg:  Config{Backend: BackendCodex, Model: "gpt-5.6-luna", Effort: "medium"},
			want: []string{"codex", "exec", "--ephemeral", "--color", "never", "--model", "gpt-5.6-luna", "--config", "model_reasoning_effort=medium", "-"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, promptFile, outputFile, err := buildCommand(tc.cfg, "/tmp/p", "/tmp/o")
			if err != nil {
				t.Fatalf("buildCommand: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) || promptFile != tc.promptFile || outputFile != tc.outputFile {
				t.Fatalf("buildCommand = %v/%v/%v, want %v/%v/%v", got, promptFile, outputFile, tc.want, tc.promptFile, tc.outputFile)
			}
		})
	}
}

func writeExecutable(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "drafter-helper")
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	return path
}
