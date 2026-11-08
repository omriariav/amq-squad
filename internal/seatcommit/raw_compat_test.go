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

func TestInterruptedCommitMigrationCompletesHeadAndLogsOnce(t *testing.T) {
	f := newFixture(t)
	l, key, legacy := installLegacyCommitLedger(t, f, nil)
	seal := writeInterruptedCommitMigration(t, f, l, key, legacy)

	got, err := ReadLedger(f.root, f.handle)
	if err != nil || got.Handle != f.handle {
		t.Fatalf("recovered ledger = %+v, %v", got, err)
	}
	head := readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	if layout, err := sealchain.HeadLayout(ledgerHeadName(f.handle), key, head); err != nil || layout != sealchain.RawLayout {
		t.Fatalf("completed head = %q, %v", layout, err)
	}
	if _, err := ReadLedger(f.root, f.handle); err != nil {
		t.Fatal(err)
	}
	decisions := readFile(t, f.root+"/extensions/amq-squad/scheduler/decisions.jsonl")
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 || !bytes.Contains(decisions, []byte(fmt.Sprintf("link %d", seal.Seq))) {
		t.Fatalf("migration decisions = %d:\n%s", got, decisions)
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

// E3: the 3.1.1 attempt number survives the raw ledger's replay.
func TestRawLedgerReplaysAttemptNumber(t *testing.T) {
	f := newFixture(t)
	l, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	l.Discarded = append(l.Discarded, LedgerDiscard{TaskID: "t3", Attempt: 3, Reason: "test", Parent: f.base, Tree: f.base, At: f.now})
	if err := writeLedger(f.root, l, "attempt replay"); err != nil {
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
