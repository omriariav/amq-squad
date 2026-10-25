// Package sealchain is the routing log's tag, seed and chain-and-head rule
// as a leaf package (phase 5 re-pass of 2b-2 milestone B, ruling): the
// routing package and the sealed state files (the commits ledger, the
// scheduler's exits queue) use this one implementation, so the rule is
// reused, never forked. internal/routing's outcome rows, decisions and head
// record are tagged with Tag under the session seed's key; a sealed state
// file carries a Link (seq from 1, the previous version's tag, an HMAC tag
// over the label, seq, prev and the file's body) and a signed head per file
// under the routing directory says which link the file must be at.
//
// Limit, the routing chain's sentence: a same-UID process that reads the
// seed can forge a file and its head; the chain detects every edit made
// without the seed (a hand edit, a copy of an older version, a stripped
// seal, a deleted head).
package sealchain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
)

// ErrTampered is a sealed file whose tag or chain fails (ledger_tampered).
var ErrTampered = errors.New("ledger_tampered")

// ErrSeedMissing is a session that has routed before but has no seed: it is
// refused, never regenerated.
var ErrSeedMissing = errors.New("the session seed is missing in a session that has routed before; refusing to draw a new one")

// SeedBytes is the session seed's size.
const SeedBytes = 32

// Dir is the routing directory under an AMQ session root.
func Dir(root string) string { return filepath.Join(root, "extensions", "amq-squad", "routing") }

// SeedPath is the session seed under the AMQ root.
func SeedPath(root string) string { return filepath.Join(Dir(root), "seed") }

// Tag is the hex HMAC-SHA256 of v's JSON under key, after a domain label.
func Tag(key []byte, label string, v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(label))
	m.Write([]byte{0})
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// Key is the HMAC key of a session seed (hex).
func Key(seed string) []byte {
	raw, err := hex.DecodeString(seed)
	if err != nil {
		return []byte(seed)
	}
	return raw
}

// RoutingStarted reports whether the session has routed before (it has an
// outcome log, a decision log or a routing head).
func RoutingStarted(root string) bool {
	for _, name := range []string{"outcomes.jsonl", "decisions.jsonl", "head.json"} {
		if _, err := os.Lstat(filepath.Join(Dir(root), name)); err == nil {
			return true
		}
	}
	return false
}

// ReadSeed reads a seed file through a no-follow, non-blocking open and
// checks the opened file itself (fstat), so a link, a FIFO or a swap between
// a stat and the read cannot substitute another file: a regular file of
// exactly SeedBytes raw bytes, mode 0600 (the mode is not checked on
// Windows). It returns the seed as hex and the file's device and inode.
func ReadSeed(path string) (string, uint64, uint64, error) {
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

// EnsureSeed reads the session seed, or writes it once (32 bytes from
// crypto/rand, 0600, linked into place so a concurrent first writer keeps
// one seed) when the session has not routed before. A missing seed in a
// session that has (started) is ErrSeedMissing; a seed that is present but
// malformed is that error. An empty root gives a fresh in-memory seed.
func EnsureSeed(root string, started bool) (string, uint64, uint64, error) {
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
	s, dev, ino, err := ReadSeed(path)
	switch {
	case err == nil:
		return s, dev, ino, nil
	case !errors.Is(err, fs.ErrNotExist):
		return "", 0, 0, err
	case started:
		return "", 0, 0, fmt.Errorf("routing seed %s: %w", path, ErrSeedMissing)
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
	return ReadSeed(path)
}

// SessionKey is the key of a session root's sealed files, the seed written
// once when absent (a session that has routed before and lost its seed is
// refused, as routing refuses it).
func SessionKey(root string) ([]byte, error) {
	seed, _, _, err := EnsureSeed(root, RoutingStarted(root) || headsExist(root))
	if err != nil {
		return nil, err
	}
	return Key(seed), nil
}

// ReadKey is the key of a session root's sealed files without writing a
// seed: ok false when the session has none.
func ReadKey(root string) (key []byte, ok bool, err error) {
	seed, _, _, err := ReadSeed(SeedPath(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return Key(seed), true, nil
}

// Link is a sealed file's place in its chain.
type Link struct {
	Seq  int    `json:"seq"`
	Prev string `json:"prev"`
	Tag  string `json:"tag"`
}

type sealedBody struct {
	Seq  int    `json:"seq"`
	Prev string `json:"prev"`
	Body any    `json:"body"`
}

// Seal returns the link of a new version of a file whose current link is
// prev (nil for the first version).
func Seal(key []byte, label string, prev *Link, body any) *Link {
	l := &Link{Seq: 1}
	if prev != nil {
		l.Seq, l.Prev = prev.Seq+1, prev.Tag
	}
	l.Tag = Tag(key, label, sealedBody{Seq: l.Seq, Prev: l.Prev, Body: body})
	return l
}

// Verify reports whether the link's tag is the key's over body.
func (l Link) Verify(key []byte, label string, body any) bool {
	want := Tag(key, label, sealedBody{Seq: l.Seq, Prev: l.Prev, Body: body})
	return l.Seq > 0 && l.Tag != "" && hmac.Equal([]byte(want), []byte(l.Tag))
}

var headName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{0,127}$`)

// HeadPath is the signed head of one sealed file of a session root.
func HeadPath(root, name string) string { return filepath.Join(Dir(root), "heads", name+".json") }

func headsExist(root string) bool {
	_, err := os.Lstat(filepath.Join(Dir(root), "heads"))
	return err == nil
}

type head struct {
	Schema int       `json:"schema"`
	Name   string    `json:"name"`
	Seq    int       `json:"seq"`
	Link   string    `json:"link_tag"`
	At     time.Time `json:"at"`
	Tag    string    `json:"tag,omitempty"`
}

func (h head) sign(key []byte) head {
	h.Tag = ""
	h.Tag = Tag(key, "amq-squad sealed head v1", h)
	return h
}

// WriteHead records that the named file is now at link l (atomicfile; the
// caller writes the file first).
func WriteHead(root, name string, key []byte, l *Link, now time.Time) error {
	if !headName.MatchString(name) || l == nil || l.Seq < 0 {
		return fmt.Errorf("sealed head %q", name)
	}
	raw, err := json.Marshal(head{Schema: 1, Name: name, Seq: l.Seq, Link: l.Tag, At: now.UTC()}.sign(key))
	if err != nil {
		return err
	}
	p := HeadPath(root, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(p, raw, 0o600)
}

// Write saves a new version of a sealed file: it seals body as the link
// after the head's (a genesis head, link 0, is written first for a file's
// first version), saves the file through save, then moves the head to the
// new link. A crash leaves the file at most one link ahead of its head,
// which Check accepts; a head always exists once a sealed file does. A head
// whose tag fails is ErrTampered, and nothing is written.
func Write(root, name string, key []byte, label string, body any, save func(*Link) error, now time.Time) (*Link, error) {
	h, found, err := readHead(root, name, key)
	if err != nil {
		return nil, err
	}
	if !found {
		if err := WriteHead(root, name, key, &Link{}, now); err != nil {
			return nil, err
		}
	}
	l := Seal(key, label, &Link{Seq: h.Seq, Tag: h.Link}, body)
	if err := save(l); err != nil {
		return nil, err
	}
	return l, WriteHead(root, name, key, l, now)
}

// readHead reads and verifies a file's head; found false when there is none.
func readHead(root, name string, key []byte) (head, bool, error) {
	var h head
	raw, err := os.ReadFile(HeadPath(root, name))
	if errors.Is(err, fs.ErrNotExist) {
		return h, false, nil
	}
	if err != nil {
		return h, false, err
	}
	if json.Unmarshal(raw, &h) != nil || h.Schema != 1 || h.Name != name || h.Tag == "" || !hmac.Equal([]byte(h.sign(key).Tag), []byte(h.Tag)) {
		return h, true, fmt.Errorf("%w: the head of %s does not decode or its tag fails", ErrTampered, name)
	}
	return h, true, nil
}

// Absent checks a sealed file that is missing: with a head past link 0 it
// existed, so its removal is ErrTampered.
func Absent(root, name string, key []byte) error {
	h, found, err := readHead(root, name, key)
	if err != nil || !found {
		return err
	}
	if h.Seq > 0 {
		return fmt.Errorf("%w: %s is missing and its head records link %d", ErrTampered, name, h.Seq)
	}
	return nil
}

// Check checks a sealed file's link against its head: the file must carry
// a link whose tag verifies over body (the file without its link), and the
// link must be the head's, or the one right after it (the file was written
// and its head write was lost). A missing head, an unsealed file or any
// other link is ErrTampered.
func Check(root, name string, key []byte, label string, l *Link, body any) error {
	h, found, err := readHead(root, name, key)
	if err != nil {
		return err
	}
	headMissing := !found
	switch {
	case l == nil:
		return fmt.Errorf("%w: %s carries no seal", ErrTampered, name)
	case !l.Verify(key, label, body):
		return fmt.Errorf("%w: the tag of %s link %d fails", ErrTampered, name, l.Seq)
	case headMissing:
		return fmt.Errorf("%w: %s is at link %d and its head is gone", ErrTampered, name, l.Seq)
	case l.Seq == h.Seq && l.Tag == h.Link:
		return nil
	case l.Seq == h.Seq+1 && l.Prev == h.Link:
		return nil // the head write after the file's was lost
	}
	return fmt.Errorf("%w: %s is at link %d, its head records link %d", ErrTampered, name, l.Seq, h.Seq)
}
