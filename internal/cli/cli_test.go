package cli

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestUnknownSubcommandErrorCanonicalFormat(t *testing.T) {
	tests := []struct {
		name  string
		verb  string
		given string
		valid []string
		want  string
	}{
		{
			name:  "one",
			verb:  "team overlay",
			given: "bogus",
			valid: []string{"init"},
			want:  `unknown 'team overlay' subcommand: "bogus". Try 'init'.`,
		},
		{
			name:  "two",
			verb:  "amq receipts",
			given: "bogus",
			valid: []string{"list", "wait"},
			want:  `unknown 'amq receipts' subcommand: "bogus". Try 'list' or 'wait'.`,
		},
		{
			name:  "many",
			verb:  "new",
			given: "bogus",
			valid: []string{"team", "profile", "session"},
			want:  `unknown 'new' subcommand: "bogus". Try 'team', 'profile', or 'session'.`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := unknownSubcommandError(tt.verb, tt.given, tt.valid...)
			if err.Error() != tt.want {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
			var usageErr UsageError
			if !errors.As(err, &usageErr) {
				t.Fatalf("error type = %T, want UsageError", err)
			}
		})
	}
}

func TestUnknownSubcommandErrorRequiresCompleteList(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("unknownSubcommandError accepted an empty valid-subcommand list")
		}
	}()
	_ = unknownSubcommandError("team", "bogus")
}

func TestRunVersionCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"version"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run version: %v\nstderr:\n%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "amq-squad v-test" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRunHelpIncludesVersionCommand(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"--help"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run help: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "version") || !strings.Contains(stdout, "Print the amq-squad version") {
		t.Fatalf("help missing version command:\n%s", stdout)
	}
	if !strings.Contains(stdout, "roles") || !strings.Contains(stdout, "List built-in role IDs") {
		t.Fatalf("help missing roles command:\n%s", stdout)
	}
}

func TestCommandRegistryPowersDispatchHelpAndCompletion(t *testing.T) {
	for _, want := range []string{"dispatch", "task", "doctor"} {
		if _, ok := lookupCommand(want, "v-test"); !ok {
			t.Fatalf("registry missing %q", want)
		}
	}
	stdout, _, err := captureOutput(t, func() error {
		return Run([]string{"--help"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run --help: %v", err)
	}
	for _, want := range []string{"dispatch", "status", "task"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help missing registry command %q:\n%s", want, stdout)
		}
	}
}

func TestV228PublicSurfacePrunesFoldedCommandsAndHidesAgent(t *testing.T) {
	// gh#759/t13: "brief" is deliberately reintroduced as a new, unrelated
	// top-level verb (the workstream-brief drafter, the one place an LLM
	// runs on any launch-capable path) -- not a silent regression of the
	// v2.28-pruned command this list originally named. Removed from both
	// lists below; brief_test.go covers the new command directly.
	// 2a-3: "context" likewise returns as a new verb, the plan 4.2 hook verb
	// (hook_context.go), not the v2 context board; hook_verbs_test.go covers it.
	removed := []string{
		"activity", "bootstrap", "threads", "thread",
		"collect", "prune-panes", "console", "monitor", "notify", "notifications",
		"history", "fork", "review-worktree", "tmux-harness", "rm", "archive", "next",
	}
	public := commandNames("v-test")
	for _, name := range removed {
		if _, ok := lookupCommand(name, "v-test"); ok {
			t.Errorf("removed command %q remains dispatchable", name)
		}
		if containsString(public, name) {
			t.Errorf("removed command %q remains public or completable", name)
		}
		if err := Run([]string{name}, "v-test"); err == nil || !strings.Contains(err.Error(), "unknown command") {
			t.Errorf("Run(%q) error = %v, want unknown command", name, err)
		}
	}

	if containsString(public, "agent") {
		t.Fatal("internal agent child route leaked into public help/completion")
	}
	help, _, err := captureOutput(t, func() error { return Run([]string{"--help"}, "v-test") })
	if err != nil {
		t.Fatalf("Run --help: %v", err)
	}
	for _, name := range removed {
		if strings.Contains(help, "amq-squad "+name+" ") {
			t.Errorf("removed command %q remains in root help examples:\n%s", name, help)
		}
	}
	if strings.Contains(help, "\n  agent ") {
		t.Fatalf("internal agent child route leaked into root help:\n%s", help)
	}

	for _, path := range []string{
		"activity.go", "bootstrap_ack.go", "threads.go", "thread.go",
		"collect.go", "prune_panes.go", "console.go", "monitor.go", "notify.go", "notifications.go",
		"history.go", "fork.go", "review_worktree.go", "tmux_harness.go", "rm.go", "next.go",
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("removed command implementation %q still exists (stat error: %v)", path, err)
		}
	}
}

func TestRunRolesListsMarketNumbers(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"roles"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run roles: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"ROLE",
		"DEFAULT RUNTIME",
		"cpo",
		"cto",
		"qa",
		"catalog",
		"lead",
		"agent",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("roles output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunRolesListsStagedCustomRolesSeparately(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	rolesDir := filepath.Join(dir, ".amq-squad", "roles")
	if err := os.MkdirAll(rolesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rolesDir, "probe-officer.md"), []byte(`---
label: Probe Officer
binary: codex
description: Investigates runtime probes.
skills:
  - /probe
peers:
  - cto
---
# Role: Probe Officer
`), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return Run([]string{"roles"}, "v-test")
	})
	if err != nil {
		t.Fatalf("Run roles: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"probe-officer",
		"probe-officer.md",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("roles output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunRolesRejectsPositionalArgs(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return Run([]string{"roles", "cto"}, "v-test")
	})
	if err == nil {
		t.Fatal("roles with a positional argument should fail")
	}
	if !strings.Contains(err.Error(), "no positional arguments") {
		t.Fatalf("roles positional error = %v", err)
	}
}

func TestRunNOCIsUnknownCommand(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return Run([]string{"noc"}, "v-test")
	})
	if err == nil {
		t.Fatal("noc should be unsupported")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("noc error = %v", err)
	}
}

// TestRunHelpVerbsStillPrintUsage proves the explicit help paths are
// unchanged after bare `amq-squad` was repurposed as the status board: help,
// -h, and --help must all print the usage block, not the board.
func TestRunHelpVerbsStillPrintUsage(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		stdout, stderr, err := captureOutput(t, func() error {
			return Run(args, "v-test")
		})
		if err != nil {
			t.Fatalf("Run %v: %v\nstderr:\n%s", args, err, stderr)
		}
		if !strings.Contains(stdout, "amq-squad - role-aware agent team launcher") {
			t.Errorf("Run %v should print usage, got:\n%s", args, stdout)
		}
		if strings.Contains(stdout, "AM_BASE_ROOT") {
			t.Errorf("Run %v should NOT render the board, got:\n%s", args, stdout)
		}
	}
}

// Sanity check on the public catalog (moved from completion_test.go in the 2a-1
// C24 cut; completionTopCommands was commandNames("")). Internal child routes
// such as agent remain dispatchable without becoming operator-facing verbs.
func TestCompletionTopCommandsMatchesPublicCatalog(t *testing.T) {
	expected := map[string]bool{
		"roles":     true,
		"role":      true,
		"brief":     true,
		"team":      true,
		"task":      true,
		"evidence":  true,
		"context":   true,
		"guard":     true,
		"seat-exec": true,
		"launch":    true,
		"verify":    true,
		"gate":      true,
		"operator":  true,
		"status":    true,
		"fleet":     true,
		"routing":   true,
		"dispatch":  true,
		"amq":       true,
		"doctor":    true,
		"trust":     true,
		"worktree":  true,
		"version":   true,
		"help":      true,
		"lead":      true,
		"pi":        true,
		"remote":    true,
	}
	for _, c := range commandNames("") {
		if !expected[c] {
			t.Errorf("public catalog has unexpected entry %q", c)
		}
		delete(expected, c)
	}
	for c := range expected {
		t.Errorf("public catalog missing %q", c)
	}
}

// TestRunBarePrintsUsage: 3.0 (2a-2) bare `amq-squad` prints the root usage; the
// v2 multi-session board moved to amq-noc (manifest C3).
func TestRunBarePrintsUsage(t *testing.T) {
	chdir(t, t.TempDir())
	stdout, _, err := captureOutput(t, func() error { return Run(nil, "v-test") })
	if err != nil {
		t.Fatalf("bare Run: %v", err)
	}
	if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "Commands:") {
		t.Fatalf("bare run should print the usage, got:\n%s", stdout)
	}
}

// TestRootUsageExamplesNameRegisteredCommands: every `amq-squad <verb>` the root
// usage text shows is a registered command (2a-2 item 5: no removed verb in
// kept help text).
func TestRootUsageExamplesNameRegisteredCommands(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error { return Run([]string{"--help"}, "v-test") })
	if err != nil {
		t.Fatalf("Run --help: %v", err)
	}
	registered := map[string]bool{}
	for _, name := range commandNames("v-test") {
		registered[name] = true
	}
	examples := regexp.MustCompile(`(?m)^\s+amq-squad ([a-z][a-z-]*)`).FindAllStringSubmatch(stdout, -1)
	if len(examples) == 0 {
		t.Fatal("root usage shows no examples; the guard has nothing to check")
	}
	for _, m := range examples {
		if !registered[m[1]] {
			t.Errorf("root usage example names %q, which is not a registered command", m[1])
		}
	}
}
