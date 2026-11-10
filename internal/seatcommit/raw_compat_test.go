package seatcommit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
			_, alarmErr := RecoverLedger(RecoverOptions{Root: f.root, Handle: f.handle, Alarm: "1234abcd", Lead: "cto", Reason: "compatibility is not tamper", Now: f.now})
			if recoverCode(t, alarmErr) != RecoverIncompatible || !strings.Contains(alarmErr.Error(), tc.want.Error()) {
				t.Fatalf("alarm recovery refusal = %v", alarmErr)
			}
			if errors.Is(tc.want, sealchain.ErrNewerWriter) && !strings.Contains(err.Error(), "amq-squad up --binary <that build>") {
				t.Fatalf("newer-writer refusal lacks install command: %v", err)
			}
			if errors.Is(tc.want, sealchain.ErrSchemaMismatch) && strings.Contains(err.Error(), "amq-squad up --binary") {
				t.Fatalf("schema-mismatch refusal invented an install command: %v", err)
			}
			if !bytes.Equal(beforeBody, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(beforeHead, f.headBytes()) {
				t.Fatal("compatibility recovery changed ledger bytes")
			}
			if _, statErr := os.Stat(QuarantineDir(f.root)); !os.IsNotExist(statErr) {
				t.Fatalf("compatibility recovery created a quarantine: %v", statErr)
			}
			if _, statErr := os.Stat(AlarmsPath(f.root, f.handle)); !os.IsNotExist(statErr) {
				t.Fatalf("compatibility alarm released a hold: %v", statErr)
			}
		})
	}
}

func editCopiedHeadSeq(t *testing.T, f *fixture, seq int) {
	t.Helper()
	p := sealchain.HeadPath(f.root, ledgerHeadName(f.handle))
	raw := readFile(t, p)
	body := raw
	var seal sealchain.RawSeal
	if split, got, err := sealchain.SplitRaw(raw); err == nil {
		body, seal = split, got
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	doc["seq"] = seq
	body, _ = json.Marshal(doc)
	if seal.Tag != "" {
		raw, _ = sealchain.JoinRaw(body, seal)
	} else {
		raw = body
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func installRawLedgerAtSeq(t *testing.T, f *fixture, seq int) {
	t.Helper()
	raw := readFile(t, ledgerPath(f.root, f.handle))
	body, _, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	key := mustSessionKey(t, f.root)
	seal := sealchain.NewRawSeal(key.Key, ledgerLabel, &sealchain.Link{Seq: seq - 1, Tag: strings.Repeat("a", 64)}, body, "v3.2.0-rc.2", LedgerSchema)
	if seal.Seq != seq {
		t.Fatalf("fixture seal seq = %d, want %d", seal.Seq, seq)
	}
	document, err := sealchain.JoinRaw(body, seal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(f.root, f.handle), document, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sealchain.CompleteRawHead(f.root, ledgerHeadName(f.handle), key, seal, f.now); err != nil {
		t.Fatal(err)
	}
}

func editCopiedRawBodySeq(t *testing.T, f *fixture, seq int) {
	t.Helper()
	p := ledgerPath(f.root, f.handle)
	body, seal, err := sealchain.SplitRaw(readFile(t, p))
	if err != nil {
		t.Fatal(err)
	}
	seal.Seq = seq
	raw, err := sealchain.JoinRaw(body, seal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "raw"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			if legacy {
				installLegacyCommitLedger(t, f, nil)
			}
			body := readFile(t, ledgerPath(f.root, f.handle))
			var bodyTag string
			if _, seal, err := sealchain.SplitRaw(body); err == nil {
				bodyTag = seal.Tag
			} else {
				var l Ledger
				if json.Unmarshal(body, &l) != nil || l.Seal == nil {
					t.Fatal("legacy body has no seal")
				}
				bodyTag = l.Seal.Tag
			}
			editCopiedHeadSeq(t, f, 7)
			tamperBase(t, f, f.base, '0')
			q := f.quarantined()
			if q.Seq != "7" {
				t.Fatalf("slot seq = %s", q.Seq)
			}
			if _, err := f.recover("1", 1); recoverCode(t, err) != RecoverSlotMissing {
				t.Fatalf("body seq addressed the slot: %v", err)
			}
			res, err := f.recover("7", 1)
			if err != nil || res.OldSeq != 7 || res.NewSeq != 8 || res.Prev != bodyTag || res.SeqFrom != "claimed_head" {
				t.Fatalf("recovery = %+v, %v", res, err)
			}
			if l, err := ReadLedger(f.root, f.handle); err != nil || l.Seal.Seq != 8 || l.Recovered.SeqFrom != "claimed_head" {
				t.Fatalf("recovered ledger = %+v, %v", l.Seal, err)
			}
			l, _ := ReadLedger(f.root, f.handle)
			if l.Recovered.Seq != 7 || l.Recovered.Link != 8 {
				t.Fatalf("persisted recovery sequence = %+v", l.Recovered)
			}
			wantMigrations := []int(nil)
			if legacy {
				wantMigrations = []int{8}
			}
			if got := migrationDecisionSeqs(t, f.root); !slices.Equal(got, wantMigrations) {
				t.Fatalf("migration lines = %v, want %v", got, wantMigrations)
			}
			if res.SeqFrom != l.Recovered.SeqFrom || res.SeqFrom != "claimed_head" {
				t.Fatalf("decision seq_from = %q, record = %q", res.SeqFrom, l.Recovered.SeqFrom)
			}
			bodyBeforeRetry, headBeforeRetry := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
			if again, err := f.recover("7", 1); err != nil || !again.AlreadyRecovered || again.NewSeq != 8 {
				t.Fatalf("retry = %+v, %v", again, err)
			}
			if !bytes.Equal(bodyBeforeRetry, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(headBeforeRetry, f.headBytes()) {
				t.Fatal("retry changed recovered body or head")
			}
		})
	}
}

func TestRecoveryTreatsTheMaximumClaimedSequenceAsUnknown(t *testing.T) {
	f := newFixture(t)
	editCopiedHeadSeq(t, f, math.MaxInt)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if q.Seq != quarantineSeqUnknown {
		t.Fatalf("slot seq = %s, want unknown", q.Seq)
	}
	res, err := f.recover(q.Seq, 1)
	if err != nil || res.SeqFrom != "body_seal" || res.NewSeq != 2 {
		t.Fatalf("maximum claim recovery = %+v, %v", res, err)
	}
}

func TestRecoveryTreatsNonPositiveHeadClaimsAsUnknownForBothLayouts(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, seq := range []int{0, -5} {
			t.Run(fmt.Sprintf("legacy=%v/seq=%d", legacy, seq), func(t *testing.T) {
				f := newFixture(t)
				if legacy {
					installLegacyCommitLedger(t, f, nil)
				}
				editCopiedHeadSeq(t, f, seq)
				tamperBase(t, f, f.base, '0')
				q := f.quarantined()
				if q.Seq != quarantineSeqUnknown {
					t.Fatalf("slot seq = %s", q.Seq)
				}
				res, err := f.recover(quarantineSeqUnknown, 1)
				if err != nil || res.NewSeq != 2 || res.SeqFrom != "body_seal" {
					t.Fatalf("recovery = %+v, %v", res, err)
				}
			})
		}
	}
}

func TestOrdinaryWriterRefusesAnExhaustedVerifiedLedgerWithoutChangingEvidence(t *testing.T) {
	f := newFixture(t)
	installRawLedgerAtSeq(t, f, math.MaxInt)
	body, head := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	_, err := RecordTarget(f.root, f.handle, f.target())
	var exhausted *sealchain.ErrSeqExhausted
	if !errors.As(err, &exhausted) || !strings.Contains(err.Error(), f.handle) || !strings.Contains(err.Error(), "the ledger's sequence is exhausted") || errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("exhausted write = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("exhausted write changed body or head")
	}
	if _, err := ReadLedger(f.root, f.handle); err != nil {
		t.Fatalf("ledger no longer verifies: %v", err)
	}
	if _, err := os.Stat(QuarantineDir(f.root)); !os.IsNotExist(err) {
		t.Fatalf("exhaustion created quarantine: %v", err)
	}
	if _, err := os.Stat(AlarmsPath(f.root, f.handle)); !os.IsNotExist(err) {
		t.Fatalf("exhaustion created alarm: %v", err)
	}
}

func TestCopiedHeadClaimsAboveTheInteroperableBoundUseTheBody(t *testing.T) {
	for _, seq := range []int{math.MaxInt, math.MaxInt - 1, 1 << 53, 1<<53 - 1} {
		t.Run(strconv.Itoa(seq), func(t *testing.T) {
			f := newFixture(t)
			editCopiedHeadSeq(t, f, seq)
			tamperBase(t, f, f.base, '0')
			q := f.quarantined()
			if q.Seq != quarantineSeqUnknown {
				t.Fatalf("slot seq = %s", q.Seq)
			}
			res, err := f.recover(quarantineSeqUnknown, 1)
			if err != nil || res.NewSeq != 2 || res.SeqFrom != "body_seal" {
				t.Fatalf("recovery = %+v, %v", res, err)
			}
		})
	}
}

func TestLargestPlausibleCopiedHeadClaimLeavesOneOrdinaryVerifiedWrite(t *testing.T) {
	f := newFixture(t)
	const claimed = 1<<53 - 2
	editCopiedHeadSeq(t, f, claimed)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if q.Seq != strconv.Itoa(claimed) {
		t.Fatalf("slot seq = %s", q.Seq)
	}
	res, err := f.recover(strconv.Itoa(claimed), 1)
	if err != nil || res.NewSeq != 1<<53-1 || res.SeqFrom != "claimed_head" {
		t.Fatalf("recovery = %+v, %v", res, err)
	}
	if _, err := RecordTarget(f.root, f.handle, f.target()); err != nil {
		t.Fatal(err)
	}
	if l, err := ReadLedger(f.root, f.handle); err != nil || l.Seal.Seq != 1<<53 {
		t.Fatalf("ordinary successor = %+v, %v", l.Seal, err)
	}
}

func TestImplausibleCopiedBodySequenceNeedsAUsableHead(t *testing.T) {
	f := newFixture(t)
	editCopiedRawBodySeq(t, f, 1<<53)
	if err := os.WriteFile(sealchain.HeadPath(f.root, ledgerHeadName(f.handle)), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, q.Slot)
	_, err := f.recover(quarantineSeqUnknown, 1)
	if recoverCode(t, err) != RecoverSeqUnknown {
		t.Fatalf("implausible body = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("refusal changed ledger")
	}
	assertNoRecoveryDecision(t, f.root)
	assertSlotSnapshot(t, q.Slot, slot)
}

func TestKnownRawBodyWithImplausibleSequenceContinuesFromVerifiedHead(t *testing.T) {
	f := newFixture(t)
	editCopiedRawBodySeq(t, f, 1<<53)
	q := f.quarantined()
	res, err := f.recover(q.Seq, 1)
	if err != nil || res.NewSeq != 2 || res.SeqFrom != "verified_head" {
		t.Fatalf("verified-head recovery = %+v, %v", res, err)
	}
	decisionPath := filepath.Join(f.root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	if raw, err := os.ReadFile(decisionPath); err == nil && bytes.Contains(raw, []byte(`"kind":"ledger_layout_migrated"`)) {
		t.Fatalf("raw chain recorded migration: %s", raw)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestKnownRawBodyWithImplausibleSequenceStillChecksCarriedEntries(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// carried\n")
	tk := f.reported("cmd/")
	step := f.run(tk)
	if !step.OK || step.Commit == "" {
		t.Fatalf("commit step = %+v", step)
	}
	live, err := ReadLedger(f.root, f.handle)
	if err != nil || len(live.Commits) != 1 {
		t.Fatalf("ledger = %+v, %v", live.Commits, err)
	}
	real := live.Commits[0]
	editCopiedRawBodySeq(t, f, 1<<53)
	q := f.quarantined()
	body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, q.Slot)
	_, err = f.recover(q.Seq, 1, CarryEntry{Task: "not-in-body", Commit: real.Commit})
	if recoverCode(t, err) != RecoverEntryUnknown {
		t.Fatalf("unknown carry = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("unknown carry changed ledger")
	}
	assertSlotSnapshot(t, q.Slot, slot)
	assertNoRecoveryDecision(t, f.root)
	res, err := f.recover(q.Seq, 1, CarryEntry{Task: tk.ID, Commit: real.Commit})
	if err != nil || res.NewSeq != live.Seal.Seq+1 || res.SeqFrom != "verified_head" || len(res.Carried) != 1 {
		t.Fatalf("checked carry = %+v, %v", res, err)
	}
	live, err = ReadLedger(f.root, f.handle)
	wantNote := "carried from quarantine seq " + q.Seq
	if err != nil || len(live.Commits) != 1 || live.Commits[0].Note != wantNote {
		t.Fatalf("carried note = %+v, want %q, err %v", live.Commits, wantNote, err)
	}
	if got := migrationDecisionSeqs(t, f.root); len(got) != 0 {
		t.Fatalf("raw carry recorded migration: %v", got)
	}
}

func installLegacyShapedBodyUnderVerifyingHead(t *testing.T, f *fixture, copiedSeal *sealchain.Link) int {
	t.Helper()
	l, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	verified := l.Seal
	if verified == nil {
		t.Fatal("fixture has no verified seal")
	}
	key := mustSessionKey(t, f.root)
	if err := sealchain.WriteHead(f.root, ledgerHeadName(f.handle), key, verified, f.now); err != nil {
		t.Fatal(err)
	}
	l.Seal = copiedSeal
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(f.root, f.handle), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	return verified.Seq
}

func installLiteralLegacySeal(t *testing.T, f *fixture, literal string) {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(readFile(t, ledgerPath(f.root, f.handle)), &doc); err != nil {
		t.Fatal(err)
	}
	doc["seal"] = json.RawMessage(literal)
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(f.root, f.handle), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyBodyWithImplausibleSequenceUsesVerifiedHeadAndChecksCarry(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// carried\n")
	tk := f.reported("cmd/")
	step := f.run(tk)
	if !step.OK || step.Commit == "" {
		t.Fatalf("commit step = %+v", step)
	}
	live, err := ReadLedger(f.root, f.handle)
	if err != nil || len(live.Commits) != 1 {
		t.Fatalf("ledger = %+v, %v", live.Commits, err)
	}
	real := live.Commits[0]
	headSeq := installLegacyShapedBodyUnderVerifyingHead(t, f, &sealchain.Link{Seq: 1 << 53, Tag: strings.Repeat("a", 64)})
	q := f.quarantined()
	body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, q.Slot)
	_, err = f.recover(q.Seq, 1, CarryEntry{Task: "not-in-body", Commit: real.Commit})
	if recoverCode(t, err) != RecoverEntryUnknown {
		t.Fatalf("unknown carry = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("unknown carry changed ledger or head")
	}
	assertNoRecoveryDecision(t, f.root)
	assertSlotSnapshot(t, q.Slot, slot)
	res, err := f.recover(q.Seq, 1, CarryEntry{Task: tk.ID, Commit: real.Commit})
	if err != nil || res.NewSeq != headSeq+1 || res.OldSeq != headSeq || res.SeqFrom != "verified_head" {
		t.Fatalf("legacy recovery = %+v, %v", res, err)
	}
	live, err = ReadLedger(f.root, f.handle)
	wantNote := "carried from quarantine seq " + strconv.Itoa(headSeq)
	if err != nil || len(live.Commits) != 1 || live.Commits[0].Note != wantNote {
		t.Fatalf("carried entry = %+v, want Note %q, err %v", live.Commits, wantNote, err)
	}
	if got := migrationDecisionSeqs(t, f.root); !slices.Equal(got, []int{headSeq + 1}) {
		t.Fatalf("migration seqs = %v, want [%d]", got, headSeq+1)
	}
}

func TestLegacyShapeWithUnusableSealStillUsesVerifiedHead(t *testing.T) {
	for _, tc := range []struct {
		name    string
		literal string
	}{
		{name: "sequence without tag", literal: `{"seq":1}`},
		{name: "empty seal", literal: `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// carried\n")
			tk := f.reported("cmd/")
			step := f.run(tk)
			if !step.OK || step.Commit == "" {
				t.Fatalf("commit step = %+v", step)
			}
			live, err := ReadLedger(f.root, f.handle)
			if err != nil || len(live.Commits) != 1 {
				t.Fatalf("ledger = %+v, %v", live.Commits, err)
			}
			real := live.Commits[0]
			headSeq := installLegacyShapedBodyUnderVerifyingHead(t, f, &sealchain.Link{Seq: 1})
			installLiteralLegacySeal(t, f, tc.literal)
			q := f.quarantined()
			body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, q.Slot)
			_, err = f.recover(q.Seq, 1, CarryEntry{Task: tk.ID, Commit: real.Commit})
			if recoverCode(t, err) != RecoverEntryUnknown || !strings.Contains(err.Error(), "holds no body that decodes") {
				t.Fatalf("held entry carry = %v", err)
			}
			if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
				t.Fatal("refused carry changed ledger or head")
			}
			assertSlotSnapshot(t, q.Slot, slot)
			assertNoRecoveryDecision(t, f.root)
			res, err := f.recover(q.Seq, 1)
			if err != nil || res.NewSeq != headSeq+1 || res.SeqFrom != "verified_head" {
				t.Fatalf("legacy-shaped recovery = %+v, %v", res, err)
			}
			after, err := ReadLedger(f.root, f.handle)
			if err != nil || len(after.Commits) != 0 {
				t.Fatalf("recovered ledger commits = %+v, %v", after.Commits, err)
			}
			if got := migrationDecisionSeqs(t, f.root); !slices.Equal(got, []int{headSeq + 1}) {
				t.Fatalf("migration seqs = %v, want [%d]", got, headSeq+1)
			}
		})
	}
}

func TestUntaggedLegacyBodyWithoutAUsableHeadRefusesSequenceUnknown(t *testing.T) {
	f := newFixture(t)
	installLegacyShapedBodyUnderVerifyingHead(t, f, &sealchain.Link{Seq: 3})
	installLiteralLegacySeal(t, f, `{"seq":3}`)
	if err := os.WriteFile(sealchain.HeadPath(f.root, ledgerHeadName(f.handle)), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, q.Slot)
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverSeqUnknown || !strings.Contains(err.Error(), "names a plausible chain link to continue after") {
		t.Fatalf("untagged legacy recovery = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("sequence refusal changed ledger or head")
	}
	assertSlotSnapshot(t, q.Slot, slot)
	assertNoRecoveryDecision(t, f.root)
}

func TestUntaggedLegacyBodySeqAboveAVerifyingHeadIsIgnored(t *testing.T) {
	f := newFixture(t)
	headSeq := installLegacyShapedBodyUnderVerifyingHead(t, f, &sealchain.Link{Seq: 9})
	installLiteralLegacySeal(t, f, `{"seq":9}`)
	q := f.quarantined()
	res, err := f.recover(q.Seq, 1)
	if err != nil || res.NewSeq != headSeq+1 || res.OldSeq != headSeq || res.SeqFrom != "verified_head" {
		t.Fatalf("untagged legacy recovery = %+v, %v", res, err)
	}
	if got := migrationDecisionSeqs(t, f.root); !slices.Equal(got, []int{headSeq + 1}) {
		t.Fatalf("migration seqs = %v, want [%d]", got, headSeq+1)
	}
}

func TestRawRecoveryWithGarbageHeadNeverRecordsAMigration(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	if err := os.WriteFile(sealchain.HeadPath(f.root, ledgerHeadName(f.handle)), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	res, err := f.recover(q.Seq, 1)
	if err != nil || res.SeqFrom != "body_seal" || res.NewSeq != 2 {
		t.Fatalf("garbage-head recovery = %+v, %v", res, err)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || l.Recovered == nil || l.Recovered.SeqFrom != "body_seal" {
		t.Fatalf("recovery record = %+v, %v", l.Recovered, err)
	}
	if got := migrationDecisionSeqs(t, f.root); len(got) != 0 {
		t.Fatalf("migration decisions after recovery = %v", got)
	}
	if _, err := RecordTarget(f.root, f.handle, f.target()); err != nil {
		t.Fatal(err)
	}
	if got := migrationDecisionSeqs(t, f.root); len(got) != 0 {
		t.Fatalf("migration decisions after ordinary write = %v", got)
	}
}

func TestRenamedSlotMustAgreeWithItsCopiedHeadClaim(t *testing.T) {
	f := newFixture(t)
	editCopiedHeadSeq(t, f, 7)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	renamed := filepath.Join(filepath.Dir(q.Slot), quarantineBase(f.handle, "8"))
	if err := os.Rename(q.Slot, renamed); err != nil {
		t.Fatal(err)
	}
	body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, renamed)
	_, err := f.recover("8", 1)
	if recoverCode(t, err) != RecoverSlotInvalid || !strings.Contains(err.Error(), "copied head names 7") {
		t.Fatalf("renamed slot = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("renamed-slot refusal changed ledger")
	}
	assertNoRecoveryDecision(t, f.root)
	assertSlotSnapshot(t, renamed, slot)
}

func TestVerifiedRecoveryRefusesASlotAboveTheLiveLink(t *testing.T) {
	f := newFixture(t)
	originalBody, originalHead := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	editCopiedHeadSeq(t, f, 7)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if err := os.WriteFile(ledgerPath(f.root, f.handle), originalBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sealchain.HeadPath(f.root, ledgerHeadName(f.handle)), originalHead, 0o600); err != nil {
		t.Fatal(err)
	}
	slot := snapshotSlot(t, q.Slot)
	_, err := f.recover("7", 1)
	if recoverCode(t, err) != RecoverNotTampered || !strings.Contains(err.Error(), "behind the slot's highest sequence 7") {
		t.Fatalf("verified behind slot = %v", err)
	}
	if !bytes.Equal(originalBody, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(originalHead, f.headBytes()) {
		t.Fatal("verified refusal changed ledger")
	}
	assertNoRecoveryDecision(t, f.root)
	assertSlotSnapshot(t, q.Slot, slot)
}

func TestVerifiedRecoveryAtMaximumSequenceRefusesBeforeWriting(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	installRawLedgerAtSeq(t, f, math.MaxInt)
	body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, q.Slot)
	_, err := f.recover(q.Seq, 1)
	want := "recover_slot_invalid: the commits ledger of seat " + f.handle + " verifies at the maximum chain sequence; no successor can be written"
	if recoverCode(t, err) != RecoverSlotInvalid || err.Error() != want {
		t.Fatalf("verified maximum = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("verified maximum changed ledger")
	}
	assertNoRecoveryDecision(t, f.root)
	assertSlotSnapshot(t, q.Slot, slot)
}

func TestTamperedBodyUnderVerifiedMaximumHeadKeepsD1cRefusal(t *testing.T) {
	f := newFixture(t)
	installRawLedgerAtSeq(t, f, math.MaxInt)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	body, head, slot := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes(), snapshotSlot(t, q.Slot)
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverSlotInvalid || !strings.Contains(err.Error(), "names the maximum chain sequence; no successor can be written") {
		t.Fatalf("D1.c refusal = %v", err)
	}
	if !bytes.Equal(body, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(head, f.headBytes()) {
		t.Fatal("D1.c refusal changed ledger")
	}
	assertNoRecoveryDecision(t, f.root)
	assertSlotSnapshot(t, q.Slot, slot)
}

func TestRecoveryRefusesASlotBodyThatIsNeitherLayout(t *testing.T) {
	f := newFixture(t)
	raw := readFile(t, ledgerPath(f.root, f.handle))
	body, _, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(f.root, f.handle), body, 0o600); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	bodyBefore, headBefore := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	slotBefore := snapshotSlot(t, q.Slot)
	decisionPath := filepath.Join(f.root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	_, err = f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverSeqUnknown || !strings.Contains(err.Error(), "neither the raw layout nor a legacy ledger") {
		t.Fatalf("neither-layout refusal = %v", err)
	}
	if !bytes.Equal(bodyBefore, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(headBefore, f.headBytes()) {
		t.Fatal("neither-layout refusal changed the ledger")
	}
	if _, statErr := os.Stat(decisionPath); !os.IsNotExist(statErr) {
		t.Fatalf("neither-layout refusal wrote a decision: %v", statErr)
	}
	assertSlotSnapshot(t, q.Slot, slotBefore)
}

func TestRecoveryRefusesANeitherLayoutHeadWithNoBody(t *testing.T) {
	f := newFixture(t)
	if err := os.Remove(ledgerPath(f.root, f.handle)); err != nil {
		t.Fatal(err)
	}
	headPath := sealchain.HeadPath(f.root, ledgerHeadName(f.handle))
	if err := os.WriteFile(headPath, []byte("not a sealed head\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	slotBefore := snapshotSlot(t, q.Slot)
	headBefore := f.headBytes()
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverSeqUnknown || !strings.Contains(err.Error(), "neither its head copy nor its body copy") {
		t.Fatalf("neither-layout head refusal = %v", err)
	}
	if _, statErr := os.Stat(ledgerPath(f.root, f.handle)); !os.IsNotExist(statErr) || !bytes.Equal(headBefore, f.headBytes()) {
		t.Fatalf("neither-layout head refusal changed live bytes: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(f.root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")); !os.IsNotExist(statErr) {
		t.Fatalf("neither-layout head refusal wrote a decision: %v", statErr)
	}
	assertSlotSnapshot(t, q.Slot, slotBefore)
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
	bodyBefore, headBefore := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	again, err := f.recover(q.Seq, 1)
	if err != nil || !again.AlreadyRecovered || again.NewSeq != res.NewSeq {
		t.Fatalf("legacy recovery retry = %+v, %v", again, err)
	}
	if !bytes.Equal(bodyBefore, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(headBefore, f.headBytes()) {
		t.Fatal("legacy recovery retry wrote a second link")
	}
	if after := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl"); !bytes.Equal(after, decisions) {
		t.Fatalf("legacy recovery retry wrote another migration decision: %s", after)
	}
}

func TestRecoveryMigratesALegacyBodyWithNoUsableHead(t *testing.T) {
	f := newFixture(t)
	_, _, legacy := installLegacyCommitLedger(t, f, nil)
	if err := os.Remove(sealchain.HeadPath(f.root, ledgerHeadName(f.handle))); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	if q.Seq != quarantineSeqUnknown {
		t.Fatalf("headless legacy slot seq = %s", q.Seq)
	}
	res, err := f.recover(q.Seq, 1)
	if err != nil || res.NewSeq != legacy.Seq+1 || res.Prev != legacy.Tag {
		t.Fatalf("headless legacy recovery = %+v, %v", res, err)
	}
	if _, _, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle))); err != nil {
		t.Fatalf("headless legacy recovery is not raw: %v", err)
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
	bodyBefore, headBefore := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	slotBefore := snapshotSlot(t, q.Slot)
	decisionPath := filepath.Join(f.root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	decisionsBefore := optionalFile(t, decisionPath)
	want := errors.New("injected migration mark failure")
	oldCreate := recoveredMarkCreate
	recoveredMarkCreate = func(slotDir, string, []byte) error { return want }
	t.Cleanup(func() { recoveredMarkCreate = oldCreate })
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverWriteFailed || !strings.Contains(err.Error(), quarantineRecovered) {
		t.Fatalf("interrupted migration mark failure = %v", err)
	}
	if !bytes.Equal(bodyBefore, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(headBefore, f.headBytes()) {
		t.Fatal("interrupted migration mark failure changed the ledger or head")
	}
	assertSlotSnapshot(t, q.Slot, slotBefore)
	if got := optionalFile(t, decisionPath); !bytes.Equal(got, decisionsBefore) {
		t.Fatalf("interrupted migration mark failure changed decisions: before %q after %q", decisionsBefore, got)
	}
	if _, err := os.Lstat(filepath.Join(q.Slot, quarantineRecovered)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted migration mark failure left a mark: %v", err)
	}
	recoveredMarkCreate = oldCreate
	res, err := f.recover(q.Seq, 1)
	if err != nil {
		t.Fatal(err)
	}
	_, seal, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle)))
	if err != nil || seal.Seq != interrupted.Seq+1 || seal.Prev != interrupted.Tag {
		t.Fatalf("recovery after interrupted migration = %+v, %v", seal, err)
	}
	if res.OldSeq != legacy.Seq || res.NewSeq != interrupted.Seq+1 || res.SeqFrom != "body_seal" {
		t.Fatalf("recovery result = %+v", res)
	}
	decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 || !bytes.Contains(decisions, []byte(fmt.Sprintf("link %d", interrupted.Seq))) {
		t.Fatalf("migration decisions = %d: %s", got, decisions)
	}
}

func TestInterruptedMigrationRecoveryDedupesTheOriginalDecision(t *testing.T) {
	for _, verified := range []bool{false, true} {
		for _, preRecorded := range []bool{false, true} {
			t.Run(fmt.Sprintf("verified=%v/pre-recorded=%v", verified, preRecorded), func(t *testing.T) {
				f := newFixture(t)
				l, key, legacy := installLegacyCommitLedger(t, f, nil)
				seal := writeInterruptedCommitMigration(t, f, l, key, legacy)
				original := readFile(t, ledgerPath(f.root, f.handle))
				if preRecorded {
					if err := recordLayoutMigration(f.root, f.handle, seal.Seq); err != nil {
						t.Fatal(err)
					}
				}
				tamperBase(t, f, f.base, '0')
				q := f.quarantined()
				if verified {
					if err := os.WriteFile(ledgerPath(f.root, f.handle), original, 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.recover(q.Seq, 1); err != nil {
					t.Fatal(err)
				}
				decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
				if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 || !bytes.Contains(decisions, []byte(fmt.Sprintf("link %d", seal.Seq))) {
					t.Fatalf("migration decisions = %d: %s", got, decisions)
				}
			})
		}
	}
}

func TestRetryCompletesARecoveredRawBodyAboveALegacyHead(t *testing.T) {
	f := newFixture(t)
	installLegacyCommitLedger(t, f, nil)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	crash := errors.New("crash before recovered raw head")
	afterLayoutMigrationDecision = func(string, string, int) error { return crash }
	_, err := f.recover(q.Seq, 1)
	afterLayoutMigrationDecision = nil
	if !strings.Contains(fmt.Sprint(err), crash.Error()) {
		t.Fatalf("interrupted recovery = %v", err)
	}
	before := readFile(t, ledgerPath(f.root, f.handle))
	_, seal, err := sealchain.SplitRaw(before)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.recover(q.Seq, 1)
	if err != nil || !res.AlreadyRecovered || res.NewSeq != seal.Seq {
		t.Fatalf("retry = %+v, %v", res, err)
	}
	if after := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(after, before) {
		t.Fatal("retry wrote a new ledger link")
	}
	head := readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	if layout, err := sealchain.HeadLayout(ledgerHeadName(f.handle), mustSessionKey(t, f.root), head); err != nil || layout != sealchain.RawLayout {
		t.Fatalf("completed head = %q, %v", layout, err)
	}
}

func TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm(t *testing.T) {
	type form struct {
		name            string
		outcome         string
		wantSeq         int
		wantMigrating   bool
		prevFrom        string
		wantSeqFrom     string
		crashMigrations []int
		finalMigrations []int
		install         func(*testing.T, *fixture)
	}
	forms := []form{
		{name: "head raw body raw", outcome: "raw successor", wantSeq: 1, prevFrom: "head", wantSeqFrom: "verified_head", install: func(t *testing.T, f *fixture) {
			tamperBase(t, f, f.base, '0')
		}},
		{name: "head raw body legacy", outcome: "raw successor", wantSeq: 1, prevFrom: "head", wantSeqFrom: "verified_head", install: func(t *testing.T, f *fixture) {
			_, key, legacy := installLegacyCommitLedger(t, f, nil)
			if _, err := sealchain.CompleteRawHead(f.root, ledgerHeadName(f.handle), key, sealchain.RawSeal{
				Seq: legacy.Seq, Prev: legacy.Prev, Tag: legacy.Tag, Version: "v3.2.0-rc.2", Schema: LedgerSchema,
			}, f.now); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "head legacy body raw with a gap", wantSeq: 3, wantMigrating: true, prevFrom: "head", wantSeqFrom: "body_seal", crashMigrations: []int{4}, finalMigrations: []int{4, 5}, install: func(t *testing.T, f *fixture) {
			l, key, legacy := installLegacyCommitLedger(t, f, nil)
			body, err := json.MarshalIndent(l, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			gap := &sealchain.Link{Seq: legacy.Seq + 1, Tag: strings.Repeat("a", 64)}
			seal := sealchain.NewRawSeal(key.Key, ledgerLabel, gap, body, "v3.2.0-rc.2", LedgerSchema)
			raw, err := sealchain.JoinRaw(body, seal)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ledgerPath(f.root, f.handle), raw, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "head legacy body legacy", outcome: "legacy successor", wantSeq: 1, wantMigrating: true, prevFrom: "head", wantSeqFrom: "verified_head", crashMigrations: []int{2}, finalMigrations: []int{2}, install: func(t *testing.T, f *fixture) {
			installLegacyCommitLedger(t, f, nil)
			tamperBase(t, f, f.base, '0')
		}},
		{name: "interrupted migration", wantSeq: 2, wantMigrating: true, prevFrom: "body", wantSeqFrom: "body_seal", crashMigrations: []int{2}, finalMigrations: []int{2, 4}, install: func(t *testing.T, f *fixture) {
			l, key, legacy := installLegacyCommitLedger(t, f, nil)
			writeInterruptedCommitMigration(t, f, l, key, legacy)
			tamperBase(t, f, f.base, '0')
		}},
	}
	for _, tc := range forms {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			carry := installFixtureEntryAtSeqOne(t, f)
			tc.install(t, f)
			first := f.quarantined()
			choice := mustSlotChoice(t, f, first)
			wantTag := slotCopiedTag(t, f, first, tc.prevFrom)
			if choice.prev.Seq != tc.wantSeq || choice.prev.Tag != wantTag || choice.migrating != tc.wantMigrating {
				t.Fatalf("slot link = seq %d tag %s migrating %v; want seq %d tag from %s (%s) migrating %v", choice.prev.Seq, choice.prev.Tag, choice.migrating, tc.wantSeq, tc.prevFrom, wantTag, tc.wantMigrating)
			}
			firstBefore := snapshotSlot(t, first.Slot)
			bodyBeforeMissing, headBeforeMissing := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
			_, missingErr := f.recover(first.Seq, mustSlotIndex(t, f.handle, first), CarryEntry{Task: "absent", Commit: carry.Commit})
			if recoverCode(t, missingErr) != RecoverEntryUnknown {
				t.Fatalf("absent carry = %v", missingErr)
			}
			if !bytes.Equal(bodyBeforeMissing, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(headBeforeMissing, f.headBytes()) {
				t.Fatal("absent carry changed body or head")
			}
			assertSlotSnapshot(t, first.Slot, firstBefore)
			crash := errors.New("crash before recovery head")
			beforeRecoveryHeadWrite = func(sealchain.RawSeal) error { return crash }
			t.Cleanup(func() { beforeRecoveryHeadWrite = nil })
			_, err := f.recover(first.Seq, mustSlotIndex(t, f.handle, first), carry)
			beforeRecoveryHeadWrite = nil
			if recoverCode(t, err) != RecoverWriteFailed || !strings.Contains(err.Error(), crash.Error()) {
				t.Fatalf("crashed recovery = %v", err)
			}
			if got := migrationDecisionSeqs(t, f.root); !slices.Equal(got, tc.crashMigrations) {
				t.Fatalf("migration seqs after crash = %v, want %v", got, tc.crashMigrations)
			}
			crashedBody := readFile(t, ledgerPath(f.root, f.handle))
			_, crashedSeal, err := sealchain.SplitRaw(crashedBody)
			if err != nil {
				t.Fatalf("crashed body is not raw: %v", err)
			}

			var finalResult RecoverResult
			if tc.outcome != "" {
				if l, err := ReadLedger(f.root, f.handle); err != nil || l.Seal == nil || l.Seal.Seq != crashedSeal.Seq {
					t.Fatalf("%s was not admitted: %v, %+v", tc.outcome, err, l.Seal)
				}
				if recovered, _, _, ok := RecoveriesOf(f.root, f.handle); !ok || recovered == nil || recovered.Slot != first.Slot {
					t.Fatalf("admitted recovery is not visible to hold reconciliation: %+v, %v", recovered, ok)
				}
				bodyBeforeRetry, headBeforeRetry := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
				res, err := f.recover(first.Seq, mustSlotIndex(t, f.handle, first), carry)
				if err != nil || !res.AlreadyRecovered || res.NewSeq != crashedSeal.Seq {
					t.Fatalf("admitted retry = %+v, %v", res, err)
				}
				finalResult = res
				if after := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(after, crashedBody) {
					t.Fatal("admitted retry wrote a second body link")
				}
				if tc.outcome == "legacy successor" {
					head := f.headBytes()
					if bytes.Equal(head, headBeforeRetry) {
						t.Fatal("legacy-successor retry did not complete the head")
					}
					if layout, err := sealchain.HeadLayout(ledgerHeadName(f.handle), mustSessionKey(t, f.root), head); err != nil || layout != sealchain.RawLayout {
						t.Fatalf("legacy-successor retry did not complete the raw head: %q, %v", layout, err)
					}
					decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
					if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
						t.Fatalf("migration decisions = %d: %s", got, decisions)
					}
				} else if !bytes.Equal(f.headBytes(), headBeforeRetry) || !bytes.Equal(readFile(t, ledgerPath(f.root, f.handle)), bodyBeforeRetry) {
					t.Fatal("raw-successor retry wrote body or head bytes")
				}
				if _, err := RecordTarget(f.root, f.handle, f.target()); err != nil {
					t.Fatalf("ordinary write after admitted recovery: %v", err)
				}
				l, err := ReadLedger(f.root, f.handle)
				if err != nil || l.Recovered == nil || l.Recovered.Slot != first.Slot {
					t.Fatalf("ordinary write lost the recovery: %v, %+v", err, l.Recovered)
				}
				head, _, err := sealchain.HeadLink(ledgerHeadName(f.handle), mustSessionKey(t, f.root), f.headBytes())
				if err != nil || l.Seal == nil || head.Seq != l.Seal.Seq || head.Tag != l.Seal.Tag {
					t.Fatalf("ordinary write left a stale head: %+v, %+v, %v", head, l.Seal, err)
				}
			} else {
				second := f.quarantined()
				if second.Slot == first.Slot {
					t.Fatalf("crashed body reused first slot %s", first.Slot)
				}
				_, err := f.recover(first.Seq, mustSlotIndex(t, f.handle, first))
				if recoverCode(t, err) != RecoverSlotStale || !strings.Contains(err.Error(), second.Slot) {
					t.Fatalf("first decision retry = %v", err)
				}
				res, err := f.recover(second.Seq, mustSlotIndex(t, f.handle, second), carry)
				if err != nil {
					t.Fatalf("second decision = %v", err)
				}
				finalResult = res
				l, err := ReadLedger(f.root, f.handle)
				if err != nil || l.Seal == nil {
					t.Fatalf("final ledger = %v", err)
				}
				firstChoice := mustSlotChoice(t, f, first)
				secondChoice := mustSlotChoice(t, f, second)
				if l.Seal.Seq <= firstChoice.prev.Seq || l.Seal.Seq <= secondChoice.prev.Seq || res.NewSeq != l.Seal.Seq {
					t.Fatalf("final seq %d is not above slot maxima %d and %d: %+v", l.Seal.Seq, firstChoice.prev.Seq, secondChoice.prev.Seq, res)
				}
			}
			if got := migrationDecisionSeqs(t, f.root); !slices.Equal(got, tc.finalMigrations) {
				t.Fatalf("migration seqs after final decision = %v, want %v", got, tc.finalMigrations)
			}
			if finalResult.SeqFrom != tc.wantSeqFrom {
				t.Fatalf("SeqFrom = %q, want %q", finalResult.SeqFrom, tc.wantSeqFrom)
			}
			final, err := ReadLedger(f.root, f.handle)
			wantNote := "carried from quarantine seq " + strconv.Itoa(finalResult.OldSeq)
			if err != nil || len(final.Commits) != 1 || final.Commits[0].Note != wantNote {
				t.Fatalf("carried entry = %+v, want Note %q, err %v", final.Commits, wantNote, err)
			}
			assertSlotSnapshot(t, first.Slot, firstBefore)
		})
	}
}

func installFixtureEntryAtSeqOne(t *testing.T, f *fixture) CarryEntry {
	t.Helper()
	l, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	tree := sh(t, f.wt, "rev-parse", f.base+"^{tree}")
	entry := LedgerCommit{Task: "held", Commit: f.base, Tree: tree, Pushed: true, Branch: "main", Base: f.base, At: f.now}
	l.Commits = []LedgerCommit{entry}
	l.Seal = nil
	body, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	key := mustSessionKey(t, f.root)
	seal := sealchain.NewRawSeal(key.Key, ledgerLabel, nil, body, "v3.2.0-rc.2", LedgerSchema)
	raw, err := sealchain.JoinRaw(body, seal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(f.root, f.handle), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := sealchain.CompleteRawHead(f.root, ledgerHeadName(f.handle), key, seal, f.now); err != nil {
		t.Fatal(err)
	}
	return CarryEntry{Task: entry.Task, Commit: entry.Commit}
}

func migrationDecisionSeqs(t *testing.T, root string) []int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
		var d struct {
			Kind string `json:"kind"`
			Seq  int    `json:"seq"`
		}
		if json.Unmarshal(line, &d) == nil && d.Kind == sealchain.LayoutMigrationDecisionKind {
			out = append(out, d.Seq)
		}
	}
	return out
}

func mustSlotIndex(t *testing.T, handle string, q *QuarantinedError) int {
	t.Helper()
	i, err := slotIndex(handle, q.Seq, q.Slot)
	if err != nil {
		t.Fatal(err)
	}
	return i
}

func mustSlotChoice(t *testing.T, f *fixture, q *QuarantinedError) slotLinkChoice {
	t.Helper()
	slot, err := readSlot(f.root, f.handle, filepath.Base(q.Slot))
	if err != nil {
		t.Fatal(err)
	}
	choice, err := slot.link(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	return choice
}

func slotCopiedTag(t *testing.T, f *fixture, q *QuarantinedError, from string) string {
	t.Helper()
	slot, err := readSlot(f.root, f.handle, filepath.Base(q.Slot))
	if err != nil {
		t.Fatal(err)
	}
	if from == "head" {
		head, _, err := sealchain.HeadLink(ledgerHeadName(f.handle), mustSessionKey(t, f.root), slot.head)
		if err != nil {
			t.Fatal(err)
		}
		return head.Tag
	}
	if _, seal, err := sealchain.SplitRaw(slot.body); err == nil {
		return seal.Tag
	}
	var legacy Ledger
	if err := json.Unmarshal(slot.body, &legacy); err != nil || legacy.Seal == nil {
		t.Fatalf("slot body has no copied tag: %v", err)
	}
	return legacy.Seal.Tag
}

func snapshotSlot(t *testing.T, path string) map[string][]byte {
	t.Helper()
	out := make(map[string][]byte)
	for _, name := range slotFiles() {
		b, err := os.ReadFile(filepath.Join(path, name))
		if os.IsNotExist(err) {
			out[name] = nil
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		out[name] = b
	}
	return out
}

func assertSlotSnapshot(t *testing.T, path string, want map[string][]byte) {
	t.Helper()
	for name, before := range want {
		after, err := os.ReadFile(filepath.Join(path, name))
		if before == nil && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(after, before) {
			t.Fatalf("first slot's %s changed", name)
		}
	}
}

func assertNoRecoveryDecision(t *testing.T, root string) {
	t.Helper()
	p := filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	raw, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"kind":"ledger_recovered"`)) || bytes.Contains(raw, []byte(`"kind":"ledger_hold_released"`)) || bytes.Contains(raw, []byte(`"kind":"ledger_layout_migrated"`)) {
		t.Fatalf("refusal wrote a decision: %s", raw)
	}
}

func mustSessionKey(t *testing.T, root string) sealchain.Seed {
	t.Helper()
	key, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration(t *testing.T) {
	f := newFixture(t)
	l, key, legacy := installLegacyCommitLedger(t, f, nil)
	interrupted := writeInterruptedCommitMigration(t, f, l, key, legacy)
	original := readFile(t, ledgerPath(f.root, f.handle))
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if err := os.WriteFile(ledgerPath(f.root, f.handle), original, 0o600); err != nil {
		t.Fatal(err)
	}
	bodyBefore, headBefore := readFile(t, ledgerPath(f.root, f.handle)), f.headBytes()
	slotBefore := snapshotSlot(t, q.Slot)
	decisionPath := filepath.Join(f.root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	decisionsBefore := optionalFile(t, decisionPath)
	want := errors.New("injected verified migration mark failure")
	oldCreate := recoveredMarkCreate
	recoveredMarkCreate = func(slotDir, string, []byte) error { return want }
	t.Cleanup(func() { recoveredMarkCreate = oldCreate })
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverWriteFailed || !strings.Contains(err.Error(), quarantineRecovered) {
		t.Fatalf("verified migration mark failure = %v", err)
	}
	if !bytes.Equal(bodyBefore, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(headBefore, f.headBytes()) {
		t.Fatal("verified migration mark failure changed the ledger or head")
	}
	assertSlotSnapshot(t, q.Slot, slotBefore)
	if got := optionalFile(t, decisionPath); !bytes.Equal(got, decisionsBefore) {
		t.Fatalf("verified migration mark failure changed decisions: before %q after %q", decisionsBefore, got)
	}
	if _, err := os.Lstat(filepath.Join(q.Slot, quarantineRecovered)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified migration mark failure left a mark: %v", err)
	}
	recoveredMarkCreate = oldCreate
	res, err := f.recover(q.Seq, 1)
	if err != nil || !res.Verified {
		t.Fatalf("verified legacy recovery = %+v, %v", res, err)
	}
	body, seal, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle)))
	if err != nil || seal.Seq != interrupted.Seq+1 || seal.Prev != interrupted.Tag || !seal.Verify(key.Key, ledgerLabel, body) {
		t.Fatalf("verified legacy raw link = %+v, %v", seal, err)
	}
	decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 || !bytes.Contains(decisions, []byte(fmt.Sprintf("link %d", interrupted.Seq))) {
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
