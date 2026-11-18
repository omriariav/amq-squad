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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
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
	ProjectMCP         = "claude_project_mcp"         // the seat's worktree or main checkout enables project MCP servers or plugins (ProjectMCPReasons)
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
	raw, ok, err := ReadMeta(filepath.Join(worktree, ".git"))
	if !ok || err != nil {
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
	c, ok, err := ReadMeta(filepath.Join(gitdir, "commondir"))
	if !ok || err != nil {
		return ""
	}
	common := strings.TrimSpace(string(c))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitdir, common)
	}
	common = filepath.Clean(common)
	if !sameDirPath(filepath.Dir(gitdir), filepath.Join(common, "worktrees")) {
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

// ProjectMCPReasons lists what, in the seat's worktree and in its main
// checkout (a linked worktree's; Claude Code reads the local settings file
// there), links the session's configuration out of the checkout (a symlinked
// .claude, settings file or .mcp.json), or would let Claude Code start an
// MCP server or a plugin the seat's launch did not bring: MCP servers run
// outside the guard and outside any Bash sandbox, and AMQ v0.140.0 does not
// admit --strict-mcp-config (3.3.1 item 5, D5 (c)). It reads:
//
//   - .mcp.json declaring any server;
//   - .claude/settings.json and .claude/settings.local.json setting
//     enableAllProjectMcpServers true, enabledMcpjsonServers, any
//     enabledPlugins entry true, or extraKnownMarketplaces (a project file
//     can register a marketplace and enable a plugin from it, and a plugin
//     can bundle MCP servers);
//   - a skills-directory plugin (.claude/skills/<dir>/.claude-plugin/
//     plugin.json) declaring mcpServers or carrying a .mcp.json with a
//     server, which Claude Code loads by its defaultEnabled in a trusted
//     folder.
//
// A file it cannot read or parse counts as enabling (fail closed).
func ProjectMCPReasons(worktree string) []string {
	if !filepath.IsAbs(worktree) {
		return []string{fmt.Sprintf("the launch records no absolute worktree (%q) to check", worktree)}
	}
	var out []string
	checkouts := []string{worktree}
	m := MainCheckout(worktree)
	if m != "" && filepath.Clean(m) != filepath.Clean(worktree) {
		checkouts = append(checkouts, m)
	}
	// a worktree whose git pointer cannot be followed has a repository root
	// Claude Code may still find: it is refused, never assumed absent
	// (Codex pass 7)
	if st, err := os.Lstat(filepath.Join(worktree, ".git")); err == nil && !st.IsDir() && m == "" {
		out = append(out, filepath.Join(worktree, ".git")+": its repository root cannot be followed")
	}
	// Every file is decoded with its keys exactly as written (Claude Code
	// looks keys up exactly; Go's struct decoding would fold case and let a
	// MCPSERVERS key mask mcpServers), and a value counts by JavaScript
	// truthiness, as Claude Code reads it.
	readObj := func(p string) (map[string]json.RawMessage, bool, error) {
		raw, ok, err := readSmall(p)
		if !ok || err != nil {
			return nil, ok, err
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, true, err
		}
		return m, true, nil
	}
	// servers: a .mcp.json declaring a server, wrapped in mcpServers or as a
	// flat map (Claude Code's plugin loader takes either).
	servers := func(p string) {
		obj, ok, err := readObj(p)
		switch {
		case !ok:
			return
		case err != nil:
			out = append(out, p+": unreadable")
			return
		}
		// Claude Code's loader takes mcpServers when it is truthy, else the
		// whole object as the server map (`G.mcpServers || G`)
		if raw, has := obj["mcpServers"]; has && !falsy(raw, "null", "false", "0", `""`) {
			var m map[string]json.RawMessage
			if json.Unmarshal(raw, &m) != nil {
				out = append(out, p+": mcpServers is not a server map")
			} else if len(m) > 0 {
				out = append(out, fmt.Sprintf("%s declares %d MCP server(s)", p, len(m)))
			}
			return
		}
		n := len(obj)
		if _, has := obj["mcpServers"]; has {
			n--
		}
		if n > 0 {
			out = append(out, fmt.Sprintf("%s declares %d MCP server(s) as a flat map", p, n))
		}
	}
	// Claude Code merges the .mcp.json of every directory above the
	// session's working directory too (Codex pass 9)
	for _, d := range ancestorsBothForms(worktree) {
		p := filepath.Join(d, ".mcp.json")
		// a symlinked one points the servers where the guard cannot hold
		// them (Codex pass 10)
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
			out = append(out, p+" is a symlink")
			continue
		}
		servers(p)
	}
	for _, c := range checkouts {
		// a config path that is a symlink points the session's settings
		// out of the checkout, where the guard cannot hold them (Codex pass
		// 4: a dangling link, a moved target's parent)
		for _, rel := range []string{".claude", filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json"), ".mcp.json", filepath.Join(".claude", "agents"), filepath.Join(".claude", "skills")} {
			p := filepath.Join(c, rel)
			if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
				out = append(out, p+" is a symlink")
			}
		}
		servers(filepath.Join(c, ".mcp.json"))
		for _, name := range []string{"settings.json", "settings.local.json"} {
			p := filepath.Join(c, ".claude", name)
			obj, ok, err := readObj(p)
			switch {
			case !ok:
				continue
			case err != nil:
				out = append(out, p+": unreadable")
				continue
			}
			if raw, has := obj["enableAllProjectMcpServers"]; has && !falsy(raw, "false", "null", "0", `""`) {
				out = append(out, p+" sets enableAllProjectMcpServers")
			}
			if raw, has := obj["enabledMcpjsonServers"]; has && !falsy(raw, "null", "[]", "false") {
				out = append(out, p+" sets enabledMcpjsonServers")
			}
			if raw, has := obj["enabledPlugins"]; has {
				var plugins map[string]json.RawMessage
				if json.Unmarshal(raw, &plugins) != nil && !falsy(raw, "null") {
					out = append(out, p+": enabledPlugins is not a map")
				}
				var on []string
				for k, v := range plugins {
					if !falsy(v, "false", "null") {
						on = append(on, k)
					}
				}
				sort.Strings(on)
				for _, k := range on {
					out = append(out, p+" enables plugin "+k)
				}
			}
			if raw, has := obj["extraKnownMarketplaces"]; has && !falsy(raw, "null", "{}") {
				out = append(out, p+" sets extraKnownMarketplaces")
			}
		}
		// project agents (.claude/agents, recursively) whose frontmatter
		// declares mcpServers: Claude Code starts an agent's inline servers
		// when the agent runs (Codex pass 6)
		agents := filepath.Join(c, ".claude", "agents")
		_ = filepath.WalkDir(agents, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if !os.IsNotExist(err) {
					out = append(out, p+": unreadable")
				}
				return nil
			}
			// a link inside the agents tree points agents outside it;
			// Claude Code follows it, this walk does not (Codex pass 7)
			if d.Type()&fs.ModeSymlink != 0 {
				out = append(out, p+" is a symlink")
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			raw, _, rerr := readSmall(p)
			if rerr != nil {
				out = append(out, p+": unreadable")
			} else if agentMayDeclareMCP(raw) {
				out = append(out, p+" may declare mcpServers")
			}
			return nil
		})
		// the skills directory is listed, never globbed: a checkout path
		// may hold glob characters
		skills := filepath.Join(c, ".claude", "skills")
		entries, err := os.ReadDir(skills)
		if err != nil && !os.IsNotExist(err) {
			out = append(out, skills+": unreadable")
		}
		// a link anywhere in the skills tree points a plugin, its manifest
		// or a skill outside it (Codex pass 7; round 2, Codex r2 pass 2),
		// and a file with a second name (a hard link) changes under a name
		// the guard does not protect
		_ = filepath.WalkDir(skills, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if !os.IsNotExist(err) && p != skills {
					out = append(out, p+": unreadable")
				}
				return nil
			}
			if p == skills {
				return nil
			}
			if d.Type()&fs.ModeSymlink != 0 {
				out = append(out, p+" is a symlink")
				return nil
			}
			if d.Type().IsRegular() {
				if st, err := os.Lstat(p); err != nil {
					out = append(out, p+": unreadable")
				} else if hardLinked(st) {
					out = append(out, p+" has another name (a hard link)")
				}
			}
			return nil
		})
		for _, d := range entries {
			dir := filepath.Join(skills, d.Name())
			// an ordinary file is no plugin (Codex pass 3); a link was
			// refused above
			if !d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
				continue
			}
			// a skills-directory plugin (Claude Code loads it by its
			// defaultEnabled in a trusted folder) can bring MCP servers, and
			// agents and hooks that start them or run outside the guard:
			// any such plugin refuses, whatever its manifest names (Codex
			// pass 11)
			// a standalone skill (no manifest) is no plugin: its .mcp.json,
			// if any, is inert (round 2, fix 4)
			mf := filepath.Join(dir, ".claude-plugin", "plugin.json")
			if _, err := os.Lstat(mf); err == nil || !os.IsNotExist(err) {
				out = append(out, filepath.Dir(filepath.Dir(mf))+" is a plugin (it can bring MCP servers, agents and hooks)")
			}
		}
	}
	return out
}

// agentMayDeclareMCP reports an agent file whose YAML frontmatter (from a
// first non-blank line "---" to the next "---", or to the end of the file)
// mentions "mcp" in any case, or holds a backslash: YAML can spell the
// mcpServers key in a flow mapping, a quoted or tagged key, an anchor or a
// merge, and in a double-quoted string with escapes, and every such
// spelling needs one or the other. Without a YAML parser this refuses more
// than Claude Code would load, never less (Codex pass 7).
func agentMayDeclareMCP(raw []byte) bool {
	lines := strings.Split(strings.TrimPrefix(string(raw), "\ufeff"), "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	if start == len(lines) || strings.TrimSpace(lines[start]) != "---" {
		return false
	}
	var fm []string
	for _, l := range lines[start+1:] {
		if strings.TrimSpace(l) == "---" {
			break
		}
		fm = append(fm, l)
	}
	text := strings.Join(fm, "\n")
	return strings.Contains(strings.ToLower(text), "mcp") || strings.Contains(text, "\\")
}

// falsy reports a JSON value that is one of the given forms (compact).
func falsy(raw json.RawMessage, forms ...string) bool {
	var buf bytes.Buffer
	if json.Compact(&buf, raw) != nil {
		return false
	}
	return slices.Contains(forms, buf.String())
}

// Ancestors are the directories above dir, nearest first, up to the root.
func Ancestors(dir string) []string {
	var out []string
	for d := filepath.Dir(filepath.Clean(dir)); ; d = filepath.Dir(d) {
		out = append(out, d)
		if filepath.Dir(d) == d {
			return out
		}
	}
}

// sameDirPath reports two spellings of one directory, as written or with
// their symlinks resolved (a commondir may name the common directory
// through an alias; Codex pass 12).
func sameDirPath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// ancestorsBothForms are the ancestors of dir as written and of dir with
// its symlinks resolved (Claude Code walks up from its resolved working
// directory; Codex pass 11), without duplicates.
func ancestorsBothForms(dir string) []string {
	out := Ancestors(dir)
	if real, err := filepath.EvalSymlinks(dir); err == nil && filepath.Clean(real) != filepath.Clean(dir) {
		seen := map[string]bool{}
		for _, d := range out {
			seen[d] = true
		}
		for _, d := range Ancestors(real) {
			if !seen[d] {
				out = append(out, d)
			}
		}
	}
	return out
}

// AncestorsBothForms is ancestorsBothForms for the guard.
func AncestorsBothForms(dir string) []string { return ancestorsBothForms(dir) }

// MainCheckout is the repository root Claude Code reads a session's local
// settings from: dir itself when its .git is a directory, else the canonical
// root of the linked worktree (canonicalRoot: the common git directory's
// parent, or the common directory itself when it is bare), "" when neither
// can be read.
func MainCheckout(dir string) string {
	st, err := os.Stat(filepath.Join(dir, ".git"))
	if err != nil {
		return ""
	}
	if st.IsDir() {
		return dir
	}
	if r := canonicalRoot(dir); r != "" {
		return r
	}
	// a submodule's or a --separate-git-dir checkout's pointer: a git
	// directory of its own (HEAD, no commondir), whose checkout is dir
	// itself (Codex pass 8)
	raw, ok, err := ReadMeta(filepath.Join(dir, ".git"))
	if !ok || err != nil {
		return ""
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, "gitdir:") {
		return ""
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	if _, ok, _ := ReadMeta(filepath.Join(gitdir, "commondir")); ok {
		return ""
	}
	if _, ok, err := ReadMeta(filepath.Join(gitdir, "HEAD")); !ok || err != nil {
		return ""
	}
	return dir
}

// maxConfigRead bounds a read of a file in a seat's tree.
const maxConfigRead = 1 << 20

// readSmall reads a file a seat can plant without blocking or exhausting
// memory: opened non-blocking, it must be a regular file of at most
// maxConfigRead bytes (a FIFO, a device or a larger file is an error, which
// every caller counts against the seat). ok is false when it does not exist.
func readSmall(p string) (data []byte, ok bool, err error) { return readBounded(p, true) }

// ReadMeta reads git metadata in a seat's tree (a .git pointer, a
// commondir) as readSmall does, but a hard link there is git's business,
// not a config file's (Codex pass 7).
func ReadMeta(p string) (data []byte, ok bool, err error) { return readBounded(p, false) }

func readBounded(p string, rejectLinks bool) (data []byte, ok bool, err error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, true, err
	}
	if !st.Mode().IsRegular() {
		return nil, true, fmt.Errorf("%s is not a regular file", p)
	}
	if rejectLinks && hardLinked(st) {
		return nil, true, fmt.Errorf("%s has another name (a hard link)", p)
	}
	data, err = io.ReadAll(io.LimitReader(f, maxConfigRead+1))
	if err == nil && len(data) > maxConfigRead {
		err = fmt.Errorf("%s is larger than %d bytes", p, maxConfigRead)
	}
	return data, true, err
}
