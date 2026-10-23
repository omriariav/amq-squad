package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestDoctorStoppedUnplannedSharedIndexDoesNotFail(t *testing.T) {
	repo := seedReviewGitRepo(t)
	if err := team.WriteProfile(repo, team.DefaultProfile, team.Team{
		Project: repo,
		Members: []team.Member{
			{Role: "one", Handle: "one", Binary: "codex", Session: "stopped"},
			{Role: "two", Handle: "two", Binary: "codex", Session: "stopped"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	d := doctorExecution{
		ProjectDir: repo, Profile: team.DefaultProfile,
		WorktreeDiagnostics: defaultDoctorWorktreeDiagnostics,
	}
	checks := doctorCheckWorktrees(d, "stopped")
	if fails := countFails(checks); fails != 0 {
		t.Fatalf("stopped, unplanned same-cwd profile produced %d doctor failures: %+v", fails, checks)
	}
	var collision doctorCheck
	for _, check := range checks {
		if check.Kind == "shared-index-collision" {
			collision = check
			break
		}
	}
	if collision.Status != doctorWarn {
		t.Fatalf("shared-index collision = %+v, want warn", collision)
	}
	for _, want := range []string{"one,two share", "fewer than two affected members are live", "worktree materialize"} {
		if !strings.Contains(collision.Detail, want) {
			t.Fatalf("collision missing %q: %+v", want, collision)
		}
	}
}

func decodeDoctorJSON(t *testing.T, buf *bytes.Buffer) doctorEnvelopeData {
	t.Helper()
	env := decodeJSONEnvelope[doctorEnvelopeData](t, buf.String())
	if env.Kind != "doctor" {
		t.Fatalf("envelope kind = %q, want doctor", env.Kind)
	}
	return env.Data
}

func writeDoctorManagedMarkers(t *testing.T, dir string) {
	t.Helper()
	body := []byte(rules.BeginMarker + "\nmanaged\n" + rules.EndMarker + "\n")
	for _, name := range []string{rules.ClaudeFile, rules.AgentsFile} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunDoctorRejectsPositionalArgs(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return runDoctor([]string{"foo"}, "") })
	if err == nil {
		t.Fatal("positional arg should be UsageError")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

func TestRunDoctorRejectsUnknownFlag(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return runDoctor([]string{"--banana"}, "") })
	if err == nil {
		t.Fatal("unknown flag should fail")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("unknown flag should be UsageError, got %T: %v", err, err)
	}
}

func TestRunDoctorProjectTargetsOtherDir(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	chdir(t, other)
	t.Setenv("PATH", "")

	stdout, _, err := captureOutput(t, func() error {
		return runDoctor([]string{"--project", project, "--json"}, "")
	})
	if err == nil {
		t.Fatal("doctor with PATH stripped should fail health checks, preserving JSON output")
	}
	env := decodeJSONEnvelope[doctorEnvelopeData](t, stdout)
	if env.Data.TeamHome != canonicalFilesystemPath(project) {
		t.Fatalf("doctor --project team_home = %q, want %s", env.Data.TeamHome, canonicalFilesystemPath(project))
	}
}

func TestRunDoctorProjectValidation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	_, _, err := captureOutput(t, func() error {
		return runDoctor([]string{"--project", missing, "--json"}, "")
	})
	if err == nil || !strings.Contains(err.Error(), "--project") {
		t.Fatalf("doctor --project missing error = %v, want --project error", err)
	}

	_, _, err = captureOutput(t, func() error {
		return runDoctor([]string{"--project", "", "--json"}, "")
	})
	if err == nil || !strings.Contains(err.Error(), "--project requires a directory") {
		t.Fatalf("doctor empty --project error = %v, want directory guidance", err)
	}
}

func TestDoctorAMQVersionResolutionDisablesChildUpdateCheck(t *testing.T) {
	t.Setenv("AMQ_NO_UPDATE_CHECK", "0")
	t.Setenv("AM_ROOT_ID", "v1:test:inherited-root")
	t.Setenv("AM_BASE_ROOT_ID", "v1:test:inherited-base")
	setupFakeAMQScript(t, `#!/bin/sh
if [ "$AMQ_NO_UPDATE_CHECK" != "1" ]; then
  echo "update available" >&2
  exit 91
fi
if [ -n "$AM_ROOT_ID$AM_BASE_ROOT_ID" ]; then
  echo "inherited AMQ root identity metadata leaked" >&2
  exit 92
fi
printf '%s\n' '{"root":"/mail","base_root":"/mail","amq_version":"0.60.0"}'
`)

	d := defaultDoctorExecution(t.TempDir())
	check := doctorCheckAMQVersion(d)
	if check.Status != doctorOK || !strings.Contains(check.Detail, "amq 0.60.0") {
		t.Fatalf("doctor amq version check = %+v, want ok 0.60.0", check)
	}
	if got := os.Getenv("AMQ_NO_UPDATE_CHECK"); got != "0" {
		t.Fatalf("parent AMQ_NO_UPDATE_CHECK = %q, want unchanged 0", got)
	}
}

func TestDoctorAMQIdentityPinHealth(t *testing.T) {
	check := func(values map[string]string) doctorCheck {
		return doctorCheckAMQIdentityPin(doctorExecution{
			LookupEnv: func(key string) (string, bool) {
				value, ok := values[key]
				return value, ok
			},
		})
	}
	tests := []struct {
		name   string
		values map[string]string
		status doctorStatus
		want   string
	}{
		{name: "no injected pin", values: map[string]string{}, status: doctorOK, want: "no injected"},
		{name: "sessionful", values: map[string]string{"AM_ROOT": "/mail/issue-96", "AM_BASE_ROOT": "/mail", "AM_SESSION": "issue-96", "AM_ME": "cto"}, status: doctorOK, want: "healthy sessionful"},
		{name: "exact root sessionless", values: map[string]string{"AM_ROOT": "/mail/review/issue-96", "AM_BASE_ROOT": "/mail/review/issue-96", "AM_ME": "cto"}, status: doctorOK, want: "healthy exact-root/sessionless"},
		{name: "legacy partial", values: map[string]string{"AM_ROOT": "/mail/issue-96", "AM_BASE_ROOT": "/mail", "AM_ME": "cto"}, status: doctorWarn, want: "stop and resume/relaunch"},
		{name: "inconsistent empty session", values: map[string]string{"AM_ROOT": "/mail/issue-96", "AM_BASE_ROOT": "/mail", "AM_SESSION": "", "AM_ME": "cto"}, status: doctorWarn, want: "legacy or inconsistent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := check(tt.values)
			if got.Status != tt.status || !strings.Contains(got.Detail, tt.want) {
				t.Fatalf("doctor pin check = %+v, want status %s and %q", got, tt.status, tt.want)
			}
		})
	}
}

func findCheck(checks []doctorCheck, name string) *doctorCheck {
	for i, c := range checks {
		if c.Name == name {
			return &checks[i]
		}
	}
	return nil
}

func firstLineWith(s, needle string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func newDoctorExec(t *testing.T, dir string) doctorExecution {
	t.Helper()
	return doctorExecution{
		ProjectDir: dir,
		Out:        &bytes.Buffer{},
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: doctorMinAMQVersion, Root: filepath.Join(dir, ".agent-mail")}, nil
		},
		RunAMQOps: func(string, amqEnv) ([]byte, error) {
			return []byte(`{"status":"ok"}`), nil
		},
		LookPath: func(name string) (string, error) {
			if name == "tmux" {
				return "/usr/bin/tmux", nil
			}
			return "", errors.New("not found")
		},
		Probe: defaultDuplicateLaunchProbe,
	}
}

func TestExecuteDoctorAMQOpsFailure(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.RunAMQOps = func(string, amqEnv) ([]byte, error) {
		return nil, errors.New("stale tmp lock")
	}
	var buf bytes.Buffer
	d.Out = &buf
	d.JSON = true
	err := executeDoctor(d)
	if err == nil || !strings.Contains(err.Error(), "doctor:") {
		t.Fatalf("want doctor fail error, got %v", err)
	}
	data := decodeDoctorJSON(t, &buf)
	got := findCheck(data.Checks, "amq ops")
	if got == nil || got.Status != doctorFail {
		t.Fatalf("amq ops check = %+v, want fail", got)
	}
	if !strings.Contains(got.Detail, "amq doctor --ops failed") || !strings.Contains(got.Detail, "stale tmp lock") {
		t.Errorf("detail should name AMQ ops failure: %q", got.Detail)
	}
}

func TestExecuteDoctorJSONCanonicalizesTeamHome(t *testing.T) {
	project := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(project, link); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, project)
	d.ProjectDir = link
	d.JSON = true
	var out bytes.Buffer
	d.Out = &out
	_ = executeDoctor(d)
	env := decodeJSONEnvelope[doctorEnvelopeData](t, out.String())
	if env.Data.TeamHome != canonicalFilesystemPath(project) {
		t.Fatalf("doctor JSON team_home = %q, want canonical %q", env.Data.TeamHome, canonicalFilesystemPath(project))
	}
}

func TestExecuteDoctorAMQVersionTooOld(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.ResolveAMQEnv = func(string) (amqEnv, error) {
		return amqEnv{AMQVersion: "0.38.0", Root: dir}, nil
	}
	var buf bytes.Buffer
	d.Out = &buf
	d.JSON = true
	err := executeDoctor(d)
	if err == nil || !strings.Contains(err.Error(), "doctor:") {
		t.Fatalf("want doctor fail error, got %v", err)
	}
	data := decodeDoctorJSON(t, &buf)
	got := findCheck(data.Checks, "amq version")
	if got == nil || got.Status != doctorFail {
		t.Fatalf("amq version check = %+v, want fail", got)
	}
	if !strings.Contains(got.Detail, "0.38.0") || !strings.Contains(got.Detail, doctorMinAMQVersion) {
		t.Errorf("detail should name the bad version: %q", got.Detail)
	}
	if !strings.Contains(got.Detail, "amq upgrade") {
		t.Errorf("detail should point at amq upgrade: %q", got.Detail)
	}
}

func TestExecuteDoctorAMQVersionMissing(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.ResolveAMQEnv = func(string) (amqEnv, error) {
		return amqEnv{AMQVersion: "", Root: dir}, nil
	}
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err == nil {
		t.Fatal("empty version should fail")
	}
	out := buf.String()
	if !strings.Contains(out, "compatibility unknown") {
		t.Errorf("expected detail about unknown compatibility, got:\n%s", out)
	}
}

func TestExecuteDoctorAMQVersionUnparseable(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.ResolveAMQEnv = func(string) (amqEnv, error) {
		return amqEnv{AMQVersion: "garbage", Root: dir}, nil
	}
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err == nil {
		t.Fatal("unparseable version should fail")
	}
	if !strings.Contains(buf.String(), "unparseable version") {
		t.Errorf("expected unparseable-version detail, got:\n%s", buf.String())
	}
}

func TestExecuteDoctorAMQEnvCommandFails(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.ResolveAMQEnv = func(string) (amqEnv, error) {
		return amqEnv{}, errors.New("amq env: not found in PATH")
	}
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err == nil {
		t.Fatal("env failure should fail doctor")
	}
	if !strings.Contains(buf.String(), "amq env failed") {
		t.Errorf("expected amq env failure detail, got:\n%s", buf.String())
	}
}

func TestExecuteDoctorAMQVersionAccepts0600AndNewerCanary(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"0.60.0", "v0.61.0-rc1", "0.61.0", "1.0.0+build60"} {
		d := newDoctorExec(t, dir)
		d.ResolveAMQEnv = func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: v, Root: dir}, nil
		}
		var buf bytes.Buffer
		d.Out = &buf
		// Avoid other checks failing: tmux ok, no team is just warn.
		err := executeDoctor(d)
		_ = err // version itself is ok; other checks may warn but not fail.
		out := buf.String()
		amqLine := firstLineWith(out, "amq version")
		if !strings.Contains(amqLine, "ok") {
			t.Errorf("amq %s should be ok, table line: %q", v, amqLine)
		}
	}
}

func TestExecuteDoctorAMQVersionRejectsOlderThan0600(t *testing.T) {
	for _, version := range []string{"0.42.1", "0.52.2", "0.54.0", "0.57.3", "0.59.1", "0.60.0-rc1"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			d := newDoctorExec(t, dir)
			d.ResolveAMQEnv = func(string) (amqEnv, error) {
				return amqEnv{AMQVersion: version, Root: dir}, nil
			}
			var buf bytes.Buffer
			d.Out = &buf
			if err := executeDoctor(d); err == nil {
				t.Fatalf("doctor should fail when amq %s is below the 0.60.0 floor", version)
			}
			amqLine := firstLineWith(buf.String(), "amq version")
			if !strings.Contains(amqLine, "fail") || !strings.Contains(amqLine, "older than required 0.60.0") {
				t.Fatalf("unexpected amq version line: %q\n%s", amqLine, buf.String())
			}
			if !strings.Contains(amqLine, "amq upgrade") {
				t.Fatalf("amq version line should point at amq upgrade: %q", amqLine)
			}
		})
	}
}

func TestExecuteDoctorTeamConfigMissingWarn(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("missing team should not fail: %v", err)
	}
	row := firstLineWith(buf.String(), "team config")
	if !strings.Contains(row, "warn") {
		t.Errorf("missing team config should warn, got: %q", row)
	}
}

func TestExecuteDoctorDefaultMissingButNamedProfilesGuideProfile(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, "review", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("named-profile-only project should warn, not fail: %v", err)
	}
	row := firstLineWith(buf.String(), "team config")
	for _, want := range []string{"warn", "no default team profile", "review", "--profile <name>"} {
		if !strings.Contains(row, want) {
			t.Errorf("team config row missing %q: %q", want, row)
		}
	}
}

func TestExecuteDoctorNamedProfile(t *testing.T) {
	dir := t.TempDir()
	writeDoctorManagedMarkers(t, dir)
	if err := team.WriteProfile(dir, "review", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
	}); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, dir)
	d.Profile = "review"
	d.JSON = true
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor --profile review failed: %v\n%s", err, buf.String())
	}
	data := decodeDoctorJSON(t, &buf)
	if data.Profile != "review" {
		t.Fatalf("profile = %q, want review", data.Profile)
	}
	if data.Workstream != "review" {
		t.Fatalf("workstream = %q, want review", data.Workstream)
	}
	got := findCheck(data.Checks, "team config")
	if got == nil || got.Status != doctorOK || got.Detail != team.ProfilePath(dir, "review") {
		t.Fatalf("team config check = %+v, want named profile path", got)
	}
}

func TestExecuteDoctorTeamConfigCorruptFails(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(team.Path(dir), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err == nil {
		t.Fatal("corrupt team.json should fail doctor")
	}
	row := firstLineWith(buf.String(), "team config")
	if !strings.Contains(row, "fail") {
		t.Errorf("corrupt team config should fail, got: %q", row)
	}
}

// JSON envelope purity: when checks fail, stdout must remain pure JSON.
// Diagnostics ride on the returned error (main prints to stderr).
func TestExecuteDoctorJSONFailKeepsStdoutPure(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.ResolveAMQEnv = func(string) (amqEnv, error) {
		return amqEnv{}, errors.New("amq missing")
	}
	d.JSON = true
	var buf bytes.Buffer
	d.Out = &buf
	err := executeDoctor(d)
	if err == nil {
		t.Fatal("expected fail")
	}
	// stdout must be parseable JSON; no extra lines.
	env := decodeDoctorJSON(t, &buf)
	if len(env.Checks) == 0 {
		t.Fatal("envelope must include checks")
	}
}
