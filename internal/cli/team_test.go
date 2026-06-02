package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// Persisted team args that contradict the trust profile must be caught
// before launch commands are emitted, not surface as per-pane errors after
// tmux panes are open.
func TestEmitTeamCommandsRejectsPersistedSandboxedBypass(t *testing.T) {
	dir := t.TempDir()
	if err := team.Write(dir, team.Team{
		Trust:      trustModeSandboxed,
		BinaryArgs: map[string][]string{"codex": {"--dangerously-bypass-approvals-and-sandbox"}},
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	err := emitTeamCommands(dir, emitTeamOptions{})
	if err == nil {
		t.Fatal("sandboxed team with bypass in stored binary args should fail")
	}
	if !strings.Contains(err.Error(), "trusted") {
		t.Fatalf("error should suggest trusted: %v", err)
	}
}

func TestEmitTeamCommandsRejectsUnknownModelRoleKey(t *testing.T) {
	dir := t.TempDir()
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	err := emitTeamCommands(dir, emitTeamOptions{
		ModelOverrides: map[string]string{"ctoo": "gpt-5"},
	})
	if err == nil {
		t.Fatal("unknown role key in --model should fail")
	}
	if !strings.Contains(err.Error(), "ctoo") {
		t.Fatalf("error should name the bad key: %v", err)
	}
}

func TestSplitCSV(t *testing.T) {
	cases := map[string][]string{
		"a,b,c":       {"a", "b", "c"},
		" a , b , c ": {"a", "b", "c"},
		",,a,,":       {"a"},
		"":            {},
		"  ":          {},
	}
	for in, want := range cases {
		got := splitCSV(in)
		if !reflect.DeepEqual(got, want) && !(len(got) == 0 && len(want) == 0) {
			t.Errorf("splitCSV(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseKV(t *testing.T) {
	got, err := parseKV("qa=codex, pm=codex,cto=claude")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"qa": "codex", "pm": "codex", "cto": "claude"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	empty, err := parseKV("")
	if err != nil || len(empty) != 0 {
		t.Errorf("parseKV(\"\") = %v, %v", empty, err)
	}

	for _, bad := range []string{"nokey", "=noval", "key="} {
		if _, err := parseKV(bad); err == nil {
			t.Errorf("parseKV(%q) expected error", bad)
		}
	}
}

func TestParseAgentArgs(t *testing.T) {
	got, err := parseAgentArgs(`--enable goals --label "hello world" --name 'cto lead'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--enable", "goals", "--label", "hello world", "--name", "cto lead"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseAgentArgs = %#v, want %#v", got, want)
	}
	if _, err := parseAgentArgs(`--label "unterminated`); err == nil {
		t.Fatal("parseAgentArgs should reject unterminated quotes")
	}
}

func TestEmitTeamCommandShape(t *testing.T) {
	m := team.Member{
		Role:    "designer",
		Binary:  "claude",
		Handle:  "designer",
		Session: "designer",
	}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/home/u/proj", SquadBin: "amq-squad", TeamHome: "/home/u/proj",
		Member: m, Workstream: "proj", TrustMode: trustModeSandboxed,
	})
	for _, want := range []string{
		"cd /home/u/proj",
		"amq-squad agent up claude",
		"--role designer",
		"--session proj",
		"--team-workstream",
		"--team-home /home/u/proj",
		"--me designer",
		"-- --permission-mode auto",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitTeamCommand missing %q in: %s", want, cmd)
		}
	}
}

func TestEmitTeamCommandIncludesCustomLauncher(t *testing.T) {
	m := team.Member{
		Role:         "qa",
		Binary:       "claude",
		Handle:       "qa",
		Launcher:     "/opt/scripts/pm-os-dev.sh",
		LauncherArgs: []string{"--pull", "--workspace", "/x"},
	}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/repo", SquadBin: "amq-squad", Member: m, Workstream: "issue-96", TrustMode: trustModeSandboxed,
	})
	for _, want := range []string{
		"--launcher /opt/scripts/pm-os-dev.sh",
		"--launcher-args=",
		"--pull",
		"--workspace",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitTeamCommand missing %q in: %s", want, cmd)
		}
	}
}

func TestEmitTeamCommandSandboxedCodexOmitsBypass(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/p", SquadBin: "amq-squad", TeamHome: "/p",
		Member: m, Workstream: "p", TrustMode: trustModeSandboxed,
	})
	if strings.Contains(cmd, "--dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("sandboxed codex should not include bypass arg in: %s", cmd)
	}
	if !strings.Contains(cmd, "--trust sandboxed") {
		t.Errorf("expected --trust sandboxed in: %s", cmd)
	}
}

func TestEmitTeamCommandTrustedCodexIncludesBypass(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/p", SquadBin: "amq-squad", TeamHome: "/p",
		Member: m, Workstream: "p", TrustMode: trustModeTrusted,
	})
	if !strings.Contains(cmd, "-- --dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("trusted codex must include bypass arg in: %s", cmd)
	}
	if !strings.Contains(cmd, "--trust trusted") {
		t.Errorf("expected --trust trusted in: %s", cmd)
	}
}

func TestEmitTeamCommandIncludesModelOverride(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto", Model: "gpt-5"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/p", SquadBin: "amq-squad", TeamHome: "/p",
		Member: m, Workstream: "p", TrustMode: trustModeSandboxed, Model: m.Model,
	})
	if !strings.Contains(cmd, "--model gpt-5") {
		t.Errorf("expected --model gpt-5 launch flag in: %s", cmd)
	}
	if !strings.Contains(cmd, "agent up codex") {
		t.Errorf("expected modern 'agent up codex' surface in: %s", cmd)
	}
	if !strings.Contains(cmd, "-- --model gpt-5") {
		t.Errorf("expected codex native --model child arg in: %s", cmd)
	}
}

func TestEmitTeamCommandClaudeModelPlacement(t *testing.T) {
	m := team.Member{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "fullstack", Model: "sonnet"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/p", SquadBin: "amq-squad", TeamHome: "/p",
		Member: m, Workstream: "p", TrustMode: trustModeSandboxed, Model: m.Model,
	})
	if !strings.Contains(cmd, "agent up claude") {
		t.Errorf("expected modern 'agent up claude' surface in: %s", cmd)
	}
	if !strings.Contains(cmd, "-- --permission-mode auto --model sonnet") {
		t.Errorf("expected claude default + model child placement in: %s", cmd)
	}
}

func TestEmitTeamCommandAddsConfiguredBinaryArgs(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/p", SquadBin: "amq-squad", TeamHome: "/p",
		Member: m, Workstream: "p", TrustMode: trustModeTrusted,
		BinaryArgs: map[string][]string{"codex": {"--enable", "goals"}},
	})
	for _, want := range []string{
		"agent up codex",
		"--codex-args='--enable goals'",
		"-- --dangerously-bypass-approvals-and-sandbox --enable goals",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitTeamCommand missing %q in: %s", want, cmd)
		}
	}
}

func TestEmitTeamCommandQuotesPathsWithSpaces(t *testing.T) {
	m := team.Member{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "cpo"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/home/user/my project", SquadBin: "amq-squad", TeamHome: "/home/user/my project",
		Member: m, Workstream: "my-project", TrustMode: trustModeSandboxed,
	})
	if !strings.Contains(cmd, "'/home/user/my project'") {
		t.Errorf("project path not quoted: %s", cmd)
	}
}

func TestEmitTeamCommandUsesBinaryPath(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/p", SquadBin: "/usr/local/bin/amq-squad", TeamHome: "/p",
		Member: m, Workstream: "p", TrustMode: trustModeSandboxed,
	})
	if !strings.Contains(cmd, "/usr/local/bin/amq-squad agent up") {
		t.Errorf("expected absolute binary path with modern agent up surface in: %s", cmd)
	}
}

func TestEmitTeamCommandNoBootstrap(t *testing.T) {
	m := team.Member{Role: "qa", Binary: "claude", Handle: "qa", Session: "qa"}
	cmd := emitTeamCommand(emitTeamCommandInput{
		CWD: "/p", SquadBin: "amq-squad", TeamHome: "/team",
		Member: m, NoBootstrap: true, Workstream: "team", TrustMode: trustModeSandboxed,
	})
	if !strings.Contains(cmd, "--no-bootstrap") {
		t.Errorf("expected --no-bootstrap in: %s", cmd)
	}
}

func TestRunTeamShowUsesDefaultSharedWorkstream(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "fullstack"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamShow: %v\nstderr:\n%s", err, stderr)
	}
	workstream := defaultWorkstreamName(dir)
	for _, want := range []string{
		"# workstream: " + workstream,
		"--session " + workstream + " --team-workstream",
		"agent up codex",
		"agent up claude",
		"--no-bootstrap --me cto",
		"--no-bootstrap --me fullstack",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("team show output missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "--session cto") || strings.Contains(stdout, "--session fullstack") {
		t.Fatalf("team show used role sessions instead of default workstream:\n%s", stdout)
	}
}

func TestRunTeamShowUsesStoredSharedWorkstream(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamShow: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "# workstream: issue-96") || !strings.Contains(stdout, "--session issue-96 --team-workstream") {
		t.Fatalf("team show did not use stored shared workstream:\n%s", stdout)
	}
}

func TestRunTeamShowMergesStoredAndRunBinaryArgs(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Trust:      trustModeTrusted,
		BinaryArgs: map[string][]string{"codex": {"--enable", "goals"}},
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--no-bootstrap", "--codex-args=--profile fast"})
	})
	if err != nil {
		t.Fatalf("runTeamShow: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# binary args: codex: --enable goals --profile fast",
		"agent up codex",
		"--codex-args='--enable goals --profile fast'",
		"-- --dangerously-bypass-approvals-and-sandbox --enable goals --profile fast",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("team show output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestRunTeamShowRejectsEmptyExplicitSession(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = captureOutput(t, func() error {
		return runTeamShow([]string{"--session", ""})
	})
	if err == nil || !strings.Contains(err.Error(), "session name cannot be empty") {
		t.Fatalf("runTeamShow error = %v, want empty session rejection", err)
	}
}

func TestRunTeamShowFreshRejectsExistingWorkstream(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	if err := os.MkdirAll(filepath.Join(base, "issue-96"), 0o755); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}

	_, _, err = captureOutput(t, func() error {
		return runTeamShow([]string{"--session", "issue-96", "--fresh", "--no-bootstrap"})
	})
	if err == nil || !strings.Contains(err.Error(), `workstream session "issue-96" already exists`) {
		t.Fatalf("runTeamShow error = %v, want existing workstream rejection", err)
	}
}

func TestRunTeamShowFreshAllowsNewWorkstream(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--session", "issue-97", "--fresh", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamShow: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "# workstream: issue-97") {
		t.Fatalf("team show output missing fresh workstream:\n%s", stdout)
	}
}

func setupFakeAMQSessionRoots(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, ".agent-mail")
	script := `#!/bin/sh
if [ "$1" != "env" ]; then
  echo "unexpected amq command: $*" >&2
  exit 1
fi
session=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --session)
      shift
      session="$1"
      ;;
  esac
  shift
done
root="$AMQ_FAKE_BASE"
if [ "$session" != "" ]; then
  root="$root/$session"
fi
printf '{"root":"%s"}\n' "$root"
`
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_BASE", base)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return base
}

func setupFakeAMQRelativeSessionRoots(t *testing.T) {
	t.Helper()
	script := `#!/bin/sh
if [ "$1" != "env" ]; then
  echo "unexpected amq command: $*" >&2
  exit 1
fi
session=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --session)
      shift
      session="$1"
      ;;
  esac
  shift
done
root=".agent-mail"
if [ "$session" != "" ]; then
  root="$root/$session"
fi
printf '{"root":"%s","base_root":".agent-mail"}\n' "$root"
`
	setupFakeAMQScript(t, script)
}

func TestShouldAppendBootstrapWithDefaultChildArgs(t *testing.T) {
	// Sandboxed Codex has no built-in default args, so bootstrap should still
	// be appended on empty input. Trusted Codex behaves like the legacy default.
	cases := []struct {
		name      string
		binary    string
		childArgs []string
		want      bool
	}{
		{name: "empty args codex", binary: "codex", want: true},
		{name: "empty args claude", binary: "claude", want: true},
		{name: "claude defaults", binary: "claude", childArgs: []string{"--permission-mode", "auto"}, want: true},
		{name: "non-default args", binary: "claude", childArgs: []string{"--resume", "abc"}, want: false},
		{name: "codex sandboxed has no defaults so bypass alone is non-default", binary: "codex", childArgs: []string{"--dangerously-bypass-approvals-and-sandbox"}, want: false},
	}
	for _, tc := range cases {
		if got := shouldAppendBootstrap(tc.binary, tc.childArgs); got != tc.want {
			t.Errorf("%s: shouldAppendBootstrap(%q, %v) = %v, want %v", tc.name, tc.binary, tc.childArgs, got, tc.want)
		}
	}
	defaults := []string{"--dangerously-bypass-approvals-and-sandbox", "--enable", "goals"}
	if !shouldAppendBootstrapWithDefaults(defaults, defaults) {
		t.Errorf("configured binary args should still allow bootstrap")
	}
	if shouldAppendBootstrapWithDefaults([]string{"--dangerously-bypass-approvals-and-sandbox", "--enable", "goals", "prompt"}, defaults) {
		t.Errorf("configured binary args plus prompt should not auto-bootstrap")
	}
}

func TestEnsureDefaultChildArgs(t *testing.T) {
	// Sandboxed Codex (the new default) has no built-in defaults to ensure.
	got := ensureDefaultChildArgs("codex", nil)
	if len(got) != 0 {
		t.Errorf("ensureDefaultChildArgs sandboxed codex = %v, want []", got)
	}
	got = ensureDefaultChildArgs("claude", nil)
	want := []string{"--permission-mode", "auto"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ensureDefaultChildArgs claude = %v, want %v", got, want)
	}
	// Trusted codex still prepends the bypass arg.
	trusted := defaultChildArgsForBinaryWithTrust("codex", trustModeTrusted)
	if got := ensureLeadingChildArgs(trusted, nil); !reflect.DeepEqual(got, []string{"--dangerously-bypass-approvals-and-sandbox"}) {
		t.Errorf("trusted codex defaults: got %v", got)
	}
	if got := ensureLeadingChildArgs(trusted, []string{"test-prompt"}); !reflect.DeepEqual(got, []string{"--dangerously-bypass-approvals-and-sandbox", "test-prompt"}) {
		t.Errorf("trusted codex prepend: got %v", got)
	}
	explicit := []string{"--dangerously-bypass-approvals-and-sandbox", "--resume", "abc"}
	if got := ensureLeadingChildArgs(trusted, explicit); !reflect.DeepEqual(got, explicit) {
		t.Errorf("trusted codex idempotent: got %v", got)
	}
	got = ensureLeadingChildArgs([]string{"--dangerously-bypass-approvals-and-sandbox", "--enable", "goals"}, []string{"--dangerously-bypass-approvals-and-sandbox", "prompt"})
	want = []string{"--dangerously-bypass-approvals-and-sandbox", "--enable", "goals", "prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ensureLeadingChildArgs should insert missing configured defaults after existing prefix: got %v, want %v", got, want)
	}
}

func TestPromptPersonaSelection(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("4,2\n"))
	var out bytes.Buffer
	got, err := promptPersonaSelection(reader, &out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"fullstack", "cto"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("promptPersonaSelection = %v, want %v", got, want)
	}
	if !strings.Contains(out.String(), "Squad market") {
		t.Errorf("prompt output missing squad market: %s", out.String())
	}
}

func TestPrintPersonaMarketIncludesEmployeeProfiles(t *testing.T) {
	var out bytes.Buffer
	printPersonaMarket(&out)
	got := out.String()
	for _, want := range []string{
		"frontend-dev",
		"Frontend Developer",
		"mobile-dev",
		"Mobile Developer",
		"junior-dev",
		"Fast on scoped tasks",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("market output missing %q in:\n%s", want, got)
		}
	}
}

func TestParsePersonaSelection(t *testing.T) {
	got, err := parsePersonaSelection("junior-dev,2")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"junior-dev", "cto"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parsePersonaSelection = %v, want %v", got, want)
	}
	got, err = parsePersonaSelection("all")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || got[0] != "cpo" {
		t.Errorf("parsePersonaSelection all = %v, want catalog IDs", got)
	}
	if _, err := parsePersonaSelection("999"); err == nil {
		t.Error("parsePersonaSelection should reject out-of-range numbers")
	}
}

func TestPromptBinarySelection(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("fullstack=codex\n"))
	var out bytes.Buffer
	overrides := map[string]string{}
	if err := promptBinarySelection(reader, &out, []string{"fullstack", "qa"}, overrides); err != nil {
		t.Fatal(err)
	}
	if overrides["fullstack"] != "codex" {
		t.Errorf("fullstack override = %q, want codex", overrides["fullstack"])
	}
	if _, ok := overrides["qa"]; ok {
		t.Errorf("qa should keep default, got override %q", overrides["qa"])
	}
	if !strings.Contains(out.String(), "Squad plan") || !strings.Contains(out.String(), "Updated squad plan") {
		t.Errorf("prompt output missing squad plans: %s", out.String())
	}
}

func TestPromptBinarySelectionPreservesFlagOverride(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("\n"))
	var out bytes.Buffer
	overrides := map[string]string{"fullstack": "codex"}
	if err := promptBinarySelection(reader, &out, []string{"fullstack"}, overrides); err != nil {
		t.Fatal(err)
	}
	if overrides["fullstack"] != "codex" {
		t.Errorf("fullstack override = %q, want codex", overrides["fullstack"])
	}
	if !strings.Contains(out.String(), "fullstack") || !strings.Contains(out.String(), "codex") {
		t.Errorf("prompt should show existing override in plan: %s", out.String())
	}
}

func TestRunTeamInitPersonasAliasAndBinaryOverride(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	if err := runTeamInit([]string{"--personas", "fullstack", "--binary", "fullstack=codex"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 {
		t.Fatalf("members = %v, want one", got.Members)
	}
	m := got.Members[0]
	if m.Role != "fullstack" || m.Binary != "codex" {
		t.Fatalf("member = %+v, want fullstack on codex", m)
	}
}

func TestRunTeamInitStoresBinaryArgs(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	if err := runTeamInit([]string{
		"--personas", "cto,fullstack",
		"--codex-args=--enable goals",
		"--claude-args=--chrome",
	}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.BinaryArgs["codex"], []string{"--enable", "goals"}) {
		t.Fatalf("codex args = %#v", got.BinaryArgs["codex"])
	}
	if !reflect.DeepEqual(got.BinaryArgs["claude"], []string{"--chrome"}) {
		t.Fatalf("claude args = %#v", got.BinaryArgs["claude"])
	}
}

func TestRunTeamInitUsesExplicitSharedWorkstream(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	if err := runTeamInit([]string{"--personas", "cto,fullstack", "--session", "issue-96"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 2 {
		t.Fatalf("members = %v, want two", got.Members)
	}
	// init no longer pins the deprecated workstream default; the chosen
	// session lives on the members and is recovered via inference at resolve
	// time.
	if got.Workstream != "" {
		t.Fatalf("team workstream = %q, want empty (init must not pin the deprecated default)", got.Workstream)
	}
	for _, m := range got.Members {
		if m.Session != "issue-96" {
			t.Fatalf("member %s session = %q, want issue-96", m.Role, m.Session)
		}
	}
}

func TestRunTeamInitDryRunPrintsProfilePreviewWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamInit([]string{"--roles", "cto,qa", "--session", "issue-96", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("team init --dry-run: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# amq-squad team init --dry-run",
		"# writes: none",
		"# profile: default",
		"# workstream: issue-96",
		"ROLE",
		"cto",
		"qa",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("dry-run output missing %q:\n%s", want, stdout)
		}
	}
	if team.Exists(dir) {
		t.Fatal("team init --dry-run must not write team.json")
	}
	if _, err := os.Stat(rules.Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("team init --dry-run must not write team-rules.md; stat err = %v", err)
	}
}

func TestRunTeamInitDryRunJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	wantDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamInit([]string{
			"--roles", "cto,qa",
			"--session", "issue-96",
			"--model", "cto=gpt-5",
			"--codex-args", "--enable goals",
			"--dry-run",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("team init --dry-run --json: %v\nstderr:\n%s", err, stderr)
	}
	env := decodeJSONEnvelope[teamProfilePlan](t, stdout)
	if env.Kind != "team_profile_plan" {
		t.Errorf("kind = %q, want team_profile_plan", env.Kind)
	}
	if env.Data.TeamHome != wantDir || env.Data.Project != wantDir {
		t.Errorf("team home/project = %q/%q, want %q", env.Data.TeamHome, env.Data.Project, wantDir)
	}
	if env.Data.Profile != team.DefaultProfile {
		t.Errorf("profile = %q, want default", env.Data.Profile)
	}
	if env.Data.Workstream != "issue-96" {
		t.Errorf("workstream = %q, want issue-96", env.Data.Workstream)
	}
	if env.Data.ExistingProfile {
		t.Errorf("existing_profile = true, want false")
	}
	if env.Data.Members != 2 || len(env.Data.Plan) != 2 {
		t.Fatalf("members/plan = %d/%d, want 2/2", env.Data.Members, len(env.Data.Plan))
	}
	var sawCTO bool
	for _, m := range env.Data.Plan {
		if m.Role == "cto" {
			sawCTO = true
			if m.Model != "gpt-5" {
				t.Errorf("cto model = %q, want gpt-5", m.Model)
			}
			if m.CWD != wantDir || m.Session != "issue-96" {
				t.Errorf("cto cwd/session = %q/%q, want %q/issue-96", m.CWD, m.Session, wantDir)
			}
		}
	}
	if !sawCTO {
		t.Fatalf("plan missing cto: %+v", env.Data.Plan)
	}
	if got := env.Data.BinaryArgs["codex"]; !reflect.DeepEqual(got, []string{"--enable", "goals"}) {
		t.Errorf("codex binary args = %#v, want --enable goals", got)
	}
	if team.Exists(dir) {
		t.Fatal("team init --dry-run --json must not write team.json")
	}
	if _, err := os.Stat(rules.Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("team init --dry-run --json must not write team-rules.md; stat err = %v", err)
	}
}

func TestRunTeamInitJSONRequiresDryRun(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runTeamInit([]string{"--roles", "cto", "--json"})
	})
	if err == nil {
		t.Fatal("team init --json without --dry-run must error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("error should mention --dry-run: %v", err)
	}
}

func TestRunTeamInitDryRunCanPreviewExistingProfileWithoutForce(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "old"}},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamInit([]string{"--roles", "qa", "--session", "issue-97", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("team init --dry-run existing profile: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "existing-profile: yes") {
		t.Fatalf("dry-run should flag existing profile:\n%s", stdout)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Members) != 1 || got.Members[0].Role != "cto" {
		t.Fatalf("dry-run should not overwrite existing profile, got %+v", got.Members)
	}
}

func TestRunTeamInitStoresSingleMemberSharedWorkstream(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	// A non-legacy session name (distinct from the role) is recovered via
	// single-member inference at resolve time; init no longer pins it.
	if err := runTeamInit([]string{"--personas", "cto", "--session", "issue-96"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Workstream != "" {
		t.Fatalf("team workstream = %q, want empty (init must not pin the deprecated default)", got.Workstream)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamShow: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "# workstream: issue-96") || !strings.Contains(stdout, "--session issue-96 --team-workstream") {
		t.Fatalf("single-member shared workstream was not inferred:\n%s", stdout)
	}
}

func TestRunTeamInitRejectsOldPerRoleSessionSyntax(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	err = runTeamInit([]string{"--personas", "cto,fullstack", "--session", "cto=stream1,fullstack=stream2"})
	if err == nil || !strings.Contains(err.Error(), "old per-role --session syntax is no longer supported") {
		t.Fatalf("runTeamInit error = %v, want old --session syntax rejection", err)
	}
}

func TestRunTeamInitMarketPersonasAndBinaryOverrides(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	err = runTeamInit([]string{
		"--personas", "cto,frontend-dev,mobile-dev,junior-dev,qa",
		"--binary", "frontend-dev=codex,mobile-dev=codex",
	})
	if err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	wantBinary := map[string]string{
		"cto":          "codex",
		"frontend-dev": "codex",
		"mobile-dev":   "codex",
		"junior-dev":   "codex",
		"qa":           "claude",
	}
	if len(got.Members) != len(wantBinary) {
		t.Fatalf("members = %v, want %d members", got.Members, len(wantBinary))
	}
	for _, m := range got.Members {
		want, ok := wantBinary[m.Role]
		if !ok {
			t.Errorf("unexpected member %+v", m)
			continue
		}
		if m.Binary != want {
			t.Errorf("member %s binary = %q, want %q", m.Role, m.Binary, want)
		}
		if m.Handle != m.Role || m.Session != defaultWorkstreamName(dir) {
			t.Errorf("member %s handle/session = %q/%q, want role handle and default workstream", m.Role, m.Handle, m.Session)
		}
	}
}

func TestRunTeamInitRejectsRolesAndPersonasTogether(t *testing.T) {
	err := runTeamInit([]string{"--roles", "cto", "--personas", "fullstack"})
	if err == nil || !strings.Contains(err.Error(), "either --personas or --roles") {
		t.Fatalf("runTeamInit error = %v, want roles/personas conflict", err)
	}
}

func TestRunTeamInitSeedsTeamRules(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	if err := runTeamInit([]string{"--roles", "pm,fullstack"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	if !team.Exists(dir) {
		t.Fatalf("team.json was not written")
	}
	if _, err := os.Stat(rules.Path(dir)); err != nil {
		t.Fatalf("team-rules.md was not written: %v", err)
	}
	got, err := os.ReadFile(rules.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	for _, want := range []string{
		"## Role Scope",
		"On first session run, start the first response by stating your role and handle",
		"pm (Project Manager / Product Owner)",
		"Turns feedback into scoped tasks for the right owner. Does not implement code unless explicitly assigned by the user.",
		"fullstack (Fullstack Developer)",
		"Owns scoped end-to-end implementation",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("team-rules.md missing %q in:\n%s", want, body)
		}
	}
}

func TestRunTeamInitDoesNotClobberTeamRules(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
	custom := "custom rules\n"
	if err := os.MkdirAll(filepath.Dir(rules.Path(dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(dir), []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runTeamInit([]string{"--roles", "cto"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	got, err := os.ReadFile(rules.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != custom {
		t.Fatalf("team-rules.md was clobbered: got %q, want %q", string(got), custom)
	}
}

func TestRunTeamRulesInitForceRefreshesScopedRules(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	if err := team.Write(dir, team.Team{
		Project: dir,
		Members: []team.Member{
			{Role: "pm", Binary: "codex", Handle: "pm", Session: "pm"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "fullstack"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(dir, "old generic stub\n"); err != nil {
		t.Fatal(err)
	}

	if err := runTeamRules([]string{"init", "--force"}); err != nil {
		t.Fatalf("runTeamRules init --force: %v", err)
	}
	got, err := os.ReadFile(rules.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	if strings.Contains(body, "old generic stub") {
		t.Fatalf("team-rules.md was not refreshed:\n%s", body)
	}
	for _, want := range []string{
		"pm (Project Manager / Product Owner)",
		"Turns feedback into scoped tasks for the right owner. Does not implement code unless explicitly assigned by the user.",
		"fullstack (Fullstack Developer)",
		fmt.Sprintf("default workstream `%s`", defaultWorkstreamName(dir)),
		"On first session run, start the first response by stating your role and handle",
		"Use the `amq-squad` skill for team setup",
		"Use `amq-cli` only for raw AMQ debugging",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("team-rules.md missing %q in:\n%s", want, body)
		}
	}
	for _, legacy := range []string{
		"default workstream `pm`",
		"default workstream `fullstack`",
	} {
		if strings.Contains(body, legacy) {
			t.Errorf("team-rules.md contains legacy role session %q in:\n%s", legacy, body)
		}
	}
}

func TestRunTeamRulesShowPrintsScopedRules(t *testing.T) {
	dir := t.TempDir()
	body := "# Team Rules\n\ncustom rules\n"
	if err := rules.Write(dir, body); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamRules([]string{"show", "--project", dir})
	})
	if err != nil {
		t.Fatalf("runTeamRules show: %v\nstderr:\n%s", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("runTeamRules show should be silent on stderr, got:\n%s", stderr)
	}
	if stdout != body {
		t.Fatalf("stdout = %q, want %q", stdout, body)
	}
}

func TestRunTeamRulesShowReportsMissingRules(t *testing.T) {
	dir := t.TempDir()

	_, _, err := captureOutput(t, func() error {
		return runTeamRules([]string{"show", "--project", dir})
	})
	if err == nil {
		t.Fatal("runTeamRules show without team-rules.md should fail")
	}
	for _, want := range []string{"no team-rules.md", rules.Path(dir), "amq-squad team rules init"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestRunTeamRulesInitUsesStoredWorkstreamEvenWhenItMatchesRole(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})

	// Legacy member session ("cto" == role) is not inferable, so the deprecated
	// pin shim ("cto") is the resolved source: the rules still render it AND the
	// deprecation notice fires on stderr.
	if err := team.Write(dir, team.Team{
		Project:    dir,
		Workstream: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := captureOutput(t, func() error {
		return runTeamRules([]string{"init", "--force"})
	})
	if err != nil {
		t.Fatalf("runTeamRules init --force: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "deprecated") || !strings.Contains(stderr, "cto") {
		t.Fatalf("pin shim path must emit the deprecation notice; stderr:\n%s", stderr)
	}
	got, err := os.ReadFile(rules.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	if !strings.Contains(body, "default workstream `cto`") {
		t.Fatalf("team-rules.md did not honor stored workstream:\n%s", body)
	}
}

func TestUniqueMemberCWDs(t *testing.T) {
	home := "/home/u/proj-a"
	members := []team.Member{
		{Role: "cto", CWD: ""},              // inherits home
		{Role: "cpo", CWD: ""},              // inherits home
		{Role: "qa", CWD: "/home/u/proj-b"}, // different project
		{Role: "fullstack", CWD: home},      // explicit but same as home
	}
	got := uniqueMemberCWDs(home, members)
	if len(got) != 2 {
		t.Fatalf("uniqueMemberCWDs = %v, want 2 entries", got)
	}
	if got[0] != "/home/u/proj-a" || got[1] != "/home/u/proj-b" {
		t.Errorf("uniqueMemberCWDs = %v, want [proj-a proj-b]", got)
	}
}

func TestSyncTargetDirsRejectsOutsideTeamHome(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	_, err := syncTargetDirs(home, []team.Member{{Role: "qa", CWD: outside}}, false)
	if err == nil || !strings.Contains(err.Error(), "outside team-home") {
		t.Fatalf("syncTargetDirs error = %v, want outside team-home", err)
	}
}

func TestSyncTargetDirsAllowsOutsideWhenExplicit(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	got, err := syncTargetDirs(home, []team.Member{{Role: "qa", CWD: outside}}, true)
	if err != nil {
		t.Fatalf("syncTargetDirs: %v", err)
	}
	want, err := filepath.EvalSymlinks(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("syncTargetDirs = %v, want [%s]", got, want)
	}
}

func TestSyncTargetDirsRequiresExistingDirectory(t *testing.T) {
	home := t.TempDir()
	missing := filepath.Join(home, "missing")
	_, err := syncTargetDirs(home, []team.Member{{Role: "qa", CWD: missing}}, true)
	if err == nil || !strings.Contains(err.Error(), "no such file") {
		t.Fatalf("syncTargetDirs error = %v, want missing dir", err)
	}
}

func TestEnsureTeamHomeSyncTargetUsesCanonicalPath(t *testing.T) {
	realHome := t.TempDir()
	linkParent := t.TempDir()
	linkHome := filepath.Join(linkParent, "team-home")
	if err := os.Symlink(realHome, linkHome); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	canonical, err := filepath.EvalSymlinks(linkHome)
	if err != nil {
		t.Fatal(err)
	}
	got, err := ensureTeamHomeSyncTarget([]string{canonical}, linkHome)
	if err != nil {
		t.Fatalf("ensureTeamHomeSyncTarget: %v", err)
	}
	if len(got) != 1 || got[0] != canonical {
		t.Fatalf("ensureTeamHomeSyncTarget = %v, want one canonical target %s", got, canonical)
	}
}

func TestExpandPathTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	cases := map[string]string{
		"~":               home,
		"~/Code/proj":     filepath.Join(home, "Code", "proj"),
		"/already/abs":    "/already/abs",
		"relative/subdir": "", // expect an absolute result; exact value depends on cwd
	}
	for in, want := range cases {
		got, err := expandPath(in)
		if err != nil {
			t.Errorf("expandPath(%q) err: %v", in, err)
			continue
		}
		if !filepath.IsAbs(got) {
			t.Errorf("expandPath(%q) = %q, not absolute", in, got)
		}
		if want != "" && got != want {
			t.Errorf("expandPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEffectiveCWDFallback(t *testing.T) {
	m := team.Member{Role: "cto"} // CWD empty
	if got := m.EffectiveCWD("/home/u/proj"); got != "/home/u/proj" {
		t.Errorf("EffectiveCWD empty: got %q, want /home/u/proj", got)
	}
	m.CWD = "/other"
	if got := m.EffectiveCWD("/home/u/proj"); got != "/other" {
		t.Errorf("EffectiveCWD set: got %q, want /other", got)
	}
}
