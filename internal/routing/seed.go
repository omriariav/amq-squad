package routing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
// At a later start (the session already has an outcome log, decisions or a
// head record) a missing seed, or one that is not exactly 32 bytes at mode
// 0600, is an *InvalidError (routing_invalid): it is refused, never
// regenerated, since a new seed would change the draws of pending tasks
// (Codex pass 2, limit b). An empty root gives a fresh in-memory seed.
func SessionSeed(root string) (string, error) {
	s, _, _, err := SessionSeedID(root)
	return s, err
}

// SessionSeedID is SessionSeed with the seed file's device and inode (from
// fstat of the opened file), which the head record keeps: a seed file
// replaced by another is refused at the next start (Codex pass 5, (e)).
func SessionSeedID(root string) (string, uint64, uint64, error) {
	fresh := func() ([]byte, error) {
		b := make([]byte, SeedBytes)
		if _, err := rand.Read(b); err != nil {
			return nil, fmt.Errorf("routing seed: %w", err)
		}
		return b, nil
	}
	if root == "" {
		b, err := fresh()
		return hex.EncodeToString(b), 0, 0, err
	}
	path := SeedPath(root)
	s, dev, ino, err := readSeed(path)
	switch {
	case err == nil:
		return s, dev, ino, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", 0, 0, &InvalidError{Err: err}
	case sessionStarted(root):
		return "", 0, 0, &InvalidError{Err: fmt.Errorf("routing seed %s is missing in a session that has routed before; refusing to draw a new one", path)}
	}
	b, err := fresh()
	if err != nil {
		return "", 0, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", 0, 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".seed-*")
	if err != nil {
		return "", 0, 0, err
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
		return "", 0, 0, err
	}
	// Link never replaces: a concurrent first start keeps one seed.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", 0, 0, err
	}
	s, dev, ino, err = readSeed(path)
	if err != nil {
		return "", 0, 0, &InvalidError{Err: err}
	}
	return s, dev, ino, nil
}

// VerifySessionLog reads the session's outcome log under its seed, never
// creating one: with a seed, it runs the start's head and seed-identity
// checks (checkHead), then walks the hash chain and returns the good
// rows and every break (history_tampered); without a seed file nothing can
// be verified (verified false, every readable row returned). A seed file
// that is present but malformed is an error.
//
// The breaks are the chain's own, the shortfall against the head record,
// the head's open breaks (they persist until acknowledged), and a torn tail
// with its byte length.
func VerifySessionLog(root string) (good []Row, problems []Problem, verified bool, err error) {
	seed, dev, ino, err := readSeed(SeedPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		rows, _, rerr := ReadRows(OutcomePath(root))
		return rows, nil, false, rerr
	}
	if err != nil {
		return nil, nil, false, &InvalidError{Err: err}
	}
	key := SeedKey(seed)
	// The start's head and seed-identity checks first (phase 5 re-pass,
	// P2-7): a log cut to a valid prefix with its head removed, or a seed
	// copied to another inode, is refused here as it is at the start.
	h, found, herr := checkHead(root, key, dev, ino)
	if herr != nil {
		return nil, nil, true, herr
	}
	good, problems, head, tail, err := verifyChain(OutcomePath(root), key)
	if err != nil {
		return nil, nil, true, err
	}
	if found {
		problems = mergeProblems(h.Open, append(problems, shortfall("outcome", head, h.OutcomeSeq, h.OutcomeTag)...))
	}
	if tail > 0 {
		problems = append(problems, Problem{ID: fmt.Sprintf("torn_tail:%d", tail), Log: "outcome",
			What: fmt.Sprintf("the outcome log ends in %d bytes after its last newline", tail)})
	}
	return good, problems, true, nil
}

// sessionStarted reports whether the session has routed before.
func sessionStarted(root string) bool {
	for _, p := range []string{OutcomePath(root), DecisionPath(root), HeadPath(root)} {
		if _, err := os.Lstat(p); err == nil {
			return true
		}
	}
	return false
}

// readSeed reads a seed file through a no-follow, non-blocking open and
// checks the opened file itself (fstat), so a link, a FIFO or a swap between
// a stat and the read cannot substitute another file (Codex passes 3 and 4):
// a regular file of exactly SeedBytes raw bytes, mode 0600 (the mode is not
// checked on Windows). It returns the seed as hex and the file's device and
// inode.
func readSeed(path string) (string, uint64, uint64, error) {
	f, err := openNoFollow(path)
	if err != nil {
		if _, lerr := os.Lstat(path); lerr == nil && !errors.Is(err, fs.ErrNotExist) {
			return "", 0, 0, fmt.Errorf("routing seed %s: %w (a symlink is refused)", path, err)
		}
		return "", 0, 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, 0, err
	}
	if !st.Mode().IsRegular() {
		return "", 0, 0, fmt.Errorf("routing seed %s is not a regular file", path)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		return "", 0, 0, fmt.Errorf("routing seed %s has mode %04o, want 0600", path, st.Mode().Perm())
	}
	if st.Size() != SeedBytes {
		return "", 0, 0, fmt.Errorf("routing seed %s has %d bytes, want %d", path, st.Size(), SeedBytes)
	}
	raw := make([]byte, SeedBytes+1)
	n, err := io.ReadFull(f, raw)
	if n != SeedBytes || !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, 0, fmt.Errorf("routing seed %s: read %d bytes, want exactly %d", path, n, SeedBytes)
	}
	dev, ino := fileID(st)
	return hex.EncodeToString(raw[:SeedBytes]), dev, ino, nil
}
