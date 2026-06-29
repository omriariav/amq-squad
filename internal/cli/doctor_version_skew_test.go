package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorCheckVersionSkew(t *testing.T) {
	check := func(running, path, ver string, found bool) doctorCheck {
		return doctorCheckVersionSkew(doctorExecution{
			RunningVersion:    running,
			PathBinaryVersion: func() (string, string, bool) { return path, ver, found },
		})
	}
	cases := []struct {
		name         string
		got          doctorCheck
		wantStatus   doctorStatus
		wantInDetail string
	}{
		{"dev build is skipped without resolving PATH", check("dev", "/x", "v1.9.1", true), doctorOK, "dev"},
		{"empty running version is skipped", check("", "/x", "v1.9.1", true), doctorOK, "dev"},
		{"not on PATH warns", check("v2.0.0", "", "", false), doctorWarn, "not on PATH"},
		{"matching version is ok", check("v2.0.0", "/usr/local/bin/amq-squad", "v2.0.0", true), doctorOK, "matches this build"},
		{"version skew warns with both versions", check("v2.0.0", "/Users/x/go/bin/amq-squad", "v1.9.1", true), doctorWarn, "version skew"},
		{"unreadable PATH version warns", check("v2.0.0", "/x", "", true), doctorWarn, "could not read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q\ndetail: %s", tc.got.Status, tc.wantStatus, tc.got.Detail)
			}
			if !strings.Contains(tc.got.Detail, tc.wantInDetail) {
				t.Errorf("detail %q missing %q", tc.got.Detail, tc.wantInDetail)
			}
		})
	}

	// The skew case must name BOTH versions so the operator can see the mismatch.
	skew := check("v2.0.0", "/old/amq-squad", "v1.9.1", true)
	if !strings.Contains(skew.Detail, "v1.9.1") || !strings.Contains(skew.Detail, "v2.0.0") {
		t.Errorf("skew detail must name both versions, got %q", skew.Detail)
	}

	// A dev build must NOT shell out (PathBinaryVersion not consulted).
	called := false
	doctorCheckVersionSkew(doctorExecution{
		RunningVersion:    "",
		PathBinaryVersion: func() (string, string, bool) { called = true; return "", "", false },
	})
	if called {
		t.Error("dev build must not resolve the PATH binary (no shell-out)")
	}
}

func TestParseAmqSquadVersion(t *testing.T) {
	for in, want := range map[string]string{
		"amq-squad v2.0.0\n": "v2.0.0",
		"amq-squad v1.9.1":   "v1.9.1",
		"v2.0.0":             "v2.0.0",
		"  amq-squad  dev  ": "dev",
	} {
		if got := parseAmqSquadVersion(in); got != want {
			t.Errorf("parseAmqSquadVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDoctorCheckCodexSkillCache(t *testing.T) {
	cache := t.TempDir()
	writeCachedSkill := func(version string) {
		t.Helper()
		path := filepath.Join(cache, version, "skills", "amq-squad", "SKILL.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# amq-squad\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := func(version string) doctorCheck {
		return doctorCheckCodexSkillCache(doctorExecution{
			RunningVersion: version,
			CodexSkillCacheRoot: func() string {
				return cache
			},
		})
	}

	writeCachedSkill("2.5.0")
	ok := check("v2.5.0")
	if ok.Status != doctorOK || !strings.Contains(ok.Detail, "2.5.0") {
		t.Fatalf("cache check = %+v, want ok for direct released bundle", ok)
	}

	stale := check("v2.6.0")
	if stale.Status != doctorWarn || !strings.Contains(stale.Detail, "not directly cached") || !strings.Contains(stale.Detail, "2.5.0") {
		t.Fatalf("stale cache check = %+v, want actionable warning naming cached versions", stale)
	}
}

func TestDoctorCheckCodexSkillCacheWarnsOnSymlink(t *testing.T) {
	cache := t.TempDir()
	target := filepath.Join(cache, "2.5.0-real")
	if err := os.MkdirAll(filepath.Join(target, "skills", "amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(cache, "2.5.0")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	got := doctorCheckCodexSkillCache(doctorExecution{
		RunningVersion: "v2.5.0",
		CodexSkillCacheRoot: func() string {
			return cache
		},
	})
	if got.Status != doctorWarn || !strings.Contains(got.Detail, "symlink") {
		t.Fatalf("symlink cache check = %+v, want warning", got)
	}
}

func TestDoctorCheckSkillVersion(t *testing.T) {
	// skillReader returns a reader that serves the given SKILL.md body as if it
	// were found at skillPath.
	skillReader := func(body, skillPath string) func(string) (string, string, bool) {
		return func(_ string) (string, string, bool) { return body, skillPath, true }
	}
	noBundle := func(_ string) (string, string, bool) { return "", "", false }

	aligned := "# amq-squad\n\n**Skill version: 2.12.0** - echo\n\nsome content\n"
	alignedWithV := "# amq-squad\n\n**Skill version: v2.12.0** - echo\n\nsome content\n"
	skewed := "# amq-squad\n\n**Skill version: 2.11.0** - old\n\n"
	noMarker := "# amq-squad\n\nno version here\n"

	cases := []struct {
		name         string
		running      string
		reader       func(string) (string, string, bool)
		wantStatus   doctorStatus
		wantInDetail string
	}{
		{"dev build skipped", "dev", skillReader(aligned, "/x/SKILL.md"), doctorOK, "dev build"},
		{"empty running version skipped", "", skillReader(aligned, "/x/SKILL.md"), doctorOK, "dev build"},
		{"no bundle warns", "v2.12.0", noBundle, doctorWarn, "no installed skill bundle"},
		{"aligned versions ok", "v2.12.0", skillReader(aligned, "/cache/2.12.0/skills/amq-squad/SKILL.md"), doctorOK, "matches binary"},
		{"aligned v-prefixed marker ok", "v2.12.0", skillReader(alignedWithV, "/cache/2.12.0/skills/amq-squad/SKILL.md"), doctorOK, "matches binary"},
		{"aligned without leading v", "2.12.0", skillReader(aligned, "/cache/2.12.0/skills/amq-squad/SKILL.md"), doctorOK, "matches binary"},
		{"skewed warns with both versions", "v2.12.0", skillReader(skewed, "/cache/2.12.0/skills/amq-squad/SKILL.md"), doctorWarn, "skew"},
		{"missing marker warns", "v2.12.0", skillReader(noMarker, "/cache/2.12.0/skills/amq-squad/SKILL.md"), doctorWarn, "no 'Skill version:'"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := doctorCheckSkillVersion(doctorExecution{
				RunningVersion: tc.running,
				SkillMDContent: tc.reader,
			})
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q\ndetail: %s", got.Status, tc.wantStatus, got.Detail)
			}
			if !strings.Contains(got.Detail, tc.wantInDetail) {
				t.Errorf("detail %q does not contain %q", got.Detail, tc.wantInDetail)
			}
		})
	}

	// The skew case must name both versions.
	skewCheck := doctorCheckSkillVersion(doctorExecution{
		RunningVersion: "v2.12.0",
		SkillMDContent: skillReader(skewed, "/SKILL.md"),
	})
	if !strings.Contains(skewCheck.Detail, "v2.11.0") || !strings.Contains(skewCheck.Detail, "v2.12.0") {
		t.Errorf("skew detail must name both versions, got: %q", skewCheck.Detail)
	}

	// Dev build must NOT call the reader.
	called := false
	doctorCheckSkillVersion(doctorExecution{
		RunningVersion: "dev",
		SkillMDContent: func(_ string) (string, string, bool) { called = true; return "", "", false },
	})
	if called {
		t.Error("dev build must not consult the skill reader")
	}
}

func TestDoctorSkillVersionAppearsInFullDoctorJSON(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.RunningVersion = "v2.12.0"
	d.SkillMDContent = func(_ string) (string, string, bool) {
		return "**Skill version: 2.12.0** - ok", "/fake/SKILL.md", true
	}
	var buf strings.Builder
	d.Out = &buf
	d.JSON = true
	if err := executeDoctor(d); err != nil {
		t.Fatalf("executeDoctor: %v\n%s", err, buf.String())
	}
	env := decodeJSONEnvelope[doctorEnvelopeData](t, buf.String())
	got := findCheck(env.Data.Checks, "skill version")
	if got == nil {
		t.Fatal("skill version check missing from doctor --json output")
	}
	if got.Status != doctorOK {
		t.Errorf("skill version check status = %q, want ok; detail: %s", got.Status, got.Detail)
	}
}
