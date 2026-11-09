package seatcommit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func writeUnknownCommitField(t *testing.T, f *fixture, version string, schema int) {
	t.Helper()
	p := ledgerPath(f.root, f.handle)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	doc["future"] = true
	body, _ = json.Marshal(doc)
	key, ok, err := sealchain.ReadKey(f.root)
	if err != nil || !ok {
		t.Fatalf("seed = %v, %v", ok, err)
	}
	head, err := os.ReadFile(sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = sealchain.WriteRaw(f.root, ledgerHeadName(f.handle), key, ledgerLabel, head, body, version, schema, func(raw []byte) error {
		return os.WriteFile(p, raw, 0o600)
	}, f.now)
	if err != nil {
		t.Fatal(err)
	}
}

// TestWriteRawCompatibilityFixture is the evidence-only fixture writer for
// rows E9 and E10. Normal test runs skip it; the execution note records the
// explicit temp root used with older tagged builds.
func TestWriteRawCompatibilityFixture(t *testing.T) {
	root := os.Getenv("AMQSQ_0E_FIXTURE_ROOT")
	if root == "" {
		t.Skip("set AMQSQ_0E_FIXTURE_ROOT for the compatibility evidence run")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	l := Ledger{Schema: LedgerSchema, Handle: "qa", Worktree: root + "/worktree", Branch: "seat/qa", Base: strings.Repeat("a", 40), At: time.Unix(10, 0)}
	if err := writeLedger(root, l, "0e compatibility fixture"); err != nil {
		t.Fatal(err)
	}
	t.Logf("commit fixture: %s", ledgerPath(root, "qa"))
}

func TestCommitCompatibilityRefusalsAreTypedAndNotQuarantined(t *testing.T) {
	for _, tc := range []struct {
		name, version, reason, route string
		schema                       int
		want                         error
	}{
		{"newer", "v9.0.0", ReasonNewerWriter, task.RouteOwner, LedgerSchema + 1, sealchain.ErrNewerWriter},
		{"same schema devel", "(devel)", ReasonSchemaMismatch, task.RouteLead, LedgerSchema, sealchain.ErrSchemaMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			writeUnknownCommitField(t, f, tc.version, tc.schema)
			if _, err := ReadLedger(f.root, f.handle); !errors.Is(err, tc.want) || errors.Is(err, sealchain.ErrTampered) || !strings.Contains(err.Error(), "future") {
				t.Fatalf("read = %v", err)
			}
			res := f.run(f.reported("cmd/"))
			if res.OK || res.Deferred || res.ReasonCode != tc.reason || res.Route != tc.route || !strings.Contains(res.Detail, tc.version) {
				t.Fatalf("commit refusal = %+v", res)
			}
			if _, err := os.Stat(QuarantineDir(f.root)); !os.IsNotExist(err) {
				t.Fatalf("compatibility refusal created a quarantine: %v", err)
			}
		})
	}
}

func TestRecoveryIsTamperOnlyForCompatibilityRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		schema        int
		want          error
	}{
		{"newer writer", "v9.0.0", LedgerSchema + 1, sealchain.ErrNewerWriter},
		{"same schema mismatch", "v3.2.0-rc.2", LedgerSchema, sealchain.ErrSchemaMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			writeUnknownCommitField(t, f, tc.version, tc.schema)
			beforeBody := readFile(t, ledgerPath(f.root, f.handle))
			beforeHead := f.headBytes()
			seq, _ := headSeqOf(t, f)
			_, err := f.recover(seq, 1)
			if recoverCode(t, err) != RecoverIncompatible || !strings.Contains(err.Error(), "tamper-only") || !strings.Contains(err.Error(), tc.want.Error()) {
				t.Fatalf("recovery refusal = %v", err)
			}
			if errors.Is(tc.want, sealchain.ErrNewerWriter) && !strings.Contains(err.Error(), "amq-squad up --binary <that build>") {
				t.Fatalf("newer-writer refusal lacks install command: %v", err)
			}
			if !bytes.Equal(beforeBody, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(beforeHead, f.headBytes()) {
				t.Fatal("compatibility recovery changed ledger bytes")
			}
			if _, statErr := os.Stat(QuarantineDir(f.root)); !os.IsNotExist(statErr) {
				t.Fatalf("compatibility recovery created a quarantine: %v", statErr)
			}
		})
	}
}

func installLegacyCommitLedger(t *testing.T, f *fixture, edit func(*Ledger)) (Ledger, sealchain.Seed, *sealchain.Link) {
	t.Helper()
	l, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	if edit != nil {
		edit(&l)
	}
	key, ok, err := sealchain.ReadKey(f.root)
	if err != nil || !ok {
		t.Fatalf("seed = %v, %v", ok, err)
	}
	l.Seal = nil
	legacyBody := l
	legacy := sealchain.Seal(key.Key, ledgerLabel, nil, legacyBody)
	l.Seal = legacy
	raw, _ := json.MarshalIndent(l, "", "  ")
	if err := os.WriteFile(ledgerPath(f.root, f.handle), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sealchain.WriteHead(f.root, ledgerHeadName(f.handle), key, legacy, f.now); err != nil {
		t.Fatal(err)
	}
	l.Seal = nil
	return l, key, legacy
}

func writeInterruptedCommitMigration(t *testing.T, f *fixture, l Ledger, key sealchain.Seed, prev *sealchain.Link) sealchain.RawSeal {
	t.Helper()
	body, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	seal := sealchain.NewRawSeal(key.Key, ledgerLabel, prev, body, "v3.2.0-rc.2", LedgerSchema)
	raw, err := sealchain.JoinRaw(body, seal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(f.root, f.handle), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return seal
}

func TestInterruptedCommitMigrationReaderIsPureAndWriterCompletesOnce(t *testing.T) {
	f := newFixture(t)
	l, key, legacy := installLegacyCommitLedger(t, f, nil)
	seal := writeInterruptedCommitMigration(t, f, l, key, legacy)

	got, err := ReadLedger(f.root, f.handle)
	if err != nil || got.Handle != f.handle {
		t.Fatalf("recovered ledger = %+v, %v", got, err)
	}
	head := readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	if layout, err := sealchain.HeadLayout(ledgerHeadName(f.handle), key, head); err != nil || layout != "" {
		t.Fatalf("reader changed legacy head = %q, %v", layout, err)
	}
	decisionPath := f.root + "/extensions/amq-squad/scheduler/decisions.jsonl"
	if _, err := os.Stat(decisionPath); !os.IsNotExist(err) {
		t.Fatalf("reader wrote a migration decision: %v", err)
	}
	if err := writeLedger(f.root, got, "migration test"); err != nil {
		t.Fatal(err)
	}
	head = readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	if layout, err := sealchain.HeadLayout(ledgerHeadName(f.handle), key, head); err != nil || layout != sealchain.RawLayout {
		t.Fatalf("writer completed head = %q, %v", layout, err)
	}
	decisions := readFile(t, decisionPath)
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 || !bytes.Contains(decisions, []byte(fmt.Sprintf("link %d", seal.Seq))) {
		t.Fatalf("migration decisions = %d:\n%s", got, decisions)
	}
}

func TestRecoveryMigratesALegacyLedgerThroughTheRawWriter(t *testing.T) {
	f := newFixture(t)
	_, key, legacy := installLegacyCommitLedger(t, f, nil)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	res, err := f.recover(q.Seq, 1)
	if err != nil {
		t.Fatal(err)
	}
	body, seal, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle)))
	if err != nil || seal.Seq != legacy.Seq+1 || seal.Prev != legacy.Tag || !seal.Verify(key.Key, ledgerLabel, body) {
		t.Fatalf("recovered raw link = %+v, %v", seal, err)
	}
	if bytes.Contains(body, []byte(`"seal"`)) || res.NewSeq != legacy.Seq+1 {
		t.Fatalf("legacy recovery used an embedded seal or wrong link: %+v", res)
	}
	decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("migration decisions = %d: %s", got, decisions)
	}
}

func TestRecoveryContinuesAfterAnInterruptedRawMigration(t *testing.T) {
	f := newFixture(t)
	l, key, legacy := installLegacyCommitLedger(t, f, nil)
	interrupted := writeInterruptedCommitMigration(t, f, l, key, legacy)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if q.Seq != fmt.Sprint(legacy.Seq) {
		t.Fatalf("slot seq = %s, want copied legacy head %d", q.Seq, legacy.Seq)
	}
	res, err := f.recover(q.Seq, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, seal, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle)))
	if err != nil || seal.Seq != interrupted.Seq+1 || seal.Prev != interrupted.Tag {
		t.Fatalf("recovery after interrupted migration = %+v, %v", seal, err)
	}
	if res.OldSeq != interrupted.Seq || res.NewSeq != interrupted.Seq+1 {
		t.Fatalf("recovery result = %+v", res)
	}
}

func TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration(t *testing.T) {
	f := newFixture(t)
	_, key, legacy := installLegacyCommitLedger(t, f, nil)
	original := readFile(t, ledgerPath(f.root, f.handle))
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if err := os.WriteFile(ledgerPath(f.root, f.handle), original, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := f.recover(q.Seq, 1)
	if err != nil || !res.Verified {
		t.Fatalf("verified legacy recovery = %+v, %v", res, err)
	}
	body, seal, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle)))
	if err != nil || seal.Seq != legacy.Seq+1 || seal.Prev != legacy.Tag || !seal.Verify(key.Key, ledgerLabel, body) {
		t.Fatalf("verified legacy raw link = %+v, %v", seal, err)
	}
	decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("migration decisions = %d: %s", got, decisions)
	}
}

func TestCommitMigrationTwoReadersNeverWrite(t *testing.T) {
	f := newFixture(t)
	l, key, legacy := installLegacyCommitLedger(t, f, nil)
	writeInterruptedCommitMigration(t, f, l, key, legacy)
	headPath := sealchain.HeadPath(f.root, ledgerHeadName(f.handle))
	before := readFile(t, headPath)
	errCh := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := ReadLedger(f.root, f.handle)
			errCh <- err
		}()
	}
	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if after := readFile(t, headPath); !bytes.Equal(after, before) {
		t.Fatal("concurrent readers changed the legacy head")
	}
	if _, err := os.Stat(f.root + "/extensions/amq-squad/scheduler/decisions.jsonl"); !os.IsNotExist(err) {
		t.Fatalf("concurrent readers wrote a decision: %v", err)
	}
}

func TestCommitMigrationPausedReaderCannotRollBackWriter(t *testing.T) {
	f := newFixture(t)
	l, key, legacy := installLegacyCommitLedger(t, f, nil)
	writeInterruptedCommitMigration(t, f, l, key, legacy)
	paused, resume := make(chan struct{}), make(chan struct{})
	afterLedgerHeadRead = func(string, string) { close(paused); <-resume }
	t.Cleanup(func() { afterLedgerHeadRead = nil })
	readErr := make(chan error, 1)
	go func() { _, err := ReadLedger(f.root, f.handle); readErr <- err }()
	<-paused
	afterLedgerHeadRead = nil
	if err := withLedgerLock(f.root, f.handle, func() error {
		if err := writeLedger(f.root, l, "interleaving writer N+2"); err != nil {
			return err
		}
		return writeLedger(f.root, l, "interleaving writer N+3")
	}); err != nil {
		t.Fatal(err)
	}
	_, writtenSeal, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle)))
	if err != nil || writtenSeal.Seq != legacy.Seq+3 {
		t.Fatalf("writer document = link %d, %v; want N+3 (%d)", writtenSeal.Seq, err, legacy.Seq+3)
	}
	written := readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	close(resume)
	if err := <-readErr; err != nil {
		t.Fatal(err)
	}
	if after := readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle))); !bytes.Equal(after, written) {
		t.Fatal("paused reader rolled back the writer's head")
	}
	decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("interleaving decisions = %d", got)
	}
}

func TestCommitMigrationDecisionSurvivesCrashAndDedupes(t *testing.T) {
	f := newFixture(t)
	l, key, legacy := installLegacyCommitLedger(t, f, nil)
	writeInterruptedCommitMigration(t, f, l, key, legacy)
	crash := errors.New("crash after migration decision")
	afterLayoutMigrationDecision = func(string, string, int) error { return crash }
	t.Cleanup(func() { afterLayoutMigrationDecision = nil })
	if err := writeLedger(f.root, l, "crash seam"); !errors.Is(err, crash) {
		t.Fatalf("first write = %v", err)
	}
	decisionPath := f.root + "/extensions/amq-squad/scheduler/decisions.jsonl"
	if got := bytes.Count(readFile(t, decisionPath), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("decisions after crash = %d", got)
	}
	afterLayoutMigrationDecision = nil
	if err := writeLedger(f.root, l, "crash retry"); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(readFile(t, decisionPath), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("decisions after retry = %d", got)
	}
}

func TestInterruptedCommitMigrationRejectsSkipAndWrongPrev(t *testing.T) {
	for _, tc := range []struct {
		name string
		prev func(*sealchain.Link) *sealchain.Link
	}{
		{"skipped sequence", func(l *sealchain.Link) *sealchain.Link { return &sealchain.Link{Seq: l.Seq + 1, Tag: l.Tag} }},
		{"wrong previous tag", func(l *sealchain.Link) *sealchain.Link {
			return &sealchain.Link{Seq: l.Seq, Tag: strings.Repeat("0", len(l.Tag))}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			l, key, legacy := installLegacyCommitLedger(t, f, nil)
			writeInterruptedCommitMigration(t, f, l, key, tc.prev(legacy))
			if _, err := ReadLedger(f.root, f.handle); !errors.Is(err, sealchain.ErrTampered) {
				t.Fatalf("invalid migration = %v", err)
			}
		})
	}
}

// E3: an actual legacy embedded-seal JSON document, in the v3.1.1 shape,
// retains its attempt number when the locked writer migrates it to raw bytes.
func TestRawLedgerReplaysAttemptNumber(t *testing.T) {
	f := newFixture(t)
	l, _, _ := installLegacyCommitLedger(t, f, func(l *Ledger) {
		l.Discarded = append(l.Discarded, LedgerDiscard{TaskID: "t3", Attempt: 3, Reason: "test", Parent: f.base, Tree: f.base, At: f.now})
	})
	raw := readFile(t, ledgerPath(f.root, f.handle))
	if !bytes.Contains(raw, []byte(`"seal"`)) || !bytes.Contains(raw, []byte(`"attempt": 3`)) {
		t.Fatalf("legacy v3.1.1 JSON fixture = %s", raw)
	}
	if err := writeLedger(f.root, l, "attempt replay migration"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLedger(f.root, f.handle)
	if err != nil || len(got.Discarded) != 1 || got.Discarded[0].Attempt != 3 {
		t.Fatalf("attempt replay = %+v, %v", got.Discarded, err)
	}
}

// E4: legacy seals cannot authenticate a field an older typed reader drops;
// the result remains tampering, never a compatibility refusal.
func TestLegacyUnknownFieldStaysTampered(t *testing.T) {
	f := newFixture(t)
	l, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := sealchain.ReadKey(f.root)
	if err != nil || !ok {
		t.Fatalf("seed = %v, %v", ok, err)
	}
	l.Seal = nil
	bodyBytes, _ := json.Marshal(l)
	var body map[string]any
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatal(err)
	}
	body["future"] = true
	link := sealchain.Seal(key.Key, ledgerLabel, nil, body)
	body["seal"] = link
	raw, _ := json.MarshalIndent(body, "", "  ")
	if err := os.WriteFile(ledgerPath(f.root, f.handle), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sealchain.WriteHead(f.root, ledgerHeadName(f.handle), key, link, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLedger(f.root, f.handle); !errors.Is(err, sealchain.ErrTampered) || errors.Is(err, sealchain.ErrNewerWriter) || errors.Is(err, sealchain.ErrSchemaMismatch) {
		t.Fatalf("legacy unknown field = %v", err)
	}
}

func TestLegacyLedgerMigrationRecordsSeatAndLink(t *testing.T) {
	f := newFixture(t)
	l, _, _ := installLegacyCommitLedger(t, f, nil)
	if err := writeLedger(f.root, l, "test migration"); err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile(ledgerPath(f.root, f.handle))
	if err != nil {
		t.Fatal(err)
	}
	if _, seal, err := sealchain.SplitRaw(document); err != nil || seal.Seq != 2 {
		t.Fatalf("migrated document seal = %+v, %v", seal, err)
	}
	decisions, err := os.ReadFile(f.root + "/extensions/amq-squad/scheduler/decisions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{[]byte(`"kind":"ledger_layout_migrated"`), []byte(`"seat":"` + f.handle + `"`), []byte("link 2")} {
		if !bytes.Contains(decisions, want) {
			t.Fatalf("migration decision lacks %q: %s", want, decisions)
		}
	}
}
