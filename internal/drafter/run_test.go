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
			if len(result.Attempts) != 1 || result.Attempts[0].Failure == "" {
				t.Fatalf("attempt evidence = %+v, want one recorded output-limit failure", result.Attempts)
			}
			if !strings.Contains(result.Reason, "read configured {out} file: output exceeds 4194304-byte limit") {
				t.Fatalf("reason = %q, want explicit output limit failure", result.Reason)
			}
		})
	}
}

func TestRunOrderedChainFallsThroughAfterOversizedOutputFile(t *testing.T) {
	oversized := writeExecutable(t, `#!/bin/sh
dd if=/dev/zero of="$1" bs=1048576 count=4 2>/dev/null
printf x >> "$1"
`)
	bin := t.TempDir()
	writeNamedExecutable(t, bin, BackendClaude, `#!/bin/sh
IFS= read -r line
printf 'claude:%s\n' "$line"
`)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cfg := &Config{
		Chain:   []string{BackendCustom, BackendClaude},
		Command: []string{oversized, "{out}"},
	}
	result, err := Run(context.Background(), cfg, Request{Prompt: "bounded fallback"})
	if err != nil {
		t.Fatalf("Run chain: %v", err)
	}
	if result.Text != "claude:bounded fallback" || len(result.Attempts) != 2 {
		t.Fatalf("result = %+v", result)
	}
	first, second := result.Attempts[0], result.Attempts[1]
	if first.Backend != BackendCustom || first.ExitCode != 0 || !strings.Contains(first.Failure, "output exceeds 4194304-byte limit") {
		t.Fatalf("oversized attempt = %+v", first)
	}
	if second.Backend != BackendClaude || second.ExitCode != 0 || second.Failure != "" {
		t.Fatalf("fallback attempt = %+v", second)
	}
}

func TestRunOrderedChainFallsThroughWithExactAttemptEvidence(t *testing.T) {
	bin := t.TempDir()
	writeNamedExecutable(t, bin, BackendYoetz, `#!/bin/sh
printf 'yoetz unavailable\n' >&2
exit 17
`)
	writeNamedExecutable(t, bin, BackendClaude, `#!/bin/sh
IFS= read -r line
printf 'claude:%s\n' "$line"
`)
	t.Setenv("PATH", bin)
	cfg := &Config{Chain: []string{BackendYoetz, BackendClaude}, Model: "fast-model", Effort: "low"}
	result, err := Run(context.Background(), cfg, Request{Prompt: "draft this"})
	if err != nil {
		t.Fatalf("Run chain: %v", err)
	}
	if result.Text != "claude:draft this" || result.UseInSession || result.Fallback {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Attempts) != 2 {
		t.Fatalf("attempts = %+v", result.Attempts)
	}
	first, second := result.Attempts[0], result.Attempts[1]
	if first.Backend != BackendYoetz || first.ExitCode != 17 || !strings.Contains(first.Failure, "yoetz unavailable") {
		t.Fatalf("first attempt = %+v", first)
	}
	if len(first.Command) == 0 || first.Command[0] != BackendYoetz || first.CommandDisplay == "" {
		t.Fatalf("first exact command evidence = %+v", first)
	}
	if first.Effort != "" {
		t.Fatalf("yoetz attempt inherited unsupported effort: %+v", first)
	}
	if second.Backend != BackendClaude || second.ExitCode != 0 || second.Failure != "" || second.Effort != "low" {
		t.Fatalf("second attempt = %+v", second)
	}
	if !reflect.DeepEqual(result.Evidence, second) {
		t.Fatalf("compatibility evidence = %+v, want final successful attempt %+v", result.Evidence, second)
	}
}

func TestRunOrderedChainRecordsMissingBinaryThenSucceeds(t *testing.T) {
	bin := t.TempDir()
	writeNamedExecutable(t, bin, BackendCodex, `#!/bin/sh
IFS= read -r line
printf 'codex:%s\n' "$line"
`)
	t.Setenv("PATH", bin)
	cfg := &Config{Chain: []string{BackendYoetz, BackendCodex}, Model: "gemini/flash"}
	result, err := Run(context.Background(), cfg, Request{Prompt: "fallback"})
	if err != nil {
		t.Fatalf("Run chain: %v", err)
	}
	if result.Text != "codex:fallback" || len(result.Attempts) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.Attempts[0].ExitCode != -1 || !strings.Contains(result.Attempts[0].Failure, "executable file not found") {
		t.Fatalf("missing binary evidence = %+v", result.Attempts[0])
	}
}

func TestRunOrderedChainExhaustionFallsBackInSession(t *testing.T) {
	bin := t.TempDir()
	writeNamedExecutable(t, bin, BackendYoetz, "#!/bin/sh\nexit 11\n")
	writeNamedExecutable(t, bin, BackendClaude, "#!/bin/sh\nexit 12\n")
	t.Setenv("PATH", bin)
	cfg := &Config{Chain: []string{BackendYoetz, BackendClaude}, Model: "gemini/flash"}
	result, err := Run(context.Background(), cfg, Request{Prompt: "draft"})
	if err != nil {
		t.Fatalf("Run exhaustion: %v", err)
	}
	if !result.UseInSession || !result.Fallback || len(result.Attempts) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if result.Attempts[0].ExitCode != 11 || result.Attempts[1].ExitCode != 12 || result.Evidence.ExitCode != 12 {
		t.Fatalf("attempt evidence = %+v; compatibility=%+v", result.Attempts, result.Evidence)
	}
	if result.Attempts[0].Failure == "" || result.Attempts[1].Failure == "" || !strings.Contains(result.Reason, BackendYoetz) || !strings.Contains(result.Reason, BackendClaude) {
		t.Fatalf("fall-through reasons missing: %+v", result)
	}
}

func TestRunOrderedChainFailureModeErrorFailsAfterExhaustion(t *testing.T) {
	bin := t.TempDir()
	writeNamedExecutable(t, bin, BackendClaude, "#!/bin/sh\nexit 21\n")
	writeNamedExecutable(t, bin, BackendCodex, "#!/bin/sh\nexit 22\n")
	t.Setenv("PATH", bin)
	cfg := &Config{Chain: []string{BackendClaude, BackendCodex}, OnFailure: FailureError}
	result, err := Run(context.Background(), cfg, Request{Prompt: "draft"})
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("Run error = %v, want RunError", err)
	}
	if result.UseInSession || len(result.Attempts) != 2 || len(runErr.Attempts) != 2 {
		t.Fatalf("result=%+v runErr=%+v", result, runErr)
	}
	if result.Attempts[0].ExitCode != 21 || result.Attempts[1].ExitCode != 22 {
		t.Fatalf("attempts = %+v", result.Attempts)
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
			want:       []string{"yoetz", "ask", "--prompt-file", "/tmp/p", "--format", "text", "--response-format", "text", "--no-notify", "--model", "gemini/flash"},
			promptFile: true,
			outputFile: false,
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

// TestYoetzPresetReadsStdoutNotOutputFinal proves the yoetz preset (gh#760)
// no longer relies on --output-final: yoetz 0.5.62 always JSON-serializes
// the whole RunResult envelope there regardless of --response-format, so
// the drafter would previously return that raw JSON envelope as the draft
// text. The fake "yoetz" binary below reproduces the real CLI's behavior of
// printing raw content to stdout under --format text and, unlike the real
// CLI, also fails loudly if it ever sees --output-final -- proving the
// preset argv no longer requests it.
func TestYoetzPresetReadsStdoutNotOutputFinal(t *testing.T) {
	bin := t.TempDir()
	writeNamedExecutable(t, bin, BackendYoetz, `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--output-final" ]; then
    printf 'unexpected --output-final in argv\n' >&2
    exit 1
  fi
done
saw_format=0
prev=""
for arg in "$@"; do
  if [ "$prev" = "--format" ] && [ "$arg" = "text" ]; then
    saw_format=1
  fi
  prev="$arg"
done
if [ "$saw_format" != "1" ]; then
  printf 'missing --format text in argv\n' >&2
  exit 1
fi
printf '{"content": "should never be parsed as an envelope"}\n'
`)
	t.Setenv("PATH", bin)
	cfg := &Config{Backend: BackendYoetz, Model: "gemini/flash"}
	result, err := Run(context.Background(), cfg, Request{Prompt: "draft"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := `{"content": "should never be parsed as an envelope"}`
	if result.Text != want {
		t.Fatalf("Text = %q, want the raw stdout line unparsed: %q", result.Text, want)
	}
	if result.UseInSession || result.Fallback || result.Evidence.ExitCode != 0 {
		t.Fatalf("result = %+v", result)
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

func writeNamedExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("write helper %s: %v", name, err)
	}
	return path
}
