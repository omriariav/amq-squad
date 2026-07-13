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

func TestCompletionCoversBootstrapAck(t *testing.T) {
	for shell, script := range map[string]string{"bash": bashCompletionScript, "zsh": zshCompletionScript, "fish": fishCompletionScript} {
		for _, want := range []string{"bootstrap", "ack", "--skill-version", "--steps"} {
			needle := want
			if shell == "fish" {
				needle = strings.TrimPrefix(want, "--")
			}
			if !strings.Contains(script, needle) {
				t.Errorf("%s completion missing %q", shell, want)
			}
		}
	}
}

func TestCompletionCoversReviewWorktreeModes(t *testing.T) {
	for shell, script := range map[string]string{"bash": bashCompletionScript, "zsh": zshCompletionScript, "fish": fishCompletionScript} {
		for _, want := range []string{"review-worktree", "create", "exec", "shell", "remove"} {
			if !strings.Contains(script, want) {
				t.Errorf("%s completion missing review-worktree token %q", shell, want)
			}
		}
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
		"new", "team", "up", "stop", "status", "history", "resume", "fork",
		"agent", "completion", "version",
		// new subcommands
		"new_subcommands", "profile", "session",
		// team subcommands
		"init", "profiles", "sync", "rules", "delete",
		// team rules subcommands
		"show",
		// goal/operator subcommands
		"goal_subcommands", "apply", "claim", "deliver", "draft", "start",
		"operator_subcommands", "answer", "directive", "poll", "watch",
		"notifications_subcommands", "notifications", "events", "probe",
		// high-traffic flags
		"--profile", "--json", "--actions", "--action", "--action-id", "--target-id", "--scope", "--run-action", "--set", "--commands", "--mutating", "--dry-run", "--force", "--force-duplicate", "--session",
		"--approved", "--denied", "--gate", "--goal-id", "--attempt-id", "--route",
		// previously missing flags + root short/version forms
		"--fresh", "--exec", "--handle", "--root", "--conversation-id",
		"--no-default-args", "--team-workstream", "--personas", "--roles",
		"--binary", "--cwd", "-p", "-s", "-P", "-h", "--version", "-v",
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
		"'new'", "'team'", "'up'", "'completion'", "'version'", "'agent'",
		"'profile'", "'session'",
		"goal_subcommands", "'apply'", "'claim'", "'deliver'", "'draft'", "'start'",
		"operator_subcommands", "'answer'", "'directive'", "'poll'", "'watch'",
		"notifications_subcommands", "'notifications'", "'events'", "'probe'",
		"'init'", "'profiles'", "'delete'", "'show'",
		"'--profile'", "'--json'", "'--actions'", "'--action'", "'--action-id'", "'--target-id'", "'--scope'", "'--run-action'", "'--set'", "'--commands'", "'--mutating'", "'--dry-run'", "'--force-duplicate'", "'--approved'", "'--denied'", "'--gate'", "'--goal-id'", "'--attempt-id'", "'--route'",
		"'--fresh'", "'--exec'", "'--handle'", "'--root'", "'--conversation-id'",
		"'--no-default-args'", "'--team-workstream'", "'--personas'", "'--roles'",
		"'--binary'", "'--cwd'", "'-p'", "'-s'", "'-P'", "'-h'", "'--version'", "'-v'",
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
		"-a 'new'", "-a 'team'", "-a 'up'", "-a 'completion'", "-a 'version'",
		"__fish_seen_subcommand_from new",
		"-a 'profile'", "-a 'session'",
		"__fish_seen_subcommand_from team",
		"-a 'init'", "-a 'profiles'", "-a 'rules'", "-a 'delete'",
		"__fish_seen_subcommand_from goal",
		"-a 'apply'", "-a 'claim'", "-a 'deliver'", "-a 'draft'", "-a 'start'",
		"__fish_seen_subcommand_from operator",
		"-a 'answer'", "-a 'directive'", "-a 'poll'", "-a 'watch'",
		"__fish_seen_subcommand_from notifications",
		"-a 'events'", "-a 'probe'",
		"__fish_seen_subcommand_from rules",
		"-a 'show'",
		"-l 'profile'", "-l 'json'", "-l 'actions'", "-l 'action'", "-l 'action-id'", "-l 'target-id'", "-l 'scope'", "-l 'run-action'", "-l 'set'", "-l 'commands'", "-l 'mutating'", "-l 'dry-run'", "-l 'force-duplicate'", "-l 'approved'", "-l 'denied'", "-l 'gate'", "-l 'goal-id'", "-l 'attempt-id'", "-l 'route'",
		"-l 'fresh'", "-l 'exec'", "-l 'handle'", "-l 'root'", "-l 'conversation-id'",
		"-l 'no-default-args'", "-l 'team-workstream'", "-l 'personas'", "-l 'roles'",
		"-l 'binary'", "-l 'cwd'", "-l 'version'",
		"-s 'p'", "-s 's'", "-s 'P'", "-s 'h'", "-s 'v'",
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
		if name == "claude_rename.go" {
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
		"new":             true,
		"roles":           true,
		"team":            true,
		"lead":            true,
		"goal":            true,
		"global":          true,
		"run":             true,
		"wizard":          true,
		"task":            true,
		"verify":          true,
		"operator":        true,
		"activity":        true,
		"up":              true,
		"stop":            true,
		"brief":           true,
		"threads":         true,
		"thread":          true,
		"status":          true,
		"focus":           true,
		"open":            true,
		"send":            true,
		"dispatch":        true,
		"collect":         true,
		"prune-panes":     true,
		"console":         true,
		"monitor":         true,
		"notify":          true,
		"notifications":   true,
		"amq":             true,
		"history":         true,
		"resume":          true,
		"fork":            true,
		"review-worktree": true,
		"rm":              true,
		"archive":         true,
		"next":            true,
		"completion":      true,
		"doctor":          true,
		"agent":           true,
		"bootstrap":       true,
		"version":         true,
		"help":            true,
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
