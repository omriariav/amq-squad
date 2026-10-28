package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
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
		raw, _ := os.ReadFile(filepath.Join(dir, name))
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
	// 2b-2: the preflight copies of the owner's global agent config (doctor
	// diffs against them; the config itself is in grants.Implicit).
	if preflight := hookOwnerPath("preflight"); preflight != "" {
		entries = append(entries, preflight+"/")
	}
	if b := hookOwnerPath("buzz"); b != "" { // the Buzz share (4-5, 5-amq-B): body key, grants, the attach inbox
		entries = append(entries, b+"/")
	}
	// The seat reads a rules snapshot (AMQ_SQUAD_RULES_PATH); the recorded
	// rules source stays protected too (fix 3).
	if rec, err := launch.Read(e.id.AgentDir); err == nil && rec.RulesPath != "" {
		entries = append(entries, rec.RulesPath)
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
