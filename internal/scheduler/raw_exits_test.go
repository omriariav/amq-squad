package scheduler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

func readRawExitsFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func writeFutureExits(t *testing.T, root, path, version string, schema int) {
	t.Helper()
	raw, err := os.ReadFile(path)
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
	key, ok, err := sealchain.ReadKey(root)
	if err != nil || !ok {
		t.Fatalf("seed = %v, %v", ok, err)
	}
	head, err := os.ReadFile(sealchain.HeadPath(root, exitsHeadName))
	if err != nil {
		t.Fatal(err)
	}
	_, err = sealchain.WriteRaw(root, exitsHeadName, key, exitsLabel, head, body, version, schema, func(raw []byte) error {
		return os.WriteFile(path, raw, 0o600)
	}, time.Unix(20, 0))
	if err != nil {
		t.Fatal(err)
	}
}

func TestExitsCompatibilityStopsAreTypedRoutedAndLoggedOnce(t *testing.T) {
	for _, tc := range []struct {
		name, version, kind, route string
		schema                     int
		want                       error
	}{
		{"newer", "v9.0.0", "newer_writer", "owner", 2, sealchain.ErrNewerWriter},
		{"same schema", "(devel)", "schema_mismatch", "lead", 1, sealchain.ErrSchemaMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			ledger := filepath.Join(t.TempDir(), "exits.json")
			cfg := Config{IdleTimeout: time.Hour, ExitsPath: ledger, SealRoot: root}
			first := newHarness(t, cfg)
			first.s.Exits = &deferringExits{}
			first.s.AdoptClosing("old-1", sonnet, first.clk.Now())
			writeFutureExits(t, root, ledger, tc.version, tc.schema)

			g := newHarness(t, cfg)
			g.s.Exits = &deferringExits{}
			g.add("work", &sonnet)
			g.up("seat-a", sonnet)
			decisions := g.tick()
			if !has(decisions, tc.kind, "", "") || has(decisions, "dispatched", "", "") || !errors.Is(g.s.exitsErr, tc.want) {
				t.Fatalf("first tick: %+v (%v)", decisions, g.s.exitsErr)
			}
			var routed bool
			for _, d := range decisions {
				if d.Kind == tc.kind && strings.HasPrefix(d.Reason, tc.route+": ") {
					routed = true
				}
			}
			if !routed {
				t.Fatalf("%s was not routed to %s: %+v", tc.kind, tc.route, decisions)
			}
			if again := g.tick(); has(again, tc.kind, "", "") || has(again, "exits_ledger_unwritable", "", "") || g.s.exitsUnwritableCount != 2 {
				t.Fatalf("persistent stop was not counted silently: %+v count=%d", again, g.s.exitsUnwritableCount)
			}
		})
	}
}

// TestWriteRawExitsCompatibilityFixture is the exits half of E9/E10. It
// shares the explicit temp root with the commit fixture above.
func TestWriteRawExitsCompatibilityFixture(t *testing.T) {
	root := os.Getenv("AMQSQ_0E_FIXTURE_ROOT")
	if root == "" {
		t.Skip("set AMQSQ_0E_FIXTURE_ROOT for the compatibility evidence run")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "exits.json")
	s := &Scheduler{Config: Config{ExitsPath: path, SealRoot: root}, Exits: &deferringExits{}}
	s.AdoptClosing("old-1", sonnet, time.Unix(10, 0))
	if s.exitsErr != nil {
		t.Fatal(s.exitsErr)
	}
	t.Logf("exits fixture: %s", path)
}

func installLegacyExits(t *testing.T, root, path string) (exitsLedger, sealchain.Seed, *sealchain.Link) {
	t.Helper()
	key, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	legacy := exitsLedger{Schema: 1, Queue: []exitItem{{Handle: "old-1", Why: "restart", At: time.Unix(1, 0)}}}
	body := legacy
	_, err = sealchain.Write(root, exitsHeadName, key, exitsLabel, nil, body, func(link *sealchain.Link) error {
		legacy.Seal = link
		raw, _ := json.Marshal(legacy)
		return os.WriteFile(path, raw, 0o600)
	}, time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	link := legacy.Seal
	legacy.Seal = nil
	return legacy, key, link
}

func writeInterruptedExitsMigration(t *testing.T, path string, l exitsLedger, key sealchain.Seed, prev *sealchain.Link) sealchain.RawSeal {
	t.Helper()
	body, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	seal := sealchain.NewRawSeal(key.Key, exitsLabel, prev, body, "v3.2.0-rc.2", 1)
	raw, err := sealchain.JoinRaw(body, seal)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return seal
}

func TestInterruptedExitsMigrationReadersArePureAndWriterCompletesOnce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "exits.json")
	l, key, legacy := installLegacyExits(t, root, path)
	seal := writeInterruptedExitsMigration(t, path, l, key, legacy)
	headPath := sealchain.HeadPath(root, exitsHeadName)
	before := append([]byte(nil), readRawExitsFile(t, headPath)...)
	errCh := make(chan error, 2)
	for range 2 {
		go func() { _, err := verifyExitsLedger(root, path); errCh <- err }()
	}
	for range 2 {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	if after := readRawExitsFile(t, headPath); !bytes.Equal(after, before) {
		t.Fatal("exits readers changed the legacy head")
	}
	g := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root})
	g.s.Exits = &deferringExits{}
	g.s.loadExits()
	if g.s.exitsErr != nil {
		t.Fatal(g.s.exitsErr)
	}
	head, err := os.ReadFile(sealchain.HeadPath(root, exitsHeadName))
	if err != nil {
		t.Fatal(err)
	}
	if layout, err := sealchain.HeadLayout(exitsHeadName, key, head); err != nil || layout != sealchain.RawLayout {
		t.Fatalf("completed head = %q, %v", layout, err)
	}
	decisions := readRawExitsFile(t, filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl"))
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 || !bytes.Contains(decisions, []byte(fmt.Sprintf("link %d", seal.Seq))) {
		t.Fatalf("migration decisions = %d: %s", got, decisions)
	}
}

func TestExitsMigrationDecisionSurvivesCrashAndDedupes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "exits.json")
	l, key, legacy := installLegacyExits(t, root, path)
	writeInterruptedExitsMigration(t, path, l, key, legacy)
	crash := errors.New("crash after exits migration decision")
	afterExitsMigrationDecision = func(string, int) error { return crash }
	t.Cleanup(func() { afterExitsMigrationDecision = nil })
	g := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root})
	g.s.Exits = &deferringExits{}
	g.s.loadExits()
	if !errors.Is(g.s.exitsErr, crash) || g.s.exitsLoaded {
		t.Fatalf("first load = loaded %v, err %v", g.s.exitsLoaded, g.s.exitsErr)
	}
	decisionPath := filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	if got := bytes.Count(readRawExitsFile(t, decisionPath), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("decisions after crash = %d", got)
	}
	afterExitsMigrationDecision = nil
	g.s.loadExits()
	if g.s.exitsErr != nil || !g.s.exitsLoaded {
		t.Fatalf("retry = loaded %v, err %v", g.s.exitsLoaded, g.s.exitsErr)
	}
	if got := bytes.Count(readRawExitsFile(t, decisionPath), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("decisions after retry = %d", got)
	}
}

func TestInterruptedExitsMigrationRejectsSkipAndWrongPrev(t *testing.T) {
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
			root := t.TempDir()
			path := filepath.Join(t.TempDir(), "exits.json")
			l, key, legacy := installLegacyExits(t, root, path)
			writeInterruptedExitsMigration(t, path, l, key, tc.prev(legacy))
			g := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root})
			g.s.Exits = &deferringExits{}
			decisions := g.tick()
			if !has(decisions, "ledger_tampered", "", "") || has(decisions, "ledger_layout_migrated", "", "") {
				t.Fatalf("invalid migration = %+v", decisions)
			}
		})
	}
}

func TestLegacyExitsMigrationRecordsLinkAndRejectsDowngrade(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "exits.json")
	legacyBody, key, legacyLink := installLegacyExits(t, root, path)

	g := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root})
	g.s.Exits = &deferringExits{}
	g.tick()
	decisions := readRawExitsFile(t, filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl"))
	if bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)) != 1 || !bytes.Contains(decisions, []byte(`"ledger_kind":"exits"`)) || !bytes.Contains(decisions, []byte("link 2")) {
		t.Fatalf("migration decision = %s", decisions)
	}
	document, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := sealchain.SplitRaw(document)
	if err != nil || !bytes.Contains(body, []byte(`"queue"`)) {
		t.Fatalf("raw exits document = %q, %v", document, err)
	}
	head, err := os.ReadFile(sealchain.HeadPath(root, exitsHeadName))
	if err != nil {
		t.Fatal(err)
	}
	legacyBody.Seal = nil
	if err := sealchain.CheckLegacy(exitsHeadName, key, exitsLabel, legacyLink, legacyBody, head); !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("legacy downgrade after exits migration = %v", err)
	}
}
