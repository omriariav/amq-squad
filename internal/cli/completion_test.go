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

func TestCompletionIncludesGlobalStatus(t *testing.T) {
	for shell, tc := range map[string]struct {
		script string
		want   string
	}{
		"bash": {script: bashCompletionScript, want: `compgen -W "start status"`},
		"zsh":  {script: zshCompletionScript, want: `compadd -- 'start' 'status'`},
		"fish": {script: fishCompletionScript, want: `__fish_seen_subcommand_from global" -a 'status'`},
	} {
		if !strings.Contains(tc.script, tc.want) {
			t.Errorf("%s completion is missing exact global status branch %q:\n%s", shell, tc.want, tc.script)
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
		"new", "team", "start", "down", "status", "resume", "completion", "version",
		// new subcommands
		"new_subcommands", "profile", "session",
		// team subcommands
		"init", "profiles", "sync", "rules", "delete",
		// team rules subcommands
		"show",
		// goal/operator subcommands
		"goal_subcommands", "apply", "claim", "deliver", "draft", "start",
		"operator_subcommands", "answer", "send", "directive", "poll", "watch",
		"gate_subcommands", "raise", "close",
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
		"'new'", "'team'", "'start'", "'down'", "'completion'", "'version'",
		"'profile'", "'session'",
		"goal_subcommands", "'apply'", "'claim'", "'deliver'", "'draft'", "'start'",
		"operator_subcommands", "'answer'", "'send'", "'directive'", "'poll'", "'watch'",
		"gate_subcommands", "'raise'", "'close'",
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
		"-a 'new'", "-a 'team'", "-a 'start'", "-a 'down'", "-a 'completion'", "-a 'version'",
		"__fish_seen_subcommand_from new",
		"-a 'profile'", "-a 'session'",
		"__fish_seen_subcommand_from team",
		"-a 'init'", "-a 'profiles'", "-a 'rules'", "-a 'delete'",
		"__fish_seen_subcommand_from goal",
		"-a 'apply'", "-a 'claim'", "-a 'deliver'", "-a 'draft'", "-a 'start'",
		"__fish_seen_subcommand_from operator",
		"-a 'answer'", "-a 'directive'", "-a 'poll'", "-a 'watch'",
		"__fish_seen_subcommand_from gate", "-a 'raise'", "-a 'close'",
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
			// --simple-start is an internal child-process contract. Offering it
			// in user-facing shell completion would expose an unsupported path.
			if flagName == "simple-start" {
				continue
			}
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

// Sanity check: completion follows the public catalog. Internal child routes
// such as agent remain dispatchable without becoming operator-facing verbs.
func TestCompletionTopCommandsMatchesPublicCatalog(t *testing.T) {
	expected := map[string]bool{
		"setup":      true,
		"init":       true,
		"new":        true,
		"roles":      true,
		"role":       true,
		"team":       true,
		"lead":       true,
		"goal":       true,
		"wizard":     true,
		"start":      true,
		"plan":       true,
		"down":       true,
		"task":       true,
		"evidence":   true,
		"namespace":  true,
		"verify":     true,
		"gate":       true,
		"operator":   true,
		"broadcast":  true,
		"status":     true,
		"focus":      true,
		"open":       true,
		"send":       true,
		"dispatch":   true,
		"amq":        true,
		"resume":     true,
		"completion": true,
		"doctor":     true,
		"worktree":   true,
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
