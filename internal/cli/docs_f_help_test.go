package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Brief 5-docs-F, part 2: help and refusal rows. Each test fails when its fix
// is reverted (docs/v3-spike/5-docs-evidence/revert-check-f.txt).

// Row 4: task widen takes no --me (R4: the scheduler proves the lead from the
// process), so the task usage must not show one.
func TestDocsFTaskUsageShowsWidenWithoutMe(t *testing.T) {
	_, usage, _ := captureOutput(t, func() error { return runTask(nil) })
	var line string
	for _, l := range strings.Split(usage, "\n") {
		if strings.Contains(l, "task widen") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("task usage has no widen line:\n%s", usage)
	}
	if strings.Contains(line, "--me LEAD") || strings.Contains(line, "--me H") {
		t.Fatalf("task usage shows a --me for widen: %q", line)
	}
	if !strings.Contains(line, "amq-squad task widen ID --files GLOB,... --reason TEXT") {
		t.Fatalf("task usage widen line = %q", line)
	}
}

// Row 5: verify action's help lists exit 12 (no_gate).
func TestDocsFVerifyActionHelpListsExit12(t *testing.T) {
	stdout, stderr, err := captureOutput(t, func() error { return Run([]string{"verify", "action", "--help"}, "test") })
	if err != nil {
		t.Fatalf("verify action --help = %v", err)
	}
	help := stdout + stderr
	if !strings.Contains(help, "  12 no gate") {
		t.Fatalf("verify action --help has no exit 12:\n%s", help)
	}
	if ExitActionNoGate != 12 {
		t.Fatalf("ExitActionNoGate = %d, the help says 12", ExitActionNoGate)
	}
}

// Row 6: three help paths that left an agent with nothing.
func TestDocsFHelpPathsPrintUsageAndExitZero(t *testing.T) {
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"amq", "--help"}, []string{"amq-squad amq env", "amq-squad amq route --to HANDLE", "Examples:"}},
		{[]string{"amq", "help"}, []string{"amq-squad amq env"}},
		{[]string{"pi", "verify", "--help"}, []string{"amq-squad pi verify [--json]", "Examples:"}},
		{[]string{"verify", "release-plan", "--help"}, []string{"Usage:", "-repository", "-head", "-version", "-notes-policy", "-preflight-sha256"}},
	}
	for _, tc := range cases {
		stdout, stderr, err := captureOutput(t, func() error { return Run(tc.args, "test") })
		if err != nil {
			t.Errorf("%v: err = %v (exit %d), want help and exit 0", tc.args, err, ExitCode(err))
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(stdout+stderr, w) {
				t.Errorf("%v: help lacks %q:\n%s", tc.args, w, stdout+stderr)
			}
		}
	}
}

// Row 7: a repeated per-seat flag of team propose is refused with the comma
// form named; the comma form is accepted.
func TestDocsFTeamProposeRefusesRepeatedPerSeatFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	base := []string{"--project", project, "--roles", "cto,fullstack", "--lead", "cto"}
	pairs := map[string][2]string{
		"worktree":   {"cto=x", "fullstack=y"},
		"runtime":    {"cto=claude", "fullstack=codex"},
		"model":      {"cto=opus", "fullstack=gpt-5.5"},
		"effort":     {"cto=high", "fullstack=medium"},
		"tools":      {"cto=coding", "fullstack=full"},
		"actor-mode": {"cto=review", "fullstack=implementation"},
	}
	for _, verb := range []string{"propose", "apply"} {
		for flagName, v := range pairs {
			args := append([]string{verb}, base...)
			if verb == "apply" {
				args = append(args, "--digest", "sha256:x")
			}
			args = append(args, "--"+flagName, v[0], "--"+flagName, v[1])
			_, _, err := captureOutput(t, func() error { return runTeam(args) })
			want := "--" + flagName + " " + v[0] + "," + v[1]
			if err == nil || !strings.Contains(err.Error(), "more than once") || !strings.Contains(err.Error(), want) || ExitCode(err) != ExitUser {
				t.Errorf("team %s with a repeated --%s = %v (exit %d), want a refusal naming %q", verb, flagName, err, ExitCode(err), want)
			}
		}
	}
	comma := append(append([]string{}, base...), "--worktree", "cto=x,fullstack=y")
	p := teamProposeJSON(t, append([]string{"propose"}, comma...)...)
	if p.Seats != 2 {
		t.Fatalf("comma form proposal = %+v", p)
	}
	teamProposeJSON(t, append([]string{"apply", "--digest", p.Digest}, comma...)...)
	s, err := team.LoadSquad(project)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, seat := range s.Seats {
		got[seat.Handle] = seat.Worktree
	}
	if got["cto"] != "x" || got["fullstack"] != "y" {
		t.Fatalf("comma form worktrees = %v", got)
	}
}
