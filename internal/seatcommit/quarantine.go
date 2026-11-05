package seatcommit

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"
)

// A tamper alarm preserves its evidence (3.2.0-0c-A). On 2026-11-05 the
// rc.1 scheduler refused the reviewer seat's commit ledger (ledger_tampered,
// link 150) on every tick for 65 minutes, and the refused seq-150 body was
// overwritten by the next writer's re-seal 2 to 4 s after the restart and
// lost: one seal per document, no history. Now the read that refuses copies
// the bytes it refused, the body and the head, under the session root
// before anything else can touch them, and every write re-reads the
// document on disk under the ledger lock first (writeLedger), so no writer
// of this binary re-seals over a body a reader refused.
//
// The layout (the plan reviewer's ruling on #893): a quarantine is a
// directory, one per distinct refusal of a head seq,
//
//	<root>/extensions/amq-squad/quarantine/commits.<handle>.seq<N>[.k]/
//	    body.json        the refused body, byte for byte (when present)
//	    head.json        its head, byte for byte (when present)
//	    quarantine.txt   time, writer verb, binary version, error, digests
//
// written whole into a temp directory beside it and published by one
// rename, so no reader sees a partial set; an identical refusal (the read
// path refuses on every tick) is a no-op, a different one takes the next
// k. Everything is created relative to one descriptor of the quarantine
// directory, opened without following links, and every file is opened
// O_NOFOLLOW: a planted link anywhere is a named copy failure, never a
// copy through it. The files are evidence: never sealed, never read as a
// ledger, never deleted by any verb.

// QuarantineDir is the quarantine directory under an AMQ session root:
// <root>/extensions/amq-squad/quarantine.
func QuarantineDir(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "quarantine")
}

// BinaryVersion is the version the quarantine's sidecar records. "" falls
// back to the build's module version (runtime/debug) with its vcs settings.
// The release build sets main.version through ldflags, which this package
// cannot see; a later brief threads it here from the CLI.
var BinaryVersion string

// quarantineSeqUnknown names the quarantine of a document whose head does
// not decode (no seq to name it by).
const quarantineSeqUnknown = "unknown"

// quarantineSlots bounds the slots of one seq (a runaway edit loop cannot
// fill the disk).
const quarantineSlots = 100

// The files of a slot, and the prefix of a copy in progress (a leftover
// of a crash is listed in the next sidecar, never reused, never removed).
const (
	quarantineBodyFile = "body.json"
	quarantineHeadFile = "head.json"
	quarantineSidecar  = "quarantine.txt"
	quarantineTempPre  = ".copying-"
)

// quarantineBase is the stem of a seat's slots for one seq:
// commits.<handle>.seq<N>.
func quarantineBase(handle, seq string) string { return "commits." + handle + ".seq" + seq }

// ledgerRaw is what readLedger read and checked, byte for byte: the body
// and the head with their presence, and the note of one that could not be
// read as a regular file (a symlink: refused, never copied through). The
// quarantine copies these and never opens the live files (Codex's P1 on
// #893): between the check and the copy a concurrent writer can replace the
// refused body, and the alarm must archive what it refused, not what came
// after.
type ledgerRaw struct {
	body, head         []byte
	hasBody, hasHead   bool
	bodyNote, headNote string
}

// quarantine publishes the refused document (raw: the bytes the check
// refused) as one slot of QuarantineDir and returns the refusal extended
// with the slot's path. The refusal stands whatever happens here: a copy
// that cannot be made is named in the error as a failed copy, never as a
// quarantine.
func quarantine(root, handle, verb string, refusal error, raw ledgerRaw) error {
	dir := QuarantineDir(root)
	d, err := openQuarantineDir(dir)
	if err != nil {
		return fmt.Errorf("%w; the quarantine copy failed: %v", refusal, err)
	}
	defer d.close()
	slot, err := quarantineCopy(d, quarantineBase(handle, headSeq(raw.head)), handle, verb, refusal, raw)
	if err != nil {
		return fmt.Errorf("%w; the quarantine copy failed: %v", refusal, err)
	}
	return fmt.Errorf("%w; the refused document is quarantined at %s", refusal, filepath.Join(dir, slot))
}

// openQuarantineDir creates the quarantine directory when absent and opens
// it by one descriptor (O_DIRECTORY, O_NOFOLLOW): the descriptor must be
// a directory of mode 0700 owned by this process's user (checked on the
// descriptor, never on a path that can be swapped under the check).
func openQuarantineDir(dir string) (slotDir, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	d, err := openDirNoFollow(dir)
	if err != nil {
		return nil, err
	}
	fi, err := d.stat()
	if err != nil {
		d.close()
		return nil, err
	}
	if !fi.IsDir() {
		d.close()
		return nil, fmt.Errorf("the quarantine directory %s is not a directory", dir)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o700 {
		d.close()
		return nil, fmt.Errorf("the quarantine directory %s has mode %04o, want 0700", dir, fi.Mode().Perm())
	}
	if err := ownedByCaller(dir, fi); err != nil {
		d.close()
		return nil, err
	}
	return d, nil
}

// slotDir is a directory held by descriptor (or, off unix, by path with
// link checks): every name is relative to it and nothing it opens follows
// a link.
type slotDir interface {
	stat() (fs.FileInfo, error)
	names() ([]string, error)
	mkdir(name string) error
	create(name string, data []byte) error  // exclusive, 0600, fsynced
	read(name string) ([]byte, bool, error) // a regular file's bytes; false when absent
	open(name string) (slotDir, error)      // a subdirectory, never through a link
	rename(from, to string) error
	remove(name string, isDir bool) error
	sync() error
	close() error
}

// slotSet is the set of one slot, by file name.
type slotSet map[string][]byte

// quarantineCopy writes the refusal's set into a fresh temp directory and
// publishes it by rename as base, or base.k for the first k whose slot is
// free, or returns the slot that already holds the identical set. A slot
// that exists but is not a published set (a planted file, link or empty
// directory, a set without its sidecar) is a failure: nothing is renamed
// over it, nothing is compared through it.
func quarantineCopy(d slotDir, base, handle, verb string, refusal error, raw ledgerRaw) (string, error) {
	names, err := d.names()
	if err != nil {
		return "", err
	}
	var leftovers []string
	for _, n := range names {
		if strings.HasPrefix(n, quarantineTempPre) {
			leftovers = append(leftovers, n)
		}
	}
	sort.Strings(leftovers)
	set := slotSet{quarantineSidecar: sidecar(handle, verb, refusal, raw, leftovers)}
	if raw.hasBody {
		set[quarantineBodyFile] = raw.body
	}
	if raw.hasHead {
		set[quarantineHeadFile] = raw.head
	}
	tmp, err := writeTempSlot(d, set)
	if err != nil {
		return "", err
	}
	drop := func() { removeSlot(d, tmp, set) }
	for k := 1; k <= quarantineSlots; k++ {
		slot := base
		if k > 1 {
			slot += "." + strconv.Itoa(k)
		}
		for attempt := 0; attempt < 2; attempt++ {
			same, present, err := sameSlot(d, slot, set)
			if err != nil {
				drop()
				return "", err
			}
			if present {
				if same {
					drop()
					return slot, nil // an identical refusal: the published slot stands
				}
				break // a different set: the next k
			}
			err = d.rename(tmp, slot)
			if err == nil {
				if err := d.sync(); err != nil {
					return "", err
				}
				return slot, nil
			}
			if !slotTaken(err) {
				drop()
				return "", err
			}
			// Taken between the look and the rename (a concurrent
			// refuser): look again, once.
		}
	}
	drop()
	return "", fmt.Errorf("%s has %d slots already", base, quarantineSlots)
}

// writeTempSlot writes set into a fresh temp directory beside the slots,
// each file fsynced, and returns its name.
func writeTempSlot(d slotDir, set slotSet) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	tmp := quarantineTempPre + hex.EncodeToString(b[:])
	if err := d.mkdir(tmp); err != nil {
		return "", err
	}
	for _, name := range slotFiles() {
		data, ok := set[name]
		if !ok {
			continue
		}
		if err := d.create(tmp+"/"+name, data); err != nil {
			removeSlot(d, tmp, set)
			return "", err
		}
	}
	if err := d.sync(); err != nil {
		removeSlot(d, tmp, set)
		return "", err
	}
	return tmp, nil
}

func slotFiles() []string {
	return []string{quarantineBodyFile, quarantineHeadFile, quarantineSidecar}
}

// removeSlot drops a temp directory this process made (its known files,
// then the directory); a failure leaves a leftover the next sidecar lists.
func removeSlot(d slotDir, name string, set slotSet) {
	for file := range set {
		_ = d.remove(name+"/"+file, false)
	}
	_ = d.remove(name, true)
}

// sameSlot reports whether slot is present, and whether it holds set: the
// same files present with the same bytes, the sidecar compared without its
// time and leftover lines. A slot that is present but is not a published
// set (no sidecar, a link, a file) is an error.
func sameSlot(d slotDir, slot string, set slotSet) (same, present bool, err error) {
	s, err := d.open(slot)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, true, err
	}
	defer s.close()
	same = true
	for _, name := range slotFiles() {
		got, ok, err := s.read(name)
		if err != nil {
			return false, true, err
		}
		want, wantOK := set[name]
		if name == quarantineSidecar {
			if !ok {
				return false, true, fmt.Errorf("%s is not a quarantine slot (no %s)", slot, quarantineSidecar)
			}
			got, want = sidecarStable(got), sidecarStable(want)
		}
		if ok != wantOK || !bytes.Equal(got, want) {
			same = false
		}
	}
	return same, true, nil
}

// sidecarStable is a sidecar without the lines that differ between two
// identical refusals: the time, the leftovers seen, and the writer (a
// prepare and then a writer refusing the same bytes with the same error
// are one refusal; the slot records the first's verb and time).
func sidecarStable(b []byte) []byte {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "time: ") || strings.HasPrefix(line, "leftover: ") || strings.HasPrefix(line, "writer: ") {
			continue
		}
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

// headSeq is the seq a head file names, read without verifying it (the
// head's tag may be what failed); quarantineSeqUnknown when it does not
// decode or is absent.
func headSeq(raw []byte) string {
	var h struct {
		Seq *int `json:"seq"`
	}
	if raw == nil || json.Unmarshal(raw, &h) != nil || h.Seq == nil {
		return quarantineSeqUnknown
	}
	return strconv.Itoa(*h.Seq)
}

// sidecar is the quarantine's record: when, which writer, which binary, the
// error, the copies with their digests (or why one is absent), and the
// leftovers of copies that did not finish.
func sidecar(handle, verb string, refusal error, raw ledgerRaw, leftovers []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "amq-squad commits ledger quarantine\n")
	fmt.Fprintf(&b, "time: %s\n", time.Now().UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "seat: %s\n", handle)
	fmt.Fprintf(&b, "writer: %s\n", verb)
	fmt.Fprintf(&b, "binary: %s\n", binaryVersion())
	fmt.Fprintf(&b, "error: %s\n", strings.ReplaceAll(refusal.Error(), "\n", " "))
	switch {
	case raw.hasBody:
		fmt.Fprintf(&b, "body: %s sha256 %s\n", quarantineBodyFile, digestOf(raw.body))
	case raw.bodyNote != "":
		fmt.Fprintf(&b, "body: absent (%s)\n", raw.bodyNote)
	default:
		fmt.Fprintf(&b, "body: absent\n")
	}
	switch {
	case raw.hasHead:
		fmt.Fprintf(&b, "head: %s sha256 %s\n", quarantineHeadFile, digestOf(raw.head))
	case raw.headNote != "":
		fmt.Fprintf(&b, "head: absent (%s)\n", raw.headNote)
	default:
		fmt.Fprintf(&b, "head: absent\n")
	}
	for _, l := range leftovers {
		fmt.Fprintf(&b, "leftover: %s (a copy that did not finish; kept, never reused)\n", l)
	}
	return []byte(b.String())
}

func digestOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// binaryVersion is BinaryVersion, else the build's module version with the
// vcs revision and the Go version (what runtime/debug knows of this binary).
func binaryVersion() string {
	if BinaryVersion != "" {
		return BinaryVersion
	}
	parts := []string{"(unknown)"}
	if info, ok := debug.ReadBuildInfo(); ok {
		parts[0] = info.Main.Version
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" || s.Key == "vcs.modified" {
				parts = append(parts, s.Key+"="+s.Value)
			}
		}
	}
	return strings.Join(append(parts, runtime.Version()), " ")
}
