//go:build unix

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var namespaceLockBeforeContainedCreate = func(string, string) error { return nil }

func openNamespaceNoFollow(path string, wantDir bool) (*os.File, os.FileInfo, error) {
	flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
	if wantDir {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Open(path, flags, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	before, err := os.Lstat(path)
	if err != nil || before.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, info) || (wantDir && !info.IsDir()) || (!wantDir && !info.Mode().IsRegular()) {
		_ = f.Close()
		return nil, nil, fmt.Errorf("path identity changed or violated no-follow contract: %s", path)
	}
	return f, info, nil
}

func openContainedNamespaceLock(projectDir, lockPath string) (*os.File, error) {
	projectDir = filepath.Clean(projectDir)
	lockPath = filepath.Clean(lockPath)
	rel, err := filepath.Rel(projectDir, lockPath)
	if err != nil || rel == "." || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("namespace lock path escapes project: %s", lockPath)
	}
	rootFD, err := unix.Open(projectDir, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open namespace project root without following links: %w", err)
	}
	defer func() { _ = unix.Close(rootFD) }()
	if err := namespaceLockBeforeContainedCreate(projectDir, rel); err != nil {
		return nil, err
	}
	// A concurrent first-time creator can race directory publication on some
	// platforms. Restart the complete descriptor walk on ENOENT; every retry
	// still opens each component with O_NOFOLLOW, so an ancestor swap cannot
	// redirect creation outside the project.
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		f, err := openContainedNamespaceLockAttempt(rootFD, lockPath, rel)
		if err == nil {
			return f, nil
		}
		lastErr = err
		if !errors.Is(err, unix.ENOENT) {
			return nil, err
		}
	}
	return nil, lastErr
}

func openContainedNamespaceLockAttempt(rootFD int, lockPath, rel string) (*os.File, error) {
	currentFD := rootFD
	currentOwned := false
	defer func() {
		if currentOwned {
			_ = unix.Close(currentFD)
		}
	}()
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("invalid namespace lock component %q", part)
		}
		if err := unix.Mkdirat(currentFD, part, 0o700); err != nil && err != unix.EEXIST {
			return nil, fmt.Errorf("create contained namespace lock directory %s: %w", part, err)
		}
		nextFD, err := unix.Openat(currentFD, part, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if err != nil {
			return nil, fmt.Errorf("open contained namespace lock directory %s: %w", part, err)
		}
		if currentOwned {
			_ = unix.Close(currentFD)
		}
		currentFD = nextFD
		currentOwned = true
	}
	leaf := parts[len(parts)-1]
	if leaf == "" || leaf == "." || leaf == ".." {
		return nil, fmt.Errorf("invalid namespace lock leaf %q", leaf)
	}
	fd, err := unix.Openat(currentFD, leaf, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open contained namespace lock %s: %w", rel, err)
	}
	f := os.NewFile(uintptr(fd), lockPath)
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("contained namespace lock is not a regular file: %s", lockPath)
	}
	return f, nil
}
