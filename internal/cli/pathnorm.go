package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// Path representation normalization.
//
// GitHub #539 and #540 were the same defect twice: a filesystem path recorded
// in one representation and compared in another. #540 recorded `--project` as
// the literal "." in the prepared manifest while resolving it absolutely at
// bootstrap, so an identity tuple compared unequal and rendered an error whose
// two operands were byte-identical. #539 was the same relative project leaking
// one level further, into `tool_policy_sources`: those entries are built with
// filepath.Join(project, ...), so a relative project produced a relative
// project-local entry that the spawn-time comparator then saw as a changed
// capability source set.
//
// The fix is that there is exactly one canonicalization function and both the
// recorder and every comparator call it. Adding a second normalizer anywhere
// re-opens this class of bug, so route new path comparisons through
// canonicalFilesystemPath rather than hand-rolling Abs/Clean/EvalSymlinks.

// There are two normalization levels on purpose, and the split matters.
//
// RECORDING uses absoluteFilesystemPath: tilde-expanded, absolute, cleaned, but
// NOT symlink-resolved. Recorded paths stay the location the operator named.
// Resolving symlinks at record time would rewrite `/var/folders/...` to
// `/private/var/folders/...` on macOS, changing user-visible output and every
// digest computed over the path, which is a different behavior change than the
// one #539/#540 ask for.
//
// COMPARISON uses canonicalFilesystemPath, which additionally resolves
// symlinks. Representation-independence of an identity is a property of the
// comparison, not of the stored bytes: a symlinked path and its target are the
// same location and must compare equal however each side was recorded.
//
// There are NO exceptions to this split. A canonical-at-record exception was
// attempted for tool_policy_sources and reverted: that field turned out to be
// printed by the overlay plan output, exported in JSON envelopes, and digested
// as part of team.json for prepared-run readiness, so canonicalizing it rewrote
// operator-visible paths and digest inputs. When two writers disagree on a
// recorded path, fix the ORIGIN they derive it from; do not collapse
// representations at record time.

// absoluteFilesystemPath is the RECORDING normalization: tilde-expanded,
// absolute, and lexically cleaned. This is what turns `--project .` into a
// stable recorded value.
//
// It is deliberately total; each step degrades to the most resolved form
// reached so far. An empty or whitespace-only input returns the empty string,
// so callers that distinguish "unset" from "mismatch" must check for empty.
func absoluteFilesystemPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

// canonicalFilesystemPath is the COMPARISON normalization: absoluteFilesystemPath
// plus symlink resolution. Two paths naming the same location canonicalize to
// the same string regardless of how either was written or recorded.
//
// Symlink resolution is best effort, because comparators must be able to
// canonicalize a location that does not exist -- a recorded worktree that has
// since been removed, or a path about to be created.
//
// EvalSymlinks fails outright when the LEAF is missing, even if an ancestor is a
// symlink. Taking the absolute form in that case would make /link/x and /real/x
// canonicalize differently for a not-yet-created x, so two records naming the
// same future location would compare as drift. Instead, resolve the longest
// EXISTING ancestor and rejoin the remainder, which gives the same answer for
// both spellings whether or not the leaf exists yet.
func canonicalFilesystemPath(p string) string {
	p = absoluteFilesystemPath(p)
	if p == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	remainder := ""
	dir := p
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the root without finding an existing ancestor. The absolute
			// form is still stable, which is what comparison needs.
			return p
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder))
		}
	}
}

// canonicalFilesystemPathIn is the COMPARISON normalization for a path that may
// be stored RELATIVE, anchoring it to base rather than to the process working
// directory.
//
// This exists because filepath.Abs resolves against the CWD, which is the wrong
// base for a persisted record. A team.json written by <=v2.24.0 still holds the
// project-relative ".claude/settings.local.json"; comparing it from outside the
// project (`amq-squad ... --project /path/to/repo` run from anywhere else, which
// is the ordinary out-of-repo invocation) would otherwise resolve it against the
// caller's CWD, name a file that does not exist, and report drift for a record
// that is actually correct. Relative entries are anchored to the project root
// they were recorded against; absolute entries are unaffected.
func canonicalFilesystemPathIn(base, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
		if base = strings.TrimSpace(base); base != "" {
			p = filepath.Join(base, p)
		}
	}
	return canonicalFilesystemPath(p)
}

// canonicalFilesystemPathsIn is the project-anchored COMPARISON normalization
// for a path set. Use it on BOTH operands whenever either side may have been
// persisted with relative entries.
func canonicalFilesystemPathsIn(base string, paths []string) []string {
	return normalizeFilesystemPathSet(paths, func(p string) string {
		return canonicalFilesystemPathIn(base, p)
	})
}

// absoluteFilesystemPaths is the RECORDING normalization for a path set:
// each entry made absolute, then deduplicated and sorted. Blank entries are
// dropped. Two runs given the same inputs in different representations record
// byte-identical sets, which is the #539 acceptance criterion.
func absoluteFilesystemPaths(paths []string) []string {
	return normalizeFilesystemPathSet(paths, absoluteFilesystemPath)
}

// canonicalFilesystemPaths is the COMPARISON normalization for a path set. Use
// it on BOTH operands of a set comparison so representation, including a stored
// relative entry written by an older release, cannot register as drift.
func canonicalFilesystemPaths(paths []string) []string {
	return normalizeFilesystemPathSet(paths, canonicalFilesystemPath)
}

func normalizeFilesystemPathSet(paths []string, normalize func(string) string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if n := normalize(p); n != "" {
			out = append(out, n)
		}
	}
	return dedupeSortedStrings(out)
}
