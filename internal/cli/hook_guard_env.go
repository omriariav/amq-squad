package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// guardEnv answers the guard's questions about the seat's surroundings from
// files only (no git or other subprocess), so a verdict stays well under the
// 50 ms hook budget.
type guardEnv struct {
	id     hookIdentity
	grants hookGrants
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
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		p = filepath.Join(home, rest)
	} else if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	p = filepath.Clean(p)
	var tail []string
	for dir := p; ; dir = filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(append([]string{real}, tail...)...)
		}
		if filepath.Dir(dir) == dir {
			return p
		}
		tail = append([]string{filepath.Base(dir)}, tail...)
	}
}

// protectedPath reports the protected entry covering a written path. The grant
// list is extended by the owner's grants file and trust store and the session
// root's amq-squad extension dir (gate store, answers, ledger): a seat that
// could write those could widen its grant or forge its own gate answers.
func (e *guardEnv) protectedPath(written string) (string, bool) {
	e.readGit()
	base, target := firstOf(e.repo, e.cwd), canonicalGuardPath(written, e.cwd)
	entries := append([]string{hookExtPath(e.id.Root) + "/"}, e.grants.ProtectedPaths...)
	entries = append(entries, positionals([]string{hookOwnerPath("grants.yaml"), hookOwnerPath("authorization-trust.json")})...)
	for _, entry := range entries {
		if matchGuardPath(entry, target, base) {
			return entry, true
		}
	}
	return "", false
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
			for i++; i < len(s) && s[i] != '"'; i++ {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				word.WriteByte(s[i])
			}
			inWord = true
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
