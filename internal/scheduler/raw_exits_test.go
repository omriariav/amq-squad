package scheduler

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

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
	_, err = sealchain.WriteRaw(root, exitsHeadName, key, exitsLabel, body, version, schema, func(raw []byte) error {
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
			if again := g.tick(); has(again, tc.kind, "", "") {
				t.Fatalf("typed stop logged twice: %+v", again)
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

func TestLegacyExitsMigrationRecordsLinkAndRejectsDowngrade(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "exits.json")
	key, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	legacy := exitsLedger{Schema: 1, Queue: []exitItem{{Handle: "old-1", Why: "restart", At: time.Unix(1, 0)}}}
	legacyBody := legacy
	_, err = sealchain.Write(root, exitsHeadName, key, exitsLabel, legacyBody, func(link *sealchain.Link) error {
		legacy.Seal = link
		raw, _ := json.Marshal(legacy)
		return os.WriteFile(path, raw, 0o600)
	}, time.Unix(2, 0))
	if err != nil {
		t.Fatal(err)
	}
	legacyLink := legacy.Seal

	g := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root})
	g.s.Exits = &deferringExits{}
	decisions := g.tick()
	var migrated bool
	for _, d := range decisions {
		if d.Kind == "ledger_layout_migrated" && strings.Contains(d.Reason, "exits ledger") && strings.Contains(d.Reason, "link 2") {
			migrated = true
		}
	}
	if !migrated {
		t.Fatalf("migration decision = %+v", decisions)
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
