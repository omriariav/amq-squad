package cli

import (
	"io"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

// TestDeprecationWarningFormat documents the exact one-line shape so
// downstream scripts/parsers can rely on it.
func TestDeprecationWarningFormat(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		deprecationWarning("team show", "up --dry-run")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "warning: 'team show' is deprecated and will be removed in 2.0; use 'up --dry-run' instead\n"
	if stderr != want {
		t.Errorf("deprecation line = %q, want %q", stderr, want)
	}
}

func TestTeamShowEmitsDeprecationWarning(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--no-bootstrap"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "'team show' is deprecated") || !strings.Contains(stderr, "use 'up --dry-run'") {
		t.Errorf("team show stderr should carry the deprecation line, got:\n%s", stderr)
	}
	// Warning must not leak onto stdout (the launch-plan output channel).
	if strings.Contains(stdout, "deprecated") {
		t.Errorf("team show stdout polluted with deprecation warning:\n%s", stdout)
	}
}

func TestTeamLaunchEmitsDeprecationWarning(t *testing.T) {
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	_, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "'team launch' is deprecated") || !strings.Contains(stderr, "use 'up'") {
		t.Errorf("team launch stderr should carry the deprecation line, got:\n%s", stderr)
	}
}

func TestTeamLaunchFreshSessionHintsAtFork(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	_ = dir
	setupFakeAMQSessionRoots(t)
	_, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--dry-run", "--fresh", "--session", "issue-97", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "fork --from issue-96 --as issue-97") {
		t.Errorf("team launch --fresh --session X should hint at fork --from <current> --as X; got:\n%s", stderr)
	}
}

func TestListEmitsDeprecationWarning(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	stdout, stderr, err := captureOutput(t, func() error {
		return runList(nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "'list' is deprecated") {
		t.Errorf("list stderr should carry the deprecation line, got:\n%s", stderr)
	}
	// JSON callers must see pure JSON on stdout.
	_, stderrJSON, err := captureOutput(t, func() error {
		return runList([]string{"--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderrJSON, "deprecated") {
		t.Errorf("list --json should still emit deprecation on stderr:\n%s", stderrJSON)
	}
	_ = stdout
}

func TestLaunchEmitsDeprecationWarning(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		return runLaunch([]string{"--dry-run", "--no-bootstrap", "codex"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "'launch' is deprecated") || !strings.Contains(stderr, "agent up") {
		t.Errorf("launch stderr should carry deprecation -> agent up, got:\n%s", stderr)
	}
}

// When agent up delegates to runLaunch, the legacy warning must NOT fire.
func TestAgentUpDoesNotEmitDeprecationWarning(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		return runAgentUp([]string{"--dry-run", "--no-bootstrap", "codex"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr, "'launch' is deprecated") {
		t.Errorf("agent up must not surface the legacy launch warning:\n%s", stderr)
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

func TestRestoreEmitsDeprecationWarning(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	t.Run("print mode -> history hint", func(t *testing.T) {
		// In an empty dir restore returns a "no matching records" error;
		// the deprecation warning is what we care about.
		_, stderr, _ := captureOutput(t, func() error { return runRestore(nil) })
		if !strings.Contains(stderr, "'restore' is deprecated") || !strings.Contains(stderr, "history") {
			t.Errorf("restore stderr should hint history, got:\n%s", stderr)
		}
	})
	t.Run("exec mode -> agent resume hint", func(t *testing.T) {
		// --exec without a match returns a system error; we only care that the
		// deprecation hint targets agent resume R.
		_, stderr, _ := captureOutput(t, func() error {
			return runRestore([]string{"--exec", "--role", "cto"})
		})
		if !strings.Contains(stderr, "agent resume R") {
			t.Errorf("restore --exec --role R should hint agent resume R, got:\n%s", stderr)
		}
	})
}

// agent resume must NOT surface the legacy restore warning when it
// delegates internally.
func TestAgentResumeDoesNotEmitRestoreWarning(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	_, stderr, _ := captureOutput(t, func() error {
		return runAgentResume([]string{"cto"})
	})
	if strings.Contains(stderr, "'restore'") {
		t.Errorf("agent resume must not surface the legacy restore warning:\n%s", stderr)
	}
}

// Help paths on every deprecated verb must NOT emit the deprecation line.
func TestDeprecatedHelpPathsAreQuiet(t *testing.T) {
	cases := [][]string{
		{"team", "show", "--help"},
		{"team", "launch", "--help"},
		{"list", "--help"},
		{"launch", "--help"},
		{"restore", "--help"},
	}
	for _, args := range cases {
		_, stderr, err := captureOutput(t, func() error { return Run(args, "test") })
		if err != nil {
			t.Errorf("Run %v: %v", args, err)
		}
		if strings.Contains(stderr, "deprecated") {
			t.Errorf("Run %v: help path should not emit deprecation warning, got:\n%s", args, stderr)
		}
	}
}

// JSON callers on deprecated verbs must still get pure JSON on stdout;
// warning goes to stderr.
func TestDeprecatedJSONStdoutPure(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	stdout, _, err := captureOutput(t, func() error {
		return runList([]string{"--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, `"schema_version"`) {
		t.Errorf("list --json stdout should be JSON envelope:\n%s", stdout)
	}
	if strings.Contains(stdout, "deprecated") {
		t.Errorf("list --json stdout polluted with warning:\n%s", stdout)
	}
}

// --quiet must NOT suppress the deprecation warning (warnings != notices).
func TestDeprecationWarningSurvivesQuiet(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withOutputPolicy(t, outputPolicy{Quiet: true})
	_, stderr, err := captureOutput(t, func() error {
		return runList(nil)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "'list' is deprecated") {
		t.Errorf("--quiet should not suppress the deprecation line; got stderr:\n%s", stderr)
	}
}

// Completion scripts include 'agent' (modern verb) without scaring the
// legacy verbs off the list per Step 12 contract.
func TestCompletionIncludesAgentAndKeepsLegacyVerbs(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error { return runCompletion([]string{"bash"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"agent", "team", "up", "down", "list", "launch", "restore"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("bash completion missing %q", want)
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
