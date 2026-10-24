//go:build unix

package atomicfile

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// WriteAt is Write relative to an open directory descriptor (the
// descriptor-anchored writers: internal/fsanchor's WriteAtomic, brief 4-1 at
// its rebase after 2b-5). The temp file is created relative to dirfd with
// O_CREAT|O_EXCL|O_NOFOLLOW under a random name (TempPrefix + name +
// "-<random>", so the gate store's sweep recognizes it), written, chmodded,
// fsynced and closed, then renamed over name with renameat relative to the
// same descriptor (a symlink at name is replaced, never followed), and the
// directory is fsynced through dirfd. No step uses a pathname, so a
// directory swapped in on the path after dirfd was opened is never written.
// dirPath names the directory in messages only.
func WriteAt(dirfd int, dirPath, name string, data []byte, mode os.FileMode) error {
	if name == "" || name == "." || name == ".." || containsSlash(name) {
		return fmt.Errorf("atomicfile: %q is not a single path component", name)
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	tmp := TempPrefix + name + "-" + hex.EncodeToString(b[:])
	fd, err := unix.Openat(dirfd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("stage %s/%s: %w", dirPath, name, err)
	}
	f := os.NewFile(uintptr(fd), dirPath+"/"+tmp)
	_, werr := f.Write(data)
	err = errors.Join(werr, f.Chmod(mode), f.Sync(), f.Close())
	if err == nil {
		err = unix.Renameat(dirfd, tmp, dirfd, name)
	}
	if err != nil {
		_ = unix.Unlinkat(dirfd, tmp, 0)
		return fmt.Errorf("write %s/%s: %w", dirPath, name, err)
	}
	if err := unix.Fsync(dirfd); err != nil {
		return fmt.Errorf("sync %s: %w", dirPath, err)
	}
	return nil
}

func containsSlash(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return true
		}
	}
	return false
}
