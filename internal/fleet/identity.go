package fleet

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// The mayor authorizes a squad on the repository its checkout is, never on
// the label a request carries, and never on anything a seat can write
// (Codex review of 4-3, P1 1). The only identity sources are
// owner-controlled:
//
//   - the fleet grant's repository_paths: the owner names the checkout he
//     permits for a repository (as the merge grant names its repository,
//     plan 7.2);
//   - the repository's identity file .amq-squad/repository (one line,
//     owner/name), which grants.Implicit protects from seats.
//
// Every path is reached through fsanchor's no-follow walk from /, and a
// mapped path matches the checkout by device and inode, so a symlink at a
// mapped path, or a symlinked .amq-squad directory, never lends another
// checkout's identity. A linked worktree's identity is its main checkout's:
// the main checkout (the worktree's gitdir commondir, resolved no-follow)
// must be the mapped path or carry the identity file. The checkout's own git
// config is not a source; it is read only to refuse a disagreement with a
// mapped remote. Two sources that disagree, or neither, refuse.

// IdentityFile is the repository's own identity file, relative to its root.
const IdentityFile = ".amq-squad/repository"

// RepositoryIdentity returns the owner/name identity of the checkout at
// projectPath under the fleet grant f, and the checkout's canonical path.
func RepositoryIdentity(projectPath string, f grants.Fleet) (identity, canonical string, err error) {
	canonical, err = filepath.EvalSymlinks(projectPath)
	if err != nil {
		return "", "", fmt.Errorf("repository root %s: %w", projectPath, err)
	}
	checkout, err := openNoFollow(canonical)
	if err != nil {
		return "", "", fmt.Errorf("repository root %s: %w", canonical, err)
	}
	defer checkout.Close()
	main, mainPath, err := mainCheckout(checkout, canonical)
	if err != nil {
		return "", "", err
	}
	if main != checkout {
		defer main.Close()
	}
	var fromGrant, remote string
	for _, rp := range f.RepositoryPaths {
		mapped, err := openNoFollow(rp.Path)
		if err != nil {
			if p, perr := filepath.EvalSymlinks(rp.Path); perr == nil && (p == mainPath || p == canonical) {
				return "", "", fmt.Errorf("the fleet grant maps %s through a symlink (%v); a mapped path must be the checkout itself", rp.Path, err)
			}
			continue
		}
		same, err := mapped.SameDir(main)
		mapped.Close()
		if err != nil {
			return "", "", err
		}
		if !same {
			continue
		}
		if fromGrant != "" && !strings.EqualFold(fromGrant, rp.Repository) {
			return "", "", fmt.Errorf("the fleet grant maps %s to both %s and %s", mainPath, fromGrant, rp.Repository)
		}
		fromGrant, remote = rp.Repository, rp.Remote
	}
	fromFile, err := identityFile(main, mainPath)
	if err != nil {
		return "", "", err
	}
	identity = fromGrant
	switch {
	case fromGrant == "" && fromFile == "":
		return "", "", fmt.Errorf("%s is not named by the fleet grant's repository_paths and has no %s: its repository identity is unknown", mainPath, IdentityFile)
	case fromGrant == "":
		identity = fromFile
	case fromFile != "" && !strings.EqualFold(fromFile, fromGrant):
		return "", "", fmt.Errorf("the fleet grant names %s as %s but its %s says %s", mainPath, fromGrant, IdentityFile, fromFile)
	}
	if remote != "" {
		url, err := originURL(main)
		if err != nil {
			return "", "", err
		}
		if url != "" && RemoteAddress(url) != strings.TrimSuffix(remote, ".git") {
			return "", "", fmt.Errorf("the origin of %s is %s, not the fleet grant's %s", mainPath, RemoteAddress(url), remote)
		}
	}
	return identity, canonical, nil
}

// openNoFollow opens an absolute directory from / one component at a time
// with O_NOFOLLOW.
func openNoFollow(p string) (*fsanchor.AnchoredDir, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return nil, fmt.Errorf("%q is not an absolute clean path", p)
	}
	vol := filepath.VolumeName(p)
	var rel []string
	for _, part := range strings.Split(strings.TrimPrefix(p[len(vol):], string(filepath.Separator)), string(filepath.Separator)) {
		if part != "" {
			rel = append(rel, part)
		}
	}
	return fsanchor.OpenAnchored(vol+string(filepath.Separator), rel, false)
}

// readBelow reads a small regular file below d through descriptors (each
// directory component and the file opened no-follow, the file fstat'd).
func readBelow(d *fsanchor.AnchoredDir, rel ...string) ([]byte, error) {
	cur := d
	for _, part := range rel[:len(rel)-1] {
		next, err := cur.Sub(part)
		if cur != d {
			cur.Close()
		}
		if err != nil {
			return nil, err
		}
		cur = next
	}
	if cur != d {
		defer cur.Close()
	}
	return fsanchor.ReadEntry(cur, rel[len(rel)-1])
}

func notExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// identityFile is the checkout's .amq-squad/repository, "" when absent.
func identityFile(d *fsanchor.AnchoredDir, path string) (string, error) {
	raw, err := readBelow(d, ".amq-squad", "repository")
	if notExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s/%s: %w", path, IdentityFile, err)
	}
	id := strings.TrimSpace(string(raw))
	if !repoPattern.MatchString(id) {
		return "", fmt.Errorf("%s/%s is not owner/name", path, IdentityFile)
	}
	return id, nil
}

// mainCheckout is the checkout itself, or, for a linked worktree (a .git
// file naming a gitdir), the main checkout: the parent of the gitdir's
// commondir. Every step is opened no-follow.
func mainCheckout(checkout *fsanchor.AnchoredDir, path string) (*fsanchor.AnchoredDir, string, error) {
	raw, err := fsanchor.ReadEntry(checkout, ".git")
	if err != nil {
		// no .git, or .git is a directory: the checkout is its own main
		return checkout, path, nil
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir:")
	if !ok {
		return nil, "", fmt.Errorf("%s/.git is not a gitdir file", path)
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(path, gitdir)
	}
	gd, err := openNoFollow(filepath.Clean(gitdir))
	if err != nil {
		return nil, "", fmt.Errorf("the worktree %s names gitdir %s: %w", path, gitdir, err)
	}
	common, err := fsanchor.ReadEntry(gd, "commondir")
	gd.Close()
	if err != nil {
		return nil, "", fmt.Errorf("the worktree %s: gitdir %s has no commondir: %w", path, gitdir, err)
	}
	c := strings.TrimSpace(string(common))
	if !filepath.IsAbs(c) {
		c = filepath.Join(gitdir, c)
	}
	mainPath := filepath.Dir(filepath.Clean(c))
	main, err := openNoFollow(mainPath)
	if err != nil {
		return nil, "", fmt.Errorf("the worktree %s's main checkout %s: %w", path, mainPath, err)
	}
	return main, mainPath, nil
}

// originURL reads remote.origin.url from a main checkout's .git/config,
// no-follow. "" when there is no origin remote.
func originURL(main *fsanchor.AnchoredDir) (string, error) {
	raw, err := readBelow(main, ".git", "config")
	if notExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return configValue(raw, `remote "origin"`, "url"), nil
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
