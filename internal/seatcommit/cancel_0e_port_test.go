package seatcommit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestCancelledStepDeferralCarriesItsReasonCode(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// cancelled\n")
	tk := f.reported("cmd/**")
	before, err := os.ReadFile(ledgerPath(f.root, f.handle))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	o := f.opts(tk)
	o.Ctx = ctx
	res := Run(o)
	after, err := os.ReadFile(ledgerPath(f.root, f.handle))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Deferred || res.ReasonCode != ReasonStepCancelled || res.Pushed || !bytes.Equal(before, after) {
		t.Fatalf("cancelled step = deferred %v reason %q pushed %v ledger changed %v", res.Deferred, res.ReasonCode, res.Pushed, !bytes.Equal(before, after))
	}
}

func TestCancelUnderALedgerThatRefusesAsNewerWriterOrSchemaMismatchHoldsTheSeat(t *testing.T) {
	for _, tc := range []struct {
		name, version, code string
		schema              int
	}{
		{"newer writer", "v9.0.0", ReasonNewerWriter, LedgerSchema + 1},
		{"schema mismatch", "(devel)", ReasonSchemaMismatch, LedgerSchema},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			tk := f.leased("keep.txt")
			target := filepath.Join(f.wt, "keep.txt")
			want := []byte("compatibility refusal keeps this work\n")
			if err := os.WriteFile(target, want, 0o644); err != nil {
				t.Fatal(err)
			}
			launcher := &cancellingProcessLauncher{}
			cleaner := &commitAttemptCleaner{f: f}
			s := cancelScheduler(t, f, launcher, cleaner, "compatrefusal")
			if _, err := f.store.Cancel(tk.ID, "lead", "stop", f.now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			pool := task.Pool{Runtime: "test", Model: "writer"}
			next, err := f.store.Add(task.AddInput{Title: "wait behind refusal", Class: "script", Pool: &pool}, f.now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			validDocument := append([]byte(nil), readFile(t, ledgerPath(f.root, f.handle))...)
			headPath := sealchain.HeadPath(f.root, ledgerHeadName(f.handle))
			validHead := append([]byte(nil), readFile(t, headPath)...)
			writeUnknownCommitField(t, f, tc.version, tc.schema)
			refusedDocument := append([]byte(nil), readFile(t, ledgerPath(f.root, f.handle))...)
			refusedHead := append([]byte(nil), readFile(t, headPath)...)

			if d, err := s.Tick(); err != nil || !decisionHas(d, "seat_closed") {
				t.Fatalf("close pass = %+v, %v", d, err)
			}
			var all []scheduler.Decision
			for range 5 {
				d, err := s.Tick()
				if err != nil {
					t.Fatal(err)
				}
				all = append(all, d...)
			}
			if got := decisionCount(all, "exit_refused"); got != 1 || cleaner.calls != 0 {
				t.Fatalf("refusal decisions = %d, cleaner calls = %d: %+v", got, cleaner.calls, all)
			}
			if got, _ := f.store.Get(next.ID); got.State != task.StatePending || got.Lease != nil {
				t.Fatalf("refused seat was granted: %+v", got)
			}
			if got, _ := f.store.Get(tk.ID); got.Attempts[0].CleanedAt != nil {
				t.Fatalf("refused cancellation was recorded cleaned: %+v", got.Attempts[0])
			}
			if body, err := os.ReadFile(target); err != nil || !bytes.Equal(body, want) {
				t.Fatalf("worktree changed: %q, %v", body, err)
			}
			if !bytes.Equal(readFile(t, ledgerPath(f.root, f.handle)), refusedDocument) || !bytes.Equal(readFile(t, headPath), refusedHead) {
				t.Fatal("exit refusal changed the ledger or head")
			}
			if entries, err := os.ReadDir(QuarantineDir(f.root)); err == nil && len(entries) != 0 {
				t.Fatalf("compatibility refusal created quarantine slots: %v", entries)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if _, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now.Add(3*time.Second)); err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("seat clean refusal = %v, want %s", err, tc.code)
			}
			if !bytes.Equal(readFile(t, ledgerPath(f.root, f.handle)), refusedDocument) || !bytes.Equal(readFile(t, headPath), refusedHead) {
				t.Fatal("manual clean refusal changed the ledger or head")
			}

			if err := os.WriteFile(ledgerPath(f.root, f.handle), validDocument, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(headPath, validHead, 0o600); err != nil {
				t.Fatal(err)
			}
			d, err := s.Tick()
			if err != nil || !decisionHas(d, "seat_cleaned") || cleaner.calls != 1 {
				t.Fatalf("readable ledger did not clean once: calls=%d decisions=%+v err=%v", cleaner.calls, d, err)
			}
			if got, _ := f.store.Get(tk.ID); got.Attempts[0].CleanedAt == nil {
				t.Fatalf("clean was not recorded: %+v", got.Attempts[0])
			}
		})
	}
}

func TestCancelArchiveOnALegacyLayoutLedgerMigratesOnce(t *testing.T) {
	f := newFixture(t)
	tk := f.leased("legacy.txt")
	if err := os.WriteFile(filepath.Join(f.wt, "legacy.txt"), []byte("cancel evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	installLegacyCommitLedger(t, f, nil)
	archived, err := ArchiveAttemptReason(f.root, f.handle, gitBin(t), nil, tk.ID, 1, task.OutcomeCancelled, f.now.Add(time.Second))
	if err != nil || !archived {
		t.Fatalf("legacy cancel archive = %v, %v", archived, err)
	}
	if _, _, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle))); err != nil {
		t.Fatalf("archive did not migrate the document to raw layout: %v", err)
	}
	key, ok, err := sealchain.ReadKey(f.root)
	if err != nil || !ok {
		t.Fatalf("session key = %v, %v", ok, err)
	}
	head := readFile(t, sealchain.HeadPath(f.root, ledgerHeadName(f.handle)))
	if layout, err := sealchain.HeadLayout(ledgerHeadName(f.handle), key, head); err != nil || layout != sealchain.RawLayout {
		t.Fatalf("head layout = %q, %v", layout, err)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || len(l.Discarded) != 1 || l.Discarded[0].TaskID != tk.ID || l.Discarded[0].Attempt != 1 {
		t.Fatalf("discard = %+v, %v", l.Discarded, err)
	}
	if got := migrationDecisionSeqs(t, f.root); len(got) != 1 {
		t.Fatalf("migration decisions = %v", got)
	}
	if archived, err = ArchiveAttemptReason(f.root, f.handle, gitBin(t), nil, tk.ID, 1, task.OutcomeCancelled, f.now.Add(2*time.Second)); err != nil || archived {
		t.Fatalf("repeated cancel archive = %v, %v", archived, err)
	}
	if got := migrationDecisionSeqs(t, f.root); len(got) != 1 {
		t.Fatalf("repeated archive wrote another migration decision: %v", got)
	}
}

func TestFailedCleanLeavesNoMarkerAndTheSeatStaysRefused(t *testing.T) {
	f := newFixture(t)
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// local commit\n")
	sh(t, f.wt, "add", "-A")
	sh(t, f.wt, "commit", "-q", "-m", "seat local commit")
	launcher := &cancellingProcessLauncher{}
	cleaner := &commitAttemptCleaner{f: f}
	exitsPath := filepath.Join(f.root, "scheduler-exits.json")
	cfg := scheduler.Config{IdleTimeout: time.Hour, ExitsPath: exitsPath, SealRoot: f.root}
	s := cancelSchedulerConfig(t, f, launcher, cleaner, "failedclean", cfg)
	if _, err := f.store.Cancel(tk.ID, "lead", "stop", f.now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if d, err := s.Tick(); err != nil || !decisionHas(d, "seat_closed") {
		t.Fatalf("close pass = %+v, %v", d, err)
	}
	failed, err := s.Tick()
	if err != nil || len(failed) != 1 || failed[0].Kind != "seat_clean_deferred" || failed[0].Reason == "" {
		t.Fatalf("failed clean pass = %+v, %v", failed, err)
	}
	deferredReason := failed[0].Reason
	marker := filepath.Join(f.store.Dir, "reserved", f.handle)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed clean left its marker: %v", err)
	}
	if got, _ := f.store.Get(tk.ID); got.State != task.StateCancelled || got.Attempts[0].CleanedAt != nil {
		t.Fatalf("cancelled attempt changed after failed clean: %+v", got)
	}
	if close, gone, ok := pendingExitState(s, f.handle, tk.ID); !ok || close || !gone {
		t.Fatalf("in-memory exit = present %v close %v gone %v", ok, close, gone)
	}
	if close, gone, ok := sealedExitState(t, exitsPath, f.handle, tk.ID); !ok || close || !gone {
		t.Fatalf("sealed exit = present %v close %v gone %v", ok, close, gone)
	}
	pool := task.Pool{Runtime: "test", Model: "writer"}
	next, err := f.store.Add(task.AddInput{Title: "must wait", Class: "script", Pool: &pool}, f.now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	// The same handle comes back up while its failed-clean item is still
	// queued. exitPending, not the absence of a counted seat, must hold it.
	s.AdoptUp(f.handle, pool, f.now.Add(2*time.Second))
	var repeated []scheduler.Decision
	for range 3 {
		d, err := s.Tick()
		if err != nil {
			t.Fatal(err)
		}
		repeated = append(repeated, d...)
		if decisionHas(d, "dispatched") {
			t.Fatalf("scheduler granted during the held exit: %+v", d)
		}
	}
	if got, _ := f.store.Get(next.ID); got.State != task.StatePending || got.Lease != nil {
		t.Fatalf("waiting task was granted: %+v", got)
	}
	for _, d := range repeated {
		if d.Kind == "seat_clean_deferred" && d.Reason == deferredReason {
			t.Fatalf("the unchanged deferred reason was logged again: %+v", repeated)
		}
	}

	secondLauncher := &cancellingProcessLauncher{}
	secondCleaner := &commitAttemptCleaner{f: f}
	second, err := scheduler.New(scheduler.Options{
		Store: f.store, Now: func() time.Time { return f.now.Add(3 * time.Second) },
		Launcher: secondLauncher, Exits: commitExitChecker{f: f}, Preparer: secondCleaner, Liveness: secondLauncher,
		Config: cfg, Incarnation: "failedrestart",
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		d, err := second.Tick()
		if err != nil {
			t.Fatal(err)
		}
		if decisionHas(d, "dispatched") {
			t.Fatalf("restart dispatched over the held item: %+v", d)
		}
	}
	if got, _ := f.store.Get(next.ID); got.State != task.StatePending || got.Lease != nil {
		t.Fatalf("restart granted the waiting task: %+v", got)
	}
	if close, gone, ok := pendingExitState(second, f.handle, tk.ID); !ok || close || !gone {
		t.Fatalf("restart in-memory exit = present %v close %v gone %v", ok, close, gone)
	}
	if close, gone, ok := sealedExitState(t, exitsPath, f.handle, tk.ID); !ok || close || !gone {
		t.Fatalf("restart sealed exit = present %v close %v gone %v", ok, close, gone)
	}

	// Live dispatch calls DispatchRefusal before task creation. The CLI's
	// fresh failed-clean row asserts this same path end to end.
	err = f.store.DispatchRefusal(f.handle)
	want := "seat " + f.handle + " holds an uncleaned cancelled attempt of " + tk.ID + "; run seat clean --seat " + f.handle + " first"
	if err == nil || err.Error() != want {
		t.Fatalf("live dispatch refusal = %v", err)
	}
}

func pendingExitState(s *scheduler.Scheduler, handle, taskID string) (close, gone, ok bool) {
	queue := reflect.ValueOf(s).Elem().FieldByName("exitQueue")
	for i := 0; i < queue.Len(); i++ {
		item := queue.Index(i)
		if item.FieldByName("Handle").String() == handle && item.FieldByName("TaskID").String() == taskID {
			return item.FieldByName("Close").Bool(), item.FieldByName("Gone").Bool(), true
		}
	}
	return false, false, false
}

func sealedExitState(t *testing.T, path, handle, taskID string) (close, gone, ok bool) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body, _, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatalf("exits ledger is not sealed raw: %v", err)
	}
	var ledger struct {
		Queue []struct {
			Handle string `json:"handle"`
			TaskID string `json:"task_id"`
			Close  bool   `json:"close"`
			Gone   bool   `json:"gone"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(body, &ledger); err != nil {
		t.Fatal(err)
	}
	for _, item := range ledger.Queue {
		if item.Handle == handle && item.TaskID == taskID {
			return item.Close, item.Gone, true
		}
	}
	return false, false, false
}

func decisionCount(ds []scheduler.Decision, kind string) int {
	n := 0
	for _, d := range ds {
		if d.Kind == kind {
			n++
		}
	}
	return n
}
