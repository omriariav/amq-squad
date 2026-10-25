package routing

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// SeedBytes is the session seed's size.
const SeedBytes = sealchain.SeedBytes

// SeedPath is the session's exploration seed under the AMQ root.
func SeedPath(root string) string { return sealchain.SeedPath(root) }

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
// replaced by another is refused at the next start (Codex pass 5, (e)). The
// seed's read and first write are sealchain's (one implementation).
func SessionSeedID(root string) (string, uint64, uint64, error) {
	s, dev, ino, err := sealchain.EnsureSeed(root, root != "" && sessionStarted(root))
	if err != nil && root != "" {
		return "", 0, 0, &InvalidError{Err: err}
	}
	return s, dev, ino, err
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
func sessionStarted(root string) bool { return sealchain.RoutingStarted(root) }

// readSeed is sealchain.ReadSeed.
func readSeed(path string) (string, uint64, uint64, error) { return sealchain.ReadSeed(path) }
