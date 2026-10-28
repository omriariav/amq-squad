package piruntime

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The pi launch shim (brief 5-amq-A, plan 4.1, R5). AMQ's pi adapter
// (v0.116.x, launchapi PiProvider) selects pi by the executable's file name,
// probes it with `pi --version` (floor 1.2.0, release builds only) and
// `pi --help` (--session-id must be listed), and composes
// `<pi> --session-id <id>` behind the seat's wrapper. The embedded runtime is
// node plus a preload plus pi's entry, not one file, so the launch names this
// shim as the pi executable: a 0700 script under ~/.amq-squad/pi-launch/
// that hands its arguments to `amq-squad pi shim`, which answers the two
// probes from the verified embedded runtime itself. seat-exec never runs
// the shim: it checks argv[0] is this shim for this amq-squad and starts the
// embedded runtime through `amq-squad pi run`, with the adapter's argv
// after `--` (R2: never a pi from PATH).

// ShimDirName is the shim's directory under ~/.amq-squad.
const ShimDirName = "pi-launch"

// ShimPath is ~/.amq-squad/pi-launch/pi.
func ShimPath(home string) string { return filepath.Join(home, ".amq-squad", ShimDirName, "pi") }

// ShimScript is the shim's exact content for the amq-squad binary self.
func ShimScript(self string) ([]byte, error) {
	if !filepath.IsAbs(self) || strings.ContainsAny(self, "'\n\x00") {
		return nil, fmt.Errorf("the amq-squad path %q cannot be named in the pi shim (absolute, no quote or newline)", self)
	}
	return []byte("#!/bin/sh\n# amq-squad pi launch shim (written by amq-squad; do not edit). AMQ's pi adapter\n# probes this file; seat-exec starts the verified embedded runtime instead.\nexec '" + self + "' pi shim \"$@\"\n"), nil
}

// EnsureShim writes the shim for self when it is missing or differs (an
// unchanged shim keeps its identity, so the launch's trust subject stays the
// same) and returns its path. A symlink or non-regular file in its place is
// refused, never followed.
func EnsureShim(home, self string) (string, error) {
	want, err := ShimScript(self)
	if err != nil {
		return "", err
	}
	path := ShimPath(home)
	if err := privateDir(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("pi shim directory %s: %w", filepath.Dir(path), err)
	}
	if err := CheckShim(home, path, self); err == nil {
		return path, nil
	} else if fi, lerr := os.Lstat(path); lerr == nil && !fi.Mode().IsRegular() {
		return "", fmt.Errorf("pi shim %s is not a regular file: %w", path, err)
	}
	tmp := path + ".tmp-" + strconv.Itoa(os.Getpid())
	if err := os.WriteFile(tmp, want, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return path, nil
}

// ErrShimMismatch is the refusal of an argv[0] that is not this binary's shim.
var ErrShimMismatch = errors.New("not the amq-squad pi shim")

// CheckShim verifies path is ShimPath(home), a regular 0700 file owned by
// this user (not a symlink) whose bytes are ShimScript(self).
func CheckShim(home, path, self string) error {
	want, err := ShimScript(self)
	if err != nil {
		return err
	}
	if filepath.Clean(path) != ShimPath(home) {
		return fmt.Errorf("%w: %s is not %s", ErrShimMismatch, path, ShimPath(home))
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrShimMismatch, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.Mode().IsRegular() || fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s is not a private regular file (%s)", ErrShimMismatch, path, fi.Mode())
	}
	if !ownedByMe(fi) {
		return fmt.Errorf("%w: %s is not owned by this user", ErrShimMismatch, path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrShimMismatch, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s does not name this amq-squad (%s)", ErrShimMismatch, path, self)
	}
	return nil
}
