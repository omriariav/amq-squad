package task

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// File locks (plan 4.5): two active tasks may not hold overlapping
// declared_files. A glob is repo-relative with path.Match syntax per
// segment; a trailing "/" or "/**" covers a tree; "**" as a segment matches
// any number of segments.

// CleanDeclaredFiles validates and normalizes a declared file set: slash
// separated, relative, no "..", valid patterns, deduplicated and sorted.
func CleanDeclaredFiles(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		p := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
		if p == "" {
			continue
		}
		tree := strings.HasSuffix(p, "/")
		p = strings.TrimPrefix(path.Clean(p), "./")
		if strings.HasPrefix(p, "/") || p == ".." || strings.HasPrefix(p, "../") || strings.Contains(p, "/../") {
			return nil, fmt.Errorf("declared file %q must be relative to the repository root", raw)
		}
		if p == "." {
			p = "**"
		} else if tree {
			p += "/"
		}
		if _, err := path.Match(strings.TrimSuffix(p, "/"), ""); err != nil {
			return nil, fmt.Errorf("declared file %q: %v", raw, err)
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// FilesOverlap reports whether any glob of a may match a path that a glob of
// b may match. It is conservative: it may report a false overlap, never a
// missed one.
func FilesOverlap(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if globsOverlap(x, y) {
				return true
			}
		}
	}
	return false
}

// splitGlob splits a glob into segments; tree is true when it covers
// everything below its last segment.
func splitGlob(g string) (segs []string, tree bool) {
	switch {
	case strings.HasSuffix(g, "/**"):
		g, tree = strings.TrimSuffix(g, "/**"), true
	case strings.HasSuffix(g, "/"):
		g, tree = strings.TrimSuffix(g, "/"), true
	}
	return strings.Split(g, "/"), tree
}

func globsOverlap(x, y string) bool {
	xs, xt := splitGlob(x)
	ys, yt := splitGlob(y)
	for i := 0; i < len(xs) && i < len(ys); i++ {
		a, b := xs[i], ys[i]
		if a == "**" || b == "**" {
			return true
		}
		if !segmentsOverlap(a, b) {
			return false
		}
	}
	switch {
	case len(xs) == len(ys):
		return true
	case len(xs) < len(ys):
		return xt
	default:
		return yt
	}
}

func segmentsOverlap(a, b string) bool {
	ga, gb := strings.ContainsAny(a, "*?["), strings.ContainsAny(b, "*?[")
	switch {
	case !ga && !gb:
		return a == b
	case ga && gb:
		return true
	case ga:
		ok, _ := path.Match(a, b)
		return ok
	default:
		ok, _ := path.Match(b, a)
		return ok
	}
}
