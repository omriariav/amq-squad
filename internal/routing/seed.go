package routing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// SeedBytes is the session seed's size.
const SeedBytes = 32

// SeedPath is the session's exploration seed under the AMQ root.
func SeedPath(root string) string { return filepath.Join(Dir(root), "seed") }

// SessionSeed is the session's exploration seed, as hex: 32 random bytes
// from crypto/rand, written raw once under the AMQ root (mode 0600) at the
// session's first router start and read back after that, so the draw
// survives a restart and is not predictable from the root path or the task
// ids (Codex pass 1, limit b). The first writer wins: the seed is linked
// into place, never replaced.
//
// At a later start (the session already has an outcome log or decisions) a
// missing seed, or one that is not exactly 32 bytes at mode 0600, is an
// *InvalidError (routing_invalid): it is refused, never regenerated, since a
// new seed would change the draws of pending tasks (Codex pass 2, limit b).
// An empty root gives a fresh in-memory seed.
func SessionSeed(root string) (string, error) {
	fresh := func() ([]byte, error) {
		b := make([]byte, SeedBytes)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("routing seed: %w", err)
		}
		return b, nil
	}
	if root == "" {
		b, err := fresh()
		return hex.EncodeToString(b), err
	}
	path := SeedPath(root)
	s, err := readSeed(path)
	switch {
	case err == nil:
		return s, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", &InvalidError{Err: err}
	case sessionStarted(root):
		return "", &InvalidError{Err: fmt.Errorf("routing seed %s is missing in a session that has routed before; refusing to draw a new one", path)}
	}
	b, err := fresh()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err = tmp.Chmod(0o600); err == nil {
		if _, err = tmp.Write(b); err == nil {
			err = tmp.Sync()
		}
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	// Link never replaces: a concurrent first start keeps one seed.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	s, err = readSeed(path)
	if err != nil {
		return "", &InvalidError{Err: err}
	}
	return s, nil
}

// sessionStarted reports whether the session has routed before.
func sessionStarted(root string) bool {
	for _, p := range []string{OutcomePath(root), DecisionPath(root)} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// readSeed reads a seed file: a regular file of exactly SeedBytes raw bytes,
// mode 0600 (the mode is not checked on Windows).
func readSeed(path string) (string, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("routing seed %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("routing seed %s has mode %04o, want 0600", path, st.Mode().Perm())
	}
	if st.Size() != SeedBytes {
		return "", fmt.Errorf("routing seed %s has %d bytes, want %d", path, st.Size(), SeedBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(raw) != SeedBytes {
		return "", fmt.Errorf("routing seed %s has %d bytes, want %d", path, len(raw), SeedBytes)
	}
	return hex.EncodeToString(raw), nil
}
