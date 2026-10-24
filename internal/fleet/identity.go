package fleet

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The mayor authorizes a squad on the repository its checkout is, never on
// the label a request carries (Codex review of 4-3, P1 1): the identity is
// derived from the canonical checkout, from the origin remote's URL in the
// repository's own git config, or, when there is no origin remote, from the
// repository's identity file .amq-squad/repository (one line, owner/name;
// protected by grants.Implicit so a seat cannot rewrite it).

// IdentityFile is the repository's own identity file, relative to its root.
const IdentityFile = ".amq-squad/repository"

// RepositoryIdentity returns the owner/name identity of the checkout at
// projectPath and the checkout's canonical path (symlinks resolved).
func RepositoryIdentity(projectPath string) (identity, canonical string, err error) {
	canonical, err = filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", "", fmt.Errorf("repository root %s: %w", projectPath, err)
	}
	if st, err := os.Stat(canonical); err != nil || !st.IsDir() {
		return "", "", fmt.Errorf("repository root %s is not a directory", canonical)
	}
	url, err := originURL(canonical)
	if err != nil {
		return "", "", err
	}
	if url != "" {
		id := identityFromURL(url)
		if !repoPattern.MatchString(id) {
			return "", "", fmt.Errorf("origin remote %q of %s names no owner/name repository", url, canonical)
		}
		return id, canonical, nil
	}
	raw, err := readRegular(filepath.Join(canonical, IdentityFile))
	if errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("%s has no origin remote and no %s: its repository identity is unknown", canonical, IdentityFile)
	}
	if err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(string(raw))
	if !repoPattern.MatchString(id) {
		return "", "", fmt.Errorf("%s/%s is not owner/name", canonical, IdentityFile)
	}
	return id, canonical, nil
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

// identityFromURL is owner/name from a remote URL: scp form
// (git@host:owner/name.git), ssh://, https://, git:// (the last two path
// segments, .git stripped).
func identityFromURL(url string) string {
	path := url
	if i := strings.Index(url, "://"); i >= 0 {
		path = url[i+3:]
		if j := strings.Index(path, "/"); j >= 0 {
			path = path[j+1:]
		} else {
			return ""
		}
	} else if i := strings.Index(url, ":"); i >= 0 && !strings.HasPrefix(url, "/") {
		path = url[i+1:]
	} else {
		return "" // a local path remote names no hosted repository
	}
	path = strings.TrimSuffix(strings.TrimSuffix(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
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
