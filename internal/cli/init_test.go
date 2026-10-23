package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTeamCreateVerbsWriteWithoutStaleRedirect: `team init`, `team rules init`
// and `team sync --apply` perform their writes. 3.0 (2a-2): the gh#762
// deprecation notices pointed at a top-level `amq-squad init` that 3.0 does not
// register, so they are gone; this test pins that no stale redirect is printed.
func TestTeamCreateVerbsWriteWithoutStaleRedirect(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		check func(t *testing.T, dir string)
	}{
		{
			name: "team init",
			args: []string{"team", "init", "--roles", "cto", "--binary", "cto=codex"},
			check: func(t *testing.T, dir string) {
				if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "team.json")); err != nil {
					t.Fatalf("team init did not write team.json: %v", err)
				}
			},
		},
		{
			name: "team rules init",
			args: []string{"team", "rules", "init"},
			check: func(t *testing.T, dir string) {
				if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "team-rules.md")); err != nil {
					t.Fatalf("team rules init did not write team-rules.md: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			_, stderr, err := captureOutput(t, func() error { return Run(tc.args, "test") })
			if err != nil {
				t.Fatalf("%v: %v\n%s", tc.args, err, stderr)
			}
			if strings.Contains(stderr, "use amq-squad init") {
				t.Fatalf("%v: stderr names the unregistered `amq-squad init`: %q", tc.args, stderr)
			}
			tc.check(t, dir)
		})
	}

	// team sync --apply needs an existing profile (and pointer stubs) to have
	// something to sync.
	t.Run("team sync --apply", func(t *testing.T) {
		dir := t.TempDir()
		chdir(t, dir)
		if _, _, err := captureOutput(t, func() error {
			return Run([]string{"team", "init", "--roles", "cto", "--binary", "cto=codex"}, "test")
		}); err != nil {
			t.Fatalf("seed team init: %v", err)
		}
		_ = os.Remove(filepath.Join(dir, "CLAUDE.md"))
		_, stderr, err := captureOutput(t, func() error {
			return Run([]string{"team", "sync", "--apply"}, "test")
		})
		if err != nil {
			t.Fatalf("team sync --apply: %v\n%s", err, stderr)
		}
		if strings.Contains(stderr, "use amq-squad init") {
			t.Fatalf("stderr names the unregistered `amq-squad init`: %q", stderr)
		}
		if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err != nil {
			t.Fatalf("team sync --apply did not (re)write CLAUDE.md: %v", err)
		}
	})
}

// TestTeamSubcommandHelpDispatch is gh#762's fifth named acceptance test:
// every second- and third-level `team` subcommand must intercept -h/--help
// and print this repo's own help text -- not fail with a validation error
// (the `team member add/rm --help` bug) and not fall through to Go's default
// flag.Usage banner (the `team member list`/`team lead show --help` bug).
// Pinned once, table-driven, so the class cannot regress subcommand by
// subcommand (task/t12's ruling: fix all four confirmed instances, not just
// two, and cover the whole surface here). `team member control-continue`
// (team_member_control_continue.go) was found to carry the exact same bug
// during review and is included below -- fixed alongside the rest so the
// whole peelPositional-before-help-check class is actually eliminated, not
// just the two originally-suspected instances.
func TestTeamSubcommandHelpDispatch(t *testing.T) {
	cases := [][]string{
		{"team", "--help"},
		{"team", "init", "--help"},
		{"team", "resume", "--help"},
		{"team", "rules", "--help"},
		{"team", "rules", "init", "--help"},
		{"team", "rules", "show", "--help"},
		{"team", "rules", "templates", "--help"},
		{"team", "operator", "--help"},
		{"team", "sync", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			_, stderr, err := captureOutput(t, func() error { return Run(args, "test") })
			if err != nil {
				t.Fatalf("%v returned an error instead of showing help: %v", args, err)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Fatalf("%v produced no help output on stderr", args)
			}
			if strings.Contains(stderr, "Usage of ") {
				t.Fatalf("%v fell through to Go's default flag.Usage banner instead of this repo's help: %q", args, stderr)
			}
			if strings.Contains(stderr, "is required") || strings.Contains(stderr, "unexpected argument") {
				t.Fatalf("%v printed a validation error instead of help: %q", args, stderr)
			}
		})
	}
}
