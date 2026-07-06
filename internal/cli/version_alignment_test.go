package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func writePluginManifest(t *testing.T, root, version, rel, manifestVersion string) string {
	t.Helper()
	path := filepath.Join(root, version, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"name":"amq-squad","version":"`+manifestVersion+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func alignedVersionSources(t *testing.T, version string) versionAlignmentSources {
	t.Helper()
	codexRoot := t.TempDir()
	claudeRoot := t.TempDir()
	trimmed := strings.TrimPrefix(version, "v")
	writePluginManifest(t, codexRoot, trimmed, ".codex-plugin/plugin.json", trimmed)
	writePluginManifest(t, claudeRoot, trimmed, ".claude-plugin/plugin.json", trimmed)
	return versionAlignmentSources{
		RunningVersion: version,
		PathBinaryVersion: func() (string, string, bool) {
			return "/bin/amq-squad", version, true
		},
		CodexSkillCacheRoot: func() string {
			return codexRoot
		},
		ClaudeSkillCacheRoot: func() string {
			return claudeRoot
		},
		SkillMDContent: func(string) (string, string, bool) {
			return "**Skill version: " + trimmed + "** - ok", "/cache/" + trimmed + "/skills/amq-squad/SKILL.md", true
		},
	}
}

func TestBuildVersionAlignmentReportsBinaryPluginAndSkillMatches(t *testing.T) {
	got := buildVersionAlignment(alignedVersionSources(t, "v2.12.0"))
	if !got.Aligned || len(got.Warnings) != 0 {
		t.Fatalf("alignment = %+v, want aligned with no warnings", got)
	}
	if got.RunningBinary != "v2.12.0" {
		t.Fatalf("running binary = %q", got.RunningBinary)
	}
	for name, component := range map[string]versionComponentData{
		"path":   got.PathBinary,
		"codex":  got.CodexPlugin,
		"claude": got.ClaudePlugin,
		"skill":  got.Skill,
	} {
		if component.Status != "ok" {
			t.Fatalf("%s component = %+v, want ok", name, component)
		}
		if component.MatchesRunning == nil || !*component.MatchesRunning {
			t.Fatalf("%s matches_running = %v, want true", name, component.MatchesRunning)
		}
		if component.Version == "" {
			t.Fatalf("%s component missing version: %+v", name, component)
		}
	}
}

func TestBuildVersionAlignmentWarnsOnPluginAndSkillMismatch(t *testing.T) {
	codexRoot := t.TempDir()
	claudeRoot := t.TempDir()
	writePluginManifest(t, codexRoot, "2.12.0", ".codex-plugin/plugin.json", "2.11.0")
	writePluginManifest(t, claudeRoot, "2.12.0", ".claude-plugin/plugin.json", "2.12.0")

	got := buildVersionAlignment(versionAlignmentSources{
		RunningVersion: "v2.12.0",
		PathBinaryVersion: func() (string, string, bool) {
			return "/old/amq-squad", "v2.10.0", true
		},
		CodexSkillCacheRoot:  func() string { return codexRoot },
		ClaudeSkillCacheRoot: func() string { return claudeRoot },
		SkillMDContent: func(string) (string, string, bool) {
			return "**Skill version: 2.9.0** - stale", "/cache/skill", true
		},
	})
	if got.Aligned {
		t.Fatalf("alignment = %+v, want mismatch", got)
	}
	if len(got.Warnings) != 3 {
		t.Fatalf("warnings = %v, want path/codex/skill warnings", got.Warnings)
	}
	for name, component := range map[string]versionComponentData{
		"path":  got.PathBinary,
		"codex": got.CodexPlugin,
		"skill": got.Skill,
	} {
		if component.Status != "warn" || component.MatchesRunning == nil || *component.MatchesRunning {
			t.Fatalf("%s component = %+v, want warning mismatch", name, component)
		}
	}
}

func TestStatusJSONIncludesVersionAlignment(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-327"}},
	})
	seedAgentRecord(t, base, "issue-327", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-327", AgentPID: 1234,
	})
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-327",
		ExplicitSession:  true,
		JSON:             true,
		RuntimeVersion:   "v2.12.0",
		VersionSources:   alignedVersionSources(t, "v2.12.0"),
		Probe:            statusProbe(map[int]bool{1234: true}, map[int]bool{1234: true}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if !env.Data.Versions.Aligned || env.Data.Versions.CodexPlugin.Version != "2.12.0" {
		t.Fatalf("status versions = %+v, want aligned plugin data", env.Data.Versions)
	}
}

func TestDoctorJSONIncludesVersionAlignment(t *testing.T) {
	dir := t.TempDir()
	d := newDoctorExec(t, dir)
	d.RunningVersion = "v2.12.0"
	sources := alignedVersionSources(t, "v2.12.0")
	d.PathBinaryVersion = sources.PathBinaryVersion
	d.CodexSkillCacheRoot = sources.CodexSkillCacheRoot
	d.ClaudeSkillCacheRoot = sources.ClaudeSkillCacheRoot
	d.SkillMDContent = sources.SkillMDContent
	var buf strings.Builder
	d.Out = &buf
	d.JSON = true
	if err := executeDoctor(d); err != nil {
		t.Fatalf("doctor: %v\n%s", err, buf.String())
	}
	env := decodeJSONEnvelope[doctorEnvelopeData](t, buf.String())
	if !env.Data.Versions.Aligned || env.Data.Versions.Skill.Version != "v2.12.0" {
		t.Fatalf("doctor versions = %+v, want aligned skill data", env.Data.Versions)
	}
}

func TestVersionAlignmentWarningBeforeLaunch(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		warnVersionAlignmentBeforeLaunchFromSources(versionAlignmentSources{
			RunningVersion: "v2.12.0",
			PathBinaryVersion: func() (string, string, bool) {
				return "/old/amq-squad", "v2.11.0", true
			},
			CodexSkillCacheRoot:  func() string { return "" },
			ClaudeSkillCacheRoot: func() string { return "" },
			SkillMDContent: func(string) (string, string, bool) {
				return "**Skill version: 2.12.0** - ok", "/cache/skill", true
			},
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "warning: version alignment before launch") || !strings.Contains(stderr, "v2.11.0") {
		t.Fatalf("pre-launch warning missing mismatch detail:\n%s", stderr)
	}
}
