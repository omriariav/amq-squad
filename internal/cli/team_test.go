package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

func setupFakeAMQSessionRoots(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, ".agent-mail")
	script := `#!/bin/sh
if [ "$1" = "doctor" ]; then
  shift
  root_arg=""
  fix_mailboxes=""
  json=""
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --root)
        shift
        root_arg="$1"
        ;;
      --fix-mailboxes)
        fix_mailboxes="yes"
        ;;
      --json)
        json="yes"
        ;;
    esac
    shift
  done
  if [ "$root_arg" = "" ] || [ "$fix_mailboxes" != "yes" ] || [ "$json" != "yes" ]; then
    echo "unexpected amq doctor command" >&2
    exit 1
  fi
  printf '{"mailbox_repair":{"created_paths":[]},"summary":{"error":0}}\n'
  exit 0
fi
if [ "$1" != "env" ]; then
  echo "unexpected amq command: $*" >&2
  exit 1
fi
session=""
root_arg=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --session)
      shift
      session="$1"
      ;;
    --root)
      shift
      root_arg="$1"
      ;;
  esac
  shift
done
root="$AMQ_FAKE_BASE"
if [ "$root_arg" != "" ]; then
  root="$root_arg"
elif [ "$session" != "" ]; then
  root="$root/$session"
fi
if [ "$AMQ_FAKE_VERSION" != "" ]; then
  printf '{"root":"%s","amq_version":"%s"}\n' "$root" "$AMQ_FAKE_VERSION"
else
  printf '{"root":"%s"}\n' "$root"
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_BASE", base)
	t.Setenv("AMQ_FAKE_VERSION", doctorMinAMQVersion)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return base
}

func TestParseKV(t *testing.T) {
	got, err := parseKV("qa=codex, pm=codex,CTO=claude")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"qa": "codex", "pm": "codex", "cto": "claude"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if empty, err := parseKV(""); err != nil || len(empty) != 0 {
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
	if want := []string{"--enable", "goals", "--label", "hello world", "--name", "cto lead"}; !reflect.DeepEqual(got, want) {
		t.Errorf("parseAgentArgs = %#v, want %#v", got, want)
	}
	if _, err := parseAgentArgs(`--label "unterminated`); err == nil {
		t.Fatal("parseAgentArgs should reject unterminated quotes")
	}
}

// snapshotTree maps every file under dir to its bytes.
func snapshotTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		out[path] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func teamProposeJSON(t *testing.T, args ...string) team.Plan {
	t.Helper()
	stdout, stderr, err := captureOutput(t, func() error { return runTeam(append(args, "--json")) })
	if err != nil {
		t.Fatalf("team %v: %v\n%s", args, err, stderr)
	}
	return decodeJSONEnvelope[team.Plan](t, stdout).Data
}

// Acceptance 3: propose prints one digest for every file it would create and
// writes nothing; apply with that digest writes them; a stale digest writes
// nothing.
func TestTeamProposeWritesNothingApplyWritesDigestedFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "CLAUDE.md"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"--project", project, "--roles", "cto,fullstack,qa", "--runtime", "fullstack=codex,qa=pi", "--model", "cto=opus",
		"--effort", "cto=high", "--lead", "cto", "--lead-mode", "planner", "--execution-mode", "project_team", "--session", "feature-x"}
	before := snapshotTree(t, project)
	p := teamProposeJSON(t, append([]string{"propose"}, args...)...)
	if !reflect.DeepEqual(snapshotTree(t, project), before) {
		t.Fatal("team propose changed the tree")
	}
	paths := []string{}
	for _, f := range p.Files {
		paths = append(paths, strings.TrimPrefix(f.Path, project+string(filepath.Separator))+":"+f.Action)
	}
	want := []string{"squad.yaml:create", ".amq-squad/team-rules.md:create", "CLAUDE.md:replace", "AGENTS.md:create"}
	if !reflect.DeepEqual(paths, want) || !strings.HasPrefix(p.Digest, "sha256:") || p.Seats != 3 {
		t.Fatalf("proposal = %+v (paths %v)", p, paths)
	}
	if again := teamProposeJSON(t, append([]string{"propose"}, args...)...); again.Digest != p.Digest {
		t.Fatal("the digest is not deterministic")
	}
	if _, _, err := captureOutput(t, func() error { return runTeam(append([]string{"apply", "--digest", "sha256:stale"}, args...)) }); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("stale digest = %v", err)
	}
	if !reflect.DeepEqual(snapshotTree(t, project), before) {
		t.Fatal("a stale apply changed the tree")
	}
	applied := teamProposeJSON(t, append([]string{"apply", "--digest", p.Digest}, args...)...)
	if applied.Digest != p.Digest {
		t.Fatalf("apply digest %s != %s", applied.Digest, p.Digest)
	}
	tm, err := team.Read(project)
	if err != nil {
		t.Fatal(err)
	}
	if tm.Lead != "cto" || tm.LeadMode != team.LeadModePlanner || len(tm.Members) != 3 || tm.Members[1].Binary != "codex" || tm.Members[2].Binary != "pi" || tm.Members[0].Effort != "high" {
		t.Fatalf("written squad = %+v", tm)
	}
	claude, _ := os.ReadFile(filepath.Join(project, "CLAUDE.md"))
	rulesBody, _ := os.ReadFile(filepath.Join(project, ".amq-squad", "team-rules.md"))
	if !strings.Contains(string(claude), "# mine") || !strings.Contains(string(rulesBody), "handle `fullstack`, runtime `codex`") {
		t.Fatalf("CLAUDE.md:\n%s\nteam-rules.md:\n%s", claude, rulesBody)
	}
	// After apply every planned file is unchanged.
	for _, f := range teamProposeJSON(t, append([]string{"propose"}, args...)...).Files {
		if f.Action != "unchanged" {
			t.Errorf("%s is %s after apply", f.Path, f.Action)
		}
	}
	out, _, err := captureOutput(t, func() error { return runTeam([]string{"check", "--project", project}) })
	if err != nil || !strings.Contains(out, "3 seats, valid") {
		t.Fatalf("check = %q, %v", out, err)
	}
	out, _, err = captureOutput(t, func() error { return runTeam([]string{"show", "--project", project}) })
	if err != nil || !strings.Contains(out, "fullstack  fullstack  codex") {
		t.Fatalf("show = %q, %v", out, err)
	}
}

func TestTeamProposeFromFileAndRefusals(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := t.TempDir()
	draft := filepath.Join(t.TempDir(), "squad.yaml")
	if err := os.WriteFile(draft, []byte(team.ExampleSquad), 0o644); err != nil {
		t.Fatal(err)
	}
	// the example's custom-free roles are catalog roles except researcher
	if _, _, err := captureOutput(t, func() error { return runTeam([]string{"propose", "--project", project, "--from", draft}) }); err == nil || !strings.Contains(err.Error(), "researcher") {
		t.Fatalf("unstaged custom role = %v", err)
	}
	if err := os.MkdirAll(team.RolesDir(project), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(team.CustomRolePath(project, "researcher"), []byte("# researcher\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := teamProposeJSON(t, "propose", "--project", project, "--from", draft)
	if p.Seats != 3 || p.Files[0].Action != "create" {
		t.Fatalf("proposal = %+v", p)
	}
	// a widening squad.yaml is refused by name
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte("max_seats: 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error { return runTeam([]string{"propose", "--project", project, "--from", draft}) })
	if !errors.Is(err, team.ErrWidens) || !strings.Contains(err.Error(), "max_seats") {
		t.Fatalf("widening = %v", err)
	}
	for verb, want := range map[string]string{"init": "team propose", "sync": "team apply", "rules": "draft-rules", "operator": "operator_interaction"} {
		if err := runTeam([]string{verb}); err == nil || !strings.Contains(err.Error(), "removed in amq-squad 3.0") || !strings.Contains(err.Error(), want) {
			t.Errorf("team %s = %v", verb, err)
		}
	}
	if err := runTeam([]string{"propose", "--project", project}); err == nil || !strings.Contains(err.Error(), "--roles or --from") {
		t.Fatalf("no seats = %v", err)
	}
	if err := runTeam([]string{"propose", "--project", project, "--roles", "cto", "--model", "qa=x"}); err == nil || !strings.Contains(err.Error(), "does not select") {
		t.Fatalf("unknown role override = %v", err)
	}
}
