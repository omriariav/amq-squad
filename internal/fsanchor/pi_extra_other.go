//go:build !unix

package fsanchor

import (
	"os"
	"path/filepath"
)

// The non-unix variants of the 4-1 staging writes use paths below the
// checked directory (3.0 hosts are macOS, plan 8); not race-free.

func (d *AnchoredDir) StageMkdir(name string, mode uint32) (*AnchoredDir, error) {
	p := filepath.Join(d.path, name)
	if err := os.Mkdir(p, os.FileMode(mode)); err != nil && !os.IsExist(err) {
		return nil, err
	}
	return &AnchoredDir{path: p}, nil
}

func (d *AnchoredDir) StageCreate(name string, mode uint32) (*os.File, error) {
	return os.OpenFile(filepath.Join(d.path, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(mode))
}

func (d *AnchoredDir) StageLock(name string) (*os.File, error) {
	return os.OpenFile(filepath.Join(d.path, name), os.O_RDWR|os.O_CREATE, 0o600)
}

func (d *AnchoredDir) StageChmod(mode uint32) error { return os.Chmod(d.path, os.FileMode(mode)) }

func (d *AnchoredDir) StageRename(from, to string) error {
	return os.Rename(filepath.Join(d.path, from), filepath.Join(d.path, to))
}

func (d *AnchoredDir) StageRemoveAll(name string) error {
	return os.RemoveAll(filepath.Join(d.path, name))
}
