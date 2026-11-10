package schedproc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestPreparationClassifiesRealTamperedAndNewerWriterLedgersSeparately(t *testing.T) {
	root := t.TempDir()
	prep := &CleanSeats{Root: root}
	for _, handle := range []string{"tampered", "newer"} {
		if _, err := seatcommit.RecordBase(root, handle, filepath.Join(root, "wt"), "seat/"+handle, strings.Repeat("a", 40), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	tamperedPath := filepath.Join(seatcommit.LedgerDir(root), "tampered.json")
	raw := readBytes(t, tamperedPath)
	if err := os.WriteFile(tamperedPath, bytes.Replace(raw, []byte(strings.Repeat("a", 40)), []byte(strings.Repeat("0", 40)), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	err := prep.PrepareSeat("tampered")
	var alarm *scheduler.LedgerTamperedError
	if !errors.As(err, &alarm) {
		t.Fatalf("tampered preparation = %v", err)
	}

	newerPath := filepath.Join(seatcommit.LedgerDir(root), "newer.json")
	body, _, err := sealchain.SplitRaw(readBytes(t, newerPath))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	doc["future"] = true
	body, _ = json.Marshal(doc)
	key, err := sealchain.SessionKey(root)
	if err != nil {
		t.Fatal(err)
	}
	head := readBytes(t, sealchain.HeadPath(root, "commits.newer"))
	if _, err := sealchain.WriteRaw(root, "commits.newer", key, "amq-squad commits ledger v1", head, body, "v9.0.0", 2, func(raw []byte) error {
		return os.WriteFile(newerPath, raw, 0o600)
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	err = prep.PrepareSeat("newer")
	var refusal *scheduler.PrepareRefusal
	if errors.As(err, &alarm) || !errors.As(err, &refusal) || refusal.Reason != "newer_writer" {
		t.Fatalf("newer-writer preparation = %v", err)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPrepareErrorWrapsOnlyTamperingAsALedgerAlarm(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		wrap bool
	}{
		{name: "tampered", err: sealchain.ErrTampered, wrap: true},
		{name: "newer writer", err: sealchain.ErrNewerWriter},
		{name: "schema mismatch", err: sealchain.ErrSchemaMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := prepareError("seat-a", tc.err)
			var alarm *scheduler.LedgerTamperedError
			if got := errors.As(err, &alarm); got != tc.wrap {
				t.Fatalf("LedgerTamperedError = %v, want %v: %v", got, tc.wrap, err)
			}
		})
	}
}

// 3.2.0-0c-A, the A4 row's scheduler half: the prepare's read of a tampered
// ledger is still seat_prepare_failed (no lease), and its reason now names
// the quarantine the refusing read made, whose copy is the refused body byte
// for byte and whose sidecar names the prepare. The root is t.TempDir
// (rule 18).
func TestPrepareNamesTheQuarantineOfATamperedLedger(t *testing.T) {
	c := &Committer{Step: func(context.Context, string, task.Task) seatcommit.Result { return seatcommit.Result{OK: true} },
		ExitStep: func(string, string, string) seatcommit.Result { return seatcommit.Result{OK: true} }}
	r := newLiveRig(t, c)
	r.s.Preparer = &CleanSeats{Root: r.root}
	tk, err := r.store.Add(task.AddInput{Title: "t", Class: "summarize", Pool: &sonnet}, r.now)
	if err != nil {
		t.Fatal(err)
	}
	// The fake launcher names the seat of the task t1 seat-t1. A sealed
	// ledger for it, then a hand edit of its base (the tag fails).
	handle := "seat-" + tk.ID
	base := strings.Repeat("a", 40)
	if _, err := seatcommit.RecordBase(r.root, handle, filepath.Join(r.root, "wt"), "seat/"+handle, base, r.now); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(seatcommit.LedgerDir(r.root), handle+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	tampered := bytes.Replace(raw, []byte(base), []byte(strings.Repeat("0", 40)), 1)
	if bytes.Equal(tampered, raw) {
		t.Fatal("the base is not in the ledger")
	}
	if err := os.WriteFile(p, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile(sealchain.HeadPath(r.root, "commits."+handle))
	if err != nil {
		t.Fatal(err)
	}
	slot := filepath.Join(seatcommit.QuarantineDir(r.root), "commits."+handle+".seq1")
	reason := ""
	for i := 0; i < 4 && reason == ""; i++ {
		d, err := r.tick(func() {})
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range d {
			if x.Kind == "seat_prepare_failed" {
				reason = x.Reason
			}
		}
	}
	if reason == "" {
		t.Fatal("no seat_prepare_failed decision")
	}
	if !strings.Contains(reason, "ledger_tampered") || !strings.Contains(reason, "quarantined at "+slot) {
		t.Fatalf("seat_prepare_failed's reason does not name the quarantine %s: %q", slot, reason)
	}
	if got, err := os.ReadFile(filepath.Join(slot, "body.json")); err != nil || !bytes.Equal(got, tampered) {
		t.Fatalf("the quarantine body is not the refused body: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(slot, "head.json")); err != nil || !bytes.Equal(got, head) {
		t.Fatalf("the quarantine head is not the head: %v", err)
	}
	if side, err := os.ReadFile(filepath.Join(slot, "quarantine.txt")); err != nil || !strings.Contains(string(side), "writer: scheduler prepare\n") {
		t.Fatalf("the sidecar does not name the prepare: %v\n%s", err, side)
	}
	if got, _ := os.ReadFile(p); !bytes.Equal(got, tampered) {
		t.Fatal("the prepare moved the document")
	}
	if got, _ := r.store.Get(tk.ID); got.Lease != nil {
		t.Fatal("the task was leased on a tampered ledger")
	}
}

// PrepareSeat reads the ledger twice: compatibility first, then the real
// clean. The injected clock edits the ledger at the only boundary between
// those reads. This pins the second return through prepareError: a tamper
// discovered by the clean retains the typed alarm and its quarantine data.
func TestPrepareClassifiesTamperDiscoveredByTheClean(t *testing.T) {
	q := squadFixture(t)
	planned, err := (&Slots{Squad: q}).Plan(scheduler.SeatRequest{ID: "setup", Pool: sonnet})
	if err != nil {
		t.Fatal(err)
	}
	seat := planned.Seats[0]
	root := q.SessionRoot()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if _, err := seatcommit.RecordWorktreeBase("git", root, seat.Handle, seat.Worktree, seat.Branch, seatcommit.Target{BaseBranch: "main"}, now); err != nil {
		t.Fatal(err)
	}
	staged := launch.StagedDir(root, seat.Handle)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: seat.Handle, Root: root, Session: q.Session, Runtime: "codex", Worktree: seat.Worktree, Branch: seat.Branch, Base: seat.Base}); err != nil {
		t.Fatal(err)
	}
	ledgerPath := filepath.Join(seatcommit.LedgerDir(root), seat.Handle+".json")
	original := readBytes(t, ledgerPath)
	tampered := bytes.Replace(original, []byte(seat.Base), []byte(strings.Repeat("0", len(seat.Base))), 1)
	if bytes.Equal(tampered, original) {
		t.Fatal("the recorded base is not in the ledger")
	}

	calls := 0
	prep := &CleanSeats{Root: root, Git: "git"}
	prep.SetLog(io.Discard, func() time.Time {
		calls++
		if calls == 1 {
			if err := os.WriteFile(ledgerPath, tampered, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return now.Add(time.Duration(calls) * time.Second)
	})
	err = prep.PrepareSeat(seat.Handle)
	var alarm *scheduler.LedgerTamperedError
	if !errors.As(err, &alarm) {
		t.Fatalf("prepare after between-read tamper = %T %v", err, err)
	}
	if calls != 1 {
		t.Fatalf("clock calls = %d, want 1 so the edit stays after compatibility and before clean", calls)
	}
	if alarm.Handle != seat.Handle || alarm.Seq != "1" || alarm.Quarantine == "" {
		t.Fatalf("typed alarm = %+v", alarm)
	}
	if _, err := os.Stat(alarm.Quarantine); err != nil {
		t.Fatalf("quarantine %q: %v", alarm.Quarantine, err)
	}
	entries, err := os.ReadDir(seatcommit.QuarantineDir(root))
	if err != nil || len(entries) != 1 {
		t.Fatalf("quarantine slots = %v, %v; want exactly the clean's slot", entries, err)
	}
}
