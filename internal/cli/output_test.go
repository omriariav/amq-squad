package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

// withOutputPolicy sets currentOutputPolicy for one test and restores it
// on cleanup. Tests that run command handlers directly use this so they
// can deterministically drive --quiet/--verbose/--color behavior.
func withOutputPolicy(t *testing.T, p outputPolicy) {
	t.Helper()
	prev := currentOutputPolicy
	currentOutputPolicy = p
	t.Cleanup(func() { currentOutputPolicy = prev })
}

func withOutputSeams(t *testing.T, isTTY bool, env map[string]string) {
	t.Helper()
	prevIsTTY := outputIsTTY
	prevGetenv := outputGetenv
	outputIsTTY = func() bool { return isTTY }
	outputGetenv = func(k string) string { return env[k] }
	t.Cleanup(func() {
		outputIsTTY = prevIsTTY
		outputGetenv = prevGetenv
	})
}

func TestParseGlobalFlagsRemovesQuietVerboseColor(t *testing.T) {
	withOutputSeams(t, true, nil)
	cases := []struct {
		name     string
		in       []string
		wantArgs []string
		wantQ    bool
		wantV    bool
		wantC    bool // expected resolved Color
	}{
		{"before subcommand", []string{"--quiet", "doctor"}, []string{"doctor"}, true, false, true},
		{"after subcommand", []string{"doctor", "--quiet"}, []string{"doctor"}, true, false, true},
		{"color always token", []string{"--color", "always", "status"}, []string{"status"}, false, false, true},
		{"color never joined", []string{"--color=never", "status"}, []string{"status"}, false, false, false},
		{"verbose after sub", []string{"doctor", "--verbose"}, []string{"doctor"}, false, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, pol, err := parseGlobalFlags(tc.in)
			if err != nil {
				t.Fatalf("parseGlobalFlags: %v", err)
			}
			if !reflect.DeepEqual(out, tc.wantArgs) {
				t.Errorf("args = %v, want %v", out, tc.wantArgs)
			}
			if pol.Quiet != tc.wantQ {
				t.Errorf("Quiet = %v, want %v", pol.Quiet, tc.wantQ)
			}
			if pol.Verbose != tc.wantV {
				t.Errorf("Verbose = %v, want %v", pol.Verbose, tc.wantV)
			}
			if pol.Color != tc.wantC {
				t.Errorf("Color = %v, want %v", pol.Color, tc.wantC)
			}
		})
	}
}

func TestParseGlobalFlagsDoesNotStripPastDashDash(t *testing.T) {
	withOutputSeams(t, true, nil)
	args := []string{"launch", "--quiet", "codex", "--", "--color", "always", "--quiet"}
	out, pol, err := parseGlobalFlags(args)
	if err != nil {
		t.Fatal(err)
	}
	if !pol.Quiet {
		t.Error("--quiet before -- should be parsed as global")
	}
	// The trailing --color always --quiet must stay intact for the child.
	want := []string{"launch", "codex", "--", "--color", "always", "--quiet"}
	if !reflect.DeepEqual(out, want) {
		t.Errorf("args = %v, want %v", out, want)
	}
}

func TestParseGlobalFlagsQuietVerboseConflict(t *testing.T) {
	withOutputSeams(t, true, nil)
	_, _, err := parseGlobalFlags([]string{"--quiet", "--verbose", "doctor"})
	if err == nil {
		t.Fatal("--quiet --verbose should conflict")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if ExitCode(err) != ExitUser {
		t.Errorf("ExitCode = %d, want ExitUser", ExitCode(err))
	}
}

func TestParseGlobalFlagsBadColorValue(t *testing.T) {
	withOutputSeams(t, true, nil)
	_, _, err := parseGlobalFlags([]string{"--color", "fuchsia", "doctor"})
	if err == nil {
		t.Fatal("bad --color value should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestParseGlobalFlagsMissingColorValue(t *testing.T) {
	withOutputSeams(t, true, nil)
	_, _, err := parseGlobalFlags([]string{"--color"})
	if err == nil {
		t.Fatal("--color without value should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestResolveColorMatrix(t *testing.T) {
	cases := []struct {
		name string
		mode string
		tty  bool
		env  map[string]string
		want bool
	}{
		{"auto + TTY", "auto", true, nil, true},
		{"auto + no TTY", "auto", false, nil, false},
		{"never + TTY", "never", true, nil, false},
		{"always + no TTY", "always", false, nil, true},
		{"NO_COLOR beats always", "always", true, map[string]string{"NO_COLOR": "1"}, false},
		{"NO_COLOR empty does not disable", "auto", true, map[string]string{"NO_COLOR": ""}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withOutputSeams(t, tc.tty, tc.env)
			got := resolveColorMode(tc.mode)
			if got != tc.want {
				t.Errorf("resolveColorMode(%q) tty=%v env=%v = %v, want %v", tc.mode, tc.tty, tc.env, got, tc.want)
			}
		})
	}
}

func TestColorizeRespectsPolicy(t *testing.T) {
	on := outputPolicy{Color: true}
	off := outputPolicy{Color: false}
	if got := colorize(on, ansiGreen, "ok"); !strings.Contains(got, ansiGreen) || !strings.Contains(got, ansiReset) {
		t.Errorf("colorize on: %q", got)
	}
	if got := colorize(off, ansiGreen, "ok"); got != "ok" {
		t.Errorf("colorize off: %q, want plain", got)
	}
}

// TestJSONOutputsHaveNoANSI guards the contract that --json output is pure
// JSON even when the resolved color policy is enabled.
func TestJSONOutputsHaveNoANSI(t *testing.T) {
	// Force color on regardless of TTY state.
	withOutputPolicy(t, outputPolicy{Color: true})
	stdout, _, err := captureOutput(t, func() error {
		return Run([]string{"version", "--json"}, "1.0.0")
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "\x1b[") {
		t.Errorf("JSON output contains ANSI escape:\n%s", stdout)
	}
}

// TestDoctorHumanOutputColorMatrix proves the doctor human table emits
// ANSI only when policy.Color is true and emits plain text otherwise.
func TestDoctorHumanOutputColorMatrix(t *testing.T) {
	dir := t.TempDir()
	stub := doctorExecution{
		ProjectDir:    dir,
		ResolveAMQEnv: func(string) (amqEnv, error) { return amqEnv{AMQVersion: "0.34.1"}, nil },
		LookPath:      func(string) (string, error) { return "/usr/bin/tmux", nil },
		Probe:         defaultDuplicateLaunchProbe,
	}
	t.Run("color on emits ANSI on status word", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{Color: true})
		var b strings.Builder
		stub.Out = &b
		_ = executeDoctor(stub)
		if !strings.Contains(b.String(), "\x1b[") {
			t.Errorf("expected ANSI in colored doctor output:\n%s", b.String())
		}
	})
	t.Run("color off emits plain text", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{Color: false})
		var b strings.Builder
		stub.Out = &b
		_ = executeDoctor(stub)
		if strings.Contains(b.String(), "\x1b[") {
			t.Errorf("plain doctor output should not contain ANSI:\n%s", b.String())
		}
	})
}

// TestDoctorJSONNeverANSIEvenWhenColorEnabled proves the typed status
// field in the JSON envelope stays plain when color is enabled. Without
// this guard, downstream JSON consumers would see "\x1b[32mok\x1b[0m".
func TestDoctorJSONNeverANSIEvenWhenColorEnabled(t *testing.T) {
	withOutputPolicy(t, outputPolicy{Color: true})
	dir := t.TempDir()
	stub := doctorExecution{
		ProjectDir:    dir,
		ResolveAMQEnv: func(string) (amqEnv, error) { return amqEnv{AMQVersion: "0.34.1"}, nil },
		LookPath:      func(string) (string, error) { return "/usr/bin/tmux", nil },
		Probe:         defaultDuplicateLaunchProbe,
		JSON:          true,
	}
	var b strings.Builder
	stub.Out = &b
	_ = executeDoctor(stub)
	if strings.Contains(b.String(), "\x1b[") {
		t.Errorf("doctor --json under color policy must be ANSI-free:\n%s", b.String())
	}
	// Bonus: typed status string must be exactly "ok" (or another vocab
	// member), never the ANSI-wrapped form.
	if !strings.Contains(b.String(), `"status": "ok"`) {
		t.Errorf("expected plain status string in JSON, got:\n%s", b.String())
	}
}

// TestStatusHumanOutputColorMatrix mirrors the doctor color matrix for
// status. statusExecution + classifyMemberStatus paths.
func TestStatusHumanOutputColorMatrix(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	t.Run("color on emits ANSI", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{Color: true})
		stdout, _, err := captureOutput(t, func() error {
			return runStatus([]string{"--session", "issue-96"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout, "\x1b[") {
			t.Errorf("expected ANSI in colored status output:\n%s", stdout)
		}
	})
	t.Run("color off emits plain", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{Color: false})
		stdout, _, err := captureOutput(t, func() error {
			return runStatus([]string{"--session", "issue-96"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stdout, "\x1b[") {
			t.Errorf("plain status output should not contain ANSI:\n%s", stdout)
		}
	})
}

// TestDownHumanOutputColorMatrix mirrors the doctor color matrix for the
// down table. Calls renderDownReports directly with a curated report set.
func TestDownHumanOutputColorMatrix(t *testing.T) {
	reports := []downReport{
		{Role: "cto", Status: downStatusStopped, Detail: "SIGTERM sent to pid 1"},
		{Role: "fullstack", Status: downStatusFailed, Detail: "boom"},
	}
	t.Run("color on emits ANSI", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{Color: true})
		var b strings.Builder
		_ = renderDownReports(&b, "stop", "issue-96", reports)
		if !strings.Contains(b.String(), "\x1b[") {
			t.Errorf("expected ANSI in colored down output:\n%s", b.String())
		}
	})
	t.Run("color off emits plain", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{Color: false})
		var b strings.Builder
		_ = renderDownReports(&b, "stop", "issue-96", reports)
		if strings.Contains(b.String(), "\x1b[") {
			t.Errorf("plain down output should not contain ANSI:\n%s", b.String())
		}
	})
}

// TestRunGlobalFlagFormsExercised drives Run() with the four positions cto
// listed (before/after subcommand for both --quiet and --color), proving
// the global parser hooks into the real dispatch path.
func TestRunGlobalFlagFormsExercised(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	cases := [][]string{
		{"--quiet", "doctor", "--json"},
		{"doctor", "--quiet", "--json"},
		{"--color=always", "doctor", "--json"},
		{"doctor", "--color", "always", "--json"},
	}
	stubDoctorSeams := func() func() {
		prevTTY := outputIsTTY
		prevEnv := outputGetenv
		outputIsTTY = func() bool { return true }
		outputGetenv = func(string) string { return "" }
		return func() { outputIsTTY = prevTTY; outputGetenv = prevEnv }
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			restore := stubDoctorSeams()
			defer restore()
			// Use the real Run entry point. Doctor will fail because tmux
			// may or may not be present in CI; we only care that the global
			// flag was parsed and the command dispatched.
			stdout, _, err := captureOutput(t, func() error { return Run(args, "test") })
			// Doctor exit code is irrelevant; we just need a successful
			// dispatch (no UsageError).
			if _, ok := err.(UsageError); ok {
				t.Fatalf("global flag should not produce UsageError: %v", err)
			}
			// JSON-mode doctor should produce a JSON envelope on stdout.
			if !strings.Contains(stdout, `"kind": "doctor"`) {
				t.Errorf("Run(%v) stdout missing doctor envelope:\n%s", args, stdout)
			}
		})
	}
}

func TestCompletionContainsGlobalFlags(t *testing.T) {
	cases := map[string][]string{
		"bash": {"--quiet", "--verbose", "--color"},
		"zsh":  {"'--quiet'", "'--verbose'", "'--color'"},
		// fish emits long flags with `-l 'name'`.
		"fish": {"-l 'quiet'", "-l 'verbose'", "-l 'color'"},
	}
	for shell, wants := range cases {
		stdout, _, err := captureOutput(t, func() error {
			return runCompletion([]string{shell})
		})
		if err != nil {
			t.Fatalf("%s: %v", shell, err)
		}
		for _, want := range wants {
			if !strings.Contains(stdout, want) {
				t.Errorf("%s completion missing %q", shell, want)
			}
		}
	}
}

func TestRootHelpDocumentsOutputFlags(t *testing.T) {
	stdout, _, err := captureOutput(t, func() error { return Run([]string{"--help"}, "test") })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--quiet", "--verbose", "--color auto|always|never"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("root --help missing %q in:\n%s", want, stdout)
		}
	}
}

// TestQuietSuppressesTeamRulesInitNotice covers a concrete locked notice
// site: `team rules init` writes "Wrote ..." to stderr. Under --quiet the
// line must be absent; without --quiet it must be present.
func TestQuietSuppressesTeamRulesInitNotice(t *testing.T) {
	t.Run("loud emits the wrote notice", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{})
		dir := t.TempDir()
		chdir(t, dir)
		_, stderr, err := captureOutput(t, func() error {
			return runTeam([]string{"rules", "init"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stderr, "Wrote ") {
			t.Errorf("loud team rules init should print Wrote notice, got stderr:\n%s", stderr)
		}
	})
	t.Run("quiet suppresses the wrote notice", func(t *testing.T) {
		withOutputPolicy(t, outputPolicy{Quiet: true})
		dir := t.TempDir()
		chdir(t, dir)
		_, stderr, err := captureOutput(t, func() error {
			return runTeam([]string{"rules", "init"})
		})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(stderr, "Wrote ") {
			t.Errorf("quiet team rules init should not print the Wrote notice, got stderr:\n%s", stderr)
		}
	})
}

// TestQuietDoesNotSuppressWarningsOrErrors confirms --quiet only gates
// non-data notices: actual error output still appears.
func TestQuietDoesNotSuppressWarningsOrErrors(t *testing.T) {
	withOutputPolicy(t, outputPolicy{Quiet: true})
	dir := t.TempDir()
	chdir(t, dir)
	// up in a dir with no team.json returns a non-UsageError, which the
	// command itself surfaces as a plain returned error. The error must
	// not be swallowed by --quiet (errors flow through main, not
	// quietNotice).
	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	})
	if err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("--quiet must not swallow command errors; got %v", err)
	}
}

// TestQuietDoesNotSuppressDryRunStdout covers the contract that --quiet
// only suppresses non-data stderr notices: stdout data (dry-run command
// preview) must still appear.
func TestQuietDoesNotSuppressDryRunStdout(t *testing.T) {
	withOutputPolicy(t, outputPolicy{Quiet: true})
	seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"},
		},
	})
	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "agent up") {
		t.Errorf("--quiet must not suppress dry-run command output on stdout:\n%s", stdout)
	}
}

// Verbose adds a footer line to the human doctor output but must not change
// JSON output.
func TestDoctorVerboseFooter(t *testing.T) {
	withOutputPolicy(t, outputPolicy{Verbose: true})
	dir := t.TempDir()
	d := doctorExecution{
		ProjectDir:    dir,
		Out:           nil,
		ResolveAMQEnv: func(string) (amqEnv, error) { return amqEnv{AMQVersion: "0.34.1"}, nil },
		LookPath:      func(string) (string, error) { return "/usr/bin/tmux", nil },
		Probe:         defaultDuplicateLaunchProbe,
	}
	var human strings.Builder
	d.Out = &human
	_ = executeDoctor(d)
	if !strings.Contains(human.String(), "verbose:") {
		t.Errorf("human doctor under --verbose should include footer; got:\n%s", human.String())
	}
	// JSON path: same policy, output should still be pure envelope.
	var jsonOut strings.Builder
	d.Out = &jsonOut
	d.JSON = true
	_ = executeDoctor(d)
	if strings.Contains(jsonOut.String(), "verbose:") {
		t.Errorf("JSON doctor output must not include the verbose footer:\n%s", jsonOut.String())
	}
}
