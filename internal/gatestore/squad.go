package gatestore

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// SquadID is the squad identity of a gate store's AMQ root (4-5 re-pass,
// amendment 28): the SHA-256 of the root with every symlink resolved and of
// the directory's device and inode, so two paths to one store give one
// identity and a directory replaced at the same path gives another. A root
// that does not exist has no stable identity; its path-only form never
// matches a record (records are written into an existing root).
func SquadID(root string) string {
	resolved, err := filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "root-sha256:" + digestHex("missing\x00"+filepath.Clean(root))
	}
	id := ""
	if info, err := os.Stat(resolved); err == nil {
		id = fileID(info)
	}
	return "root-sha256:" + digestHex(resolved+"\x00"+id)
}

// EnsureSquadID creates the root (0700, as Store.Raise does) and then takes
// its identity (3.3.x item 1): a writer that stamps a record takes the
// identity once the root exists, never the path-only form of a missing root,
// which no verifier reading the same root would match.
func EnsureSquadID(root string) (string, error) {
	if err := os.MkdirAll(filepath.Clean(root), 0o700); err != nil {
		return "", err
	}
	return SquadID(root), nil
}

func digestHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
