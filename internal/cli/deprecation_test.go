package cli

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// The five legacy verbs (top-level launch/restore/list and team show/launch)
// were removed in 2.0. Each must return a UsageError (exit 1) whose message
// names the modern replacement -- a helpful migration hint, NOT a silent
// unknown-command. These tests pin both the exit classification and the hint.

// assertRemovedHint runs args through the public Run dispatcher and asserts it
// returns a UsageError (exit 1) whose message contains each wanted substring.
func assertRemovedHint(t *testing.T, args []string, wants ...string) {
	t.Helper()
	_, _, err := captureOutput(t, func() error { return Run(args, "test") })
	if err == nil {
		t.Fatalf("Run %v: want UsageError, got nil", args)
	}
	var ue UsageError
	if !errors.As(err, &ue) {
		t.Fatalf("Run %v: want UsageError (exit 1), got %T: %v", args, err, err)
	}
	if code := ExitCode(err); code != ExitUser {
		t.Errorf("Run %v: want exit %d, got %d", args, ExitUser, code)
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run %v: error %q missing hint %q", args, err.Error(), want)
		}
	}
}

func TestLaunchVerbRemovedWithHint(t *testing.T) {
	assertRemovedHint(t, []string{"launch", "codex"}, "removed in 2.0", "agent up")
}

func TestRestoreVerbRemovedWithHint(t *testing.T) {
	// The restore hint must name both the print-mode replacement (history)
	// and the exec-mode replacement (agent resume).
	assertRemovedHint(t, []string{"restore", "--exec", "--role", "cto"},
		"removed in 2.0", "history", "agent resume")
}

func TestListVerbRemovedWithHint(t *testing.T) {
	assertRemovedHint(t, []string{"list"}, "removed in 2.0", "status", "history")
}

func TestTeamShowRemovedWithHint(t *testing.T) {
	assertRemovedHint(t, []string{"team", "show"}, "removed in 2.0", "up --dry-run")
}

func TestTeamLaunchRemovedWithHint(t *testing.T) {
	assertRemovedHint(t, []string{"team", "launch"}, "removed in 2.0", "up")
}

// The removed verbs must not be silently swallowed as unknown-command: the
// hint text proves we routed to the dedicated removal message, not the
// generic "unknown command" branch.
func TestRemovedVerbsAreNotUnknownCommand(t *testing.T) {
	cases := [][]string{
		{"launch"},
		{"restore"},
		{"list"},
		{"team", "show"},
		{"team", "launch"},
	}
	for _, args := range cases {
		_, _, err := captureOutput(t, func() error { return Run(args, "test") })
		if err == nil {
			t.Errorf("Run %v: want UsageError, got nil", args)
			continue
		}
		if strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown 'team' subcommand") {
			t.Errorf("Run %v: removed verb should emit a migration hint, not unknown-command: %q", args, err.Error())
		}
	}
}

// MODERN verbs that depend on the relocated launcher/replay/preview bodies
// must keep working unchanged. These guard against the silent-breakage trap:
// removing the dispatch must not strip the logic the modern verbs call.

// agent up still drives the real launcher body (runLaunch). --dry-run proves
// the launch flags were parsed and the coop-exec command was built.
func TestAgentUpDryRunStillWorks(t *testing.T) {
	withOutputPolicy(t, outputPolicy{})
	stdout, stderr, err := captureOutput(t, func() error {
		return runAgentUp([]string{"codex", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("agent up --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec") {
		t.Errorf("agent up --dry-run should print the coop exec command on stdout, got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "(dry run - no files written, not execing)") {
		t.Errorf("agent up --dry-run should honor the launch flag; got stderr:\n%s", stderr)
	}
	// The removed-verb warning machinery is gone: no deprecation line.
	if strings.Contains(stderr, "deprecated") {
		t.Errorf("agent up must not emit any deprecation line:\n%s", stderr)
	}
}

// agent resume still routes through the replay body (runRestore --exec --role).
// In an empty dir it surfaces the "no matching records" scan error -- proving
// the replay/scan body is reachable -- with no deprecation noise.
func TestAgentResumeRoutingStillWorks(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, stderr, err := captureOutput(t, func() error {
		return runAgentResume([]string{"cto"})
	})
	if err == nil {
		t.Fatal("agent resume in an empty dir should surface a scan error")
	}
	if !strings.Contains(err.Error(), "no matching launch.json records") {
		t.Errorf("agent resume should reach the replay/scan body; got: %v", err)
	}
	if strings.Contains(stderr, "deprecated") || strings.Contains(stderr, "'restore'") {
		t.Errorf("agent resume must not surface any legacy restore warning:\n%s", stderr)
	}
}

// Top-level resume still emits replay commands via emitCommand* / the restore
// helpers. It must run without error and reference the modern agent up shape.
func TestTopLevelResumeStillWorks(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	setupFakeAMQSessionRoots(t)
	stdout, _, err := captureOutput(t, func() error {
		return runResume([]string{"--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(stdout, "agent up") {
		t.Errorf("resume should emit modern 'agent up' replay commands, got:\n%s", stdout)
	}
}

// up --dry-run still drives the team preview body (emitTeamCommands). It must
// print one launch command per member with no deprecation noise.
func TestUpDryRunStillWorks(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	stdout, stderr, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("up --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "agent up") {
		t.Errorf("up --dry-run should print modern 'agent up' launch commands, got:\n%s", stdout)
	}
	if strings.Contains(stderr, "deprecated") {
		t.Errorf("up --dry-run must not emit any deprecation line:\n%s", stderr)
	}
}

// TestTranslateAgentUpArgs documents the reorder rule directly.
func TestTranslateAgentUpArgs(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"binary only", []string{"codex"}, []string{"codex"}},
		{"binary then flags", []string{"codex", "--dry-run", "--no-bootstrap"}, []string{"--dry-run", "--no-bootstrap", "codex"}},
		{"binary then mixed flags", []string{"claude", "--role", "cto", "--session", "issue-96"}, []string{"--role", "cto", "--session", "issue-96", "claude"}},
		{"binary flags then child", []string{"codex", "--role", "cto", "--", "--child", "x"}, []string{"--role", "cto", "codex", "--", "--child", "x"}},
		{"implicit child after bool flags", []string{"codex", "--dry-run", "--no-bootstrap", "test-prompt"}, []string{"--dry-run", "--no-bootstrap", "codex", "--", "test-prompt"}},
		{"implicit child after string-valued flag", []string{"codex", "--role", "cto", "test-prompt"}, []string{"--role", "cto", "codex", "--", "test-prompt"}},
		{"implicit child after --flag=value", []string{"codex", "--role=cto", "test-prompt"}, []string{"--role=cto", "codex", "--", "test-prompt"}},
		{"unknown -flag is child block", []string{"codex", "--dry-run", "--no-bootstrap", "--foo"}, []string{"--dry-run", "--no-bootstrap", "codex", "--", "--foo"}},
		{"unknown -flag with following positional", []string{"codex", "--foo", "bar"}, []string{"codex", "--", "--foo", "bar"}},
		{"all known launch flags after binary", []string{"codex", "--role", "cto", "--session", "issue-96"}, []string{"--role", "cto", "--session", "issue-96", "codex"}},
		{"bare string flag with no value is child", []string{"codex", "--dry-run", "--no-bootstrap", "--role"}, []string{"--dry-run", "--no-bootstrap", "codex", "--", "--role"}},
		{"string flag followed by dash-prefixed token is child", []string{"codex", "--dry-run", "--no-bootstrap", "--role", "--foo"}, []string{"--dry-run", "--no-bootstrap", "codex", "--", "--role", "--foo"}},
		{"codex-args consumes dash-prefixed value", []string{"codex", "--dry-run", "--no-bootstrap", "--codex-args", "--enable goals"}, []string{"--dry-run", "--no-bootstrap", "--codex-args", "--enable goals", "codex"}},
		{"claude-args consumes dash-prefixed value", []string{"claude", "--dry-run", "--no-bootstrap", "--claude-args", "--chrome"}, []string{"--dry-run", "--no-bootstrap", "--claude-args", "--chrome", "claude"}},
		{"trailing codex-args with no value is child", []string{"codex", "--dry-run", "--no-bootstrap", "--codex-args"}, []string{"--dry-run", "--no-bootstrap", "codex", "--", "--codex-args"}},
		{"post-binary --help routes to runLaunch help", []string{"codex", "--dry-run", "--help"}, []string{"--dry-run", "--help", "codex"}},
		{"post-binary -h routes to runLaunch help", []string{"codex", "--dry-run", "-h"}, []string{"--dry-run", "-h", "codex"}},
		{"explicit -- --help is child", []string{"codex", "--dry-run", "--", "--help"}, []string{"--dry-run", "codex", "--", "--help"}},
		{"help passthrough", []string{"--help"}, []string{"--help"}},
		{"empty", nil, nil},
	}
	for _, tc := range cases {
		got := translateAgentUpArgs(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s[%d]: got %q, want %q (got=%v want=%v)", tc.name, i, got[i], tc.want[i], got, tc.want)
				break
			}
		}
	}
}

// TestAgentUpHonorsPostBinaryFlags is the functional regression senior-dev
// flagged: a user typing `agent up codex --dry-run ...` should see the
// dry-run note on stderr, proving the launch flag was actually parsed.
func TestAgentUpHonorsPostBinaryFlags(t *testing.T) {
	withOutputPolicy(t, outputPolicy{})
	_, stderr, err := captureOutput(t, func() error {
		return runAgentUp([]string{"codex", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("agent up codex --dry-run --no-bootstrap: %v", err)
	}
	if !strings.Contains(stderr, "(dry run - no files written, not execing)") {
		t.Errorf("agent up should honor post-binary --dry-run; got stderr:\n%s", stderr)
	}
}

// Completion scripts must include the modern 'agent' verb and must NOT
// complete the removed legacy verbs (launch, restore, list) or the removed
// team subcommands (show, launch).
func TestCompletionDropsRemovedVerbs(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error { return runCompletion([]string{"bash"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agent", "team", "up", "down"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("bash completion missing modern verb %q", want)
		}
	}
	// Top-level removed verbs must be gone from the top-command list.
	for _, gone := range []string{"launch", "restore", "list"} {
		if containsString(completionTopCommands, gone) {
			t.Errorf("completionTopCommands should not list removed verb %q", gone)
		}
	}
	// Removed team subcommands must be gone from the team-subcommand list.
	for _, gone := range []string{"show", "launch"} {
		if containsString(completionTeamSubcommands, gone) {
			t.Errorf("completionTeamSubcommands should not list removed subcommand %q", gone)
		}
	}
}

func TestAgentDispatchCovered(t *testing.T) {
	t.Run("agent --help exits zero", func(t *testing.T) {
		_, stderr, err := captureOutput(t, func() error { return runAgent([]string{"--help"}) })
		if err != nil {
			t.Fatalf("agent --help: %v", err)
		}
		if !strings.Contains(stderr, "amq-squad agent") {
			t.Errorf("agent help missing header in:\n%s", stderr)
		}
	})
	t.Run("agent bare returns UsageError", func(t *testing.T) {
		_, _, err := captureOutput(t, func() error { return runAgent(nil) })
		if err == nil {
			t.Fatal("agent without subcommand should return UsageError")
		}
		if _, ok := err.(UsageError); !ok {
			t.Fatalf("want UsageError, got %T: %v", err, err)
		}
	})
	t.Run("agent unknown subcommand", func(t *testing.T) {
		_, _, err := captureOutput(t, func() error { return runAgent([]string{"banana"}) })
		if err == nil {
			t.Fatal("agent banana should fail")
		}
		if _, ok := err.(UsageError); !ok {
			t.Fatalf("want UsageError, got %T: %v", err, err)
		}
	})
}

// silence unused-import warnings when none of the tests above reference io.
var _ = io.Discard
