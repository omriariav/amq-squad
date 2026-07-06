package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func newDoctorExec(t *testing.T, dir string) doctorExecution {
	t.Helper()
	return doctorExecution{
		ProjectDir: dir,
		Out:        &bytes.Buffer{},
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: "0.40.0", Root: filepath.Join(dir, ".agent-mail")}, nil
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
		WakeOverride: func(team.Team, string) []doctorCheck {
			return []doctorCheck{{Name: "wake cto", Status: doctorOK, Detail: "no live signals"}}
		},
		CodexSkillCacheRoot: func() string {
			return filepath.Join(dir, ".codex-cache", "amq-squad")
		},
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

func syncDoctorPointers(t *testing.T, dir, rulesBody string) {
	t.Helper()
	plans, err := rules.Plan(dir, rulesBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Apply(plans); err != nil {
		t.Fatal(err)
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

func TestRunDoctorRejectsAllProfilesWithProfile(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runDoctor([]string{"--all-profiles", "--profile", "review"}, "")
	})
	if err == nil || !strings.Contains(err.Error(), "--all-profiles cannot be combined with --profile") {
		t.Fatalf("all-profiles/profile error = %v", err)
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
	if env.Data.TeamHome != project {
		t.Fatalf("doctor --project team_home = %q, want %s", env.Data.TeamHome, project)
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
	if !strings.Contains(got.Detail, "0.38.0") || !strings.Contains(got.Detail, "0.40.0") {
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

func TestExecuteDoctorAMQVersionAccepts040x(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []string{"0.40.0", "v0.40.1-rc1", "1.0.0+build42"} {
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

func TestExecuteDoctorAMQVersionRejectsOlderThan040(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.ResolveAMQEnv = func(string) (amqEnv, error) {
		return amqEnv{AMQVersion: "0.39.1", Root: dir}, nil
	}
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err == nil {
		t.Fatal("doctor should fail when amq is below the 0.40.0 floor")
	}
	amqLine := firstLineWith(buf.String(), "amq version")
	if !strings.Contains(amqLine, "fail") || !strings.Contains(amqLine, "older than required 0.40.0") {
		t.Fatalf("unexpected amq version line: %q\n%s", amqLine, buf.String())
	}
	if !strings.Contains(amqLine, "amq upgrade") {
		t.Fatalf("amq version line should point at amq upgrade: %q", amqLine)
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

func TestExecuteDoctorAllProfilesChecksDefaultAndNamed(t *testing.T) {
	dir := t.TempDir()
	writeDoctorManagedMarkers(t, dir)
	if err := team.Write(dir, team.Team{
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"}},
		Workstream: "main",
	}); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(dir, "review", team.Team{
		Members:    []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", Session: "review"}},
		Workstream: "review",
	}); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, dir)
	d.AllProfiles = true
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor --all-profiles failed: %v\n%s", err, buf.String())
	}
	out := buf.String()
	for _, want := range []string{"PROFILE default", "WORKSTREAM main", "PROFILE review", "WORKSTREAM review"} {
		if !strings.Contains(out, want) {
			t.Fatalf("all-profiles output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "no default team profile") {
		t.Fatalf("all-profiles output should check configured profiles, not warn about missing default:\n%s", out)
	}
}

func TestExecuteDoctorAllProfilesJSONNamedOnly(t *testing.T) {
	dir := t.TempDir()
	writeDoctorManagedMarkers(t, dir)
	if err := team.WriteProfile(dir, "review", team.Team{
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review"}},
		Workstream: "review",
	}); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, dir)
	d.AllProfiles = true
	d.JSON = true
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor --all-profiles --json failed: %v\n%s", err, buf.String())
	}
	data := decodeDoctorJSON(t, &buf)
	if data.Profile != "all" {
		t.Fatalf("profile = %q, want all", data.Profile)
	}
	if len(data.Profiles) != 1 || data.Profiles[0].Profile != "review" {
		t.Fatalf("profiles = %+v, want only review", data.Profiles)
	}
	if got := findCheck(data.Checks, "profile review"); got == nil || got.Status == doctorFail {
		t.Fatalf("summary check = %+v, want non-failing profile review", got)
	}
	for _, c := range data.Checks {
		if strings.Contains(c.Name, "default") {
			t.Fatalf("named-only all-profiles summary should not include default: %+v", data.Checks)
		}
	}
}

func TestExecuteDoctorNamedProfileMarkersUseMemberCWD(t *testing.T) {
	dir := t.TempDir()
	memberDir := t.TempDir()
	writeDoctorManagedMarkers(t, memberDir)
	if err := team.WriteProfile(dir, "review", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review", CWD: memberDir}},
	}); err != nil {
		t.Fatal(err)
	}
	d := newDoctorExec(t, dir)
	d.Profile = "review"
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor --profile review failed: %v\n%s", err, buf.String())
	}
	out := buf.String()
	if strings.Contains(out, "not found") {
		t.Fatalf("named profile doctor should not inspect missing team-home markers:\n%s", out)
	}
	if !strings.Contains(out, filepath.Join(memberDir, rules.AgentsFile)) {
		t.Fatalf("named profile marker check should inspect member cwd:\n%s", out)
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

func TestExecuteDoctorTmuxMissingFails(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err == nil {
		t.Fatal("missing tmux should fail")
	}
	row := firstLineWith(buf.String(), "tmux")
	if !strings.Contains(row, "fail") {
		t.Errorf("missing tmux should fail, got: %q", row)
	}
}

func TestDoctorCheckTmuxExtendedKeys(t *testing.T) {
	// Not inside tmux -> skipped, OK, no hint.
	t.Run("not in tmux", func(t *testing.T) {
		d := doctorExecution{
			Getenv:          func(string) string { return "" },
			TmuxShowOptions: func(string) (string, bool) { t.Fatal("must not probe tmux when $TMUX is unset"); return "", false },
		}
		c := doctorCheckTmuxExtendedKeys(d)
		if c.Status != doctorOK {
			t.Errorf("not-in-tmux status = %q, want ok", c.Status)
		}
		if !strings.Contains(c.Detail, "skipped") || strings.Contains(c.Detail, "Shift+Enter") {
			t.Errorf("not-in-tmux detail should be a skip with no hint: %q", c.Detail)
		}
	})

	// Inside tmux, extended-keys off -> OK (never fails) WITH the hint.
	t.Run("off shows hint", func(t *testing.T) {
		d := doctorExecution{
			Getenv:          func(name string) string { return map[string]string{"TMUX": "/tmp/tmux-1/default,1,0"}[name] },
			TmuxShowOptions: func(string) (string, bool) { return "off", true },
		}
		c := doctorCheckTmuxExtendedKeys(d)
		if c.Status != doctorOK {
			t.Errorf("extended-keys off must NOT fail doctor; status = %q", c.Status)
		}
		for _, want := range []string{
			"Shift+Enter",
			"tmux set-option -s extended-keys on",
			"extended-keys-format csi-u",
			"xterm*:extkeys",
			"tmux -CC",
			"amq-squad does not change it for you",
		} {
			if !strings.Contains(c.Detail, want) {
				t.Errorf("hint missing %q: %q", want, c.Detail)
			}
		}
	})

	// Inside tmux, extended-keys unset (ok=false) -> OK with the hint too.
	t.Run("unset shows hint", func(t *testing.T) {
		d := doctorExecution{
			Getenv:          func(string) string { return "/tmp/tmux-1/default,1,0" },
			TmuxShowOptions: func(string) (string, bool) { return "", false },
		}
		c := doctorCheckTmuxExtendedKeys(d)
		if c.Status != doctorOK || !strings.Contains(c.Detail, "Shift+Enter") {
			t.Errorf("unset extended-keys should be ok with hint, got %+v", c)
		}
		if !strings.Contains(c.Detail, "unset") {
			t.Errorf("unset detail should name the unset state: %q", c.Detail)
		}
	})

	// Inside tmux, extended-keys on -> OK, no hint.
	t.Run("on no hint", func(t *testing.T) {
		d := doctorExecution{
			Getenv:          func(string) string { return "/tmp/tmux-1/default,1,0" },
			TmuxShowOptions: func(string) (string, bool) { return "on", true },
		}
		c := doctorCheckTmuxExtendedKeys(d)
		if c.Status != doctorOK {
			t.Errorf("extended-keys on status = %q, want ok", c.Status)
		}
		// The remediation hint (set-option commands) must be absent when on.
		if strings.Contains(c.Detail, "set-option") || strings.Contains(c.Detail, "may not reach agents") {
			t.Errorf("extended-keys on must not print the hint: %q", c.Detail)
		}
	})
}

func TestExecuteDoctorMarkerIntegrity(t *testing.T) {
	cases := map[string]struct {
		body   string
		expect doctorStatus
	}{
		"ok":         {body: rules.BeginMarker + "\nhi\n" + rules.EndMarker + "\n", expect: doctorOK},
		"no_markers": {body: "# Project\nsome content\n", expect: doctorWarn},
		"two_begins": {body: rules.BeginMarker + "\n" + rules.BeginMarker + "\n" + rules.EndMarker + "\n", expect: doctorFail},
		"reversed":   {body: rules.EndMarker + "\n\n" + rules.BeginMarker + "\n", expect: doctorFail},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, rules.ClaudeFile), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			d := newDoctorExec(t, dir)
			var buf bytes.Buffer
			d.Out = &buf
			_ = executeDoctor(d)
			row := firstLineWith(buf.String(), "markers "+rules.ClaudeFile)
			if !strings.Contains(row, string(tc.expect)) {
				t.Errorf("CLAUDE.md marker check should be %s, got: %q", tc.expect, row)
			}
		})
	}
}

func TestExecuteDoctorMarkerIntegrityMissingWarn(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	_ = executeDoctor(d)
	row := firstLineWith(buf.String(), "markers "+rules.ClaudeFile)
	if !strings.Contains(row, "warn") {
		t.Errorf("missing CLAUDE.md should warn, got: %q", row)
	}
}

func TestExecuteDoctorPointerSyncOKWhenApplied(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	body := "# Team Rules\n"
	if err := rules.Write(dir, body); err != nil {
		t.Fatal(err)
	}
	syncDoctorPointers(t, dir, body)

	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, buf.String())
	}
	row := firstLineWith(buf.String(), "pointer sync "+rules.ClaudeFile)
	if !strings.Contains(row, "ok") {
		t.Fatalf("synced pointer should be ok, got: %q", row)
	}
}

func TestExecuteDoctorPointerSyncWarnsOnDrift(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(dir, "# Team Rules\n"); err != nil {
		t.Fatal(err)
	}
	writeDoctorManagedMarkers(t, dir)

	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor drift warning should not fail: %v\n%s", err, buf.String())
	}
	row := firstLineWith(buf.String(), "pointer sync "+rules.ClaudeFile)
	for _, want := range []string{"warn", "out of date", "amq-squad team sync --apply"} {
		if !strings.Contains(row, want) {
			t.Fatalf("pointer drift row missing %q: %q", want, row)
		}
	}
}

func TestExecuteDoctorTeamRulesRosterWarnsOnDrift(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	// A team-rules.md that does NOT name the cto member: the shared file was
	// authored for a different roster (finding #155). The hint warns, but never
	// fails — agents route from the live bootstrap block.
	if err := rules.Write(dir, "# Team Rules\n\n## Role Scope\n\n- pm (Product Manager): handle `pm`, ...\n"); err != nil {
		t.Fatal(err)
	}

	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("roster drift hint must not fail doctor: %v\n%s", err, buf.String())
	}
	row := firstLineWith(buf.String(), "team-rules roster")
	for _, want := range []string{"warn", "cto", "cosmetic"} {
		if !strings.Contains(row, want) {
			t.Fatalf("roster drift row missing %q: %q", want, row)
		}
	}
	// The hint must NOT steer the operator at `team rules init --force` (that
	// re-renders the default profile = wrong roster for a named profile).
	if strings.Contains(row, "--force") {
		t.Errorf("roster drift row must not suggest --force: %q", row)
	}
}

func TestExecuteDoctorTeamRulesRosterOKWhenDescribed(t *testing.T) {
	dir := t.TempDir()
	tm := team.Team{
		Project: dir,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}
	if err := team.WriteProfile(dir, team.DefaultProfile, tm); err != nil {
		t.Fatal(err)
	}
	body, err := renderTeamRules(tm)
	if err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(dir, body); err != nil {
		t.Fatal(err)
	}

	d := newDoctorExec(t, dir)
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, buf.String())
	}
	row := firstLineWith(buf.String(), "team-rules roster")
	if !strings.Contains(row, "ok") {
		t.Fatalf("team-rules that describes the roster should be ok, got: %q", row)
	}
}

func TestExecuteDoctorPointerSyncNamedProfileOutsideHint(t *testing.T) {
	dir := t.TempDir()
	memberDir := t.TempDir()
	if err := team.WriteProfile(dir, "review", team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "review", CWD: memberDir}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rules.Write(dir, "# Team Rules\n"); err != nil {
		t.Fatal(err)
	}

	d := newDoctorExec(t, dir)
	d.Profile = "review"
	var buf bytes.Buffer
	d.Out = &buf
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor named-profile pointer warning should not fail: %v\n%s", err, buf.String())
	}
	row := firstLineWith(buf.String(), "pointer sync "+rules.ClaudeFile)
	if row == "" {
		row = firstLineWith(buf.String(), "pointer sync "+filepath.Base(memberDir)+"/"+rules.ClaudeFile)
	}
	for _, want := range []string{"warn", "missing", "amq-squad team sync --profile review --apply --allow-outside"} {
		if !strings.Contains(row, want) {
			t.Fatalf("named pointer sync row missing %q: %q\nfull output:\n%s", want, row, buf.String())
		}
	}
}

func TestExecuteDoctorWakeReuseClassifyMemberStatus(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// cto is live (PID alive + binary match) -> ok.
	// fullstack has no signals -> ok.
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{Binary: "codex", Handle: "cto", AgentPID: 9999})
	d := doctorExecution{
		ProjectDir: dir,
		Out:        &bytes.Buffer{},
		ResolveAMQEnv: func(string) (amqEnv, error) {
			return amqEnv{AMQVersion: "0.40.0", Root: filepath.Join(base, "issue-96")}, nil
		},
		LookPath: func(string) (string, error) { return "/usr/bin/tmux", nil },
		Probe: duplicateLaunchProbe{
			PIDAlive:     func(pid int) bool { return pid == 9999 },
			ProcessMatch: func(pid int, _ func(string) bool) bool { return pid == 9999 },
			Now:          func() time.Time { return time.Now() },
		},
	}
	d.JSON = true
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor failed: %v\n%s", err, d.Out)
	}
	out := d.Out.(*bytes.Buffer).String()
	env := decodeJSONEnvelope[doctorEnvelopeData](t, out)
	if env.Data.Workstream != "issue-96" {
		t.Errorf("envelope workstream = %q, want issue-96", env.Data.Workstream)
	}
	if env.Data.TeamHome != dir {
		t.Errorf("envelope team_home = %q, want %s", env.Data.TeamHome, dir)
	}
	ctoCheck := findCheck(env.Data.Checks, "wake cto")
	if ctoCheck == nil || ctoCheck.Status != doctorOK {
		t.Errorf("wake cto = %+v, want ok", ctoCheck)
	}
}

// Per cto: AMQ env resolution failures must surface in detail and not panic.
func TestExecuteDoctorWakeHandlesAMQEnvErrorPerMember(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// PATH has no amq binary -> resolveAMQEnvInDir fails per member.
	t.Setenv("PATH", "")
	d := doctorExecution{
		ProjectDir:    dir,
		Out:           &bytes.Buffer{},
		ResolveAMQEnv: func(string) (amqEnv, error) { return amqEnv{AMQVersion: "0.40.0"}, nil },
		LookPath:      func(string) (string, error) { return "/usr/bin/tmux", nil },
		Probe:         defaultDuplicateLaunchProbe,
	}
	if err := executeDoctor(d); err != nil {
		// Wake env failure is warn, not fail; other checks may pass.
		// Doctor returns error only when any check is fail.
		// We only require: no panic, detail present.
		_ = err
	}
	if !strings.Contains(d.Out.(*bytes.Buffer).String(), "amq env unresolved") {
		// The status-classifier path prints this detail when amq env fails.
		t.Errorf("expected amq env unresolved detail in:\n%s", d.Out.(*bytes.Buffer).String())
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
