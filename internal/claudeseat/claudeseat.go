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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/plugins"
	"golang.org/x/text/unicode/norm"
)

// Reason codes seat-exec refuses with.
const (
	PermissionMismatch = "claude_permission_mismatch" // the dir is missing, not 0700, not ours, or not what the launch wrote
	HooksMissing       = "claude_hooks_missing"       // the dir's plugin does not run the guard on PreToolUse
	AllowRulesPresent  = "claude_allow_rules_present" // a worktree settings file Claude merges carries allow rules
	// SettingsEnvForbidden: a settings file Claude merges sets, in its env
	// block, a variable that would redirect the hook or the seat's identity
	// (phase 5 re-pass of 4-2, P1-1).
	SettingsEnvForbidden = "claude_settings_env_forbidden"
	// ConfigChanged: during the run (the hook's context call) or at a seat
	// exit, the seat's config dir no longer has the launch's digest; the
	// seat is quarantined (milestone B first pass P1).
	ConfigChanged = "claude_config_changed"
	// OnboardingMissing: the seat's config dir does not carry the
	// onboarding-complete state or a theme; Claude Code would open its
	// first-run flow in the pane (3.0.1 t6).
	OnboardingMissing = "claude_onboarding_missing"
	// TrustMissing: the seat's config dir does not record the seat's
	// worktree as trusted; Claude Code would open its trust dialog (3.0.1
	// t6).
	TrustMissing = "claude_trust_missing"
	// ConfigUnexpected: the seat's .claude.json carries a key the launch
	// did not write and Claude Code does not keep as bookkeeping: another
	// project's entry (trust beyond the seat's worktree) or a permission
	// input such as allowedTools (3.0.1 t6 fix round).
	ConfigUnexpected = "claude_config_unexpected"
	// TrustRootRefused: the seat's .claude.json has an entry for the
	// worktree's canonical repository root, the key 2.1.314 checks first
	// for trust; trust there would extend to every sibling worktree (3.0.1
	// t6 fix round, reviewer addition).
	TrustRootRefused = "claude_trust_root_refused"
)

// ConfigDrift reports whether a Claude seat's config dir differs from the
// digest its launch recorded, with a detail naming both digests (never a
// file's content). An empty recorded digest is not a Claude seat of 2b-2 B.
func ConfigDrift(root, handle, launchID, recorded string) (bool, string) {
	if recorded == "" {
		return false, ""
	}
	got, err := Digest(Dir(root, handle, launchID))
	if err != nil {
		return true, "the seat's Claude config dir cannot be read: " + err.Error()
	}
	if got != recorded {
		return true, fmt.Sprintf("the seat's Claude config dir is %s, the launch wrote %s", got, recorded)
	}
	return false, ""
}

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
	Digest string   // sha256 over the managed files and the first-run facts
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

// WorktreeFile names the seat's worktree inside the config dir: a managed
// file, so the worktree the digest reads cannot be swapped unseen.
const WorktreeFile = "amq-squad-worktree.json"

// worktreeSettings are the settings files Claude Code merges from the
// project directory (the seat's worktree).
var worktreeSettings = []string{"settings.json", "settings.local.json"}

// managed reports whether a dir-relative slash path is a managed file:
// settings.json, the worktree pointer and everything under plugins/.
func managed(rel string) bool {
	return rel == "settings.json" || rel == WorktreeFile || strings.HasPrefix(rel, "plugins/")
}

// recordedWorktree reads the worktree the launch recorded in the config dir
// ("" when it recorded none).
func recordedWorktree(dir string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, WorktreeFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var w struct {
		Worktree string `json:"worktree"`
	}
	if json.Unmarshal(raw, &w) != nil || !filepath.IsAbs(w.Worktree) {
		return "", fmt.Errorf("%s does not name an absolute worktree", WorktreeFile)
	}
	return w.Worktree, nil
}

// recordedTrust reads the trust keys the launch recorded in the worktree
// pointer (none when it recorded none or cannot be parsed).
func recordedTrust(raw []byte) []string {
	var w struct {
		Trusted []string `json:"trusted"`
	}
	if json.Unmarshal(raw, &w) != nil {
		return nil
	}
	return w.Trusted
}

// readWorktreeSettings reads the worktree's .claude settings files through
// descriptors with no symlink followed; a missing .claude or file is absent
// (not in the map), a symlink or a non-regular file fails.
func readWorktreeSettings(worktree string) (map[string][]byte, error) {
	out := map[string][]byte{}
	anchor, err := filepath.EvalSymlinks(filepath.Clean(worktree))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	wt, err := fsanchor.OpenAnchored(anchor, nil, false)
	if err != nil {
		return nil, err
	}
	defer wt.Close()
	d, err := wt.Sub(".claude")
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%s/.claude: %w (a symlink or not a directory)", worktree, err)
	}
	defer d.Close()
	for _, name := range worktreeSettings {
		f, kind, _, _, err := d.OpenEntry(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if kind != fsanchor.KindRegular {
			f.Close()
			return nil, fmt.Errorf("%s/.claude/%s is not a regular file", worktree, name)
		}
		raw, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}
		out[name] = raw
	}
	return out, nil
}

// ForbiddenSettingsEnv lists the env entries, in every settings source
// Claude merges for the seat (its config dir's settings.json and the
// worktree's .claude/settings.json and settings.local.json), that name
// AMQ_SQUAD_BIN, an AM_* identity variable, an AMQ_SQUAD_* variable or a
// CLAUDE_CODE_* switch: Claude's settings env wins over the launch's value,
// so such an entry could point the hook at another binary or move the seat's
// identity (phase 5 re-pass of 4-2, P1-1). A source that cannot be read or
// parsed is listed (fail closed).
func ForbiddenSettingsEnv(dir string) []string {
	sources := map[string][]byte{}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return []string{"settings.json: unreadable"}
	}
	sources["settings.json"] = raw
	worktree, err := recordedWorktree(dir)
	if err != nil {
		return []string{WorktreeFile + ": " + err.Error()}
	}
	if worktree != "" {
		files, err := readWorktreeSettings(worktree)
		if err != nil {
			return []string{"worktree settings: " + err.Error()}
		}
		for name, b := range files {
			sources[filepath.Join("worktree", ".claude", name)] = b
		}
	}
	var out []string
	for src, b := range sources {
		var s struct {
			Env map[string]json.RawMessage `json:"env"`
		}
		if json.Unmarshal(b, &s) != nil {
			out = append(out, src+": unparsable")
			continue
		}
		for k := range s.Env {
			if forbiddenEnvName(k) {
				out = append(out, src+": env "+k)
			}
		}
	}
	sort.Strings(out)
	return out
}

func forbiddenEnvName(name string) bool {
	u := strings.ToUpper(strings.TrimSpace(name))
	return u == "AMQ_SQUAD_BIN" || strings.HasPrefix(u, "AM_") || strings.HasPrefix(u, "AMQ_SQUAD_") || strings.HasPrefix(u, "CLAUDE_CODE_")
}

// Digest is the sha256 over the managed files of a config dir: for each, in
// path order, the path, the permission bits and the content, length
// prefixed. It reads through descriptors opened from the anchored config dir
// with no symlink followed (P1-10 of the milestone B second pass: a path
// that a symlink swap redirects is never hashed in place of the file Claude
// loads). A symlink or a non-regular file among them fails.
func Digest(dir string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(dir)))
	if err != nil {
		return "", err
	}
	d, err := fsanchor.OpenAnchored(parent, []string{filepath.Base(dir)}, false)
	if err != nil {
		return "", err
	}
	defer d.Close()
	type entry struct {
		rel  string
		perm uint32
		data []byte
	}
	var entries []entry
	var (
		trusted       []string
		globalRaw     []byte
		globalPresent bool
	)
	read := func(rel string, f *os.File, mode uint32) error {
		defer f.Close()
		raw, err := io.ReadAll(f)
		if err != nil {
			return err
		}
		entries = append(entries, entry{rel: rel, perm: mode & 0o777, data: raw})
		return nil
	}
	names, err := d.Names()
	if err != nil {
		return "", err
	}
	for _, name := range names {
		switch name {
		case "settings.json":
			f, kind, _, mode, err := d.OpenEntry(name)
			if err != nil {
				return "", err
			}
			if kind != fsanchor.KindRegular {
				f.Close()
				return "", fmt.Errorf("%s is not a regular file", name)
			}
			if err := read(name, f, mode); err != nil {
				return "", err
			}
		case WorktreeFile:
			f, kind, _, mode, err := d.OpenEntry(name)
			if err != nil {
				return "", err
			}
			if kind != fsanchor.KindRegular {
				f.Close()
				return "", fmt.Errorf("%s is not a regular file", name)
			}
			if err := read(name, f, mode); err != nil {
				return "", err
			}
			trusted = recordedTrust(entries[len(entries)-1].data)
		case GlobalConfigFile:
			// Claude Code rewrites this file as it runs (numStartups, its
			// own project state): only the first-run facts are hashed, read
			// through the anchored descriptor like the managed files.
			f, kind, _, _, err := d.OpenEntry(name)
			if err != nil {
				return "", err
			}
			if kind != fsanchor.KindRegular {
				f.Close()
				return "", fmt.Errorf("%s is not a regular file", name)
			}
			globalRaw, err = io.ReadAll(f)
			f.Close()
			if err != nil {
				return "", err
			}
			globalPresent = true
		case "plugins":
			sub, err := d.Sub(name)
			if err != nil {
				return "", fmt.Errorf("plugins: %w", err)
			}
			err = fsanchor.WalkAnchored(sub, "plugins", nil, func(rel string, f *os.File, kind fsanchor.EntryKind, _ int64, mode uint32) error {
				if kind != fsanchor.KindRegular {
					return nil // a directory: descended
				}
				return read(rel, f, mode)
			})
			sub.Close()
			if err != nil {
				return "", err
			}
		}
	}
	// The first-run facts and the permission inputs of .claude.json, by
	// value (3.0.1 t6).
	entries = append(entries, entry{rel: GlobalConfigFile + "#facts", data: configFacts(globalRaw, globalPresent, trusted)})
	// The worktree's settings files, which Claude merges: present with their
	// bytes, or absent (phase 5 re-pass of 4-2, P1-1).
	worktree, err := recordedWorktree(dir)
	if err != nil {
		return "", err
	}
	if worktree != "" {
		files, err := readWorktreeSettings(worktree)
		if err != nil {
			return "", err
		}
		for _, name := range worktreeSettings {
			rel := "worktree/.claude/" + name
			if b, ok := files[name]; ok {
				entries = append(entries, entry{rel: rel, perm: 0o600, data: b})
			} else {
				entries = append(entries, entry{rel: rel + "\x00absent"})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].rel < entries[j].rel })
	h := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(h, "%d:%s\x00%o\x00%d:", len(e.rel), e.rel, os.FileMode(e.perm).Perm(), len(e.data))
		h.Write(e.data)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// Check is seat-exec's check of a Claude seat's config dir before exec: it
// exists, is a real directory, 0700, owned by uid, its managed files have
// the launch's digest, its settings carry the launch's permission mode with
// no allow rule, it is past first-run onboarding (hasCompletedOnboarding and
// a theme: claude_onboarding_missing), its worktree is trusted
// (claude_trust_missing), its .claude.json carries nothing beyond what the
// launch wrote and Claude Code's bookkeeping (claude_config_unexpected), and
// its plugin runs the guard on PreToolUse
// (claude_hooks_missing). It returns the reason code and the detail of the
// first failure ("" when it passes).
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
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || json.Unmarshal(raw, &settings) != nil {
		return PermissionMismatch, "the seat's settings.json is unreadable"
	}
	if settings.Permissions.DefaultMode != permissionMode || len(settings.Permissions.Allow) != 0 {
		return PermissionMismatch, fmt.Sprintf("the seat's settings.json has mode %q and %d allow rules; the launch record says %q and none", settings.Permissions.DefaultMode, len(settings.Permissions.Allow), permissionMode)
	}
	if env := ForbiddenSettingsEnv(dir); len(env) > 0 {
		return SettingsEnvForbidden, "a settings file Claude merges for the seat sets a forbidden env variable: " + strings.Join(env, "; ")
	}
	return screensGap(dir)
}

// screensGap is the one check of what keeps a Claude seat's pane free of
// first-run screens, shared by Check (seat-exec, before exec) and
// StartupScreensClosed (the launch's wake condition): the onboarding state
// and a theme (claude_onboarding_missing), the worktree's trust under its
// NFC keys (claude_trust_missing), no entry for the canonical repository
// root (claude_trust_root_refused), nothing beyond the launch's keys and
// the binary's bookkeeping and no trust or permission input of the wrong
// shape (claude_config_unexpected), no CLAUDE.md import from outside the
// worktree pending approval (claude_external_includes_pending), and the
// hooks plugin enabled and wired (claude_hooks_missing). It reads only what Write seeds
// (settings.json, .claude.json, the worktree pointer, plugins/) and the
// worktree's own .git layout, never anything Claude Code writes after the
// launch. It returns the reason code and the detail of the first gap ("",
// "" when there is none).
func screensGap(dir string) (string, string) {
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
		Theme          string          `json:"theme"`
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil || json.Unmarshal(raw, &settings) != nil {
		return OnboardingMissing, "the seat's settings.json is missing or unreadable"
	}
	if cfg, err := readGlobalConfig(dir); err == nil {
		if bad := malformedInputs(cfg); len(bad) > 0 {
			return ConfigUnexpected, "the seat's " + GlobalConfigFile + " carries a trust or permission input of the wrong shape (2.1.314 tests them by truthiness): " + strings.Join(bad, "; ")
		}
	}
	onboarding, trust := firstRunGaps(dir, settings.Theme)
	if len(onboarding) > 0 {
		return OnboardingMissing, "the seat's Claude config dir is not past first-run onboarding: " + strings.Join(onboarding, "; ")
	}
	if len(trust) > 0 {
		return TrustMissing, "the seat's Claude config dir does not trust its worktree: " + strings.Join(trust, "; ")
	}
	if roots := trustedRoot(dir); len(roots) > 0 {
		return TrustRootRefused, "the seat's " + GlobalConfigFile + " has an entry for the worktree's canonical repository root, which Claude Code checks first and which would trust every sibling worktree: " + strings.Join(roots, "; ")
	}
	if extra := unexpectedConfig(dir); len(extra) > 0 {
		return ConfigUnexpected, "the seat's " + GlobalConfigFile + " carries what the launch did not write and Claude Code does not keep as bookkeeping: " + strings.Join(extra, "; ")
	}
	if worktree, err := recordedWorktree(dir); err == nil {
		if ext := ExternalIncludes(worktree); len(ext) > 0 {
			return ExternalIncludesPending, "a CLAUDE.md the seat loads at start imports files outside its worktree and nothing approves them, so Claude Code would open its external-includes dialog: " + strings.Join(ext, "; ")
		}
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

// OnboardingUnverified is StartupScreensClosed's reason when there is no
// config dir to verify (it is missing or not a directory).
const OnboardingUnverified = "claude_onboarding_unverified"

// StartupScreensClosed reports whether a Claude seat's config dir rules out
// every first-run screen that a raw AMQ wake doorbell could answer: a
// condition of the seat's wake (3.0.1). In 2.1.314 those are the onboarding
// flow (hasCompletedOnboarding, with the theme in settings.json), the folder
// trust dialog (projects[<worktree>].hasTrustDialogAccepted under the NFC
// keys, with no canonical-root entry and nothing else in projects), the
// external-includes dialog (no CLAUDE.md the seat loads imports a file
// outside its worktree), and the hooks, which the binary holds until that
// trust (loadHooksModulesHeldForTrust) and which run only with the plugin
// enabled and wired. It is screensGap,
// the check seat-exec's Check runs before exec: one source of truth.
//
// It depends only on what Write seeds, never on anything written after the
// launch: the launch's compile step calls it before the seat's own dir
// exists, on a scratch dir Write produced for the same mode and worktree
// (seatlaunch's scratchClaudeWakeConditions), and the seat's dir, written
// later by the same Write, carries the same answer. It returns true and ""
// when every screen is closed, else false and the reason code of the first
// gap (claude_onboarding_unverified for a missing dir).
func StartupScreensClosed(dir string) (bool, string) {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return false, OnboardingUnverified
	}
	code, _ := screensGap(dir)
	return code == "", code
}
