package cli

import (
	"strings"
	"testing"
)

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
		{"team", "propose", "--help"},
		{"team", "apply", "--help"},
		{"team", "show", "--help"},
		{"team", "check", "--help"},
		{"team", "draft-rules", "--help"},
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
