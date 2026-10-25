package migrate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// root is the one project-root descriptor of a migrate run (M2). The project
// path is resolved once with EvalSymlinks and opened O_DIRECTORY|O_NOFOLLOW;
// the descriptor is kept across the plan and the apply. Every v2 file is read,
// every file is created and every file is removed relative to it, one
// O_NOFOLLOW component at a time, so a symlink at any directory on the way
// refuses (migrate_refused_symlink) and retargeting the root path changes
// nothing: the run keeps the directory it opened. The absolute paths a plan
// prints and stores are derived from the resolved path.
type root struct {
	dir      *fsanchor.AnchoredDir
	path     string // the resolved project root
	dev, ino uint64
}

var errAbsent = errors.New("absent")

func openRoot(project string) (*root, error) {
	real, err := filepath.EvalSymlinks(project)
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{"the project root"}, Detail: "the project root cannot be resolved"}
	}
	real = filepath.Clean(real)
	if looksLikeCredential(real) {
		return nil, &Refusal{Code: CodeCredential, Fields: []string{"the project path"}, Detail: "the project path has the shape of a real credential; nothing is read or written"}
	}
	dir, err := fsanchor.OpenAnchored(real, nil, false)
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{"the project root"}, Detail: "the project root cannot be opened without following a symlink"}
	}
	dev, ino, err := dirIdentity(dir, real)
	if err != nil {
		dir.Close()
		return nil, err
	}
	return &root{dir: dir, path: real, dev: dev, ino: ino}, nil
}

func (r *root) Close() {
	if r != nil && r.dir != nil {
		r.dir.Close()
	}
}

// abs is the absolute path of a project-relative slash path, derived from the
// resolved root.
func (r *root) abs(rel string) string { return filepath.Join(r.path, filepath.FromSlash(rel)) }

// rel is the project-relative slash path of an absolute path under the root.
func (r *root) rel(abs string) (string, bool) {
	p, err := filepath.Rel(r.path, abs)
	if err != nil || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(p), true
}

// changed refuses (root_changed) when the path no longer names the directory
// the descriptor holds.
func (r *root) changed() error {
	dev, ino, err := pathIdentity(r.path)
	if err != nil || dev != r.dev || ino != r.ino {
		return &Refusal{Code: CodeRootChanged, Fields: []string{"the project root"}, Detail: "the project root is no longer the directory this run opened; the run stops at this refusal"}
	}
	return nil
}

func splitRel(rel string) []string {
	if rel == "" || rel == "." {
		return nil
	}
	return strings.Split(rel, "/")
}

// classify turns a descriptor-walk error into a refusal or errAbsent.
func classify(err error, rel string) error {
	switch {
	case errors.Is(err, syscall.ENOENT) || errors.Is(err, os.ErrNotExist):
		return errAbsent
	case errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) || strings.Contains(err.Error(), "symlink"):
		return &Refusal{Code: CodeSymlink, Fields: []string{rel}, Detail: "a symlink on the way to a v2 file; migrate follows none"}
	}
	return &Refusal{Code: CodeInput, Fields: []string{rel}, Detail: fmt.Sprintf("cannot open the file: %v", err)}
}

// walk opens the directory rel (slash path, "" for the root) below the root
// descriptor; the returned close func must be called.
func (r *root) walk(rel string) (*fsanchor.AnchoredDir, func(), error) {
	cur, closeCur := r.dir, func() {}
	for i, part := range splitRel(rel) {
		next, err := cur.Sub(part)
		closeCur()
		if err != nil {
			return nil, func() {}, classify(err, strings.Join(splitRel(rel)[:i+1], "/"))
		}
		cur = next
		c := next
		closeCur = func() { c.Close() }
	}
	return cur, closeCur, nil
}

// read returns the regular file at rel, or (nil, nil) when it is absent. A
// symlink anywhere on the way, including the file itself, is a refusal.
func (r *root) read(rel string) (*file, error) {
	dir, closeDir, err := r.walk(path.Dir(rel))
	if path.Dir(rel) == "." {
		dir, closeDir, err = r.dir, func() {}, nil
	}
	if errors.Is(err, errAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer closeDir()
	f, kind, size, _, err := dir.OpenEntry(path.Base(rel))
	if err != nil {
		if err = classify(err, rel); errors.Is(err, errAbsent) {
			return nil, nil
		}
		return nil, err
	}
	if f == nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{rel}, Detail: "not a regular file"}
	}
	defer f.Close()
	if kind != fsanchor.KindRegular || size > maxInputBytes {
		return nil, &Refusal{Code: CodeInput, Fields: []string{rel}, Detail: fmt.Sprintf("not a regular file of at most %d bytes", maxInputBytes)}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxInputBytes+1))
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{rel}, Detail: "the file cannot be read"}
	}
	return &file{Path: r.abs(rel), Data: data}, nil
}

// names lists the directory rel (nil when absent).
func (r *root) names(rel string) ([]string, error) {
	dir, closeDir, err := r.walk(rel)
	if errors.Is(err, errAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer closeDir()
	names, err := dir.Names()
	if err != nil {
		return nil, &Refusal{Code: CodeInput, Fields: []string{rel}, Detail: "the directory cannot be listed"}
	}
	return names, nil
}

// glob lists the names in dir that match pattern, sorted, as rel paths.
func (r *root) glob(dir, pattern string) ([]string, error) {
	names, err := r.names(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if ok, _ := path.Match(pattern, n); ok {
			out = append(out, path.Join(dir, n))
		}
	}
	sort.Strings(out)
	return out, nil
}

// remove unlinks the file at rel through the descriptor walk; an absent file
// is not an error.
func (r *root) remove(rel string) error {
	if err := r.changed(); err != nil {
		return err
	}
	dir, closeDir, err := r.walk(path.Dir(rel))
	if path.Dir(rel) == "." {
		dir, closeDir, err = r.dir, func() {}, nil
	}
	if errors.Is(err, errAbsent) {
		return nil
	}
	if err != nil {
		return err
	}
	defer closeDir()
	if err := unlinkAt(dir, path.Base(rel)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return &Refusal{Code: CodeInput, Fields: []string{rel}, Detail: "the file cannot be removed"}
	}
	return nil
}

// create writes data to rel only when absent: the directories below the root
// are made through the descriptor (0755), the content goes to a temporary file
// beside the target (O_EXCL, O_NOFOLLOW, mode set explicitly) and is
// hard-linked into place, which fails with os.ErrExist if the target exists.
func (r *root) create(rel string, data []byte, mode os.FileMode) error {
	if err := r.changed(); err != nil {
		return err
	}
	cur, closeCur := r.dir, func() {}
	parts := splitRel(path.Dir(rel))
	if path.Dir(rel) == "." {
		parts = nil
	}
	for i, part := range parts {
		next, err := cur.StageMkdir(part, 0o755)
		closeCur()
		if err != nil {
			return classify(err, strings.Join(parts[:i+1], "/"))
		}
		cur = next
		c := next
		closeCur = func() { c.Close() }
	}
	defer closeCur()
	name := path.Base(rel)
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmpName := ".migrate-" + hex.EncodeToString(rnd[:])
	tmp, err := cur.StageCreate(tmpName, 0o600)
	if err != nil {
		return classify(err, rel)
	}
	defer unlinkAt(cur, tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return linkAt(cur, tmpName, name)
}

// isDir reports whether rel is a directory below the root, stepping one
// component at a time through the descriptor: an absent component or a plain
// file on the way is (false, nil); a symlink on the way is a refusal.
func (r *root) isDir(rel string) (bool, error) {
	cur, closeCur := r.dir, func() {}
	defer func() { closeCur() }()
	for i, part := range splitRel(rel) {
		f, kind, _, _, err := cur.OpenEntry(part)
		if err != nil {
			err = classify(err, strings.Join(splitRel(rel)[:i+1], "/"))
			if errors.Is(err, errAbsent) {
				return false, nil
			}
			return false, err
		}
		if f == nil || kind != fsanchor.KindDir {
			if f != nil {
				f.Close()
			}
			return false, nil
		}
		next, err := fsanchor.AsDir(f)
		if err != nil {
			f.Close()
			return false, classify(err, strings.Join(splitRel(rel)[:i+1], "/"))
		}
		closeCur()
		cur = next
		c := next
		closeCur = func() { c.Close() }
	}
	return true, nil
}
