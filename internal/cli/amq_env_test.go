package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAMQEnvUsesV1Fields(t *testing.T) {
	setupFakeAMQEnv(t, `{"schema_version":1,"amq_version":"0.34.0","root":"/tmp/mail/stream1","base_root":"/tmp/mail","session_name":"stream1","me":"cto","project":"amq-squad","root_source":"project_amqrc"}`)

	got, err := resolveAMQEnv("/ignored", "stream1", "codex")
	if err != nil {
		t.Fatalf("resolveAMQEnv: %v", err)
	}
	if got.SchemaVersion != 1 || got.AMQVersion != "0.34.0" ||
		got.Root != "/tmp/mail/stream1" || got.BaseRoot != "/tmp/mail" ||
		got.SessionName != "stream1" || got.Me != "cto" ||
		got.Project != "amq-squad" || got.RootSource != "project_amqrc" {
		t.Fatalf("resolveAMQEnv = %+v", got)
	}
}

func TestResolveAMQEnvBackfillsOlderRootOnlyOutput(t *testing.T) {
	setupFakeAMQEnv(t, `{"root":"/tmp/mail/stream1"}`)

	got, err := resolveAMQEnv("", "stream1", "cto")
	if err != nil {
		t.Fatalf("resolveAMQEnv: %v", err)
	}
	if got.Root != "/tmp/mail/stream1" || got.BaseRoot != "/tmp/mail/stream1" ||
		got.SessionName != "stream1" || got.Me != "cto" {
		t.Fatalf("resolveAMQEnv = %+v", got)
	}
}

func TestResolveAMQEnvInDirClearsInheritedAMQIdentity(t *testing.T) {
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"env\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"  actual_cwd=$(pwd)\n" +
		"  if [ \"$actual_cwd\" != \"$AMQ_EXPECT_CWD\" ]; then\n" +
		"    echo \"unexpected cwd: $actual_cwd\" >&2\n" +
		"    exit 2\n" +
		"  fi\n" +
		"  if [ -n \"$AM_ROOT$AM_BASE_ROOT$AM_ME\" ]; then\n" +
		"    printf '%s\\n' '{\"root\":\"/live/session\",\"base_root\":\"/live\",\"me\":\"cto\",\"project\":\"live-project\"}'\n" +
		"    exit 0\n" +
		"  fi\n" +
		"  printf '%s\\n' '{\"root\":\"/target/session\",\"base_root\":\"/target\",\"me\":\"amq-squad\",\"project\":\"target-project\"}'\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unexpected amq command: $*\" >&2\n" +
		"exit 1\n"
	setupFakeAMQScript(t, script)
	projectDir := t.TempDir()
	expectedCWD := projectDir
	if resolved, err := filepath.EvalSymlinks(projectDir); err == nil {
		expectedCWD = resolved
	}
	t.Setenv("AMQ_EXPECT_CWD", expectedCWD)
	t.Setenv("AM_ROOT", "/live/session")
	t.Setenv("AM_BASE_ROOT", "/live")
	t.Setenv("AM_ME", "cto")

	got, err := resolveAMQEnvInDir(projectDir, "", "", "amq-squad")
	if err != nil {
		t.Fatalf("resolveAMQEnvInDir: %v", err)
	}
	if got.Root != "/target/session" || got.BaseRoot != "/target" ||
		got.Me != "amq-squad" || got.Project != "target-project" {
		t.Fatalf("resolveAMQEnvInDir = %+v", got)
	}
}

// TestResolveAMQEnvDropsRootWhenSessionProvided covers the boundary fix
// for the mutual-exclusion bug: amq treats --session NAME as shorthand
// for --root .agent-mail/<name> and rejects the call when both are set.
// resolveAMQEnvInDir must forward only --session in that case. The fake
// amq exits 2 with a recognizable error when it sees --root, so a regress
// would fail this test.
func TestResolveAMQEnvDropsRootWhenSessionProvided(t *testing.T) {
	// Fake amq: exit 2 if --root is seen, exit 2 if --session is missing,
	// success otherwise. The two-sided check proves resolveAMQEnvInDir
	// both drops --root AND forwards --session — dropping both would also
	// fail without the missing-session guard.
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"env\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"  saw_session=0\n" +
		"  for arg in \"$@\"; do\n" +
		"    if [ \"$arg\" = \"--root\" ]; then\n" +
		"      echo 'fake amq: --session and --root are mutually exclusive' >&2\n" +
		"      exit 2\n" +
		"    fi\n" +
		"    if [ \"$arg\" = \"--session\" ]; then\n" +
		"      saw_session=1\n" +
		"    fi\n" +
		"  done\n" +
		"  if [ \"$saw_session\" != \"1\" ]; then\n" +
		"    echo 'fake amq: --session must be forwarded' >&2\n" +
		"    exit 2\n" +
		"  fi\n" +
		"  printf '%s\\n' '{\"root\":\"/p/.agent-mail/stream1\",\"base_root\":\"/p/.agent-mail\",\"session_name\":\"stream1\",\"me\":\"cto\"}'\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unexpected amq command: $*\" >&2\n" +
		"exit 1\n"
	setupFakeAMQScript(t, script)

	got, err := resolveAMQEnv("/p/.agent-mail", "stream1", "cto")
	if err != nil {
		t.Fatalf("resolveAMQEnv with both flags must drop --root and keep --session: %v", err)
	}
	if got.SessionName != "stream1" || got.Me != "cto" {
		t.Fatalf("resolveAMQEnv = %+v", got)
	}
}

// TestResolveAMQEnvWarnsWhenBothFlagsPresent: silent override of
// operator-supplied --root would be worse than the prior failure, so the
// boundary policy emits a stderr warning naming the dropped flag.
func TestResolveAMQEnvWarnsWhenBothFlagsPresent(t *testing.T) {
	setupFakeAMQEnv(t, `{"root":"/p/.agent-mail/stream1","session_name":"stream1","me":"cto"}`)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = origStderr }()

	if _, err := resolveAMQEnv("/p/.agent-mail/some/override", "stream1", "cto"); err != nil {
		t.Fatalf("resolveAMQEnv: %v", err)
	}
	w.Close()
	out, _ := io.ReadAll(r)
	got := string(out)
	if !strings.Contains(got, "ignoring conflicting --root") {
		t.Errorf("expected stderr warning when both --session and --root supplied; got: %q", got)
	}
	if !strings.Contains(got, "stream1") {
		t.Errorf("warning should name the session: %q", got)
	}
}

func TestResolveAMQEnvForTeamProfileUsesExplicitRootNotSession(t *testing.T) {
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"env\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"  saw_root=0\n" +
		"  saw_session=0\n" +
		"  root_value=''\n" +
		"  while [ \"$#\" -gt 0 ]; do\n" +
		"    case \"$1\" in\n" +
		"      --root)\n" +
		"        saw_root=1\n" +
		"        shift\n" +
		"        root_value=\"$1\"\n" +
		"        ;;\n" +
		"      --session)\n" +
		"        saw_session=1\n" +
		"        ;;\n" +
		"    esac\n" +
		"    shift\n" +
		"  done\n" +
		"  if [ \"$saw_session\" = \"1\" ]; then\n" +
		"    echo 'fake amq: named-profile status/liveness must not use --session' >&2\n" +
		"    exit 2\n" +
		"  fi\n" +
		"  if [ \"$saw_root\" != \"1\" ]; then\n" +
		"    echo 'fake amq: named-profile status/liveness must pass --root' >&2\n" +
		"    exit 2\n" +
		"  fi\n" +
		"  printf '{\"root\":\"%s\",\"base_root\":\"%s/.agent-mail/review\",\"me\":\"cto\"}\\n' \"$root_value\" \"$AMQ_TEST_PROJECT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unexpected amq command: $*\" >&2\n" +
		"exit 1\n"
	setupFakeAMQScript(t, script)
	project := t.TempDir()
	t.Setenv("AMQ_TEST_PROJECT", project)

	got, err := resolveAMQEnvForTeamProfile(project, "review", "review", "cto")
	if err != nil {
		t.Fatalf("resolveAMQEnvForTeamProfile: %v", err)
	}
	if got.Root != filepath.Join(project, ".agent-mail", "review", "review") {
		t.Fatalf("Root = %q", got.Root)
	}
	if got.SessionName != "review" {
		t.Fatalf("SessionName = %q, want backfilled review", got.SessionName)
	}
}

// TestResolveAMQEnvIncludesStderrOnFailure covers #46: amq env failures
// must surface stderr text in the wrapped error. Previously cmd.Output()
// dropped stderr and operators only saw "amq env: exit status N".
func TestResolveAMQEnvIncludesStderrOnFailure(t *testing.T) {
	script := "#!/bin/sh\n" +
		"echo 'error: cannot resolve workstream \"ghost\" under .agent-mail/' >&2\n" +
		"exit 2\n"
	setupFakeAMQScript(t, script)

	_, err := resolveAMQEnv("", "ghost", "cto")
	if err == nil {
		t.Fatal("expected error from amq env exit 2")
	}
	if !strings.Contains(err.Error(), "cannot resolve workstream") {
		t.Errorf("error should include stderr text from amq; got: %v", err)
	}
	if !strings.Contains(err.Error(), "exit status 2") {
		t.Errorf("error should still include exit status; got: %v", err)
	}
}

// TestChooseProjectBaseRootPrefersAgentMailContainer covers the real-bug fix:
// `amq env` reports an unreliable base_root when it believes it is "in a
// session" (base_root points at the project dir, or "."/".."), while the real
// sessions container — the `.agent-mail` directory — is in `root`. The board
// scans <baseRoot>/<session>/agents, so chooseProjectBaseRoot must return the
// candidate whose basename is `.agent-mail` and which exists on disk, across all
// three observed live layouts.
func TestChooseProjectBaseRootPrefersAgentMailContainer(t *testing.T) {
	projectDir := t.TempDir()
	agentMail := filepath.Join(projectDir, ".agent-mail")
	// Seed a real session dir so the chosen container exists as a directory.
	if err := os.MkdirAll(filepath.Join(agentMail, "issue-96", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		env  amqEnv
	}{
		{
			// omri-pm (in_session): base_root points at the PROJECT dir, the
			// real container is in root=<project>/.agent-mail.
			name: "in-session base_root is the project dir",
			env: amqEnv{
				BaseRoot:  projectDir,
				Root:      agentMail,
				InSession: true,
			},
		},
		{
			// taboola-sales-skills: base_root="." (relative to the project),
			// root="<project>/.agent-mail" (here as a relative ".agent-mail").
			name: "relative dot base_root, agent-mail root",
			env: amqEnv{
				BaseRoot:  ".",
				Root:      ".agent-mail",
				InSession: true,
			},
		},
		{
			// taboola-pm-os (working): base_root and root both ".agent-mail".
			name: "working: both base_root and root are .agent-mail",
			env: amqEnv{
				BaseRoot: ".agent-mail",
				Root:     ".agent-mail",
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := chooseProjectBaseRoot(projectDir, c.env)
			want := agentMail
			if resolved, err := filepath.EvalSymlinks(agentMail); err == nil {
				// t.TempDir on darwin lives under /var -> /private/var symlink;
				// accept either spelling since os.Stat resolves both.
				if got == resolved {
					return
				}
			}
			if got != want {
				t.Errorf("chooseProjectBaseRoot = %q, want %q", got, want)
			}
		})
	}
}

// TestChooseProjectBaseRootFallsBackWhenNoAgentMail proves graceful degradation:
// when no `.agent-mail` container exists on disk (amq missing or a custom root),
// chooseProjectBaseRoot returns the resolved base_root (or root when base_root
// is empty) so downstream degradation is preserved rather than returning "".
func TestChooseProjectBaseRootFallsBackWhenNoAgentMail(t *testing.T) {
	projectDir := t.TempDir()

	// No .agent-mail anywhere; a custom absolute root is reported.
	custom := filepath.Join(projectDir, "custom-mail")
	got := chooseProjectBaseRoot(projectDir, amqEnv{BaseRoot: custom, Root: custom})
	if got != custom {
		t.Errorf("custom-root fallback = %q, want %q", got, custom)
	}

	// base_root empty -> fall back to root (resolved absolute).
	got = chooseProjectBaseRoot(projectDir, amqEnv{Root: "weird-root"})
	want := filepath.Join(projectDir, "weird-root")
	if got != want {
		t.Errorf("empty base_root fallback = %q, want %q", got, want)
	}

	// A relative base_root with no .agent-mail resolves against projectDir.
	got = chooseProjectBaseRoot(projectDir, amqEnv{BaseRoot: "."})
	if got != filepath.Clean(projectDir) {
		t.Errorf("relative dot fallback = %q, want %q", got, projectDir)
	}
}

// TestScanBaseRootForProjectReturnsAbsoluteAgentMail proves the integration:
// even when `amq env` reports the in-session base_root (the project dir) with
// the real container only in root=<project>/.agent-mail, scanBaseRootForProject
// returns the ABSOLUTE .agent-mail path the board can scan — not the misleading
// relative/parent base_root it used to trust.
func TestScanBaseRootForProjectReturnsAbsoluteAgentMail(t *testing.T) {
	projectDir := t.TempDir()
	resolvedProject := projectDir
	if r, err := filepath.EvalSymlinks(projectDir); err == nil {
		resolvedProject = r
	}
	agentMail := filepath.Join(resolvedProject, ".agent-mail")
	if err := os.MkdirAll(filepath.Join(agentMail, "issue-96", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Fake amq: report the BROKEN in-session shape — base_root is the project
	// dir, root is the real .agent-mail container.
	body := `{"base_root":"` + resolvedProject + `","root":"` + agentMail + `","in_session":true,"session_name":".agent-mail"}`
	setupFakeAMQEnv(t, body)

	got, err := scanBaseRootForProject(resolvedProject)
	if err != nil {
		t.Fatalf("scanBaseRootForProject: %v", err)
	}
	if got != agentMail {
		t.Errorf("scanBaseRootForProject = %q, want the .agent-mail container %q", got, agentMail)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("scanBaseRootForProject must return an absolute path, got %q", got)
	}
}

// TestScanBaseRootForProjectPropagatesAMQError proves the error path is intact:
// when `amq env` fails the board must still receive the error so its
// graceful-degradation guidance fires (rather than scanning a bogus root).
func TestScanBaseRootForProjectPropagatesAMQError(t *testing.T) {
	script := "#!/bin/sh\n" +
		"echo 'cannot determine root' >&2\n" +
		"exit 2\n"
	setupFakeAMQScript(t, script)
	if _, err := scanBaseRootForProject(t.TempDir()); err == nil {
		t.Fatal("expected scanBaseRootForProject to propagate the amq env failure")
	}
}

func setupFakeAMQEnv(t *testing.T, body string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = \"env\" ] && [ \"$2\" = \"--json\" ]; then\n" +
		"  printf '%s\\n' \"$AMQ_FAKE_ENV\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"echo \"unexpected amq command: $*\" >&2\n" +
		"exit 1\n"
	setupFakeAMQScript(t, script)
	t.Setenv("AMQ_FAKE_ENV", body)
}

func setupFakeAMQScript(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "amq"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
