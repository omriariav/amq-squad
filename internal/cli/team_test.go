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

	"github.com/omriariav/amq-squad/internal/rules"
	"github.com/omriariav/amq-squad/internal/team"
)

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

func TestEmitTeamCommandShape(t *testing.T) {
	m := team.Member{
		Role:    "designer",
		Binary:  "claude",
		Handle:  "designer",
		Session: "designer",
	}
	cmd := emitTeamCommand("/home/u/proj", "amq-squad", "/home/u/proj", m, false, "proj")
	for _, want := range []string{
		"cd /home/u/proj",
		"amq-squad launch",
		"--role designer",
		"--session proj",
		"--team-workstream",
		"--team-home /home/u/proj",
		"--me designer",
		" claude",
		"-- --permission-mode auto",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("emitTeamCommand missing %q in: %s", want, cmd)
		}
	}
}

func TestEmitTeamCommandAddsCodexDefaultArgs(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}
	cmd := emitTeamCommand("/p", "amq-squad", "/p", m, false, "p")
	if !strings.Contains(cmd, "-- --dangerously-bypass-approvals-and-sandbox") {
		t.Errorf("expected codex default args in: %s", cmd)
	}
}

func TestEmitTeamCommandQuotesPathsWithSpaces(t *testing.T) {
	m := team.Member{Role: "cpo", Binary: "codex", Handle: "cpo", Session: "cpo"}
	cmd := emitTeamCommand("/home/user/my project", "amq-squad", "/home/user/my project", m, false, "my-project")
	if !strings.Contains(cmd, "'/home/user/my project'") {
		t.Errorf("project path not quoted: %s", cmd)
	}
}

func TestEmitTeamCommandUsesBinaryPath(t *testing.T) {
	m := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"}
	cmd := emitTeamCommand("/p", "/usr/local/bin/amq-squad", "/p", m, false, "p")
	if !strings.Contains(cmd, "/usr/local/bin/amq-squad launch") {
		t.Errorf("expected absolute binary path in: %s", cmd)
	}
}

func TestEmitTeamCommandNoBootstrap(t *testing.T) {
	m := team.Member{Role: "qa", Binary: "claude", Handle: "qa", Session: "qa"}
	cmd := emitTeamCommand("/p", "amq-squad", "/team", m, true, "team")
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
		"--session " + workstream + " --team-workstream --team-home",
		"--no-bootstrap --me cto codex",
		"--no-bootstrap --me fullstack claude",
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
	if !strings.Contains(stdout, "# workstream: issue-96") || !strings.Contains(stdout, "--session issue-96 --team-workstream --team-home") {
		t.Fatalf("team show did not use stored shared workstream:\n%s", stdout)
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

func TestShouldAppendBootstrapWithDefaultChildArgs(t *testing.T) {
	cases := []struct {
		name      string
		binary    string
		childArgs []string
		want      bool
	}{
		{name: "empty args", binary: "codex", want: true},
		{name: "codex defaults", binary: "codex", childArgs: []string{"--dangerously-bypass-approvals-and-sandbox"}, want: true},
		{name: "claude defaults", binary: "claude", childArgs: []string{"--permission-mode", "auto"}, want: true},
		{name: "non-default args", binary: "claude", childArgs: []string{"--resume", "abc"}, want: false},
		{name: "defaults plus custom args", binary: "codex", childArgs: []string{"--dangerously-bypass-approvals-and-sandbox", "--foo"}, want: false},
	}
	for _, tc := range cases {
		if got := shouldAppendBootstrap(tc.binary, tc.childArgs); got != tc.want {
			t.Errorf("%s: shouldAppendBootstrap(%q, %v) = %v, want %v", tc.name, tc.binary, tc.childArgs, got, tc.want)
		}
	}
}

func TestEnsureDefaultChildArgs(t *testing.T) {
	got := ensureDefaultChildArgs("codex", nil)
	want := []string{"--dangerously-bypass-approvals-and-sandbox"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ensureDefaultChildArgs codex = %v, want %v", got, want)
	}
	got = ensureDefaultChildArgs("claude", nil)
	want = []string{"--permission-mode", "auto"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ensureDefaultChildArgs claude = %v, want %v", got, want)
	}
	got = ensureDefaultChildArgs("codex", []string{"test-prompt"})
	want = []string{"--dangerously-bypass-approvals-and-sandbox", "test-prompt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ensureDefaultChildArgs should prepend defaults: got %v, want %v", got, want)
	}
	explicit := []string{"--dangerously-bypass-approvals-and-sandbox", "--resume", "abc"}
	got = ensureDefaultChildArgs("codex", explicit)
	if !reflect.DeepEqual(got, explicit) {
		t.Errorf("ensureDefaultChildArgs should not duplicate defaults: got %v, want %v", got, explicit)
	}
	got = ensureDefaultChildArgs("codex", []string{"test-prompt", "--dangerously-bypass-approvals-and-sandbox"})
	want = []string{"--dangerously-bypass-approvals-and-sandbox", "test-prompt", "--dangerously-bypass-approvals-and-sandbox"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ensureDefaultChildArgs should keep defaults before prompts: got %v, want %v", got, want)
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
	if got.Workstream != "issue-96" {
		t.Fatalf("team workstream = %q, want issue-96", got.Workstream)
	}
	for _, m := range got.Members {
		if m.Session != "issue-96" {
			t.Fatalf("member %s session = %q, want issue-96", m.Role, m.Session)
		}
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

	if err := runTeamInit([]string{"--personas", "cto", "--session", "cto"}); err != nil {
		t.Fatalf("runTeamInit: %v", err)
	}
	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamShow([]string{"--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("runTeamShow: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "# workstream: cto") || !strings.Contains(stdout, "--session cto --team-workstream") {
		t.Fatalf("single-member stored workstream was not honored:\n%s", stdout)
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

	if err := team.Write(dir, team.Team{
		Project:    dir,
		Workstream: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "cto"},
		},
	}); err != nil {
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
