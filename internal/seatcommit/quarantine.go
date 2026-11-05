package seatcommit

import (
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
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// A tamper alarm preserves its evidence (3.2.0-0c-A). On 2026-11-05 the
// rc.1 scheduler refused the reviewer seat's commit ledger (ledger_tampered,
// link 150) on every tick for 65 minutes, and the refused seq-150 body was
// overwritten by the next writer's re-seal 2 to 4 s after the restart and
// lost: one seal per document, no history. Now the read that refuses copies
// the refused document and its head, byte for byte, under the session root
// before anything else can touch them, and every write re-reads the
// document on disk under the ledger lock first (writeLedger), so no writer
// of this binary re-seals over a body a reader refused.

// QuarantineDir is the quarantine directory under an AMQ session root:
// <root>/extensions/amq-squad/quarantine. Its files are evidence: never
// sealed, never read as a ledger, never deleted by any verb.
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

// quarantineBase is the stem of a seat's quarantine files for one seq:
// commits.<handle>.seq<N> (.json the body, .head.json the head, .txt the
// sidecar).
func quarantineBase(handle, seq string) string { return "commits." + handle + ".seq" + seq }

// quarantine copies the refused document of a seat's commit ledger and its
// head, byte for byte, into QuarantineDir, named by the head's seq (unknown
// when the head does not decode), once: each file is created exclusively
// (O_EXCL), so a second refusal of the same seq leaves the first copy. A
// sidecar carries the time, the writer verb, the binary version and the
// error text. It returns the refusal extended with the quarantine path (the
// body's; the head's when the body is absent). The refusal stands whatever
// happens here: a copy that cannot be written is named in the error, never
// a reason to pass.
func quarantine(root, handle, verb string, refusal error) error {
	body, berr := os.ReadFile(ledgerPath(root, handle))
	if berr != nil && !errors.Is(berr, fs.ErrNotExist) {
		return fmt.Errorf("%w; the refused document could not be read for quarantine: %v", refusal, berr)
	}
	head, herr := os.ReadFile(sealchain.HeadPath(root, ledgerHeadName(handle)))
	if herr != nil && !errors.Is(herr, fs.ErrNotExist) {
		return fmt.Errorf("%w; the refused document's head could not be read for quarantine: %v", refusal, herr)
	}
	dir := QuarantineDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("%w; the refused document could not be quarantined under %s: %v", refusal, dir, err)
	}
	stem := quarantineBase(handle, headSeq(head))
	bodyPath, headPath := filepath.Join(dir, stem+".json"), filepath.Join(dir, stem+".head.json")
	named := bodyPath
	var errs []error
	if berr == nil {
		errs = append(errs, writeOnce(bodyPath, body))
	} else {
		named = headPath
	}
	if herr == nil {
		errs = append(errs, writeOnce(headPath, head))
	} else if berr != nil {
		named = filepath.Join(dir, stem+".txt")
	}
	errs = append(errs, writeOnce(filepath.Join(dir, stem+".txt"), sidecar(handle, verb, refusal, stem, body, berr == nil, head, herr == nil)))
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w; the refused document could not be quarantined under %s: %v", refusal, dir, err)
	}
	return fmt.Errorf("%w; the refused document is quarantined at %s", refusal, named)
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

// writeOnce creates path exclusively with data, 0600, fsynced with its
// directory; an existing file is kept as it is (the first copy stands).
func writeOnce(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return atomicfile.SyncDir(filepath.Dir(path))
}

// sidecar is the quarantine's record: when, which writer, which binary, the
// error, and the copies with their digests.
func sidecar(handle, verb string, refusal error, stem string, body []byte, hasBody bool, head []byte, hasHead bool) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "amq-squad commits ledger quarantine\n")
	fmt.Fprintf(&b, "time: %s\n", time.Now().UTC().Format(time.RFC3339Nano))
	fmt.Fprintf(&b, "seat: %s\n", handle)
	fmt.Fprintf(&b, "writer: %s\n", verb)
	fmt.Fprintf(&b, "binary: %s\n", binaryVersion())
	fmt.Fprintf(&b, "error: %s\n", strings.ReplaceAll(refusal.Error(), "\n", " "))
	if hasBody {
		fmt.Fprintf(&b, "body: %s.json sha256 %s\n", stem, digestOf(body))
	} else {
		fmt.Fprintf(&b, "body: absent\n")
	}
	if hasHead {
		fmt.Fprintf(&b, "head: %s.head.json sha256 %s\n", stem, digestOf(head))
	} else {
		fmt.Fprintf(&b, "head: absent\n")
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
