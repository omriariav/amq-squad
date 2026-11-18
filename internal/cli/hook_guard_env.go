package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"

	"golang.org/x/text/unicode/norm"
)

// guardEnv answers the guard's questions about the seat's surroundings from
// files only (no git or other subprocess), so a verdict stays well under the
// 50 ms hook budget.
type guardEnv struct {
	id     hookIdentity
	grants grants.Resolved
	cwd    string

	gitRead                bool
	repo, current, defBase string
}

// readGit finds the repository from cwd and reads HEAD and origin/HEAD.
func (e *guardEnv) readGit() {
	if e.gitRead {
		return
	}
	e.gitRead = true
	read := func(dir, name string) string { // a git file's first line, "" if absent
		// bounded and non-blocking: a seat can point its .git at files it
		// made (a FIFO would hang the hook into its fail-open watchdog;
		// 3.3.1 item 5, Codex pass 10)
		raw, _, _ := claudeseat.ReadMeta(filepath.Join(dir, name))
		return strings.TrimSpace(string(raw))
	}
	rel := func(dir, p string) string {
		if p != "" && !filepath.IsAbs(p) {
			return filepath.Join(dir, p)
		}
		return p
	}
	for dir := e.cwd; filepath.IsAbs(dir); dir = filepath.Dir(dir) {
		if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			e.repo = dir
			gitDir := filepath.Join(dir, ".git")
			if !st.IsDir() { // a linked worktree: .git names the gitdir
				gitDir = rel(dir, strings.TrimPrefix(read(dir, ".git"), "gitdir: "))
			}
			e.current = strings.TrimPrefix(read(gitDir, "HEAD"), "ref: refs/heads/")
			common := firstOf(rel(gitDir, read(gitDir, "commondir")), gitDir)
			e.defBase = strings.TrimPrefix(read(common, "refs/remotes/origin/HEAD"), "ref: refs/remotes/origin/")
			return
		}
		if filepath.Dir(dir) == dir {
			return
		}
	}
}

func (e *guardEnv) currentBranch() string { e.readGit(); return e.current }

func (e *guardEnv) defaultBranch() string {
	e.readGit()
	return firstOf(e.grants.DefaultBranch, firstOf(e.defBase, "main"))
}

// ownBranch is the seat's branch: AMQ_SQUAD_BRANCH from the launch, else the
// checked-out branch unless that is the default branch.
func (e *guardEnv) ownBranch() string {
	env := strings.TrimSpace(os.Getenv("AMQ_SQUAD_BRANCH"))
	if b := e.currentBranch(); b != e.defaultBranch() {
		return firstOf(env, b)
	}
	return env
}

// branchOf names the branch a push refspec destination updates.
func (e *guardEnv) branchOf(dst string) string {
	if dst == "" || dst == "HEAD" {
		return e.currentBranch()
	}
	return strings.TrimPrefix(dst, "refs/heads/")
}

func (e *guardEnv) seatCount() int {
	entries, _ := os.ReadDir(filepath.Join(e.id.Root, "agents"))
	n := 0
	for _, d := range entries {
		if d.IsDir() && !strings.HasPrefix(d.Name(), ".") {
			n++
		}
	}
	return n
}

// canonical makes p absolute (against base), expands ~/ and resolves the
// symlinks of its longest existing prefix, so /var and /private/var or a
// symlinked parent compare equal.
func canonicalGuardPath(p, base string) string {
	sep := string(filepath.Separator)
	// Join by hand: filepath.Join would Clean, collapsing ".." before the
	// symlinks are resolved.
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		p = home + sep + rest
	} else if !filepath.IsAbs(p) {
		p = base + sep + p
	}
	vol := filepath.VolumeName(p)
	cur, exists := vol+sep, true
	// Walk component by component, resolving each existing prefix before the
	// next "..", as the kernel does: /repo/link/../secret with link ->
	// /vault/sub is /vault/secret, not /repo/secret.
	for _, c := range strings.Split(filepath.ToSlash(p[len(vol):]), "/") {
		switch c {
		case "", ".":
			continue
		case "..":
			cur = filepath.Dir(cur)
			_, err := os.Lstat(cur)
			exists = err == nil
			continue
		}
		next := filepath.Join(cur, c)
		if exists {
			if real, err := filepath.EvalSymlinks(next); err == nil {
				next = real
			} else {
				exists = false
			}
		}
		cur = next
	}
	return cur
}

// protectedPath reports the protected entry covering a written path: the
// grant's entries (the implicit R37 set of internal/grants, then the owner's
// and squad.yaml's lists), the owner's grants file and trust store, and the
// session root's amq-squad extension dir (gate store, answers, ledger): a
// seat that could write those could widen its grant or forge its own gate
// answers. A "managed:" entry covers only a file that holds an amq-squad
// managed block, and only a write that is not known to leave the block
// unchanged (blockSafe, set for Write and Edit calls that keep it intact).
func (e *guardEnv) protectedPath(written string, blockSafe bool) (string, bool) {
	e.readGit()
	base, target := firstOf(e.repo, e.cwd), canonicalGuardPath(written, e.cwd)
	entries := append([]string{hookExtPath(e.id.Root) + "/"}, e.grants.ProtectedEntries()...)
	entries = append(entries, positionals([]string{hookOwnerPath("grants.yaml"), hookOwnerPath("authorization-trust.json"), hookOwnerPath("gate-binding.json"), hookOwnerPath("issuer-key.pem"), os.Getenv("AMQ_SQUAD_RULES_PATH")})...)
	if b := hookOwnerPath("buzz"); b != "" { // the Buzz share (4-5, 5-amq-B): body key, grants, the attach inbox
		entries = append(entries, b+"/")
	}
	// The seat reads a rules snapshot (AMQ_SQUAD_RULES_PATH); the recorded
	// rules source stays protected too (fix 3), as the core staged it for the
	// launch, never as a record in the seat's own agent directory names it
	// (C5a).
	staged, serr := launch.ReadStaged(e.id.Root, e.id.Handle)
	if serr == nil && staged.RulesPath != "" {
		entries = append(entries, staged.RulesPath)
	}
	pinned := ""
	if ex, err := launch.ReadExec(e.id.Root, e.id.Handle); err == nil && serr == nil && staged.LaunchID != "" && ex.LaunchID == staged.LaunchID {
		pinned = ex.MainCheckout
	}
	if entry, ok := e.sessionConfigFile(target, staged.Worktree, pinned); ok {
		return entry, true
	}
	// C5a (Codex pass 3, finding 1): every session's extensions/amq-squad/
	// under the AMQ base root is the core's, not only this seat's session
	// root's: another session's staged records decide its routing and roles.
	if entry, ok := underAnySessionExt(firstOf(staged.BaseRoot, firstOf(os.Getenv("AM_BASE_ROOT"), filepath.Dir(e.id.Root))), target, e.cwd); ok {
		return entry, true
	}
	for _, entry := range entries {
		if name, ok := strings.CutPrefix(entry, grants.ManagedPrefix); ok {
			if filepath.Base(target) == name && !blockSafe && hasManagedBlock(target) {
				return entry, true
			}
			continue
		}
		if matchGuardPath(entry, target, base) {
			return entry, true
		}
	}
	return "", false
}

func hasManagedBlock(path string) bool {
	raw, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(raw), rules.BeginMarker)
}

// managedBlockKept reports whether a Write, Edit or MultiEdit call leaves the
// managed block of its file unchanged. The call is applied to the current
// text in memory (Edit replaces the first match, or every match with
// replace_all) and the result must keep the same begin and end marker counts
// and the same block text. An edit whose search text is absent, empty or
// unparseable counts as changing the block.
func managedBlockKept(tool, path, cwd string, args map[string]string) bool {
	raw, err := os.ReadFile(canonicalGuardPath(path, cwd))
	if err != nil {
		return true // no file, no block to change
	}
	before := string(raw)
	if !strings.Contains(before, rules.BeginMarker) && !strings.Contains(before, rules.EndMarker) {
		return true
	}
	type edit struct {
		Old        string `json:"old_string"`
		New        string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	var edits []edit
	after := before
	switch tool {
	case "Write", "write":
		after = args["content"]
	case "Edit", "edit":
		edits = []edit{{args["old_string"], args["new_string"], args["replace_all"] == "true"}}
	case "MultiEdit":
		if json.Unmarshal([]byte(args["edits"]), &edits) != nil {
			return false
		}
	default:
		return false
	}
	for _, ed := range edits {
		if ed.Old == "" || !strings.Contains(after, ed.Old) {
			return false
		}
		if ed.ReplaceAll {
			after = strings.ReplaceAll(after, ed.Old, ed.New)
		} else {
			after = strings.Replace(after, ed.Old, ed.New, 1)
		}
	}
	return slices.Equal(seatcommit.ManagedShape(after), seatcommit.ManagedShape(before))
}

var guardCredentialPaths = []string{"~/.ssh/", "~/.aws/", "~/.netrc", "~/.git-credentials", "~/.config/gh/", "~/.docker/config.json", "~/.npmrc", "~/.gnupg/", "~/.kube/config"}

func (e *guardEnv) credentialPath(written string) (string, bool) {
	target := canonicalGuardPath(written, e.cwd)
	for _, entry := range guardCredentialPaths {
		if matchGuardPath(entry, target, e.cwd) {
			return target, true
		}
	}
	return "", false
}

func matchGuardPath(entry, target, base string) bool {
	p := canonicalGuardPath(entry, base)
	if strings.ContainsAny(entry, "*?[") {
		ok, _ := filepath.Match(p, target)
		return ok
	}
	st, err := os.Stat(p)
	dir := strings.HasSuffix(entry, "/") || (err == nil && st.IsDir())
	return target == p || (dir && strings.HasPrefix(target, p+string(filepath.Separator)))
}

type shellSegment struct{ words, writes []string }

// splitShell splits a command line into simple commands at ; & | newlines,
// parentheses, backquotes and $( ), honoring quotes and backslashes, and
// collects > and >> targets as writes. It is a guard heuristic, not a shell:
// the guard is UX and evidence, enforcement stays in the core (R32).
func splitShell(s string) []shellSegment {
	var segs []shellSegment
	var cur shellSegment
	var word strings.Builder
	inWord, redirect := false, false
	flush := func() {
		if !inWord {
			return
		}
		if redirect {
			cur.writes = append(cur.writes, word.String())
		} else {
			cur.words = append(cur.words, word.String())
		}
		word.Reset()
		inWord, redirect = false, false
	}
	end := func() {
		flush()
		if len(cur.words)+len(cur.writes) > 0 {
			segs = append(segs, cur)
		}
		cur, redirect = shellSegment{}, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'':
			j := i + 1
			for ; j < len(s) && s[j] != '\''; j++ {
			}
			word.WriteString(s[i+1 : j])
			inWord, i = true, j
		case c == '"':
			inWord = true
			for i++; i < len(s) && s[i] != '"'; i++ {
				// $( and backquotes run a command inside double quotes too:
				// break the segment there and parse the rest unquoted.
				if s[i] == '`' || (s[i] == '$' && i+1 < len(s) && s[i+1] == '(') {
					end()
					if s[i] == '$' {
						i++
					}
					break
				}
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				word.WriteByte(s[i])
			}
		case c == '\\' && i+1 < len(s):
			i++
			word.WriteByte(s[i])
			inWord = true
		case c == ' ' || c == '\t':
			flush()
		case c == '$' && i+1 < len(s) && s[i+1] == '(':
			end()
			i++
		case strings.IndexByte(";&|\n()`", c) >= 0:
			end()
		case c == '>':
			if w := word.String(); inWord && strings.Trim(w, "0123456789") == "" {
				word.Reset()
				inWord = false
			}
			flush()
			for i+1 < len(s) && (s[i+1] == '>' || s[i+1] == '|') {
				i++
			}
			if i+1 < len(s) && s[i+1] == '&' {
				for i++; i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'; i++ {
				}
				continue
			}
			redirect = true
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	end()
	return segs
}

// underAnySessionExt reports that target lies in an extensions/amq-squad/
// directory at any level under the AMQ base root (a session root, a
// profile's session root, or the base root itself).
func underAnySessionExt(baseRoot, target, cwd string) (string, bool) {
	if baseRoot == "" {
		return "", false
	}
	b := canonicalGuardPath(baseRoot, cwd)
	rel, err := filepath.Rel(b, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "extensions" && parts[i+1] == "amq-squad" {
			return filepath.Join(append([]string{b}, parts[:i+2]...)...) + "/", true
		}
	}
	return "", false
}

// sessionConfigFiles are the files Claude Code loads a seat session's
// configuration from, which it reloads mid-session (3.3.1 item 5, D3), and
// the directories whose move would replace them (the .claude directories,
// and a main checkout root that is not the seat's own tree):
// .claude/settings.json, .claude/settings.local.json and .mcp.json in the
// seat's own worktree (its staged record's, whatever the hook's cwd), in
// the repository the hook's cwd is in, and in each one's repository root
// (claudeseat.MainCheckout: in a linked worktree Claude Code reads the local
// settings file there; the bare common directory for a bare repository).
func (e *guardEnv) sessionConfigFiles(worktree, pinned string) []string {
	var checkouts []string
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	// a root that is not the seat's own tree, and its ancestors: a move of
	// any of them replaces every config file under it (Codex passes 2 and 6)
	addRoot := func(m string) {
		// the root and its ancestors, as written and symlink-resolved
		// (Codex pass 12)
		add(filepath.Clean(m))
		if real, err := filepath.EvalSymlinks(m); err == nil {
			add(filepath.Clean(real))
		}
		for _, d := range claudeseat.AncestorsBothForms(m) {
			add(d)
		}
	}
	// the root seat-exec pinned at the launch (the exec record of the
	// current launch), whatever the seat's .git pointer says now
	if pinned != "" {
		checkouts = append(checkouts, pinned)
		if filepath.Clean(pinned) != filepath.Clean(worktree) {
			addRoot(pinned)
		}
	}
	for _, c := range []string{worktree, e.repo} {
		if c == "" {
			continue
		}
		checkouts = append(checkouts, c)
		if m := claudeseat.MainCheckout(c); m != "" {
			checkouts = append(checkouts, m)
			// a main checkout root that is not the seat's own tree: a move
			// of it replaces every config file under it (Codex pass 2)
			if filepath.Clean(m) != filepath.Clean(c) {
				addRoot(m)
			}
		}
	}
	// the .mcp.json of every directory above the seat's tree, which Claude
	// Code merges too (Codex pass 9)
	if worktree != "" {
		for _, d := range claudeseat.AncestorsBothForms(worktree) {
			add(filepath.Join(d, ".mcp.json"))
		}
	}
	for _, c := range checkouts {
		// the .claude directory itself: a move or removal of it replaces
		// the settings files under it (Codex pass 2)
		add(filepath.Join(c, ".claude"))
		for _, rel := range []string{filepath.Join(".claude", "settings.json"), filepath.Join(".claude", "settings.local.json"), ".mcp.json"} {
			add(filepath.Join(c, rel))
		}
	}
	return out
}

// sessionConfigFile reports target as one of sessionConfigFiles, compared
// as canonical paths (a symlinked file, or a write through its link, is the
// file it names) and without regard to case (a case-insensitive filesystem
// writes .MCP.JSON to .mcp.json), never as a glob (a checkout path may hold
// glob characters).
func (e *guardEnv) sessionConfigFile(target, worktree, pinned string) (string, bool) {
	for _, p := range e.sessionConfigFiles(worktree, pinned) {
		forms := append([]string{filepath.Clean(p), canonicalGuardPath(p, "/")}, linkTargets(p)...)
		for _, form := range forms {
			// the same file under another Unicode normalization or case
			// (macOS filesystems ignore both; Codex passes 1 and 8)
			if strings.EqualFold(norm.NFC.String(form), norm.NFC.String(target)) {
				return p, true
			}
		}
	}
	return "", false
}

// linkTargets are the paths a symlinked config file names, hop by hop (at
// most 8), each canonical as far as it exists: a link whose target does not
// exist yet still protects the file a write there would create (Codex pass
// 3 on 3.3.1 item 5).
func linkTargets(p string) []string {
	var out []string
	for i := 0; i < 8; i++ {
		st, err := os.Lstat(p)
		if err != nil || st.Mode()&os.ModeSymlink == 0 {
			return out
		}
		t, err := os.Readlink(p)
		if err != nil {
			return out
		}
		if !filepath.IsAbs(t) {
			t = filepath.Join(filepath.Dir(p), t)
		}
		out = append(out, filepath.Clean(t), canonicalGuardPath(t, "/"))
		p = t
	}
	return out
}
