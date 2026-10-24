package seatcommit

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// The harness's git (R2 of the milestone B first pass): the harness never
// reads configuration from the seat's repository. Everything under the
// seat's .git is writable by a same-UID seat (remote.origin.pushurl,
// url.*.insteadOf, filter drivers that git add runs, hooks, replace refs,
// gpg.program, core.sshCommand), so:
//
//   - every harness git call runs with GIT_CONFIG_GLOBAL=/dev/null,
//     GIT_CONFIG_NOSYSTEM=1, GIT_NO_REPLACE_OBJECTS=1, GIT_ATTR_NOSYSTEM=1,
//     core.attributesFile and core.hooksPath pointed at /dev/null, and no
//     credential or GIT_* redirection from the caller;
//   - every object-level call (the snapshot, the diffs, commit-tree, the
//     push, CleanSeat's checkout) runs in a shadow: a git directory the
//     harness creates with its own config, with GIT_OBJECT_DIRECTORY at the
//     seat repository's object store and GIT_WORK_TREE at the seat worktree,
//     so the seat repository's config, info/attributes and hooks are never
//     read;
//   - the snapshot is hash-object --no-filters plus update-index into a
//     temporary index, never git add;
//   - the push goes to the URL the launch recorded in the commit ledger from
//     the owner's grant, passed explicitly, never a remote name;
//   - the one call in the seat repository itself, update-ref of the seat
//     branch, also unsets every filter.* driver the repository defines.

// git runs the git CLI for the harness with the hardened environment.
type git struct {
	bin string
	dir string   // the working directory of the call
	env []string // harnessEnv plus the call's context (GIT_DIR, GIT_INDEX_FILE...)
	pre []string // extra -c settings (the seat repository's filter drivers unset)
}

// safeConfig is prepended to every git call.
func safeConfig() []string {
	return []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.attributesFile=" + os.DevNull, "-c", "core.fsmonitor=false",
		"-c", "core.untrackedCache=false", "-c", "advice.detachedHead=false", "-c", "commit.gpgSign=false", "-c", "protocol.ext.allow=never"}
}

// keptGitVars are the GIT_* names the harness keeps from its environment:
// the transport's SSH plumbing for the push (the owner's, never the seat's).
var keptGitVars = map[string]bool{"GIT_SSH": true, "GIT_SSH_COMMAND": true, "GIT_SSH_VARIANT": true, "GIT_ASKPASS": true}

// hardenedVars is the R2 set every harness git call carries.
var hardenedVars = []string{"GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_ATTR_NOSYSTEM=1"}

// harnessEnv is the caller's environment minus GIT_* redirection and every
// credential-named variable (a harness subprocess gets no credential), plus
// a fixed locale, no terminal prompt and the R2 set.
func harnessEnv(parent []string) []string {
	out := make([]string, 0, len(parent)+8)
	for _, kv := range redact.StripEnv(parent) {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "GIT_") && !keptGitVars[name] {
			continue
		}
		if name == "LC_ALL" || name == "LANG" {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "LC_ALL=C", "LANG=C", "GIT_TERMINAL_PROMPT=0")
	return append(out, hardenedVars...)
}

// gitError is a failed git call with its stderr.
type gitError struct {
	args   []string
	code   int
	stderr string
}

func (e *gitError) Error() string {
	return fmt.Sprintf("git %s: exit %d: %s", strings.Join(e.args, " "), e.code, strings.TrimSpace(e.stderr))
}

// run executes git with the safe config; extra environment entries are
// appended (the identity of commit-tree).
func (g git) run(stdin []byte, extra []string, args ...string) ([]byte, error) {
	argv := append(append(safeConfig(), g.pre...), args...)
	cmd := exec.Command(g.bin, argv...)
	cmd.Dir = g.dir
	cmd.Env = append(append([]string(nil), g.env...), extra...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		code := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return stdout.Bytes(), &gitError{args: args, code: code, stderr: stderr.String() + errText(err, code)}
	}
	return stdout.Bytes(), nil
}

func errText(err error, code int) string {
	if code >= 0 {
		return ""
	}
	return err.Error()
}

// line runs git and returns its trimmed stdout.
func (g git) line(args ...string) (string, error) {
	out, err := g.run(nil, nil, args...)
	return strings.TrimSpace(string(out)), err
}

// with returns g with more environment entries.
func (g git) with(extra ...string) git {
	g.env = append(append([]string(nil), g.env...), extra...)
	return g
}

// repoPaths are a seat worktree's git locations, read from the files git
// writes (.git, gitdir, commondir, HEAD), never from git's view of the
// seat repository's config (core.worktree there would move it).
type repoPaths struct {
	Worktree string // the worktree root
	GitDir   string // its git dir (<common>/worktrees/<name> for a linked worktree)
	Common   string // the repository's common dir
	Index    string // the worktree's index
	Objects  string // the repository's object store
	HeadRef  string // what HEAD names ("refs/heads/<branch>"), "" when detached
}

const maxGitFile = 4096

func readSmall(p string) (string, error) {
	st, err := os.Lstat(p)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() || st.Size() > maxGitFile {
		return "", fmt.Errorf("%s is not a small regular file", p)
	}
	raw, err := os.ReadFile(p)
	return strings.TrimSpace(string(raw)), err
}

// worktreePaths resolves a checkout's git paths from its .git (a directory
// for a main checkout, a "gitdir:" file for a linked worktree whose
// back-pointer must name this worktree).
func worktreePaths(wt string) (repoPaths, error) {
	rp := repoPaths{Worktree: wt}
	dotgit := filepath.Join(wt, ".git")
	st, err := os.Lstat(dotgit)
	switch {
	case err != nil:
		return rp, fmt.Errorf("%s is not a git checkout: %v", wt, err)
	case st.IsDir():
		rp.GitDir = dotgit
	case st.Mode().IsRegular():
		line, err := readSmall(dotgit)
		if err != nil {
			return rp, err
		}
		gd, ok := strings.CutPrefix(line, "gitdir: ")
		if !ok || gd == "" {
			return rp, fmt.Errorf("%s is not a gitdir file", dotgit)
		}
		if !filepath.IsAbs(gd) {
			gd = filepath.Join(wt, gd)
		}
		rp.GitDir = filepath.Clean(gd)
		back, err := readSmall(filepath.Join(rp.GitDir, "gitdir"))
		if err != nil || evalOr(filepath.Clean(back)) != evalOr(dotgit) {
			return rp, fmt.Errorf("the git dir %s does not belong to the worktree %s", rp.GitDir, wt)
		}
	default:
		return rp, fmt.Errorf("%s is neither a directory nor a gitdir file", dotgit)
	}
	rp.Common = rp.GitDir
	if c, err := readSmall(filepath.Join(rp.GitDir, "commondir")); err == nil && c != "" {
		if !filepath.IsAbs(c) {
			c = filepath.Join(rp.GitDir, c)
		}
		rp.Common = filepath.Clean(c)
	}
	rp.Index, rp.Objects = filepath.Join(rp.GitDir, "index"), filepath.Join(rp.Common, "objects")
	if st, err := os.Stat(rp.Objects); err != nil || !st.IsDir() {
		return rp, fmt.Errorf("the repository of %s has no object store at %s", wt, rp.Objects)
	}
	head, err := readSmall(filepath.Join(rp.GitDir, "HEAD"))
	if err != nil {
		return rp, err
	}
	rp.HeadRef, _ = strings.CutPrefix(head, "ref: ")
	if rp.HeadRef == head {
		rp.HeadRef = "" // detached
	}
	return rp, nil
}

// shadow is the harness's own git directory for one step: created with the
// hardened environment (so its config is git's defaults), removed at close.
type shadow struct {
	dir string
	rp  repoPaths
	g   git // GIT_DIR, GIT_OBJECT_DIRECTORY and GIT_WORK_TREE set; cwd the worktree
}

func newShadow(bin string, env []string, rp repoPaths) (*shadow, error) {
	dir, err := os.MkdirTemp("", "amq-squad-shadow-*")
	if err != nil {
		return nil, err
	}
	init := git{bin: bin, dir: dir, env: env}
	if _, err := init.run(nil, nil, "init", "-q", "--template=", dir); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	s := &shadow{dir: dir, rp: rp}
	s.g = git{bin: bin, dir: rp.Worktree, env: env}.with("GIT_DIR="+filepath.Join(dir, ".git"), "GIT_OBJECT_DIRECTORY="+rp.Objects, "GIT_WORK_TREE="+rp.Worktree)
	return s, nil
}

func (s *shadow) close() {
	if s != nil {
		os.RemoveAll(s.dir)
	}
}

// index is the shadow's git with an index file.
func (s *shadow) index(path string) git { return s.g.with("GIT_INDEX_FILE=" + path) }

// tempIndex is a fresh index path inside the shadow.
func (s *shadow) tempIndex() string { return filepath.Join(s.dir, "index.tmp") }

// seatRepo is a git for the seat repository's own refs (update-ref): its
// filter drivers are listed and each unset by -c, with the R2 environment.
func seatRepo(bin string, env []string, rp repoPaths) git {
	g := git{bin: bin, dir: rp.Common, env: env}.with("GIT_DIR=" + rp.Common)
	out, _ := g.run(nil, nil, "config", "--name-only", "--get-regexp", `^filter\.`)
	seen := map[string]bool{}
	for _, key := range strings.Fields(string(out)) {
		i := strings.LastIndexByte(key, '.')
		if i <= len("filter.") || seen[key[:i]] {
			continue
		}
		seen[key[:i]] = true
		for _, k := range []string{"clean", "smudge", "process"} {
			g.pre = append(g.pre, "-c", key[:i]+"."+k+"=")
		}
		g.pre = append(g.pre, "-c", key[:i]+".required=false")
	}
	return g
}

// snapshot builds the tree of the worktree as it is now in a temporary
// index started from parent (the seat's own index is not trusted): every
// tracked path and every untracked one .gitignore does not exclude, each
// regular file hashed with hash-object --no-filters (no clean filter, no
// attributes), each symbolic link as its target text, each nested
// repository as a gitlink at its HEAD; a missing path, or one under a
// symbolic link or a non-directory, is removed.
func snapshot(s *shadow, parent string) (string, error) {
	idx := s.tempIndex()
	os.Remove(idx)
	defer os.Remove(idx)
	g := s.index(idx)
	if _, err := g.run(nil, nil, "read-tree", parent); err != nil {
		return "", err
	}
	out, err := g.run(nil, nil, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	seen := map[string]bool{}
	var files []string // regular files to hash, in order
	var fileModes []string
	var fileStats []os.FileInfo
	var info bytes.Buffer // update-index --index-info records
	for _, p := range strings.Split(string(out), "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		nested := strings.HasSuffix(p, "/")
		p = strings.TrimSuffix(p, "/")
		abs := filepath.Join(s.rp.Worktree, filepath.FromSlash(p))
		if !underRealDirs(s.rp.Worktree, p) {
			fmt.Fprintf(&info, "0 %s\t%s\x00", zeroID, p)
			continue
		}
		st, err := os.Lstat(abs)
		switch {
		case os.IsNotExist(err):
			fmt.Fprintf(&info, "0 %s\t%s\x00", zeroID, p)
		case err != nil:
			return "", err
		case st.Mode().IsRegular():
			mode := "100644"
			if st.Mode().Perm()&0o100 != 0 {
				mode = "100755"
			}
			files, fileModes, fileStats = append(files, p), append(fileModes, mode), append(fileStats, st)
		case st.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(abs)
			if err != nil {
				return "", err
			}
			sum, err := g.run([]byte(target), nil, "hash-object", "-w", "--no-filters", "--stdin")
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&info, "120000 %s\t%s\x00", strings.TrimSpace(string(sum)), p)
		case st.IsDir():
			// A nested repository or a submodule: a gitlink at its HEAD.
			if _, err := os.Lstat(filepath.Join(abs, ".git")); err != nil && !nested {
				continue // an empty directory left by a deletion
			}
			head, err := git{bin: g.bin, dir: abs, env: harnessBase(g.env)}.line("rev-parse", "--verify", "-q", "HEAD")
			if err != nil || len(head) < 40 {
				head = nestedPlaceholder
			}
			fmt.Fprintf(&info, "160000 %s\t%s\x00", head, p)
		}
	}
	// N1 of the second pass: a filename is never fed through quoted stdin
	// (hash-object --stdin-paths unquotes a line that starts with a double
	// quote, so a file named "\057etc\057hosts" read /etc/hosts); each path
	// goes as an argument after "--", relative to the worktree, in batches.
	// A path that stops being the same regular file between the listing and
	// the hash defers the step.
	for start := 0; start < len(files); start += hashBatch {
		batch := files[start:min(start+hashBatch, len(files))]
		sums, err := g.run(nil, nil, append([]string{"hash-object", "-w", "--no-filters", "--"}, batch...)...)
		if err != nil {
			return "", err
		}
		ids := strings.Fields(string(sums))
		if len(ids) != len(batch) {
			return "", fmt.Errorf("hash-object returned %d ids for %d files", len(ids), len(batch))
		}
		for i, p := range batch {
			st, err := os.Lstat(filepath.Join(s.rp.Worktree, filepath.FromSlash(p)))
			if err != nil || !st.Mode().IsRegular() || !os.SameFile(st, fileStats[start+i]) || !underRealDirs(s.rp.Worktree, p) {
				return "", fmt.Errorf("%s changed while the snapshot read it", p)
			}
			fmt.Fprintf(&info, "%s %s\t%s\x00", fileModes[start+i], ids[i], p)
		}
	}
	if info.Len() > 0 {
		if _, err := g.run(info.Bytes(), nil, "update-index", "-z", "--replace", "--index-info"); err != nil {
			return "", err
		}
	}
	out, err = g.run(nil, nil, "write-tree")
	return strings.TrimSpace(string(out)), err
}

// hashBatch is how many paths one hash-object call takes as arguments.
const hashBatch = 256

// zeroID removes an index entry in --index-info; nestedPlaceholder is the
// gitlink of a nested repository whose HEAD cannot be read (a gitlink is
// refused as nested_repository whatever it names).
const (
	zeroID            = "0000000000000000000000000000000000000000"
	nestedPlaceholder = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
)

// harnessBase is an environment without the shadow's context (GIT_DIR,
// GIT_WORK_TREE, GIT_INDEX_FILE, GIT_OBJECT_DIRECTORY): a nested
// repository's HEAD is read in that repository.
func harnessBase(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY":
			continue
		}
		out = append(out, kv)
	}
	return out
}

// underRealDirs reports whether every parent of a worktree-relative path is
// a real directory (not a symbolic link): git itself refuses a path beyond
// a symbolic link, so such a path reads as removed.
func underRealDirs(root, rel string) bool {
	parts := strings.Split(rel, "/")
	cur := root
	for _, part := range parts[:len(parts)-1] {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil || !st.IsDir() {
			return false
		}
	}
	return true
}

// numstat is one changed path of a diff with its line counts; Binary is set
// when git counts no lines.
type numstat struct {
	Path           string
	Added, Deleted int
	Binary         bool
}

// parseNumstat reads `diff-tree --numstat -z --no-renames` output: records
// "added\tdeleted\tpath\0".
func parseNumstat(out []byte) ([]numstat, error) {
	var list []numstat
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		parts := strings.SplitN(rec, "\t", 3)
		if len(parts) != 3 || parts[2] == "" {
			return nil, fmt.Errorf("unexpected numstat record %q", rec)
		}
		n := numstat{Path: parts[2]}
		if parts[0] == "-" && parts[1] == "-" {
			n.Binary = true
		} else {
			a, errA := strconv.Atoi(parts[0])
			d, errD := strconv.Atoi(parts[1])
			if errA != nil || errD != nil {
				return nil, fmt.Errorf("unexpected numstat record %q", rec)
			}
			n.Added, n.Deleted = a, d
		}
		list = append(list, n)
	}
	return list, nil
}

// rawEntry is one record of `diff-tree --raw -z --no-renames`.
type rawEntry struct {
	OldMode, NewMode, Status, Path string
}

// parseRaw reads ":oldmode newmode oldsha newsha status\0path\0" records.
func parseRaw(out []byte) ([]rawEntry, error) {
	fields := strings.Split(string(out), "\x00")
	var list []rawEntry
	for i := 0; i < len(fields); i++ {
		head := fields[i]
		if head == "" {
			continue
		}
		if !strings.HasPrefix(head, ":") || i+1 >= len(fields) {
			return nil, fmt.Errorf("unexpected raw diff record %q", head)
		}
		meta := strings.Fields(strings.TrimPrefix(head, ":"))
		if len(meta) != 5 {
			return nil, fmt.Errorf("unexpected raw diff record %q", head)
		}
		i++
		list = append(list, rawEntry{OldMode: meta[0], NewMode: meta[1], Status: meta[4], Path: fields[i]})
	}
	return list, nil
}

// AddWorktree creates a seat worktree with the hardened git (R2): the
// repository's own config is shared with every seat worktree, so a seat
// may have planted a filter driver or a hook there; the worktree is added
// without a checkout (hooks off, every filter driver unset), then its files
// are written from the branch tip in the harness's shadow, where no filter
// or attribute of the repository applies. The branch is created from base
// when it does not exist.
func AddWorktree(gitBin, projectRoot, worktree, branch, base string, env []string) error {
	if gitBin == "" {
		gitBin = "git"
	}
	if env == nil {
		env = os.Environ()
	}
	henv := harnessEnv(env)
	pp, err := worktreePaths(projectRoot)
	if err != nil {
		return err
	}
	repo := seatRepo(gitBin, henv, pp)
	args := []string{"worktree", "add", "--no-checkout"}
	if _, err := repo.line("rev-parse", "--verify", "-q", "refs/heads/"+branch); err == nil {
		args = append(args, worktree, branch)
	} else {
		args = append(args, "-b", branch, worktree, base)
	}
	if _, err := repo.run(nil, nil, args...); err != nil {
		return err
	}
	tip, err := repo.line("rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return err
	}
	rp, err := worktreePaths(worktree)
	if err != nil {
		return err
	}
	sh, err := newShadow(gitBin, henv, rp)
	if err != nil {
		return err
	}
	defer sh.close()
	_, err = sh.index(rp.Index).run(nil, nil, "read-tree", "-u", "--reset", tip)
	return err
}
