package cli

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// The legacy verbs (top-level launch/restore/list, the old `down` alias, and
// team show/launch) are fully removed in 2.0. Each must now return a
// UsageError (exit 1); the modern replacements are documented in MIGRATION.md
// and the top-level --help "Removed in 2.0" note. These tests pin the exit
// classification so a removed verb can never silently succeed.

// assertRemovedUsageError runs args through the public Run dispatcher and
// asserts it returns a UsageError (exit 1).
func assertRemovedUsageError(t *testing.T, args []string) {
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
}

func TestRemovedVerbsReturnUsageError(t *testing.T) {
	cases := [][]string{
		{"launch", "codex"},
		{"restore", "--exec", "--role", "cto"},
		{"list"},
		{"down", "--role", "cto"},
		{"team", "show"},
		{"team", "launch"},
	}
	for _, args := range cases {
		assertRemovedUsageError(t, args)
	}
}

// MODERN verbs that depend on the relocated launcher/replay/preview bodies
// must keep working unchanged. These guard against the silent-breakage trap:
// removing the dispatch must not strip the logic the modern verbs call.

// agent up still drives the real launcher body (runLaunch). --dry-run proves
// the launch flags were parsed and the coop-exec command was built.
func TestAgentUpDryRunStillWorks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agent-mail"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
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

func TestAgentUpProjectDryRunTargetsOtherDir(t *testing.T) {
	withOutputPolicy(t, outputPolicy{})
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	expectedCWD, err := canonicalDir(project)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), ".agent-mail")
	script := `#!/bin/sh
if [ "$1" = "env" ]; then
  actual=$(pwd)
  if [ "$actual" != "$AMQ_EXPECT_CWD" ]; then
    echo "unexpected cwd: $actual" >&2
    exit 17
  fi
  printf '{"root":"%s"}\n' "$AMQ_FAKE_ROOT"
  exit 0
fi
echo "unexpected amq command: $*" >&2
exit 1
`
	setupFakeAMQScript(t, script)
	t.Setenv("AMQ_EXPECT_CWD", expectedCWD)
	t.Setenv("AMQ_FAKE_ROOT", root)

	stdout, stderr, err := captureOutput(t, func() error {
		return runAgentUp([]string{"codex", "--project", project, "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("agent up --project --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "amq coop exec") {
		t.Errorf("agent up --project --dry-run should print coop exec command, got:\n%s", stdout)
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
		{"post-binary --no-require-wake is a launch flag", []string{"codex", "--dry-run", "--no-require-wake"}, []string{"--dry-run", "--no-require-wake", "codex"}},
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

func TestDefaultMeFromRole(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"role no me injects handle", []string{"codex", "--role", "cto"}, []string{"codex", "--me", "cto", "--role", "cto"}},
		{"role=value form injects", []string{"claude", "--role=fullstack"}, []string{"claude", "--me", "fullstack", "--role=fullstack"}},
		{"hyphen role is a valid handle", []string{"claude", "--role", "frontend-dev"}, []string{"claude", "--me", "frontend-dev", "--role", "frontend-dev"}},
		{"uppercase role lowercased for handle", []string{"claude", "--role", "CTO"}, []string{"claude", "--me", "cto", "--role", "CTO"}},
		{"explicit me wins", []string{"codex", "--role", "cto", "--me", "cto2"}, []string{"codex", "--role", "cto", "--me", "cto2"}},
		{"me before role still wins", []string{"codex", "--me", "cto2", "--role", "cto"}, []string{"codex", "--me", "cto2", "--role", "cto"}},
		{"no role unchanged", []string{"codex", "--dry-run"}, []string{"codex", "--dry-run"}},
		{"exotic role keeps basename default", []string{"codex", "--role", "Senior Dev"}, []string{"codex", "--role", "Senior Dev"}},
		{"role only in child block ignored", []string{"codex", "--no-bootstrap", "--", "--role", "x"}, []string{"codex", "--no-bootstrap", "--", "--role", "x"}},
		{"bare role with no value unchanged", []string{"codex", "--dry-run", "--role"}, []string{"codex", "--dry-run", "--role"}},
		{"role followed by dash token unchanged", []string{"codex", "--role", "--dry-run"}, []string{"codex", "--role", "--dry-run"}},
		{"role after other string flag", []string{"claude", "--session", "issue-96", "--role", "cto"}, []string{"claude", "--me", "cto", "--session", "issue-96", "--role", "cto"}},
		{"binary only", []string{"codex"}, []string{"codex"}},
		{"help passthrough", []string{"--help"}, []string{"--help"}},
		{"empty", nil, nil},
	}
	for _, tc := range cases {
		got := defaultMeFromRole(tc.in)
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
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agent-mail"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, dir)
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
	for _, want := range []string{"agent", "team", "up", "stop"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("bash completion missing modern verb %q", want)
		}
	}
	// Top-level removed verbs must be gone from the top-command list.
	for _, gone := range []string{"launch", "restore", "list", "down"} {
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
