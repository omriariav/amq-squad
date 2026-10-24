package fleet

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// The mayor authorizes a squad on the repository its checkout is, never on
// the label a request carries, and never on anything a seat can write
// (Codex review of 4-3, P1 1, second pass). The only identity sources are
// owner-controlled:
//
//   - the fleet grant's repository_paths: the owner names the canonical
//     checkout he permits for a repository (as the merge grant names its
//     repository, plan 7.2);
//   - the repository's identity file .amq-squad/repository (one line,
//     owner/name), which grants.Implicit protects from seats.
//
// The checkout's own git config is not a source: a seat can rewrite it. It
// is read only to refuse a disagreement: where a repository_paths entry
// names its remote (host plus full path), an origin that differs makes the
// checkout repository_unidentified. Two sources that disagree do too.

// IdentityFile is the repository's own identity file, relative to its root.
const IdentityFile = ".amq-squad/repository"

// RepositoryIdentity returns the owner/name identity of the checkout at
// projectPath under the fleet grant f, and the checkout's canonical path.
func RepositoryIdentity(projectPath string, f grants.Fleet) (identity, canonical string, err error) {
	canonical, err = filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", "", fmt.Errorf("repository root %s: %w", projectPath, err)
	}
	if st, err := os.Stat(canonical); err != nil || !st.IsDir() {
		return "", "", fmt.Errorf("repository root %s is not a directory", canonical)
	}
	var fromGrant, remote string
	for _, rp := range f.RepositoryPaths {
		p, err := filepath.EvalSymlinks(rp.Path)
		if err != nil || p != canonical {
			continue
		}
		if fromGrant != "" && !strings.EqualFold(fromGrant, rp.Repository) {
			return "", "", fmt.Errorf("the fleet grant maps %s to both %s and %s", canonical, fromGrant, rp.Repository)
		}
		fromGrant, remote = rp.Repository, rp.Remote
	}
	fromFile := ""
	raw, err := readRegular(filepath.Join(canonical, IdentityFile))
	switch {
	case err == nil:
		fromFile = strings.TrimSpace(string(raw))
		if !repoPattern.MatchString(fromFile) {
			return "", "", fmt.Errorf("%s/%s is not owner/name", canonical, IdentityFile)
		}
	case !errors.Is(err, os.ErrNotExist):
		return "", "", err
	}
	identity = fromGrant
	switch {
	case fromGrant == "" && fromFile == "":
		return "", "", fmt.Errorf("%s is not named by the fleet grant's repository_paths and has no %s: its repository identity is unknown", canonical, IdentityFile)
	case fromGrant == "":
		identity = fromFile
	case fromFile != "" && !strings.EqualFold(fromFile, fromGrant):
		return "", "", fmt.Errorf("the fleet grant names %s as %s but its %s says %s", canonical, fromGrant, IdentityFile, fromFile)
	}
	if remote != "" {
		url, err := originURL(canonical)
		if err != nil {
			return "", "", err
		}
		if url != "" && RemoteAddress(url) != strings.TrimSuffix(remote, ".git") {
			return "", "", fmt.Errorf("the origin of %s is %s, not the fleet grant's %s", canonical, RemoteAddress(url), remote)
		}
	}
	return identity, canonical, nil
}

// RemoteAddress is a remote URL as host plus full path: the transport
// (scheme, user, scp colon) and a .git suffix stripped, nothing else, so
// https://evil.example/owner/app.git is evil.example/owner/app and never
// github.com/owner/app.
func RemoteAddress(url string) string {
	addr := url
	if i := strings.Index(addr, "://"); i >= 0 {
		addr = addr[i+3:]
	} else if i := strings.Index(addr, ":"); i >= 0 && !strings.HasPrefix(addr, "/") {
		addr = addr[:i] + "/" + strings.TrimPrefix(addr[i+1:], "/")
	}
	if at := strings.Index(addr, "@"); at >= 0 && (strings.Index(addr, "/") < 0 || at < strings.Index(addr, "/")) {
		addr = addr[at+1:]
	}
	return strings.TrimSuffix(strings.TrimSuffix(addr, "/"), ".git")
}

// originURL reads remote.origin.url from the checkout's git config: .git as
// a directory, or a .git file naming the gitdir (a linked worktree, whose
// commondir holds the shared config). "" when there is no origin remote.
func originURL(root string) (string, error) {
	gitPath := filepath.Join(root, ".git")
	st, err := os.Lstat(gitPath)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	gitDir := gitPath
	switch {
	case st.Mode().IsRegular():
		raw, err := readRegular(gitPath)
		if err != nil {
			return "", err
		}
		dir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
		if !ok {
			return "", fmt.Errorf("%s is not a gitdir file", gitPath)
		}
		gitDir = strings.TrimSpace(dir)
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(root, gitDir)
		}
		if common, err := readRegular(filepath.Join(gitDir, "commondir")); err == nil {
			c := strings.TrimSpace(string(common))
			if !filepath.IsAbs(c) {
				c = filepath.Join(gitDir, c)
			}
			gitDir = c
		}
	case !st.IsDir():
		return "", fmt.Errorf("%s is neither a directory nor a gitdir file", gitPath)
	}
	raw, err := readRegular(filepath.Join(gitDir, "config"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return configValue(raw, `remote "origin"`, "url"), nil
}

// configValue is the last value of key in [section] of a git config file.
func configValue(raw []byte, section, key string) string {
	in, val := false, ""
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if strings.HasPrefix(line, "[") {
			in = strings.TrimSpace(strings.Trim(line, "[]")) == section
			continue
		}
		if !in {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(k), key) {
			val = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return val
}

// readRegular reads a regular, non-symlink file.
func readRegular(path string) ([]byte, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 1<<20 {
		return nil, fmt.Errorf("%s is not a small regular file", path)
	}
	return os.ReadFile(path)
}
