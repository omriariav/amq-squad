package claudeseat

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ExternalIncludesPending: a CLAUDE.md that Claude Code loads at the seat's
// start imports a file outside the seat's worktree, and nothing approves
// it, so 2.1.314 opens ClaudeMdExternalIncludesDialog in the pane (3.0.1
// t6, fifth commit). The launch never seeds the approval. Since brief 31-1
// such a seat starts, and status shows this code as a seat warning (the seat
// may stall at the dialog, a screen line, never a refusal).
const ExternalIncludesPending = "claude_external_includes_pending"

// The 2.1.314 rule, from its strings:
//
//   - lmn: a memory file is external when its type is not User, it was
//     reached through an import (parent) or a link (linkedFrom), and
//     !lB(path), where lB(e) is lu(e, ve()): inside the original cwd, which
//     for a seat is its worktree. p9s opens the dialog when any exists and
//     neither hasClaudeMdExternalIncludesApproved nor
//     hasClaudeMdExternalIncludesWarningShown is set.
//   - MHo: imports are matched in markdown text, never in code blocks or
//     code spans, with HTML comments removed, by
//     (?:^|\s)@((?:[^\s\\]|\\ )+); a #fragment is cut, "\ " is a space; a
//     path counts when it starts with ./, ~/ or / (not "/" alone), or with
//     [a-zA-Z0-9._-] (not @, not only #%^&*()); it resolves against the
//     including file's directory. Imports nest to depth 5 (A2r).
//
// The scan follows that rule over the memory files 2.1.314 loads at start
// for a cwd: CLAUDE.md, CLAUDE.local.md, .claude/CLAUDE.md and
// .claude/rules/**/*.md in the worktree and in each of its ancestors, and
// the managed CLAUDE.md. User memory ($CLAUDE_CONFIG_DIR/CLAUDE.md) is type
// User and never external; the seat's dir has none. It errs toward finding
// an import: indented code is read as text, and every import that resolves
// to an existing regular file counts, whatever its extension.

var includePattern = regexp.MustCompile(`(?:^|\s)@((?:[^\s\\]|\\ )+)`)

var (
	includeOnlyPunct = regexp.MustCompile(`^[#%^&*()]+`)
	includeBareStart = regexp.MustCompile(`^[a-zA-Z0-9._-]`)
	htmlComment      = regexp.MustCompile(`(?s)<!--.*?-->`)
	codeSpan         = regexp.MustCompile("`+[^`]*`+")
)

// includeDepth is 2.1.314's import depth (A2r).
const includeDepth = 5

// managedMemory is the managed CLAUDE.md on macOS (a test seam).
var managedMemory = "/Library/Application Support/ClaudeCode/CLAUDE.md"

// importsOf lists the import paths of one memory file's text, resolved
// against dir.
func importsOf(text, dir string) []string {
	var out []string
	inFence := ""
	for _, line := range strings.Split(htmlComment.ReplaceAllString(text, ""), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if inFence != "" {
			if strings.HasPrefix(trimmed, inFence) {
				inFence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = trimmed[:3]
			continue
		}
		for _, m := range includePattern.FindAllStringSubmatch(codeSpan.ReplaceAllString(line, " "), -1) {
			p := m[1]
			if i := strings.Index(p, "#"); i >= 0 {
				p = p[:i]
			}
			p = strings.ReplaceAll(p, `\ `, " ")
			if p == "" {
				continue
			}
			ok := strings.HasPrefix(p, "./") || strings.HasPrefix(p, "~/") || (strings.HasPrefix(p, "/") && p != "/") ||
				(!strings.HasPrefix(p, "@") && !includeOnlyPunct.MatchString(p) && includeBareStart.MatchString(p))
			if !ok {
				continue
			}
			switch {
			case strings.HasPrefix(p, "~/"):
				home, err := os.UserHomeDir()
				if err != nil {
					continue
				}
				p = filepath.Join(home, p[2:])
			case !filepath.IsAbs(p):
				p = filepath.Join(dir, p)
			}
			out = append(out, filepath.Clean(p))
		}
	}
	return out
}

// within reports whether p is root or below it.
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// startMemoryFiles are the memory files 2.1.314 loads at start for a cwd.
func startMemoryFiles(worktree string) []string {
	var out []string
	for dir := worktree; ; dir = filepath.Dir(dir) {
		for _, name := range []string{"CLAUDE.md", "CLAUDE.local.md", filepath.Join(".claude", "CLAUDE.md")} {
			out = append(out, filepath.Join(dir, name))
		}
		rules := filepath.Join(dir, ".claude", "rules")
		_ = filepath.WalkDir(rules, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
				out = append(out, p)
			}
			return nil
		})
		if filepath.Dir(dir) == dir {
			break
		}
	}
	return append(out, managedMemory)
}

// regular reports whether p resolves to a readable regular file.
func regular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ExternalIncludes lists the memory files Claude Code 2.1.314 would load at
// a seat's start from outside its worktree through an import or a link (the
// files its external-includes dialog names), sorted; none when the worktree
// is "" or unreadable.
func ExternalIncludes(worktree string) []string {
	if worktree == "" {
		return nil
	}
	root := filepath.Clean(worktree)
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	inside := func(p string) bool {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		return within(root, p) || within(filepath.Clean(worktree), p)
	}
	found := map[string]bool{}
	seen := map[string]bool{}
	var visit func(path string, depth int)
	visit = func(path string, depth int) {
		if seen[path] || depth > includeDepth {
			return
		}
		seen[path] = true
		raw, err := os.ReadFile(path)
		if err != nil {
			return
		}
		for _, inc := range importsOf(string(raw), filepath.Dir(path)) {
			if !regular(inc) {
				continue
			}
			if !inside(inc) {
				found[inc] = true
				continue // it loads, and is external; its own imports load with it
			}
			visit(inc, depth+1)
		}
	}
	for _, f := range startMemoryFiles(root) {
		st, err := os.Lstat(f)
		if errors.Is(err, fs.ErrNotExist) || err != nil {
			continue
		}
		if st.Mode()&fs.ModeSymlink != 0 && within(root, f) {
			// A linked memory file in the worktree (linkedFrom) whose
			// target is outside it is external.
			if real, err := filepath.EvalSymlinks(f); err == nil && !inside(real) && regular(real) {
				found[f] = true
			}
		}
		if regular(f) {
			visit(f, 1)
		}
	}
	out := make([]string, 0, len(found))
	for p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
