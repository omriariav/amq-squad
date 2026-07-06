package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type versionComponentData struct {
	Status            string   `json:"status"`
	Version           string   `json:"version,omitempty"`
	Path              string   `json:"path,omitempty"`
	MatchesRunning    *bool    `json:"matches_running,omitempty"`
	AvailableVersions []string `json:"available_versions,omitempty"`
	Detail            string   `json:"detail,omitempty"`
}

type versionAlignmentData struct {
	RunningBinary string               `json:"running_binary,omitempty"`
	PathBinary    versionComponentData `json:"path_binary"`
	CodexPlugin   versionComponentData `json:"codex_plugin"`
	ClaudePlugin  versionComponentData `json:"claude_plugin"`
	Skill         versionComponentData `json:"skill"`
	Aligned       bool                 `json:"aligned"`
	Warnings      []string             `json:"warnings,omitempty"`
}

type versionAlignmentSources struct {
	RunningVersion       string
	PathBinaryVersion    func() (path, version string, found bool)
	CodexSkillCacheRoot  func() string
	ClaudeSkillCacheRoot func() string
	SkillMDContent       func(runningVersion string) (content, path string, found bool)
}

func defaultVersionAlignmentSources(running string) versionAlignmentSources {
	return versionAlignmentSources{
		RunningVersion:       running,
		PathBinaryVersion:    defaultPathBinaryVersion,
		CodexSkillCacheRoot:  defaultCodexSkillCacheRoot,
		ClaudeSkillCacheRoot: defaultClaudeSkillCacheRoot,
		SkillMDContent:       defaultSkillMDContent,
	}
}

func versionAlignmentSourcesFromDoctor(d doctorExecution) versionAlignmentSources {
	s := defaultVersionAlignmentSources(d.RunningVersion)
	if d.PathBinaryVersion != nil {
		s.PathBinaryVersion = d.PathBinaryVersion
	}
	if d.CodexSkillCacheRoot != nil {
		s.CodexSkillCacheRoot = d.CodexSkillCacheRoot
	}
	if d.ClaudeSkillCacheRoot != nil {
		s.ClaudeSkillCacheRoot = d.ClaudeSkillCacheRoot
	}
	if d.SkillMDContent != nil {
		s.SkillMDContent = d.SkillMDContent
	}
	return s
}

func buildVersionAlignment(s versionAlignmentSources) versionAlignmentData {
	running := normalizeRuntimeVersion(s.RunningVersion)
	if strings.TrimSpace(running) == "" {
		running = "dev"
	}
	defaults := defaultVersionAlignmentSources(running)
	if s.PathBinaryVersion == nil {
		s.PathBinaryVersion = defaults.PathBinaryVersion
	}
	if s.CodexSkillCacheRoot == nil {
		s.CodexSkillCacheRoot = defaults.CodexSkillCacheRoot
	}
	if s.ClaudeSkillCacheRoot == nil {
		s.ClaudeSkillCacheRoot = defaults.ClaudeSkillCacheRoot
	}
	if s.SkillMDContent == nil {
		s.SkillMDContent = defaults.SkillMDContent
	}
	out := versionAlignmentData{
		RunningBinary: running,
		PathBinary:    pathBinaryVersionComponent(s.PathBinaryVersion, running),
		CodexPlugin:   pluginVersionComponent("Codex plugin", s.CodexSkillCacheRoot, ".codex-plugin/plugin.json", running),
		ClaudePlugin:  pluginVersionComponent("Claude plugin", s.ClaudeSkillCacheRoot, ".claude-plugin/plugin.json", running),
		Skill:         skillVersionComponent(s.SkillMDContent, running),
		Aligned:       true,
	}
	for _, c := range []versionComponentData{out.PathBinary, out.CodexPlugin, out.ClaudePlugin, out.Skill} {
		if c.Status == "warn" {
			out.Aligned = false
			out.Warnings = append(out.Warnings, c.Detail)
		}
	}
	return out
}

func versionAlignmentWarnings(v versionAlignmentData) []string {
	return append([]string(nil), v.Warnings...)
}

func warnVersionAlignmentBeforeLaunch(running string) {
	warnVersionAlignmentBeforeLaunchFromSources(defaultVersionAlignmentSources(running))
}

func warnVersionAlignmentBeforeLaunchFromSources(s versionAlignmentSources) {
	report := buildVersionAlignment(s)
	for _, warning := range versionAlignmentWarnings(report) {
		quietNotice("warning: version alignment before launch: %s\n", warning)
	}
}

func normalizeRuntimeVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || v == "(devel)" {
		return "dev"
	}
	return v
}

func devRuntimeVersion(v string) bool {
	v = normalizeRuntimeVersion(v)
	return v == "" || strings.EqualFold(v, "dev")
}

func boolPtr(v bool) *bool {
	return &v
}

func versionMatchesRunning(version, running string) bool {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	running = strings.TrimPrefix(strings.TrimSpace(running), "v")
	return version != "" && running != "" && version == running
}

func pathBinaryVersionComponent(resolve func() (path, version string, found bool), running string) versionComponentData {
	if devRuntimeVersion(running) {
		return versionComponentData{Status: "skipped", Detail: "running a dev/unstamped build; PATH binary comparison skipped"}
	}
	if resolve == nil {
		resolve = defaultPathBinaryVersion
	}
	path, version, found := resolve()
	if !found {
		return versionComponentData{
			Status:         "warn",
			MatchesRunning: boolPtr(false),
			Detail:         "amq-squad is not on PATH; launched agents call bare `amq-squad`, so runtime alignment cannot be guaranteed",
		}
	}
	if strings.TrimSpace(version) == "" {
		return versionComponentData{
			Status:         "warn",
			Path:           path,
			MatchesRunning: boolPtr(false),
			Detail:         fmt.Sprintf("could not read the version of amq-squad on PATH (%s)", path),
		}
	}
	match := versionMatchesRunning(version, running)
	status := "ok"
	detail := fmt.Sprintf("PATH binary %s matches running binary %s", version, running)
	if !match {
		status = "warn"
		detail = fmt.Sprintf("PATH binary is %s but running binary is %s; launched agents inherit the PATH binary", version, running)
	}
	return versionComponentData{
		Status:         status,
		Version:        version,
		Path:           path,
		MatchesRunning: boolPtr(match),
		Detail:         detail,
	}
}

func defaultClaudeSkillCacheRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".claude", "plugins", "cache", "amq-squad", "amq-squad")
}

func pluginVersionComponent(label string, rootFn func() string, manifestRel, running string) versionComponentData {
	if devRuntimeVersion(running) {
		return versionComponentData{Status: "skipped", Detail: "running a dev/unstamped build; plugin-cache comparison skipped"}
	}
	if rootFn == nil {
		rootFn = func() string { return "" }
	}
	root := strings.TrimSpace(rootFn())
	if root == "" {
		return versionComponentData{Status: "unknown", Detail: label + " cache root unavailable"}
	}
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return versionComponentData{Status: "unknown", Detail: fmt.Sprintf("%s cache root unavailable at %s", label, root)}
	}
	want := strings.TrimPrefix(strings.TrimSpace(running), "v")
	available := pluginCacheVersions(root)
	manifest := filepath.Join(root, want, manifestRel)
	info, err := os.Lstat(filepath.Join(root, want))
	if err != nil {
		return versionComponentData{
			Status:            "warn",
			MatchesRunning:    boolPtr(false),
			AvailableVersions: available,
			Detail:            fmt.Sprintf("%s bundle %s is not directly cached under %s", label, want, root),
		}
	}
	status := "ok"
	if info.Mode()&os.ModeSymlink != 0 {
		status = "warn"
	}
	version, err := readPluginManifestVersion(manifest)
	if err != nil {
		return versionComponentData{
			Status:            "warn",
			Path:              manifest,
			MatchesRunning:    boolPtr(false),
			AvailableVersions: available,
			Detail:            fmt.Sprintf("%s manifest unreadable at %s: %v", label, manifest, err),
		}
	}
	match := versionMatchesRunning(version, running)
	detail := fmt.Sprintf("%s manifest %s matches running binary %s", label, version, running)
	if !match {
		status = "warn"
		detail = fmt.Sprintf("%s manifest is %s but running binary is %s", label, version, running)
	} else if info.Mode()&os.ModeSymlink != 0 {
		detail = fmt.Sprintf("%s bundle %s matches running binary but is a symlink at %s; refresh the plugin cache", label, version, filepath.Join(root, want))
	}
	return versionComponentData{
		Status:            status,
		Version:           version,
		Path:              manifest,
		MatchesRunning:    boolPtr(match),
		AvailableVersions: available,
		Detail:            detail,
	}
}

func pluginCacheVersions(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var versions []string
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			versions = append(versions, entry.Name())
		}
	}
	sort.Strings(versions)
	return versions
}

func readPluginManifestVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return "", fmt.Errorf("missing version")
	}
	return strings.TrimSpace(manifest.Version), nil
}

func skillVersionComponent(reader func(runningVersion string) (content, path string, found bool), running string) versionComponentData {
	if devRuntimeVersion(running) {
		return versionComponentData{Status: "skipped", Detail: "running a dev/unstamped build; skill marker comparison skipped"}
	}
	if reader == nil {
		reader = defaultSkillMDContent
	}
	content, path, found := reader(running)
	if !found {
		return versionComponentData{
			Status:         "warn",
			MatchesRunning: boolPtr(false),
			Detail:         fmt.Sprintf("no installed amq-squad skill bundle found for %s", running),
		}
	}
	m := skillVersionMarkerRE.FindStringSubmatch(content)
	if m == nil {
		return versionComponentData{
			Status:         "warn",
			Path:           path,
			MatchesRunning: boolPtr(false),
			Detail:         fmt.Sprintf("installed skill at %s has no 'Skill version:' marker", path),
		}
	}
	version := "v" + m[1]
	match := versionMatchesRunning(version, running)
	status := "ok"
	detail := fmt.Sprintf("skill marker %s matches running binary %s", version, running)
	if !match {
		status = "warn"
		detail = fmt.Sprintf("skill marker is %s but running binary is %s", version, running)
	}
	return versionComponentData{
		Status:         status,
		Version:        version,
		Path:           path,
		MatchesRunning: boolPtr(match),
		Detail:         detail,
	}
}
