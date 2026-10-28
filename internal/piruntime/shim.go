package piruntime

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
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
// the shim: it checks argv[0] is the shim the launch record names, for this
// amq-squad, and starts the embedded runtime through `amq-squad pi run`,
// with the adapter's argv after `--` (R2: never a pi from PATH).

// ShimDirName is the shim's directory under ~/.amq-squad.
const ShimDirName = "pi-launch"

// ShimMode is the shim's exact mode: AMQ's Apply executes it.
const ShimMode os.FileMode = 0o700

// ShimPath is ~/.amq-squad/pi-launch/pi.
func ShimPath(home string) string { return filepath.Join(home, ".amq-squad", ShimDirName, "pi") }

// ShimScript is the shim's exact content for the amq-squad binary self.
func ShimScript(self string) ([]byte, error) {
	if !filepath.IsAbs(self) || strings.ContainsAny(self, "'\n\x00") {
		return nil, fmt.Errorf("the amq-squad path %q cannot be named in the pi shim (absolute, no quote or newline)", self)
	}
	return []byte("#!/bin/sh\n# amq-squad pi launch shim (written by amq-squad; do not edit). AMQ's pi adapter\n# probes this file; seat-exec starts the verified embedded runtime instead.\nexec '" + self + "' pi shim \"$@\"\n"), nil
}

// EnsureShim writes the shim for self under home when it is missing, differs
// or has another mode than 0700 (an unchanged shim keeps its identity, so the
// launch's trust subject stays the same) and returns its path. The write is
// internal/atomicfile's (a random O_EXCL temp, fsynced, renamed; a planted
// name is never followed). A symlink or non-regular file in its place is
// refused, never followed or replaced.
func EnsureShim(home, self string) (string, error) {
	want, err := ShimScript(self)
	if err != nil {
		return "", err
	}
	path := ShimPath(home)
	if err := privateDir(filepath.Dir(path)); err != nil {
		return "", fmt.Errorf("pi shim directory %s: %w", filepath.Dir(path), err)
	}
	if err := CheckShim(path, self); err == nil {
		return path, nil
	} else if fi, lerr := os.Lstat(path); lerr == nil && !fi.Mode().IsRegular() {
		return "", fmt.Errorf("pi shim %s is not a regular file: %w", path, err)
	}
	if err := atomicfile.Write(path, want, ShimMode); err != nil {
		return "", err
	}
	return path, nil
}

// ErrShimMismatch is the refusal of an argv[0] that is not this binary's shim.
var ErrShimMismatch = errors.New("not the amq-squad pi shim")

// CheckShim verifies the file at path, opened without following a symlink
// and checked and read through that one descriptor: a regular file of mode
// exactly 0700, owned by this user, whose bytes are ShimScript(self).
func CheckShim(path, self string) error {
	want, err := ShimScript(self)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) != "pi" {
		return fmt.Errorf("%w: %q is not an absolute clean path to a file named pi", ErrShimMismatch, path)
	}
	f, err := openNoFollow(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrShimMismatch, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrShimMismatch, err)
	}
	if !fi.Mode().IsRegular() || fi.Mode().Perm() != ShimMode {
		return fmt.Errorf("%w: %s is not a regular file of mode 0700 (%s)", ErrShimMismatch, path, fi.Mode())
	}
	if !ownedByMe(fi) {
		return fmt.Errorf("%w: %s is not owned by this user", ErrShimMismatch, path)
	}
	got, err := io.ReadAll(io.LimitReader(f, int64(len(want))+1))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrShimMismatch, err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("%w: %s does not name this amq-squad (%s)", ErrShimMismatch, path, self)
	}
	return nil
}
