// Package claudeseat is a Claude seat's own CLAUDE_CONFIG_DIR (2b-2
// milestone B; plan 4.3, R36; lead and reviewer ruling 2026-10-24). The core
// writes it fresh for every launch, 0700, under the seat's staged launch
// directory (<root>/extensions/amq-squad/launch/<handle>/claude-config/<launch
// id>, inside the guard-protected extension dir), with exactly:
//
//   - settings.json: permissions.defaultMode (the launch's mode) and an empty
//     allow list, the amq-squad plugin enabled (enabledPlugins), and the
//     theme (Theme), which Claude Code 2.1.314 resolves from the user
//     settings first;
//   - the amq-squad hooks plugin, installed at user scope in Claude Code's
//     layout (plugins/installed_plugins.json, plugins/known_marketplaces.json,
//     plugins/cache/amq-squad/amq-squad/<version>/), from the files embedded in
//     this binary;
//   - the first-run state in .claude.json (Claude Code's global config under
//     a custom CLAUDE_CONFIG_DIR): hasCompletedOnboarding, and
//     projects[<worktree>].hasTrustDialogAccepted for the seat's worktree
//     (3.0.1 t6; reviewer ruling 2026-10-30: the seat config is born
//     complete, so the pane never opens the first-run flow or the trust
//     dialog). The field names and their sources are in
//     docs/v3-spike/5-claudeseat-execution.md.
//
// The launch record carries the digest of settings.json, the plugin files,
// the worktree pointer and the first-run facts of .claude.json (their
// values only: Claude Code rewrites the rest of that file as it runs).
// Everything else Claude Code writes into the dir (projects, session files,
// todos, the other .claude.json keys) is its own state, outside the digest.
// The owner's user-scope settings and permission rules never reach the
// seat.
package claudeseat

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/plugins"
	"golang.org/x/text/unicode/norm"
)

// Reason codes: seat-exec's argv refusals, and the seat warnings status
// shows for what the deleted config-dir refusals used to stop (brief 31-1).
const (
	PermissionMismatch = "claude_permission_mismatch" // the argv does not carry exactly the recorded mode, or carries a bypass
	HooksMissing       = "claude_hooks_missing"       // the argv drops the user settings scope or skips hooks
	AllowRulesPresent  = "claude_allow_rules_present" // a warning: a worktree settings file Claude merges carries allow rules
	// HooksSilent is a warning: a Claude seat that ran (an accepted proof)
	// made no hook report since its launch.
	HooksSilent = "claude_hooks_silent"
)

// PluginKey is the plugin's key ("<plugin>@<marketplace>").
const PluginKey = "amq-squad@amq-squad"

// GlobalConfigFile is Claude Code's global config inside its config dir:
// with CLAUDE_CONFIG_DIR set, 2.1.314 reads $CLAUDE_CONFIG_DIR/.claude.json
// (the ".claude" + o6() + ".json" join, o6() empty in the prod build without
// CLAUDE_CODE_CUSTOM_OAUTH_URL, which the seat scrub drops).
const GlobalConfigFile = ".claude.json"

// Theme is the theme the seat's settings.json names: "dark", Claude Code
// 2.1.314's own default (resolveSetting("theme","dark")), set explicitly so
// nothing in the seat asks for one.
const Theme = "dark"

// The first-run field names Claude Code 2.1.314 reads (sources in
// docs/v3-spike/5-claudeseat-execution.md).
const (
	onboardedKey = "hasCompletedOnboarding"
	projectsKey  = "projects"
	trustKey     = "hasTrustDialogAccepted"
)

// trustKeys are the projects keys the launch records the worktree under: the
// path as recorded and, when it differs, its symlink-resolved form (Claude
// Code's own headless runner writes both), each NFC-normalized, as 2.1.314's
// trust walk normalizes the cwd (`Nn(t){return t.normalize("NFC")}`).
func trustKeys(worktree string) []string {
	return pathKeys(worktree)
}

// pathKeys is a path's NFC form and, when it differs, its symlink-resolved
// NFC form.
func pathKeys(p string) []string {
	keys := []string{norm.NFC.String(filepath.Clean(p))}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		if r := norm.NFC.String(real); r != keys[0] {
			keys = append(keys, r)
		}
	}
	return keys
}

// canonicalRoot is the main repository a git worktree belongs to, the way
// 2.1.314 finds it (its Rt and sr): the worktree's .git file names a gitdir
// under <common>/worktrees/, the gitdir's commondir names <common>, and the
// root is <common>'s parent when <common> is a .git directory. "" when the
// worktree is a main checkout (a .git directory), not a git worktree, or the
// layout does not check out.
func canonicalRoot(worktree string) string {
	raw, err := os.ReadFile(filepath.Join(worktree, ".git"))
	if err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(worktree, gitdir)
	}
	gitdir = filepath.Clean(gitdir)
	c, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return ""
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	common = filepath.Clean(common)
	if filepath.Dir(gitdir) != filepath.Join(common, "worktrees") {
		return ""
	}
	if filepath.Base(common) == ".git" {
		return filepath.Dir(common)
	}
	return common
}

// rootKeys are the projects keys of the worktree's canonical repository
// root that are not the worktree's own keys (none for a main checkout).
func rootKeys(worktree string, own []string) []string {
	root := canonicalRoot(worktree)
	if root == "" {
		return nil
	}
	mine := map[string]bool{}
	for _, k := range own {
		mine[k] = true
	}
	var out []string
	for _, k := range pathKeys(root) {
		if !mine[k] {
			out = append(out, k)
		}
	}
	return out
}

// Dir is the config dir of one launch of a seat.
func Dir(root, handle, launchID string) string {
	return filepath.Join(launch.StagedDir(root, handle), "claude-config", launchID)
}

// Written is what Write produced.
type Written struct {
	Dir    string
	Seeded []string // the first-run facts written into .claude.json (sorted)
}

// Write creates the launch's config dir (it must not exist) with the
// managed files and the first-run state, all by value (no symlink into the
// owner's config).
func Write(dir, permissionMode, worktree string, now time.Time) (Written, error) {
	w := Written{Dir: dir, Seeded: []string{}}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return w, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return w, fmt.Errorf("claude config dir %s: %w (a launch gets a fresh dir)", dir, err)
	}
	version, err := pluginVersion()
	if err != nil {
		return w, err
	}
	install := filepath.Join(dir, "plugins", "cache", "amq-squad", "amq-squad", version)
	marketplace := filepath.Join(dir, "plugins", "marketplaces", "amq-squad")
	stamp := now.UTC().Format(time.RFC3339)
	files := map[string]any{
		"settings.json": map[string]any{
			"permissions":    map[string]any{"defaultMode": permissionMode, "allow": []string{}},
			"enabledPlugins": map[string]bool{PluginKey: true},
			"theme":          Theme,
		},
		"plugins/installed_plugins.json": map[string]any{"version": 2, "plugins": map[string]any{
			PluginKey: []map[string]any{{"scope": "user", "installPath": install, "version": version, "installedAt": stamp, "lastUpdated": stamp}},
		}},
		"plugins/known_marketplaces.json": map[string]any{
			"amq-squad": map[string]any{"source": map[string]any{"source": "directory", "path": marketplace}, "installLocation": marketplace, "lastUpdated": stamp},
		},
	}
	first := map[string]any{onboardedKey: true}
	w.Seeded = append(w.Seeded, onboardedKey)
	if worktree != "" {
		// The worktree's settings files are part of the digest (phase 5
		// re-pass of 4-2, P1-1): Claude merges them into the seat's settings.
		// The trust keys are recorded with it, so the digest reads the same
		// keys the launch wrote.
		keys := trustKeys(worktree)
		files[WorktreeFile] = map[string]any{"worktree": worktree, "trusted": keys, "canonical_root": rootKeys(worktree, keys)}
		projects := map[string]any{}
		for _, k := range keys {
			projects[k] = map[string]bool{trustKey: true}
			w.Seeded = append(w.Seeded, fmt.Sprintf("%s[%q].%s", projectsKey, k, trustKey))
		}
		first[projectsKey] = projects
	}
	files[GlobalConfigFile] = first
	sort.Strings(w.Seeded)
	for rel, v := range files {
		raw, _ := json.MarshalIndent(v, "", "  ")
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(rel)), append(raw, '\n'), 0o600); err != nil {
			return w, err
		}
	}
	// The plugin files, in the cache install and in the marketplace copy.
	err = fs.WalkDir(plugins.Claude, plugins.ClaudeRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := plugins.Claude.ReadFile(p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if strings.HasSuffix(p, ".sh") {
			mode = 0o700
		}
		rel := strings.TrimPrefix(p, plugins.ClaudeRoot+"/")
		for _, base := range []string{install, marketplace} {
			if err := writeFile(filepath.Join(base, filepath.FromSlash(rel)), raw, mode); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return w, err
	}
	return w, nil
}

func pluginVersion() (string, error) {
	raw, err := plugins.Claude.ReadFile(path.Join(plugins.ClaudeRoot, ".claude-plugin", "plugin.json"))
	if err != nil {
		return "", err
	}
	var m struct{ Version string }
	if json.Unmarshal(raw, &m) != nil || m.Version == "" || strings.ContainsAny(m.Version, "/\\") {
		return "", fmt.Errorf("the embedded plugin.json names no version")
	}
	return m.Version, nil
}

func writeFile(p string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// WorktreeFile names the seat's worktree inside the config dir, with the
// trust keys Write recorded for it.
const WorktreeFile = "amq-squad-worktree.json"

// WorktreeAllowRules lists the permissions.allow rules of the settings files
// Claude Code merges from the seat's worktree (.claude/settings.json and
// .claude/settings.local.json); a file it cannot parse counts as carrying
// rules (fail closed).
func WorktreeAllowRules(worktree string) []string {
	var out []string
	for _, name := range []string{"settings.json", "settings.local.json"} {
		p := filepath.Join(worktree, ".claude", name)
		raw, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		var s struct {
			Permissions struct {
				Allow []string `json:"allow"`
			} `json:"permissions"`
		}
		if err != nil || json.Unmarshal(raw, &s) != nil {
			out = append(out, filepath.Join(".claude", name)+": unreadable")
			continue
		}
		for _, r := range s.Permissions.Allow {
			out = append(out, filepath.Join(".claude", name)+": "+r)
		}
	}
	return out
}
