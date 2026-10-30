package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func decodeDoctorJSON(t *testing.T, buf *bytes.Buffer) doctorEnvelopeData {
	t.Helper()
	env := decodeJSONEnvelope[doctorEnvelopeData](t, buf.String())
	if env.Kind != "doctor" {
		t.Fatalf("envelope kind = %q, want doctor", env.Kind)
	}
	return env.Data
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
printf '%s\n' '{"root":"/mail","base_root":"/mail","amq_version":"0.116.2"}'
`)

	d := defaultDoctorExecution(t.TempDir())
	check := doctorCheckAMQVersion(d)
	if check.Status != doctorOK || !strings.Contains(check.Detail, "amq 0.116.2") {
		t.Fatalf("doctor amq version check = %+v, want ok 0.116.2", check)
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

// The floor is doctorMinAMQVersion, 0.116.2 since the 5-amq-A fix round.
func TestExecuteDoctorAMQVersionAcceptsTheFloorAndNewerCanary(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"0.116.2", "v0.117.0-rc1", "0.117.0", "1.0.0+build60"} {
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

func TestExecuteDoctorAMQVersionRejectsOlderThanTheFloor(t *testing.T) {
	for _, version := range []string{"0.42.1", "0.60.0", "0.74.1", "0.107.0", "0.116.1", "0.116.2-rc1"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			d := newDoctorExec(t, dir)
			d.ResolveAMQEnv = func(string) (amqEnv, error) {
				return amqEnv{AMQVersion: version, Root: dir}, nil
			}
			var buf bytes.Buffer
			d.Out = &buf
			if err := executeDoctor(d); err == nil {
				t.Fatalf("doctor should fail when amq %s is below the 0.116.2 floor", version)
			}
			amqLine := firstLineWith(buf.String(), "amq version")
			if !strings.Contains(amqLine, "fail") || !strings.Contains(amqLine, "older than required 0.116.2") {
				t.Fatalf("unexpected amq version line: %q\n%s", amqLine, buf.String())
			}
			if !strings.Contains(amqLine, "amq upgrade") {
				t.Fatalf("amq version line should point at amq upgrade: %q", amqLine)
			}
		})
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
