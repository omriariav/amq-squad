package seatcommit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// 3.2.0-0c-A: a tamper alarm preserves its evidence. The rows run under a
// fixture root in t.TempDir (rule 18), never a real session root.

// ledgerWriter is one writer of the fixture set, driven through the
// seatcommit function its caller uses. refused is true when the writer
// refused as ledger_tampered; detail is its error text.
type ledgerWriter struct {
	name  string
	verb  string
	write func(t *testing.T, f *fixture) (detail string, refused bool)
}

func errRow(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	return err.Error(), errors.Is(err, sealchain.ErrTampered)
}

// quarantineWriters is the fixture set of the A1 row: every function the
// writers of the commit ledger call (the enumeration in
// quarantine_callers_test.go holds this table to the source).
var quarantineWriters = []ledgerWriter{
	{"RecordBase", "seat commit --base", func(t *testing.T, f *fixture) (string, bool) {
		_, err := RecordBase(f.root, f.handle, f.wt, f.branch, f.base, f.now)
		return errRow(err)
	}},
	{"RecordWorktreeBase", "launch record base", func(t *testing.T, f *fixture) (string, bool) {
		_, err := RecordWorktreeBase(gitBin(t), f.root, f.handle, f.wt, f.branch, f.target(), f.now)
		return errRow(err)
	}},
	{"RecordTarget", "seat commit --base", func(t *testing.T, f *fixture) (string, bool) {
		_, err := RecordTarget(f.root, f.handle, f.target())
		return errRow(err)
	}},
	{"ArchiveAttempt", "archive attempt", func(t *testing.T, f *fixture) (string, bool) {
		_, err := ArchiveAttempt(f.root, f.handle, gitBin(t), nil, "t1", 1, f.now)
		return errRow(err)
	}},
	{"CleanSeat", "clean seat", func(t *testing.T, f *fixture) (string, bool) {
		_, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now)
		return errRow(err)
	}},
	{"StartTask", "start task", func(t *testing.T, f *fixture) (string, bool) {
		tk, err := f.store.Add(task.AddInput{Title: "the task", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: f.main}, f.now)
		if err != nil {
			t.Fatal(err)
		}
		if tk, err = f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now); err != nil {
			t.Fatal(err)
		}
		return errRow(StartTask(f.root, f.handle, gitBin(t), nil, tk, "", f.now))
	}},
	{"Run", "commit step", func(t *testing.T, f *fixture) (string, bool) {
		write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// quarantine\n")
		res := f.run(f.reported("cmd/"))
		return res.Detail, !res.OK && !res.Deferred && res.ReasonCode == ReasonLedgerTampered
	}},
}

// tamperBase hand-edits the ledger's base to a run of one digit (the JSON
// stays valid, the tag fails) and returns the bytes on disk. from is the
// text the edit replaces: the base, or an earlier edit's run.
func tamperBase(t *testing.T, f *fixture, from string, with byte) []byte {
	t.Helper()
	p := ledgerPath(f.root, f.handle)
	orig, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	edited := []byte(strings.Replace(string(orig), from, strings.Repeat(string(with), len(from)), 1))
	if bytes.Equal(edited, orig) {
		t.Fatalf("%q is not in the ledger", from)
	}
	if err := os.WriteFile(p, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	return edited
}

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// headSeqOf decodes the head's seq on its own (the test's reading, not the
// code's).
func headSeqOf(t *testing.T, f *fixture) (seq string, raw []byte) {
	t.Helper()
	raw = readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	body := raw
	if split, _, err := sealchain.SplitRaw(raw); err == nil {
		body = split
	}
	var h struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(body, &h); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(h.Seq), raw
}

func TestCopiedHeadSequenceMustLeaveAPlausibleSuccessor(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`{"seq":0}`, quarantineSeqUnknown},
		{`{"seq":-5}`, quarantineSeqUnknown},
		{`{"seq":9007199254740990}`, "9007199254740990"},
		{`{"seq":9007199254740991}`, quarantineSeqUnknown},
		{`{"seq":9007199254740992}`, quarantineSeqUnknown},
	} {
		if got := headSeq([]byte(tc.raw)); got != tc.want {
			t.Errorf("headSeq(%s) = %s, want %s", tc.raw, got, tc.want)
		}
	}
}

// slotOf is a seat's quarantine slot directory for a seq (k 1 is the
// first slot, commits.<h>.seq<N>; k 2 is commits.<h>.seq<N>.2).
func slotOf(f *fixture, seq string, k int) string {
	slot := filepath.Join(QuarantineDir(f.root), "commits."+f.handle+".seq"+seq)
	if k > 1 {
		slot += "." + strconv.Itoa(k)
	}
	return slot
}

// quarantinePaths are the first slot's files for a seq.
func quarantinePaths(f *fixture, seq string) (body, head, side string) {
	slot := slotOf(f, seq, 1)
	return filepath.Join(slot, quarantineBodyFile), filepath.Join(slot, quarantineHeadFile), filepath.Join(slot, quarantineSidecar)
}

// namesSlot reports whether a refusal names exactly this slot ("quarantined
// at <slot>" not followed by a further ".k").
func namesSlot(detail, slot string) bool {
	return regexp.MustCompile(`quarantined at ` + regexp.QuoteMeta(slot) + `(?:$|[^.0-9])`).MatchString(detail)
}

func sha(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// slotNames are the entries of the quarantine directory (slots and
// leftovers), sorted.
func slotNames(t *testing.T, f *fixture) []string {
	t.Helper()
	ents, err := os.ReadDir(QuarantineDir(f.root))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

// A1: a tampered document followed by each writer of the fixture set leaves
// the quarantine's body copy byte-identical to the refused body (sha256
// equal) and the head copy byte-identical to the head; the writer refuses
// with the tamper error, which names the slot; the document and head are
// unchanged after.
func TestTamperedLedgerIsQuarantinedByEveryWriter(t *testing.T) {
	for _, w := range quarantineWriters {
		t.Run(w.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			f := newFixture(t)
			tampered := tamperBase(t, f, f.base, '0')
			seq, head := headSeqOf(t, f)
			bodyQ, headQ, sideQ := quarantinePaths(f, seq)
			detail, refused := w.write(t, f)
			if !refused {
				t.Fatalf("%s on a tampered ledger did not refuse as ledger_tampered: %q", w.name, detail)
			}
			if !namesSlot(detail, slotOf(f, seq, 1)) {
				t.Fatalf("%s's error does not name the quarantine %s: %q", w.name, slotOf(f, seq, 1), detail)
			}
			if got := readFile(t, bodyQ); sha(got) != sha(tampered) {
				t.Fatalf("the quarantine body is not the refused body: sha256 %s, want %s", sha(got), sha(tampered))
			}
			if got := readFile(t, headQ); !bytes.Equal(got, head) {
				t.Fatalf("the quarantine head is not the head: %q", got)
			}
			side := string(readFile(t, sideQ))
			for _, want := range []string{"writer: " + w.verb + "\n", "binary: ", "time: ", "error: commit ledger " + f.handle + ": ledger_tampered", "body: body.json sha256 " + sha(tampered)} {
				if !strings.Contains(side, want) {
					t.Fatalf("the sidecar lacks %q:\n%s", want, side)
				}
			}
			if got := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(got, tampered) {
				t.Fatalf("%s moved the document after refusing", w.name)
			}
			if _, got := headSeqOf(t, f); !bytes.Equal(got, head) {
				t.Fatalf("%s moved the head after refusing", w.name)
			}
			if names := slotNames(t, f); len(names) != 1 {
				t.Fatalf("quarantine entries: %v, want the one slot", names)
			}
		})
	}
}

// A2: an identical second refusal of the same seq keeps the first slot:
// the error names the same slot, its body, head and sidecar are the first
// refusal's, and no second slot or leftover appears (the read path refuses
// on every tick; one copy).
func TestIdenticalSecondRefusalKeepsTheFirstCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	first := tamperBase(t, f, f.base, '0')
	seq, _ := headSeqOf(t, f)
	bodyQ, headQ, sideQ := quarantinePaths(f, seq)
	if _, err := RecordTarget(f.root, f.handle, f.target()); !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("first refusal: %v", err)
	}
	keep := [3][]byte{readFile(t, bodyQ), readFile(t, headQ), readFile(t, sideQ)}
	_, err := RecordTarget(f.root, f.handle, f.target())
	if !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slotOf(f, seq, 1)) {
		t.Fatalf("second refusal: %v", err)
	}
	for i, p := range []string{bodyQ, headQ, sideQ} {
		if got := readFile(t, p); !bytes.Equal(got, keep[i]) {
			t.Fatalf("the second refusal changed %s", p)
		}
	}
	if got := readFile(t, bodyQ); !bytes.Equal(got, first) {
		t.Fatal("the quarantine body is not the first refused body")
	}
	if names := slotNames(t, f); len(names) != 1 {
		t.Fatalf("quarantine entries: %v, want the one slot", names)
	}
}

// A8: a second, different refused body under one head seq takes the next
// slot (commits.<h>.seq<N>.2 with its own head copy and sidecar), the
// first slot untouched; the same second body again is a no-op. Two
// different bodies give two slots, the same body twice gives one.
func TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	first := tamperBase(t, f, f.base, '0')
	seq, head := headSeqOf(t, f)
	bodyQ, headQ, sideQ := quarantinePaths(f, seq)
	if _, err := RecordTarget(f.root, f.handle, f.target()); !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slotOf(f, seq, 1)) {
		t.Fatalf("first refusal: %v", err)
	}
	keep := [3][]byte{readFile(t, bodyQ), readFile(t, headQ), readFile(t, sideQ)}
	second := tamperBase(t, f, strings.Repeat("0", len(f.base)), '1')
	if bytes.Equal(second, first) {
		t.Fatal("the second edit equals the first")
	}
	slot2 := slotOf(f, seq, 2)
	for i := 0; i < 2; i++ {
		_, err := RecordTarget(f.root, f.handle, f.target())
		if !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slot2) {
			t.Fatalf("refusal %d of the second body does not name the second slot: %v", i+1, err)
		}
	}
	for i, p := range []string{bodyQ, headQ, sideQ} {
		if got := readFile(t, p); !bytes.Equal(got, keep[i]) {
			t.Fatalf("the second body's refusal changed the first slot's %s", p)
		}
	}
	if got := readFile(t, filepath.Join(slot2, quarantineBodyFile)); !bytes.Equal(got, second) {
		t.Fatal("the second slot's body is not the second refused body")
	}
	if got := readFile(t, filepath.Join(slot2, quarantineHeadFile)); !bytes.Equal(got, head) {
		t.Fatal("the second slot's head copy is not the head")
	}
	if side := string(readFile(t, filepath.Join(slot2, quarantineSidecar))); !strings.Contains(side, "body: body.json sha256 "+sha(second)) {
		t.Fatalf("the second slot's sidecar does not name its body:\n%s", side)
	}
	if names := slotNames(t, f); len(names) != 2 {
		t.Fatalf("quarantine entries: %v, want two slots (not three)", names)
	}
}

// A9: something planted where the slot would land is a named copy failure,
// never "quarantined at", and the refusal stands: an empty directory (not
// a published set: no sidecar) and a regular file. Nothing is renamed
// over it, no leftover temp directory remains.
func TestPlantedSlotIsAQuarantineFailureNotACopy(t *testing.T) {
	plant := func(t *testing.T, mk func(slot string) error, want string) {
		t.Setenv("HOME", t.TempDir())
		f := newFixture(t)
		tamperBase(t, f, f.base, '0')
		seq, _ := headSeqOf(t, f)
		slot := slotOf(f, seq, 1)
		if err := os.MkdirAll(QuarantineDir(f.root), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := mk(slot); err != nil {
			t.Fatal(err)
		}
		_, err := RecordTarget(f.root, f.handle, f.target())
		if !errors.Is(err, sealchain.ErrTampered) {
			t.Fatalf("not ledger_tampered: %v", err)
		}
		if !strings.Contains(err.Error(), "the quarantine copy failed: "+want) || strings.Contains(err.Error(), "quarantined at") {
			t.Fatalf("the error does not say the copy failed (%q): %v", want, err)
		}
		if names := slotNames(t, f); len(names) != 1 {
			t.Fatalf("quarantine entries: %v, want the planted one alone (no leftover, nothing renamed)", names)
		}
	}
	t.Run("empty directory", func(t *testing.T) {
		plant(t, func(slot string) error { return os.Mkdir(slot, 0o700) }, filepath.Base(slotOf(&fixture{handle: "s1"}, "1", 1))+" is not a quarantine slot (no quarantine.txt)")
	})
	t.Run("regular file", func(t *testing.T) {
		plant(t, func(slot string) error { return os.WriteFile(slot, []byte("{}"), 0o600) }, "")
	})
}

// A10: the copy is of the bytes the check refused. A body swapped on disk
// between the refusal and the copy (the afterLedgerRefusal seam stands in
// for a concurrent writer) is not what gets quarantined: the slot holds
// the refused bytes, its sidecar their digest; the swapped body stays on
// disk (the writer moves nothing).
func TestQuarantineHoldsTheRefusedBytesNotALaterSwap(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	refused := tamperBase(t, f, f.base, '0')
	seq, head := headSeqOf(t, f)
	bodyQ, headQ, sideQ := quarantinePaths(f, seq)
	var swapped []byte
	afterLedgerRefusal = func(root, handle string) {
		if root == f.root && handle == f.handle && swapped == nil {
			swapped = tamperBase(t, f, strings.Repeat("0", len(f.base)), '1')
		}
	}
	t.Cleanup(func() { afterLedgerRefusal = nil })
	_, err := RecordTarget(f.root, f.handle, f.target())
	if !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slotOf(f, seq, 1)) {
		t.Fatalf("refusal: %v", err)
	}
	if swapped == nil || bytes.Equal(swapped, refused) {
		t.Fatal("the swap did not happen between the refusal and the copy")
	}
	if got := readFile(t, bodyQ); !bytes.Equal(got, refused) {
		t.Fatalf("the quarantine holds the swapped body, not the refused one: sha256 %s, refused %s, swapped %s", sha(got), sha(refused), sha(swapped))
	}
	if got := readFile(t, headQ); !bytes.Equal(got, head) {
		t.Fatal("the quarantine head is not the checked head")
	}
	if side := string(readFile(t, sideQ)); !strings.Contains(side, "sha256 "+sha(refused)) {
		t.Fatalf("the sidecar's digest is not the refused body's:\n%s", side)
	}
	if got := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(got, swapped) {
		t.Fatal("the writer moved the swapped document")
	}
}

// A11: a ledger that is a symlink (here to the session seed) is a refusal
// in its own right: ledger_tampered naming the link, nothing read or
// copied through it. The slot holds the head copy and a sidecar that names
// the symlink; no file of the slot carries the seed's bytes.
func TestLedgerSymlinkedToTheSeedIsRefusedAndNeverCopied(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	seq, head := headSeqOf(t, f)
	p := ledgerPath(f.root, f.handle)
	seed := readFile(t, sealchain.SeedPath(f.root))
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(sealchain.SeedPath(f.root), p); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := RecordTarget(f.root, f.handle, f.target())
	if !errors.Is(err, sealchain.ErrTampered) || !strings.Contains(err.Error(), "ledger_tampered: "+p+" is a symlink") {
		t.Fatalf("a symlinked ledger: %v", err)
	}
	slot := slotOf(f, seq, 1)
	if !namesSlot(err.Error(), slot) {
		t.Fatalf("the refusal does not name the slot: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(slot, quarantineBodyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a body was copied through the link")
	}
	if got := readFile(t, filepath.Join(slot, quarantineHeadFile)); !bytes.Equal(got, head) {
		t.Fatal("the head copy is not the head")
	}
	side := readFile(t, filepath.Join(slot, quarantineSidecar))
	if !strings.Contains(string(side), "body: absent ("+p+" is a symlink (not copied))") {
		t.Fatalf("the sidecar does not name the symlink:\n%s", side)
	}
	ents, _ := os.ReadDir(slot)
	for _, e := range ents {
		if b := readFile(t, filepath.Join(slot, e.Name())); bytes.Contains(b, seed) {
			t.Fatalf("%s carries the seed's bytes", e.Name())
		}
	}
	if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the planted link was moved")
	}
}

// A12: a body-absent refusal (the ledger deleted under a head past link 0)
// and then a tampered body under the same head are two distinct refusals:
// two slots, the first with no body file, the second with the tampered
// body.
func TestAbsentBodyThenTamperedBodyUnderOneSeqGiveTwoSlots(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	seq, head := headSeqOf(t, f)
	p := ledgerPath(f.root, f.handle)
	orig := readFile(t, p)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	_, err := RecordTarget(f.root, f.handle, f.target())
	if !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slotOf(f, seq, 1)) {
		t.Fatalf("the absent body: %v", err)
	}
	slot1 := slotOf(f, seq, 1)
	if _, err := os.Lstat(filepath.Join(slot1, quarantineBodyFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("an absent body got a body file")
	}
	if got := readFile(t, filepath.Join(slot1, quarantineHeadFile)); !bytes.Equal(got, head) {
		t.Fatal("the first slot's head copy is not the head")
	}
	if side := string(readFile(t, filepath.Join(slot1, quarantineSidecar))); !strings.Contains(side, "body: absent\n") {
		t.Fatalf("the first sidecar does not say the body is absent:\n%s", side)
	}
	if err := os.WriteFile(p, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	tampered := tamperBase(t, f, f.base, '0')
	_, err = RecordTarget(f.root, f.handle, f.target())
	slot2 := slotOf(f, seq, 2)
	if !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slot2) {
		t.Fatalf("the tampered body does not take the second slot: %v", err)
	}
	if got := readFile(t, filepath.Join(slot2, quarantineBodyFile)); !bytes.Equal(got, tampered) {
		t.Fatal("the second slot's body is not the tampered body")
	}
	if names := slotNames(t, f); len(names) != 2 {
		t.Fatalf("quarantine entries: %v, want two slots", names)
	}
}

// A13: two concurrent identical refusers (two readers of one tampered
// document at once, through ReadLedgerAs, no lock) publish one slot: the
// loser's rename finds the slot taken, compares, finds its own set, and
// drops its temp directory; no leftover, no second slot.
func TestConcurrentIdenticalRefusersPublishOneSlot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tampered := tamperBase(t, f, f.base, '0')
	seq, _ := headSeqOf(t, f)
	slot := slotOf(f, seq, 1)
	const n = 8
	start := make(chan struct{})
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			<-start
			_, err := ReadLedgerAs(f.root, f.handle, "read")
			errs <- err
		}()
	}
	close(start)
	for i := 0; i < n; i++ {
		if err := <-errs; !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slot) {
			t.Fatalf("refuser: %v", err)
		}
	}
	if names := slotNames(t, f); len(names) != 1 || names[0] != filepath.Base(slot) {
		t.Fatalf("quarantine entries: %v, want the one slot and no leftover", names)
	}
	if got := readFile(t, filepath.Join(slot, quarantineBodyFile)); !bytes.Equal(got, tampered) {
		t.Fatal("the slot's body is not the refused body")
	}
}

// The 0e head-swap row: a same-UID writer replaces the live head after the
// reader captured it but before verification. The check and quarantine use
// the captured bytes, never a second read of the replacement.
func TestRawHeadSwapQuarantinesTheHeadThatWasChecked(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	seq, checkedHead := headSeqOf(t, f)
	headPath := sealchain.HeadPath(f.root, ledgerHeadName(f.handle))
	replacement := []byte("replacement head\n")
	afterLedgerHeadRead = func(root, handle string) {
		afterLedgerHeadRead = nil
		if err := os.WriteFile(headPath, replacement, 0o600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { afterLedgerHeadRead = nil })
	_, err := ReadLedgerAs(f.root, f.handle, "head-swap row")
	if !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("head swap = %v", err)
	}
	var q *QuarantinedError
	if !errors.As(err, &q) || q.Seq != seq || q.Slot != slotOf(f, seq, 1) {
		t.Fatalf("head swap quarantine names the wrong captured bytes: %+v, %v", q, err)
	}
	_, quarantinedHead, _ := quarantinePaths(f, seq)
	if got := readFile(t, quarantinedHead); !bytes.Equal(got, checkedHead) {
		t.Fatalf("quarantine copied the replacement head: got %q, checked %q", got, checkedHead)
	}
	if got := readFile(t, headPath); !bytes.Equal(got, replacement) {
		t.Fatalf("live replacement changed: %q", got)
	}
}

// A14: the staged directory swapped for a link after its mkdir (the
// afterTempMkdir seam stands in for a same-user process) is a named copy
// failure: the staged writes go through the temp directory's own
// descriptor, opened O_DIRECTORY|O_NOFOLLOW, so nothing is written at the
// link's target, the error says the copy failed, and no slot is named.
func TestStagedDirectorySwappedForALinkIsAQuarantineFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	target := t.TempDir()
	var swapped string
	afterTempMkdir = func(dir, tmp string) {
		if swapped != "" {
			return
		}
		p := filepath.Join(dir, tmp)
		if err := os.Remove(p); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink(target, p); err != nil {
			t.Error(err)
			return
		}
		swapped = p
	}
	t.Cleanup(func() { afterTempMkdir = nil })
	_, err := RecordTarget(f.root, f.handle, f.target())
	if !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("not ledger_tampered: %v", err)
	}
	if swapped == "" {
		t.Fatal("the swap did not happen")
	}
	if want := "the quarantine copy failed: the staged copy " + filepath.Base(swapped) + ": " + swapped + " is a symlink"; !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "quarantined at") {
		t.Fatalf("the error does not say the copy failed (%q): %v", want, err)
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Fatalf("the link's target got %d entries: the staged writes followed the swap", len(ents))
	}
	if fi, err := os.Lstat(swapped); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the planted link was removed or replaced")
	}
}

// A15: the staged directory is fsynced before it is renamed into place
// and the root is fsynced after the rename: a crash after the publish
// cannot leave a slot missing files. The quarantineTrace seam records the
// directory fsyncs (the staged directory's, the root's) and the renameat,
// in order; the per-file fsyncs of the staged files are not traced.
func TestStagedDirectoryIsFsyncedBeforeItsRename(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	seq, _ := headSeqOf(t, f)
	var ops []string
	quarantineTrace = func(op, name string) { ops = append(ops, op+" "+name) }
	t.Cleanup(func() { quarantineTrace = nil })
	_, err := RecordTarget(f.root, f.handle, f.target())
	if !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slotOf(f, seq, 1)) {
		t.Fatalf("refusal: %v", err)
	}
	rename, stagedSync, rootSyncAfter := -1, -1, -1
	var tmp string
	for i, op := range ops {
		if strings.HasPrefix(op, "renameat ") && rename < 0 {
			rename = i
			tmp = strings.TrimPrefix(strings.SplitN(strings.TrimPrefix(op, "renameat "), " -> ", 2)[0], "")
		}
	}
	if rename < 0 {
		t.Fatalf("no renameat in the trace: %v", ops)
	}
	for i, op := range ops {
		if op == "fsync "+tmp && i < rename && stagedSync < 0 {
			stagedSync = i
		}
		if op == "fsync "+QuarantineDir(f.root) && i > rename {
			rootSyncAfter = i
		}
	}
	if stagedSync < 0 {
		t.Fatalf("the staged directory %s was not fsynced before its rename: %v", tmp, ops)
	}
	if rootSyncAfter < 0 {
		t.Fatalf("the root was not fsynced after the rename: %v", ops)
	}
}

// A7: a planted link never passes as a copy. A symlink where the slot
// would land, or a symlink where the quarantine directory should be, makes
// the copy a named failure ("the quarantine copy failed: ... is a
// symlink"), never a "quarantined at"; the refusal stands; nothing lands at
// the link's target.
func TestPlantedSymlinkIsAQuarantineFailureNotACopy(t *testing.T) {
	t.Run("slot path", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		f := newFixture(t)
		tamperBase(t, f, f.base, '0')
		seq, _ := headSeqOf(t, f)
		slot := slotOf(f, seq, 1)
		target := t.TempDir()
		if err := os.MkdirAll(QuarantineDir(f.root), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, slot); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		_, err := RecordTarget(f.root, f.handle, f.target())
		if !errors.Is(err, sealchain.ErrTampered) {
			t.Fatalf("not ledger_tampered: %v", err)
		}
		if want := "the quarantine copy failed: " + slot + " is a symlink"; !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "quarantined at") {
			t.Fatalf("the error does not say the copy failed (%q): %v", want, err)
		}
		if ents, _ := os.ReadDir(target); len(ents) != 0 {
			t.Fatalf("the link's target got %d entries", len(ents))
		}
		if names := slotNames(t, f); len(names) != 1 {
			t.Fatalf("quarantine entries: %v, want the planted link alone", names)
		}
	})
	t.Run("directory", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		f := newFixture(t)
		tamperBase(t, f, f.base, '0')
		elsewhere := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(QuarantineDir(f.root)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, QuarantineDir(f.root)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		_, err := RecordTarget(f.root, f.handle, f.target())
		if !errors.Is(err, sealchain.ErrTampered) {
			t.Fatalf("not ledger_tampered: %v", err)
		}
		if want := "the quarantine copy failed: the quarantine directory " + QuarantineDir(f.root) + " is a symlink"; !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "quarantined at") {
			t.Fatalf("the error does not say the copy failed (%q): %v", want, err)
		}
		if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
			t.Fatalf("the link's target directory got %d entries", len(ents))
		}
	})
}

// A3: a head that does not decode quarantines under seq unknown: the body
// copy is the document as it is, the head copy is the undecodable head.
func TestUndecodableHeadQuarantinesUnderSeqUnknown(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	doc := readFile(t, ledgerPath(f.root, f.handle))
	headPath := sealchain.HeadPath(f.root, ledgerHeadName(f.handle))
	garbage := []byte("{not a head\n")
	if err := os.WriteFile(headPath, garbage, 0o600); err != nil {
		t.Fatal(err)
	}
	bodyQ, headQ, sideQ := quarantinePaths(f, "unknown")
	_, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now)
	if !errors.Is(err, sealchain.ErrTampered) || !namesSlot(err.Error(), slotOf(f, "unknown", 1)) {
		t.Fatalf("CleanSeat under an undecodable head: %v", err)
	}
	if got := readFile(t, bodyQ); !bytes.Equal(got, doc) {
		t.Fatal("the quarantine body is not the document")
	}
	if got := readFile(t, headQ); !bytes.Equal(got, garbage) {
		t.Fatalf("the quarantine head is not the head on disk: %q", got)
	}
	if side := string(readFile(t, sideQ)); !strings.Contains(side, "does not decode or its tag fails") {
		t.Fatalf("the sidecar lacks the error:\n%s", side)
	}
	if got := readFile(t, headPath); !bytes.Equal(got, garbage) {
		t.Fatal("the head was moved")
	}
}

// A4 (seatcommit's half): the refusing error is ledger_tampered and names
// the slot, from the read that refuses (ReadLedger) as from a writer. The
// scheduler's half, the prepare's seat_prepare_failed reason, is
// internal/schedproc's TestPrepareNamesTheQuarantineOfATamperedLedger.
func TestRefusalNamesTheQuarantinePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	seq, _ := headSeqOf(t, f)
	_, _, sideQ := quarantinePaths(f, seq)
	_, err := ReadLedgerAs(f.root, f.handle, "scheduler prepare")
	if !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("not ledger_tampered: %v", err)
	}
	if want := "; the refused document is quarantined at " + slotOf(f, seq, 1); !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("the error does not end with %q: %q", want, err.Error())
	}
	if side := string(readFile(t, sideQ)); !strings.Contains(side, "writer: scheduler prepare\n") {
		t.Fatalf("the sidecar lacks the prepare's verb:\n%s", side)
	}
}

// A5: the check runs under the lock. The first writer (ArchiveAttempt)
// holds the ledger lock, paused after its read and snapshot; the edit lands
// then; a second writer (RecordTarget) waits on the lock and neither checks
// nor quarantines while it waits. Released, the first writer's write
// re-reads the disk, refuses and quarantines (never a re-seal of its stale
// in-memory copy over the edited body); the second then sees the first's
// result: the same quarantine path, the document untouched, one copy.
func TestWriteCheckRunsUnderTheLedgerLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "first.go"), "package first\n")
	blocked, err := f.store.BlockWithPaths(tk.ID, f.handle, task.ReasonWidenRequested, "", task.RouteLead, []string{"first.go"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	releaseOnce := closeOnce(release)
	beforeArchiveAttemptWrite = func(string, string) {
		entered <- struct{}{}
		waitForArchiveRelease(release)
	}
	cleanupArchiveHook(t, releaseOnce)
	type outcome struct {
		err error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		_, err := ArchiveAttempt(f.root, f.handle, gitBin(t), nil, tk.ID, blocked.BlockAttempt, f.now)
		firstDone <- outcome{err}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the first writer did not reach its critical section")
	}
	// The edit lands while the first writer holds the lock, after its read.
	tampered := tamperBase(t, f, f.base, '0')
	seq, head := headSeqOf(t, f)
	bodyQ, _, _ := quarantinePaths(f, seq)
	slot := slotOf(f, seq, 1)
	secondDone := make(chan outcome, 1)
	go func() {
		_, err := RecordTarget(f.root, f.handle, f.target())
		secondDone <- outcome{err}
	}()
	select {
	case got := <-secondDone:
		t.Fatalf("the second writer ran while the first held the lock: %v", got.err)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := os.Stat(QuarantineDir(f.root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a quarantine appeared while the first writer held the lock: the check ran outside it")
	}
	releaseOnce()
	var first, second outcome
	select {
	case first = <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the first writer did not finish")
	}
	select {
	case second = <-secondDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the second writer did not finish")
	}
	if !errors.Is(first.err, sealchain.ErrTampered) || !namesSlot(fmt.Sprint(first.err), slot) {
		t.Fatalf("the first writer did not refuse the edited body at its write: %v", first.err)
	}
	if !errors.Is(second.err, sealchain.ErrTampered) || !namesSlot(second.err.Error(), slot) {
		t.Fatalf("the second writer did not see the first's result: %v", second.err)
	}
	if got := readFile(t, bodyQ); !bytes.Equal(got, tampered) {
		t.Fatal("the quarantine body is not the edited body")
	}
	if got := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(got, tampered) {
		t.Fatal("a writer re-sealed over the edited body")
	}
	if _, got := headSeqOf(t, f); !bytes.Equal(got, head) {
		t.Fatal("a writer moved the head")
	}
	if names := slotNames(t, f); len(names) != 1 {
		t.Fatalf("quarantine entries: %v, want the one slot", names)
	}
}

// E2: a valid raw seal with a higher schema and an unknown field is a
// newer_writer refusal. The original bytes stay put and no quarantine is
// created because this is compatibility, not tampering.
func TestVersionSkewIsNewerWriterAndNotQuarantined(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "first.go"), "package first\n")
	if _, err := f.store.BlockWithPaths(tk.ID, f.handle, task.ReasonWidenRequested, "", task.RouteLead, []string{"first.go"}, f.now); err != nil {
		t.Fatal(err)
	}
	if discarded, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now); err != nil || !discarded {
		t.Fatalf("CleanSeat = %v, %v", discarded, err)
	}
	p := ledgerPath(f.root, f.handle)
	raw := readFile(t, p)
	body, previous, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	entry := doc["discarded"].([]any)[0].(map[string]any)
	entry["attempt_note"] = "a field of a newer amq-squad"
	seed, ok, err := sealchain.ReadKey(f.root)
	if err != nil || !ok {
		t.Fatalf("the session seed: %v, %v", ok, err)
	}
	skewedBody, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var skewed []byte
	head := readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	_, err = sealchain.WriteRaw(f.root, ledgerHeadName(f.handle), seed, ledgerLabel, head, skewedBody, "v9.0.0", LedgerSchema+1, func(raw []byte) error {
		skewed = append([]byte(nil), raw...)
		return os.WriteFile(p, raw, 0o600)
	}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	seq, _ := headSeqOf(t, f)
	if seq != strconv.Itoa(previous.Seq+1) {
		t.Fatalf("the head is at seq %s, want %d", seq, previous.Seq+1)
	}
	_, err = ReadLedger(f.root, f.handle)
	if !errors.Is(err, sealchain.ErrNewerWriter) || errors.Is(err, sealchain.ErrTampered) || !strings.Contains(err.Error(), "v9.0.0") || !strings.Contains(err.Error(), "attempt_note") {
		t.Fatalf("ReadLedger on the newer document: %v", err)
	}
	if got := readFile(t, p); !bytes.Equal(got, skewed) {
		t.Fatal("the skewed document was moved")
	}
	if _, err := os.Stat(QuarantineDir(f.root)); !os.IsNotExist(err) {
		t.Fatalf("compatibility refusal created a quarantine: %v", err)
	}
}
