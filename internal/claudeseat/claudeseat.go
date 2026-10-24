// Package claudeseat is a Claude seat's own CLAUDE_CONFIG_DIR (2b-2
// milestone B; plan 4.3, R36; lead and reviewer ruling 2026-10-24). The core
// writes it fresh for every launch, 0700, under the seat's staged launch
// directory (<root>/extensions/amq-squad/launch/<handle>/claude-config/<launch
// id>, inside the guard-protected extension dir), with exactly:
//
//   - settings.json: permissions.defaultMode (the launch's mode) and an empty
//     allow list, and the amq-squad plugin enabled;
//   - the amq-squad hooks plugin, installed at user scope in Claude Code's
//     layout (plugins/installed_plugins.json, plugins/known_marketplaces.json,
//     plugins/cache/amq-squad/amq-squad/<version>/), from the files embedded in
//     this binary;
//   - the first-run seed (Seed), an explicit list that is empty until a live
//     run shows Claude Code needs one.
//
// The launch record carries the digest of settings.json and the plugin files
// (Managed). Everything else Claude Code writes into the dir as it runs
// (projects, session files, todos, .claude.json updates) is its own state,
// outside the digest. The owner's user-scope settings and permission rules
// never reach the seat.
package claudeseat

import (
	"crypto/sha256"
	"encoding/hex"
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
)

// Reason codes seat-exec refuses with.
const (
	PermissionMismatch = "claude_permission_mismatch" // the dir is missing, not 0700, not ours, or not what the launch wrote
	HooksMissing       = "claude_hooks_missing"       // the dir's plugin does not run the guard on PreToolUse
	AllowRulesPresent  = "claude_allow_rules_present" // a worktree settings file Claude merges carries allow rules
)

// PluginKey is the plugin's key ("<plugin>@<marketplace>").
const PluginKey = "amq-squad@amq-squad"

// Seed is the first-run state the core writes into .claude.json: the keys
// that mark onboarding complete and the project trusted, if a live run
// shows a fresh config dir stops at a first-run prompt. Empty: nothing is
// seeded (2026-10-24, before the live run).
var Seed = map[string]any{}

// Dir is the config dir of one launch of a seat.
func Dir(root, handle, launchID string) string {
	return filepath.Join(launch.StagedDir(root, handle), "claude-config", launchID)
}

// Written is what Write produced.
type Written struct {
	Dir    string
	Digest string   // sha256 over the managed files
	Seeded []string // the .claude.json keys seeded (sorted; empty when none)
}

// Write creates the launch's config dir (it must not exist) with the
// managed files and the seed.
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
		},
		"plugins/installed_plugins.json": map[string]any{"version": 2, "plugins": map[string]any{
			PluginKey: []map[string]any{{"scope": "user", "installPath": install, "version": version, "installedAt": stamp, "lastUpdated": stamp}},
		}},
		"plugins/known_marketplaces.json": map[string]any{
			"amq-squad": map[string]any{"source": map[string]any{"source": "directory", "path": marketplace}, "installLocation": marketplace, "lastUpdated": stamp},
		},
	}
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
	if len(Seed) > 0 {
		seed := map[string]any{}
		for k, v := range Seed {
			seed[k] = v
			w.Seeded = append(w.Seeded, k)
		}
		sort.Strings(w.Seeded)
		raw, _ := json.MarshalIndent(seed, "", "  ")
		if err := writeFile(filepath.Join(dir, ".claude.json"), append(raw, '\n'), 0o600); err != nil {
			return w, err
		}
	}
	w.Digest, err = Digest(dir)
	return w, err
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

// managed reports whether a dir-relative slash path is a managed file:
// settings.json and everything under plugins/.
func managed(rel string) bool {
	return rel == "settings.json" || strings.HasPrefix(rel, "plugins/")
}

// Digest is the sha256 over the managed files of a config dir: for each, in
// path order, the path, the permission bits and the content, length
// prefixed. A symlink or a non-regular file among them fails.
func Digest(dir string) (string, error) {
	h := sha256.New()
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if rel != "plugins" && !strings.HasPrefix(rel, "plugins/") {
				return filepath.SkipDir // Claude's own state
			}
			return nil
		}
		if !managed(rel) {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", rel)
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	for _, rel := range paths {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d:%s\x00%o\x00%d:", len(rel), rel, st.Mode().Perm(), len(raw))
		h.Write(raw)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Check is seat-exec's check of a Claude seat's config dir before exec: it
// exists, is a real directory, 0700, owned by uid, its managed files have
// the launch's digest, its settings carry the launch's permission mode with
// no allow rule, and its plugin runs the guard on PreToolUse. It returns the
// reason code and the detail of the first failure ("" when it passes).
func Check(dir string, uid int, digest, permissionMode string) (string, string) {
	st, err := os.Lstat(dir)
	switch {
	case err != nil:
		return PermissionMismatch, "the seat's Claude config dir is missing: " + err.Error()
	case !st.IsDir():
		return PermissionMismatch, "the seat's Claude config dir is not a directory"
	case st.Mode().Perm() != 0o700:
		return PermissionMismatch, fmt.Sprintf("the seat's Claude config dir is %o, not 0700", st.Mode().Perm())
	}
	if owner, ok := ownerUID(st); ok && uid > 0 && owner != uid {
		return PermissionMismatch, fmt.Sprintf("the seat's Claude config dir is owned by uid %d, not %d", owner, uid)
	}
	got, err := Digest(dir)
	if err != nil || digest == "" || got != digest {
		return PermissionMismatch, fmt.Sprintf("the seat's Claude config dir is not what the launch wrote (digest %s, the launch record %s, %v)", got, digest, err)
	}
	var settings struct {
		Permissions struct {
			DefaultMode string   `json:"defaultMode"`
			Allow       []string `json:"allow"`
		} `json:"permissions"`
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || json.Unmarshal(raw, &settings) != nil {
		return PermissionMismatch, "the seat's settings.json is unreadable"
	}
	if settings.Permissions.DefaultMode != permissionMode || len(settings.Permissions.Allow) != 0 {
		return PermissionMismatch, fmt.Sprintf("the seat's settings.json has mode %q and %d allow rules; the launch record says %q and none", settings.Permissions.DefaultMode, len(settings.Permissions.Allow), permissionMode)
	}
	if !settings.EnabledPlugins[PluginKey] {
		return HooksMissing, "the amq-squad plugin is not enabled in the seat's settings.json"
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
		} `json:"plugins"`
	}
	raw, err = os.ReadFile(filepath.Join(dir, "plugins", "installed_plugins.json"))
	if err != nil || json.Unmarshal(raw, &installed) != nil {
		return HooksMissing, "the seat's plugins/installed_plugins.json is missing or unreadable"
	}
	for _, e := range installed.Plugins[PluginKey] {
		if e.Scope == "user" && inside(dir, e.InstallPath) && hooksWired(e.InstallPath) {
			return "", ""
		}
	}
	return HooksMissing, "the seat's amq-squad plugin install does not run amq-squad-hook.sh pre-tool on PreToolUse"
}

func inside(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && filepath.IsAbs(p) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hooksWired reports whether an installed plugin directory's hooks.json runs
// the amq-squad hook script on PreToolUse and the script is there.
func hooksWired(installPath string) bool {
	if st, err := os.Lstat(filepath.Join(installPath, "hooks", "amq-squad-hook.sh")); err != nil || !st.Mode().IsRegular() {
		return false
	}
	var hooks struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	raw, err := os.ReadFile(filepath.Join(installPath, "hooks", "hooks.json"))
	if err != nil || json.Unmarshal(raw, &hooks) != nil {
		return false
	}
	for _, group := range hooks.Hooks["PreToolUse"] {
		for _, h := range group.Hooks {
			if strings.Contains(h.Command, "amq-squad-hook.sh") && strings.Contains(h.Command, "pre-tool") {
				return true
			}
		}
	}
	return false
}

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
