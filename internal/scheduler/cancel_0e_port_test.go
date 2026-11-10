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
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// rc1ExitItem and rc1ExitsLedger freeze the field order and JSON tags from
// v3.2.0-rc.1, commit 3966afb72b85e1ab6c0fc8c879f1c5ef03f92a40,
// internal/scheduler/exits.go:73-85 and :165-173. Production types must not
// participate in writing this compatibility fixture.
type rc1ExitItem struct {
	Handle    string    `json:"handle"`
	TaskID    string    `json:"task_id,omitempty"`
	Why       string    `json:"why"`
	Gone      bool      `json:"gone,omitempty"`
	At        time.Time `json:"at"`
	Deferrals int       `json:"deferrals,omitempty"`
	Attempt   string    `json:"attempt,omitempty"`
}

type rc1ExitsLedger struct {
	Schema int             `json:"schema"`
	Queue  []rc1ExitItem   `json:"queue"`
	Seal   *sealchain.Link `json:"seal,omitempty"`
}

func installFrozenRC1Exits(t *testing.T, root, path string) {
	t.Helper()
	key, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	legacy := rc1ExitsLedger{Schema: 1, Queue: []rc1ExitItem{{Handle: "old-1", Why: "restart", At: time.Unix(1, 0).UTC()}}}
	body := legacy
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	const rc1Body = `{"schema":1,"queue":[{"handle":"old-1","why":"restart","at":"1970-01-01T00:00:01Z"}]}`
	if string(bodyBytes) != rc1Body {
		t.Fatalf("rc.1 body bytes = %q, want literal %q", bodyBytes, rc1Body)
	}
	_, err = sealchain.Write(root, exitsHeadName, key, exitsLabel, nil, body, func(link *sealchain.Link) error {
		legacy.Seal = link
		raw, err := json.Marshal(legacy)
		if err != nil {
			return err
		}
		return os.WriteFile(path, raw, 0o600)
	}, time.Unix(2, 0).UTC())
	if err != nil {
		t.Fatal(err)
	}
}

func TestExitsLedgerWrittenByRC1ReadsWithTheNewFieldsAbsent(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "exits.json")
	installFrozenRC1Exits(t, root, path)
	s := &Scheduler{Config: Config{ExitsPath: path, SealRoot: root}, Exits: &deferringExits{}}
	s.loadExits()
	if s.exitsErr != nil || len(s.exitQueue) != 1 || s.exitQueue[0].TaskAttempt != 0 || s.exitQueue[0].Close {
		t.Fatalf("legacy exits = err %v queue %+v", s.exitsErr, s.exitQueue)
	}
	raw := readRawExitsFile(t, path)
	if _, _, err := sealchain.SplitRaw(raw); err != nil {
		t.Fatalf("first locked write did not migrate to raw layout: %v", err)
	}
	decisions := readRawExitsFile(t, filepath.Join(root, "extensions", "amq-squad", "scheduler", "decisions.jsonl"))
	if got := bytes.Count(decisions, []byte(`"kind":"ledger_layout_migrated"`)); got != 1 {
		t.Fatalf("migration records = %d: %s", got, decisions)
	}
}

func TestSealedExitsLedgerCarriesTaskAttemptAndCloseAcrossARestart(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "exits.json")
	now := t0
	tk := cancelledOn(t, store, "seat-a", now)
	newScheduler := func(incarnation string, launcher SeatLauncher, cleaner *attemptCleanRecorder) *Scheduler {
		s, err := New(Options{Store: store, Now: func() time.Time { return now.Add(2 * time.Second) }, Launcher: launcher, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root}, Incarnation: incarnation})
		if err != nil {
			t.Fatal(err)
		}
		s.AdoptUp("seat-a", sonnet, now.Add(2*time.Second))
		return s
	}
	firstCleaner := &attemptCleanRecorder{}
	first := newScheduler("firstsealed", &asyncHoldLauncher{&holdLauncher{fakeLauncher: &fakeLauncher{}}}, firstCleaner)
	if decisions, err := first.Tick(); err != nil || !has(decisions, "seat_closing", tk.ID, "seat-a") {
		t.Fatalf("first tick = %+v, %v", decisions, err)
	}
	raw := readRawExitsFile(t, path)
	body, _, err := sealchain.SplitRaw(raw)
	if err != nil || !bytes.Contains(body, []byte(`"task_attempt": 1`)) || !bytes.Contains(body, []byte(`"close": true`)) {
		t.Fatalf("sealed body = %s, %v", body, err)
	}
	secondCleaner := &attemptCleanRecorder{}
	launcher := &holdLauncher{fakeLauncher: &fakeLauncher{}}
	second := newScheduler("secondsealed", launcher, secondCleaner)
	decisions, err := second.Tick()
	if err != nil || second.exitsErr != nil || !has(decisions, "seat_closed", "", "seat-a") || len(secondCleaner.cleaned) != 0 {
		t.Fatalf("restart = decisions %+v exits %v cleaned %v err %v", decisions, second.exitsErr, secondCleaner.cleaned, err)
	}
}

func installSealedExitsWithCancelFields(t *testing.T) (string, string, []byte, sealchain.RawSeal) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "exits.json")
	s := &Scheduler{Config: Config{ExitsPath: path, SealRoot: root}, Exits: &deferringExits{}, exitsLoaded: true,
		exitQueue: []exitItem{{Handle: "seat-a", TaskID: "t1", Why: task.OutcomeCancelled, At: time.Unix(1, 0), TaskAttempt: 2, Close: true}}}
	if err := s.saveExits(); err != nil {
		t.Fatal(err)
	}
	raw := readRawExitsFile(t, path)
	body, seal, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	return root, path, body, seal
}

func TestSealedExitsLedgerStillRefusesAnUnknownFieldBesideTheNewOnes(t *testing.T) {
	type oldExitItem struct {
		Handle    string    `json:"handle"`
		TaskID    string    `json:"task_id,omitempty"`
		Why       string    `json:"why"`
		Gone      bool      `json:"gone,omitempty"`
		At        time.Time `json:"at"`
		Deferrals int       `json:"deferrals,omitempty"`
		Attempt   string    `json:"attempt,omitempty"`
	}
	var old struct {
		Schema int           `json:"schema"`
		Queue  []oldExitItem `json:"queue"`
	}
	_, _, body, seal := installSealedExitsWithCancelFields(t)
	if err := sealchain.DecodeStrict("the pre-port exits ledger", body, seal, 1, &old); !errors.Is(err, sealchain.ErrSchemaMismatch) {
		t.Fatalf("old field set accepted task_attempt/close: %v", err)
	}

	for _, tc := range []struct {
		name, version, kind, route string
		schema                     int
		want                       error
	}{
		{"same schema", "(devel)", "schema_mismatch", task.RouteLead, 1, sealchain.ErrSchemaMismatch},
		{"newer schema", "v9.0.0", "newer_writer", task.RouteOwner, 2, sealchain.ErrNewerWriter},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, path, _, _ := installSealedExitsWithCancelFields(t)
			writeFutureExits(t, root, path, tc.version, tc.schema)
			g := newHarness(t, Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root})
			g.s.Exits = &deferringExits{}
			pending := g.add("must stay pending", &sonnet)
			g.s.AdoptUp("seat-idle", sonnet, t0)
			var all []Decision
			for range 3 {
				all = append(all, g.tick()...)
			}
			kindCount := 0
			for _, d := range all {
				if d.Kind == tc.kind {
					kindCount++
					if !strings.HasPrefix(d.Reason, tc.route+": ") {
						t.Fatalf("%s route = %q, want %s", tc.kind, d.Reason, tc.route)
					}
				}
				if d.Kind == "dispatched" {
					t.Fatalf("future exits dispatched an idle seat: %+v", all)
				}
			}
			if kindCount != 1 || !errors.Is(g.s.exitsErr, tc.want) {
				t.Fatalf("future exits = decisions %+v count %d err %v", all, kindCount, g.s.exitsErr)
			}
			if got, err := g.store.Get(pending.ID); err != nil || got.State != task.StatePending || got.Lease != nil {
				t.Fatalf("pending task changed under refusal: %+v, %v", got, err)
			}
		})
	}
}

func TestCancelWaitsWhileTheExitsLedgerRefuses(t *testing.T) {
	root, path, _, _ := installSealedExitsWithCancelFields(t)
	document := append([]byte(nil), readRawExitsFile(t, path)...)
	headPath := sealchain.HeadPath(root, exitsHeadName)
	head := append([]byte(nil), readRawExitsFile(t, headPath)...)
	writeFutureExits(t, root, path, "v9.0.0", 2)
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tk := cancelledOn(t, store, "seat-b", t0)
	cleaner := &attemptCleanRecorder{}
	s, err := New(Options{Store: store, Now: func() time.Time { return t0.Add(2 * time.Second) }, Launcher: &holdLauncher{fakeLauncher: &fakeLauncher{}}, Exits: &exitRecorder{bad: map[string]bool{}}, Preparer: cleaner, Liveness: goneLiveness{}, Config: Config{IdleTimeout: time.Hour, ExitsPath: path, SealRoot: root}, Incarnation: "refused"})
	if err != nil {
		t.Fatal(err)
	}
	s.AdoptUp("seat-b", sonnet, t0)
	decisions, err := s.Tick()
	if err != nil || !has(decisions, "newer_writer", "", "") || has(decisions, "seat_closed", "", "seat-b") || len(cleaner.cleaned) != 0 {
		t.Fatalf("refused tick = %+v cleaned %v err %v", decisions, cleaner.cleaned, err)
	}
	if got, _ := store.Get(tk.ID); got.Attempts[0].CleanedAt != nil {
		t.Fatalf("refused exits ledger cleaned the cancel: %+v", got.Attempts[0])
	}
	// Restore a readable sealed ledger. The following tick may now close and
	// clean the cancellation; the compatibility stop no longer guards it.
	if err := os.WriteFile(path, document, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(headPath, head, 0o600); err != nil {
		t.Fatal(err)
	}
	var rounds [4][]Decision
	for i := range rounds {
		rounds[i], err = s.Tick()
		if err != nil {
			t.Fatalf("readable tick %d: %v", i+1, err)
		}
	}
	if !has(rounds[0], "seat_closing", tk.ID, "seat-b") || !has(rounds[0], "seat_closed", "", "seat-b") {
		t.Fatalf("readable tick 1 did not close: %+v", rounds[0])
	}
	if !has(rounds[1], "seat_cleaned", tk.ID, "seat-b") {
		t.Fatalf("readable tick 2 did not clean: %+v", rounds[1])
	}
	if len(rounds[2]) != 0 || len(rounds[3]) != 0 {
		t.Fatalf("readable ticks 3 and 4 repeated decisions: %+v %+v", rounds[2], rounds[3])
	}
	if s.exitsErr != nil {
		t.Fatalf("restored exits ledger remains refused: %v", s.exitsErr)
	}
	if got, _ := store.Get(tk.ID); got.Attempts[0].CleanedAt == nil {
		t.Fatalf("restored exits ledger did not record the clean: %+v", got.Attempts[0])
	}
}
