package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRunCompletionRejectsMissingShell(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runCompletion(nil)
	})
	if err == nil {
		t.Fatal("completion without a shell argument should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestRunCompletionRejectsExtraArgs(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runCompletion([]string{"bash", "extra"})
	})
	if err == nil {
		t.Fatal("completion with extra args should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestRunCompletionRejectsUnsupportedShell(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error {
		return runCompletion([]string{"powershell"})
	})
	if err == nil {
		t.Fatal("completion with unsupported shell should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if stdout != "" {
		t.Errorf("unsupported shell must not print a partial script:\n%s", stdout)
	}
}

func TestRunCompletionBashContainsRepresentativeTokens(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runCompletion([]string{"bash"})
	})
	if err != nil {
		t.Fatalf("completion bash: %v", err)
	}
	if stderr != "" {
		t.Errorf("successful completion bash should be silent on stderr, got:\n%s", stderr)
	}
	for _, want := range []string{
		"_amq_squad_complete",
		"complete -F _amq_squad_complete amq-squad",
		// commands
		"team", "up", "down", "status", "history", "resume", "fork",
		"launch", "restore", "list", "completion", "version",
		// team subcommands
		"init", "show", "profiles", "sync", "rules",
		// high-traffic flags
		"--profile", "--json", "--dry-run", "--force", "--force-duplicate", "--session",
		// previously missing flags + root short/version forms
		"--fresh", "--exec", "--handle", "--root", "--conversation-id",
		"--no-default-args", "--team-workstream", "--personas", "--roles",
		"--binary", "--cwd", "-h", "--version", "-v",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("bash completion missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunCompletionZshContainsRepresentativeTokens(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runCompletion([]string{"zsh"})
	})
	if err != nil {
		t.Fatalf("completion zsh: %v", err)
	}
	if stderr != "" {
		t.Errorf("successful completion zsh should be silent on stderr, got:\n%s", stderr)
	}
	for _, want := range []string{
		"#compdef amq-squad",
		"_amq_squad",
		"compdef _amq_squad amq-squad",
		"'team'", "'up'", "'completion'", "'version'",
		"'init'", "'show'", "'profiles'",
		"'--profile'", "'--json'", "'--dry-run'", "'--force-duplicate'",
		"'--fresh'", "'--exec'", "'--handle'", "'--root'", "'--conversation-id'",
		"'--no-default-args'", "'--team-workstream'", "'--personas'", "'--roles'",
		"'--binary'", "'--cwd'", "'-h'", "'--version'", "'-v'",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("zsh completion missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunCompletionFishContainsRepresentativeTokens(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return runCompletion([]string{"fish"})
	})
	if err != nil {
		t.Fatalf("completion fish: %v", err)
	}
	if stderr != "" {
		t.Errorf("successful completion fish should be silent on stderr, got:\n%s", stderr)
	}
	for _, want := range []string{
		"complete -c amq-squad",
		"-a 'team'", "-a 'up'", "-a 'completion'", "-a 'version'",
		"__fish_seen_subcommand_from team",
		"-a 'init'", "-a 'profiles'", "-a 'rules'",
		"__fish_seen_subcommand_from rules",
		"-l 'profile'", "-l 'json'", "-l 'dry-run'", "-l 'force-duplicate'",
		"-l 'fresh'", "-l 'exec'", "-l 'handle'", "-l 'root'", "-l 'conversation-id'",
		"-l 'no-default-args'", "-l 'team-workstream'", "-l 'personas'", "-l 'roles'",
		"-l 'binary'", "-l 'cwd'", "-l 'version'",
		"-s 'h'", "-s 'v'",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("fish completion missing %q in:\n%s", want, stdout)
		}
	}
}

// TestCompletionFlagsCoverDispatcher is a drift guard: every flag name
// declared with flag.NewFlagSet/fs.{String,Bool,Duration,Int} across the
// CLI must appear in completionCommonFlags. If a new flag is added, this
// test fails until the completion list is updated.
func TestCompletionFlagsCoverDispatcher(t *testing.T) {
	flagPattern := regexp.MustCompile(`fs\.(String|Bool|Duration|Int)\("([a-z][a-z0-9_-]*)"`)
	known := make(map[string]bool)
	for _, f := range completionCommonFlags {
		if !strings.HasPrefix(f, "--") {
			continue
		}
		known[strings.TrimPrefix(f, "--")] = true
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == "completion.go" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range flagPattern.FindAllStringSubmatch(string(data), -1) {
			flagName := m[2]
			if !known[flagName] {
				t.Errorf("flag %q (declared in %s) missing from completionCommonFlags", flagName, name)
			}
		}
	}
}

// TestCompletionRootFlagsOfferedAsFirstToken proves the bash/zsh scripts
// surface root flags when the user starts the first token with `-`.
func TestCompletionRootFlagsOfferedAsFirstToken(t *testing.T) {
	bashOut, _, err := captureOutput(t, func() error { return runCompletion([]string{"bash"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`if [[ "$cur" == -* ]]; then`,
		`compgen -W "$common_flags"`,
	} {
		if !strings.Contains(bashOut, want) {
			t.Errorf("bash first-token flag branch missing %q in:\n%s", want, bashOut)
		}
	}
	zshOut, _, err := captureOutput(t, func() error { return runCompletion([]string{"zsh"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`if [[ "${words[CURRENT]}" == -* ]]; then`,
		`compadd -- "${common_flags[@]}"`,
	} {
		if !strings.Contains(zshOut, want) {
			t.Errorf("zsh first-token flag branch missing %q in:\n%s", want, zshOut)
		}
	}
}

// Sanity check: every top-level command in the dispatcher should appear in
// the completion top-level command list, and vice versa. Drift between the
// two means tab-completion stops covering a verb (or completes a verb that
// no longer exists).
func TestCompletionTopCommandsMatchesDispatch(t *testing.T) {
	expected := map[string]bool{
		"team":       true,
		"up":         true,
		"down":       true,
		"status":     true,
		"history":    true,
		"resume":     true,
		"fork":       true,
		"launch":     true,
		"restore":    true,
		"list":       true,
		"completion": true,
		"version":    true,
		"help":       true,
	}
	for _, c := range completionTopCommands {
		if !expected[c] {
			t.Errorf("completionTopCommands has unexpected entry %q", c)
		}
		delete(expected, c)
	}
	for c := range expected {
		t.Errorf("completionTopCommands missing %q", c)
	}
}
