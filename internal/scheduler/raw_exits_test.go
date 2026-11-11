package scheduler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// schedulerRC1Head freezes v3.2.0-rc.1's legacy head fields at 3966afb7,
// internal/sealchain/sealchain.go:261-270. It deliberately has no layout
// field and is independent of the current production head type.
type schedulerRC1Head struct {
	Schema  int       `json:"schema"`
	Name    string    `json:"name"`
	Seq     int       `json:"seq"`
	Link    string    `json:"link_tag"`
	SeedDev uint64    `json:"seed_dev"`
	SeedIno uint64    `json:"seed_ino"`
	At      time.Time `json:"at"`
	Tag     string    `json:"tag,omitempty"`
}

func (h schedulerRC1Head) signed(key []byte) schedulerRC1Head {
	h.Tag = ""
	h.Tag = sealchain.Tag(key, "amq-squad sealed head v1", h)
	return h
}

func installLiteralRC1Exits(t *testing.T, root, path string) {
	t.Helper()
	key, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	// Frozen from v3.2.0-rc.1 internal/scheduler/exits.go:73-85 and
	// :165-173. Current exitItem and exitsLedger do not write this fixture.
	const body = `{"schema":1,"queue":[{"handle":"old-1","task_id":"t1","why":"restart","gone":true,"at":"1970-01-01T00:00:01Z","deferrals":2,"attempt":"launch-1"}]}`
	link := sealchain.Seal(key.Key, exitsLabel, nil, json.RawMessage(body))
	linkBytes, err := json.Marshal(link)
	if err != nil {
		t.Fatal(err)
	}
	document := append([]byte(body[:len(body)-1]), []byte(`,"seal":`)...)
	document = append(document, linkBytes...)
	document = append(document, '}')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	head := schedulerRC1Head{Schema: 1, Name: exitsHeadName, Seq: link.Seq, Link: link.Tag, SeedDev: key.Dev, SeedIno: key.Ino, At: time.Unix(2, 0).UTC()}.signed(key.Key)
	headBytes, err := json.Marshal(head)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(sealchain.HeadPath(root, exitsHeadName)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sealchain.HeadPath(root, exitsHeadName), headBytes, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRC1WrittenExitsLedgerReadsAndMigratesOnce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "extensions", "amq-squad", "scheduler", "exits.json")
	installLiteralRC1Exits(t, root, path)
	s := &Scheduler{Config: Config{ExitsPath: path, SealRoot: root}, Exits: &deferringExits{}}
	s.loadExits()
	if s.exitsErr != nil || len(s.exitQueue) != 1 || s.exitQueue[0].TaskAttempt != 0 || s.exitQueue[0].Close {
		t.Fatalf("frozen rc.1 load = err %v queue %+v", s.exitsErr, s.exitQueue)
	}
	key, ok, err := sealchain.ReadKey(root)
	if err != nil || !ok {
		t.Fatalf("key = %v, %v", ok, err)
	}
	head := readRawExitsFile(t, sealchain.HeadPath(root, exitsHeadName))
	link, layout, err := sealchain.HeadLink(exitsHeadName, key, head)
	if err != nil || layout != sealchain.RawLayout || link.Seq != 2 {
		t.Fatalf("first write head = %+v layout %q err %v", link, layout, err)
	}
	decisionPath := filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")
	if got := bytes.Count(readRawExitsFile(t, decisionPath), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("first write migration records = %d", got)
	}
	if err := s.saveExits(); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(readRawExitsFile(t, decisionPath), []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("second write migration records = %d", got)
	}
}

func schedulerRollbackJSONKeys(typ reflect.Type) []string {
	var keys []string
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if key == "" {
			key = field.Name
		}
		if key != "-" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func TestRC1ExitsSealedTypeKeyGuard(t *testing.T) {
	tag := map[string]map[string]bool{
		"exitsLedger": {"schema": true, "queue": true, "seal": true},
		"exitItem":    {"handle": true, "task_id": true, "why": true, "gone": true, "at": true, "deferrals": true, "attempt": true},
	}
	post := map[string]map[string]string{
		"exitItem": {
			"task_attempt": "TestExitsLedgerWrittenByRC1ReadsWithTheNewFieldsAbsent",
			"close":        "TestExitsLedgerWrittenByRC1ReadsWithTheNewFieldsAbsent",
		},
	}
	types := map[string]reflect.Type{"exitsLedger": reflect.TypeOf(exitsLedger{}), "exitItem": reflect.TypeOf(exitItem{})}
	var packageTests []byte
	paths, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		packageTests = append(packageTests, raw...)
	}
	for typeName, typ := range types {
		for _, key := range schedulerRollbackJSONKeys(typ) {
			if tag[typeName][key] {
				continue
			}
			row := post[typeName][key]
			if row == "" {
				t.Errorf("add a frozen rc.1 row for %s.%s and list it here", typeName, key)
				continue
			}
			if !bytes.Contains(packageTests, []byte("func "+row+"(")) {
				t.Errorf("listed frozen rc.1 row %s for %s.%s does not exist", row, typeName, key)
			}
		}
	}
}

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
	path := filepath.Join(root, "extensions", "amq-squad", "scheduler", "exits.json")
	s := &Scheduler{Config: Config{ExitsPath: path, SealRoot: root}, Exits: &deferringExits{}}
	s.AdoptClosing("old-1", sonnet, time.Unix(10, 0))
	if s.exitsErr != nil {
		t.Fatal(s.exitsErr)
	}
	t.Logf("exits fixture: %s", path)
}

// TestContinueRollbackExitsFixture is the exits half of the rollback fixture.
// A tag-local writer created the legacy queue before this current-build write.
func TestContinueRollbackExitsFixture(t *testing.T) {
	root := os.Getenv("AMQSQ_ROLLBACK_FIXTURE_ROOT")
	mode := os.Getenv("AMQSQ_ROLLBACK_MODE")
	if root == "" || mode == "" {
		t.Skip("set AMQSQ_ROLLBACK_FIXTURE_ROOT and AMQSQ_ROLLBACK_MODE")
	}
	path := filepath.Join(root, "extensions", "amq-squad", "scheduler", "exits.json")
	s := &Scheduler{Config: Config{ExitsPath: path, SealRoot: root}, Exits: &deferringExits{}}
	if mode == "f2" {
		crash := errors.New("rollback exits fixture crash after migration decision")
		afterExitsMigrationDecision = func(string, int) error { return crash }
		t.Cleanup(func() { afterExitsMigrationDecision = nil })
		s.loadExits()
		if !errors.Is(s.exitsErr, crash) {
			t.Fatalf("interrupted exits migration = %v", s.exitsErr)
		}
		afterExitsMigrationDecision = nil
	} else {
		s.AdoptClosing("current-1", sonnet, time.Unix(30, 0))
		if s.exitsErr != nil {
			t.Fatal(s.exitsErr)
		}
	}
	current, err := verifyExitsLedger(root, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("current exits fixture mode=%s legacy=%v migration=%+v queue=%d", mode, current.legacy, current.migration, len(current.ledger.Queue))
}

// TestRollbackRollForwardSchedulerCompatibility runs the current scheduler
// after an old tag probe and records the complete one-tick decision list.
func TestRollbackRollForwardSchedulerCompatibility(t *testing.T) {
	root := os.Getenv("AMQSQ_ROLLFORWARD_ROOT")
	if root == "" {
		t.Skip("set AMQSQ_ROLLFORWARD_ROOT")
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	key, ok, err := sealchain.ReadKey(root)
	if err != nil || !ok {
		t.Fatalf("roll-forward key = %v, %v", ok, err)
	}
	headPath := sealchain.HeadPath(root, exitsHeadName)
	beforeHead := readRawExitsFile(t, headPath)
	beforeLink, beforeLayout, err := sealchain.HeadLink(exitsHeadName, key, beforeHead)
	if err != nil {
		t.Fatal(err)
	}
	s := &Scheduler{Store: store, Now: func() time.Time { return time.Unix(60, 0) }, Exits: &deferringExits{}, Config: Config{ExitsPath: filepath.Join(root, "extensions", "amq-squad", "scheduler", "exits.json"), SealRoot: root, IdleTimeout: time.Hour}}
	decisions, tickErr := s.Tick()
	t.Logf("roll-forward scheduler tick error: %v", tickErr)
	if tickErr != nil {
		t.Fatal(tickErr)
	}
	for _, decision := range decisions {
		t.Logf("roll-forward decision: %s reason=%s", decision.Kind, decision.Reason)
		if decision.Kind == "ledger_tampered" {
			t.Fatalf("roll-forward wrote ledger_tampered: %+v", decision)
		}
	}
	if errors.Is(s.exitsErr, sealchain.ErrTampered) {
		t.Fatalf("roll-forward exits ledger_tampered: %v", s.exitsErr)
	}
	if s.exitsErr != nil {
		t.Fatalf("roll-forward exits error: %v", s.exitsErr)
	}
	afterHead := readRawExitsFile(t, headPath)
	afterLink, afterLayout, err := sealchain.HeadLink(exitsHeadName, key, afterHead)
	if err != nil || afterLayout != sealchain.RawLayout || afterLink.Seq <= beforeLink.Seq || afterLink.Tag == "" {
		t.Fatalf("roll-forward exits head = %+v layout %q err %v; before %+v %q", afterLink, afterLayout, err, beforeLink, beforeLayout)
	}
	decisionBytes := readRawExitsFile(t, filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl"))
	migrations := 0
	for _, line := range bytes.Split(decisionBytes, []byte{'\n'}) {
		var row struct {
			Kind       string `json:"kind"`
			LedgerKind string `json:"ledger_kind"`
		}
		if json.Unmarshal(line, &row) == nil && row.Kind == sealchain.LayoutMigrationDecisionKind && row.LedgerKind == "exits" {
			migrations++
		}
	}
	if migrations != 1 {
		t.Fatalf("exits migration records = %d", migrations)
	}
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

// TestWriteRollbackCancelFixtures prepares P5's F5a/F5b roots through the
// current product verbs. The old-version process probe consumes the roots;
// normal package runs skip this disposable fixture writer.
func TestWriteRollbackCancelFixtures(t *testing.T) {
	base := os.Getenv("AMQSQ_WRITE_CANCEL_ROOT")
	if base == "" {
		t.Skip("set AMQSQ_WRITE_CANCEL_ROOT")
	}
	for _, cleaned := range []bool{false, true} {
		name := "f5a"
		if cleaned {
			name = "f5b"
		}
		root := filepath.Join(base, name)
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		store, err := task.Open(root)
		if err != nil {
			t.Fatal(err)
		}
		pool := task.Pool{Runtime: "codex", Model: "gpt-5.6-sol", Effort: "high"}
		now := time.Unix(40, 0).UTC()
		cancelled, err := store.Add(task.AddInput{Title: "cancelled attempt", Class: "plan", Pool: &pool}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Grant(cancelled.ID, "qa", &pool, time.Hour, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Cancel(cancelled.ID, "lead", "rollback F5", now.Add(2*time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Add(task.AddInput{Title: "queued after cancel", Class: "plan", Pool: &pool}, now.Add(3*time.Second)); err != nil {
			t.Fatal(err)
		}

		path := filepath.Join(root, "extensions", "amq-squad", "scheduler", "exits.json")
		s := &Scheduler{
			Config:      Config{ExitsPath: path, SealRoot: root},
			Exits:       &deferringExits{},
			exitsLoaded: true,
			exitQueue: []exitItem{{Handle: "qa", TaskID: cancelled.ID, Why: task.OutcomeCancelled,
				At: now.Add(4 * time.Second), TaskAttempt: 1, Close: true}},
		}
		if err := s.writeExits(); err != nil {
			t.Fatal(err)
		}
		if cleaned {
			if _, err := store.MarkAttemptCleaned(cancelled.ID, 1, now.Add(5*time.Second)); err != nil {
				t.Fatal(err)
			}
			s.exitQueue = nil
			if err := s.writeExits(); err != nil {
				t.Fatal(err)
			}
		} else {
			later, nonce, _, err := store.ReserveCancel("qa", cancelled.ID, 1, now.Add(5*time.Second))
			if err != nil || later != "" || nonce == "" {
				t.Fatalf("reserve cancel = later %q nonce %q err %v", later, nonce, err)
			}
		}
	}
}
