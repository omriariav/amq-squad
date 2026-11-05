package seatcommit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	var h struct {
		Seq int `json:"seq"`
	}
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(h.Seq), raw
}

func quarantinePaths(f *fixture, seq string) (body, head, side string) {
	stem := filepath.Join(QuarantineDir(f.root), "commits."+f.handle+".seq"+seq)
	return stem + ".json", stem + ".head.json", stem + ".txt"
}

func sha(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

// A1: a tampered document followed by each writer of the fixture set leaves
// the quarantine copy byte-identical to the refused body (sha256 equal) and
// the head copy byte-identical to the head; the writer refuses with the
// tamper error, which names the quarantine path; the document and head are
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
			if !strings.Contains(detail, "quarantined at "+bodyQ) {
				t.Fatalf("%s's error does not name the quarantine %s: %q", w.name, bodyQ, detail)
			}
			if got := readFile(t, bodyQ); sha(got) != sha(tampered) {
				t.Fatalf("the quarantine body is not the refused body: sha256 %s, want %s", sha(got), sha(tampered))
			}
			if got := readFile(t, headQ); !bytes.Equal(got, head) {
				t.Fatalf("the quarantine head is not the head: %q", got)
			}
			side := string(readFile(t, sideQ))
			for _, want := range []string{"writer: " + w.verb + "\n", "binary: ", "time: ", "error: commit ledger " + f.handle + ": ledger_tampered"} {
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
		})
	}
}

// A2: a second refusal of the same seq leaves the first copy untouched: the
// error names the same path, and the body, head and sidecar are the first
// refusal's.
func TestSecondRefusalOfTheSameSeqKeepsTheFirstCopy(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	first := tamperBase(t, f, f.base, '0')
	seq, _ := headSeqOf(t, f)
	bodyQ, headQ, sideQ := quarantinePaths(f, seq)
	if _, err := RecordTarget(f.root, f.handle, f.target()); !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("first refusal: %v", err)
	}
	keep := [3][]byte{readFile(t, bodyQ), readFile(t, headQ), readFile(t, sideQ)}
	second := tamperBase(t, f, strings.Repeat("0", len(f.base)), '1')
	if bytes.Equal(second, first) {
		t.Fatal("the second tamper equals the first")
	}
	_, err := RecordTarget(f.root, f.handle, f.target())
	if !errors.Is(err, sealchain.ErrTampered) || !strings.Contains(err.Error(), "quarantined at "+bodyQ) {
		t.Fatalf("second refusal: %v", err)
	}
	for i, p := range []string{bodyQ, headQ, sideQ} {
		if got := readFile(t, p); !bytes.Equal(got, keep[i]) {
			t.Fatalf("the second refusal changed %s", p)
		}
	}
	if got := readFile(t, bodyQ); !bytes.Equal(got, first) || bytes.Equal(got, second) {
		t.Fatal("the quarantine body is not the first refused body")
	}
	ents, _ := os.ReadDir(QuarantineDir(f.root))
	if len(ents) != 3 {
		t.Fatalf("quarantine files: %d, want 3", len(ents))
	}
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
	if !errors.Is(err, sealchain.ErrTampered) || !strings.Contains(err.Error(), "quarantined at "+bodyQ) {
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
// the quarantine path, from the read that refuses (ReadLedger) as from a
// writer. The scheduler's half, the prepare's seat_prepare_failed reason,
// is internal/schedproc's TestPrepareNamesTheQuarantineOfATamperedLedger.
func TestRefusalNamesTheQuarantinePath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	seq, _ := headSeqOf(t, f)
	bodyQ, _, sideQ := quarantinePaths(f, seq)
	_, err := ReadLedgerAs(f.root, f.handle, "scheduler prepare")
	if !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("not ledger_tampered: %v", err)
	}
	if want := "; the refused document is quarantined at " + bodyQ; !strings.HasSuffix(err.Error(), want) {
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
	if !errors.Is(first.err, sealchain.ErrTampered) || !strings.Contains(fmt.Sprint(first.err), "quarantined at "+bodyQ) {
		t.Fatalf("the first writer did not refuse the edited body at its write: %v", first.err)
	}
	if !errors.Is(second.err, sealchain.ErrTampered) || !strings.Contains(second.err.Error(), "quarantined at "+bodyQ) {
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
	if ents, _ := os.ReadDir(QuarantineDir(f.root)); len(ents) != 3 {
		t.Fatalf("quarantine files: %d, want 3", len(ents))
	}
}

// A6: version skew as the tamper. A document a newer amq-squad wrote
// carries a field this binary's Ledger schema lacks (here attempt_note on a
// discard entry, built the way 3.1.1's attempt reached rc.1): sealed with
// the session's key over the body with the field, its head moved to that
// link. This binary decodes without the field and re-encodes, so the tag
// fails: refused and quarantined like any other tamper, with that error
// text. (This blocks the seat on version skew until the lead's recovery;
// the verifier fix of a later brief turns it into the distinct "written by
// a newer amq-squad" refusal.)
func TestVersionSkewIsQuarantinedLikeATamper(t *testing.T) {
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
	var doc map[string]any
	if err := json.Unmarshal(readFile(t, p), &doc); err != nil {
		t.Fatal(err)
	}
	var prev sealchain.Link
	if raw, _ := json.Marshal(doc["seal"]); json.Unmarshal(raw, &prev) != nil || prev.Seq == 0 {
		t.Fatalf("the document's seal: %v", doc["seal"])
	}
	entry := doc["discarded"].([]any)[0].(map[string]any)
	entry["attempt_note"] = "a field of a newer amq-squad"
	delete(doc, "seal")
	seed, ok, err := sealchain.ReadKey(f.root)
	if err != nil || !ok {
		t.Fatalf("the session seed: %v, %v", ok, err)
	}
	// The newer binary's seal: the next link, over its body with the field.
	link := sealchain.Seal(seed.Key, ledgerLabel, &prev, doc)
	if !link.Verify(seed.Key, ledgerLabel, doc) {
		t.Fatal("the newer binary's seal does not verify over its own body")
	}
	doc["seal"] = link
	skewed, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	skewed = append(skewed, '\n')
	if err := os.WriteFile(p, skewed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sealchain.WriteHead(f.root, ledgerHeadName(f.handle), seed, link, f.now); err != nil {
		t.Fatal(err)
	}
	// This binary's reading of the same bytes drops the field, so the same
	// link does not verify over what it re-encodes.
	var mine Ledger
	if err := json.Unmarshal(skewed, &mine); err != nil {
		t.Fatal(err)
	}
	mine.Seal = nil
	if link.Verify(seed.Key, ledgerLabel, mine) {
		t.Fatal("the seal verifies over this binary's decode: the field was not dropped")
	}
	seq, head := headSeqOf(t, f)
	if seq != strconv.Itoa(prev.Seq+1) {
		t.Fatalf("the head is at seq %s, want %d", seq, prev.Seq+1)
	}
	bodyQ, headQ, sideQ := quarantinePaths(f, seq)
	want := "ledger_tampered: the tag of " + ledgerHeadName(f.handle) + " link " + seq + " fails"
	_, err = CleanSeat(f.root, f.handle, gitBin(t), nil, f.now)
	if !errors.Is(err, sealchain.ErrTampered) || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "quarantined at "+bodyQ) {
		t.Fatalf("CleanSeat on the skewed document: %v (want %q and the quarantine path)", err, want)
	}
	if got := readFile(t, bodyQ); sha(got) != sha(skewed) {
		t.Fatalf("the quarantine body is not the skewed body: sha256 %s, want %s", sha(got), sha(skewed))
	}
	if got := readFile(t, headQ); !bytes.Equal(got, head) {
		t.Fatal("the quarantine head is not the head")
	}
	if side := string(readFile(t, sideQ)); !strings.Contains(side, "error: commit ledger "+f.handle+": "+want) {
		t.Fatalf("the sidecar lacks the error text:\n%s", side)
	}
	if got := readFile(t, p); !bytes.Equal(got, skewed) {
		t.Fatal("the skewed document was moved")
	}
}
