package cli

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestTeamUsageNamesExactlyTheDispatchedSubcommands keeps `amq-squad team
// --help` from drifting away from runTeam's dispatch again (2a-1: the usage
// text still advertised member, profiles, rm and start after those verbs were
// cut). Every subcommand the usage names must dispatch, every dispatched
// subcommand must be named, and the removed ones must not appear.
func TestTeamUsageNamesExactlyTheDispatchedSubcommands(t *testing.T) {
	_, usage, err := captureOutput(t, func() error { return runTeam([]string{"--help"}) })
	if err != nil {
		t.Fatalf("team --help: %v", err)
	}
	named := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^  amq-squad team ([a-z][a-z-]*)`).FindAllStringSubmatch(usage, -1) {
		named[m[1]] = true
	}
	delete(named, "resume") // the deleted-verb redirect is documented as deleted
	want := append([]string(nil), teamSubcommandHints...)
	got := make([]string, 0, len(named))
	for name := range named {
		got = append(got, name)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("team usage names %v, dispatch hints are %v", got, want)
	}
	for _, name := range teamSubcommandHints {
		_, _, err := captureOutput(t, func() error { return runTeam([]string{name, "--help"}) })
		if err != nil && strings.Contains(err.Error(), "unknown 'team' subcommand") {
			t.Errorf("team %s is named in usage but does not dispatch: %v", name, err)
		}
	}
	for _, removed := range []string{"member", "profiles", "rm", "delete", "lead", "autonomous", "shared-cwd-exception", "show", "launch"} {
		if named[removed] {
			t.Errorf("team usage still names removed subcommand %q", removed)
		}
		_, _, err := captureOutput(t, func() error { return runTeam([]string{removed}) })
		if err == nil || !strings.Contains(err.Error(), "unknown 'team' subcommand") {
			t.Errorf("removed team subcommand %q still dispatches: %v", removed, err)
		}
	}
	for _, verb := range []string{"amq-squad start", "amq-squad up", "amq-squad new", "amq-squad resume"} {
		if strings.Contains(usage, verb) {
			t.Errorf("team usage points at the removed verb %q", verb)
		}
	}
}
