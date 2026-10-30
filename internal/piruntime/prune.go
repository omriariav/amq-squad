package piruntime

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// KeepRuntimes is how many unpacked runtime versions doctor --fix keeps
// (brief 4-1 ruling 3: the last two).
const KeepRuntimes = 2

// Installed is one runtime directory found under the base.
type Installed struct {
	Name      string
	Dir       string
	Installed time.Time // the completion marker's modification time
	Current   bool      // the runtime this binary embeds
}

// List returns the unpacked runtimes under base, newest first. A directory
// counts only when it carries a completion marker; staging directories and
// anything else are left out (and never touched).
func List(base, current string) ([]Installed, error) {
	ents, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Installed
	for _, d := range ents {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		dir := filepath.Join(base, d.Name())
		fi, err := os.Lstat(filepath.Join(dir, MarkerName))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, Installed{Name: d.Name(), Dir: dir, Installed: fi.ModTime(), Current: d.Name() == current})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Current != out[j].Current {
			return out[i].Current
		}
		if !out[i].Installed.Equal(out[j].Installed) {
			return out[i].Installed.After(out[j].Installed)
		}
		return out[i].Name > out[j].Name
	})
	return out, nil
}

// Prune removes every runtime beyond the newest keep, never the current
// one, plus their install lock files and stale staging directories. It
// returns the names it removed. Only doctor --fix calls it; a launch never
// deletes a runtime.
func Prune(base, current string, keep int) ([]string, error) {
	list, err := List(base, current)
	if err != nil {
		return nil, err
	}
	var removed []string
	for i, rt := range list {
		if i < keep || rt.Current {
			continue
		}
		if err := removeTree(rt.Dir); err != nil {
			return removed, err
		}
		_ = os.Remove(filepath.Join(base, "."+rt.Name+".lock"))
		removed = append(removed, rt.Name)
	}
	stale, _ := filepath.Glob(filepath.Join(base, ".unpack-*"))
	for _, s := range stale {
		if fi, err := os.Lstat(s); err == nil && fi.IsDir() && time.Since(fi.ModTime()) > time.Hour {
			if err := removeTree(s); err == nil {
				removed = append(removed, filepath.Base(s))
			}
		}
	}
	return removed, nil
}

// removeTree makes read-only directories writable first so RemoveAll can
// unlink their files. It follows no symlink.
func removeTree(dir string) error {
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	return os.RemoveAll(dir)
}
