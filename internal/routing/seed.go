package routing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// SeedPath is the session's exploration seed under the AMQ root.
func SeedPath(root string) string { return filepath.Join(Dir(root), "seed") }

// SessionSeed is the session's exploration seed: 32 random bytes from
// crypto/rand, hex, written once under the AMQ root (mode 0600) the first
// time a router starts and read back after that, so the draw survives a
// restart and is not predictable from the root path or the task ids (Codex
// pass 1, limit b). The first writer wins: the seed is linked into place,
// never replaced. An empty root gives a fresh in-memory seed.
func SessionSeed(root string) (string, error) {
	fresh := func() (string, error) {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", fmt.Errorf("routing seed: %w", err)
		}
		return hex.EncodeToString(b[:]), nil
	}
	if root == "" {
		return fresh()
	}
	path := SeedPath(root)
	if s, err := readSeed(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		return s, err
	}
	seed, err := fresh()
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
	if err := tmp.Chmod(0o600); err == nil {
		_, err = tmp.WriteString(seed + "\n")
		if err == nil {
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
	return readSeed(path)
}

func readSeed(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(raw))
	if len(s) != 64 {
		return "", fmt.Errorf("routing seed %s: want 64 hex characters, got %d", path, len(s))
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", fmt.Errorf("routing seed %s: %w", path, err)
	}
	return s, nil
}
