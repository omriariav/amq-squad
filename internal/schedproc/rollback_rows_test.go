package schedproc

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

type rollbackCurrentExits struct{}

func (rollbackCurrentExits) CheckExit(string, string, string) scheduler.ExitResult {
	return scheduler.ExitResult{Deferred: true, Detail: "rollback current process probe"}
}

type rollbackCurrentLauncher struct{}

func (rollbackCurrentLauncher) RequestSeat(scheduler.SeatRequest) error       { return nil }
func (rollbackCurrentLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (rollbackCurrentLauncher) CloseSeat(string, string) error                { return nil }

type rollbackGoneLiveness struct{}

func (rollbackGoneLiveness) SeatProcess(string, string) (bool, string, error) {
	return true, "the rollback probe's stopped seat is gone", nil
}

type rollbackAttemptCleaner struct {
	store task.Store
	now   time.Time
}

func (rollbackAttemptCleaner) PrepareSeat(string) error { return nil }

func (c rollbackAttemptCleaner) CleanStopped(handle string) (bool, error) {
	list, err := c.store.List()
	if err != nil {
		return false, err
	}
	item, attempt, ok := task.UnresolvedCancellation(list, handle)
	if !ok {
		return false, nil
	}
	_, err = c.store.MarkAttemptCleaned(item.ID, attempt.N, c.now)
	return false, err
}

func TestRollbackCurrentProcessConsumesOldProcessRows(t *testing.T) {
	root := os.Getenv("AMQSQ_ROLLFORWARD_ROOT")
	if root == "" {
		t.Skip("set AMQSQ_ROLLFORWARD_ROOT")
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	fixture := strings.ToLower(os.Getenv("AMQSQ_ROLLBACK_FIXTURE"))
	beforeList, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	_, beforeCancel, unresolvedBefore := task.UnresolvedCancellation(beforeList, "qa")
	if fixture == "f5a" && !unresolvedBefore {
		t.Fatal("F5a lost its unresolved cancellation before the current process")
	}
	if fixture == "f5b" && unresolvedBefore {
		t.Fatalf("F5b cleaned_at was lost before the current process: %+v", beforeCancel)
	}
	state, err := scheduler.ReadState(scheduler.StatePathFor(root))
	if err != nil {
		t.Fatal(err)
	}
	probe, err := scheduler.New(scheduler.Options{Store: store, Now: time.Now, Launcher: rollbackCurrentLauncher{}, Exits: rollbackCurrentExits{}, Config: scheduler.Config{StatePath: scheduler.StatePathFor(root), ExitsPath: Dir(root) + "/exits.json", SealRoot: root}})
	if err != nil {
		t.Fatal(err)
	}
	if restored := probe.RestoreLedgerTampers(state); restored != len(state.LedgerTampered) {
		t.Fatalf("RestoreLedgerTampers = %d, state has %d", restored, len(state.LedgerTampered))
	}
	recoverReply := recoverDecision(probe, root, nil, time.Now)("lead", RecoverRequest{Seat: "qa", Alarm: "00000000", Reason: "rollback probe"})
	if recoverReply.OK || recoverReply.Code != RefusedRecoverAlarmNotHeld {
		t.Fatalf("recovery without a restored hold = %+v", recoverReply)
	}

	decisionPath := DecisionsPath(root)
	before, err := os.ReadFile(decisionPath)
	if err != nil {
		t.Fatal(err)
	}
	type migration struct {
		Kind       string `json:"kind"`
		LedgerKind string `json:"ledger_kind"`
		Seat       string `json:"seat"`
		Seq        int    `json:"seq"`
	}
	var migrations []migration
	for _, line := range bytes.Split(before, []byte{'\n'}) {
		var row migration
		if json.Unmarshal(line, &row) == nil && row.Kind == sealchain.LayoutMigrationDecisionKind {
			migrations = append(migrations, row)
		}
	}
	for _, row := range migrations {
		appended, err := sealchain.RecordLayoutMigration(decisionPath, row.LedgerKind, row.Seat, row.Seq, "rollback dedupe probe", time.Unix(100, 0))
		if err != nil || appended {
			t.Fatalf("migration dedupe %+v = appended %v err %v", row, appended, err)
		}
	}
	afterDedupe, err := os.ReadFile(decisionPath)
	if err != nil || !bytes.Equal(afterDedupe, before) {
		t.Fatalf("migration dedupe changed old-process log: err=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := 0
	wake := make(chan struct{}, 1)
	err = Run(ctx, Options{
		Root: root, Session: "rollback-current", ProjectRoot: root,
		Launcher: rollbackCurrentLauncher{}, Exits: rollbackCurrentExits{}, Tick: time.Hour, Wake: wake,
		Now: func() time.Time { return time.Unix(110+int64(ticks), 0).UTC() },
		OnTick: func([]scheduler.Decision) {
			ticks++
			if ticks == 3 {
				cancel()
			} else {
				wake <- struct{}{}
			}
		},
	})
	if err != nil || ticks != 3 {
		t.Fatalf("current process = ticks %d err %v", ticks, err)
	}
	after, err := os.ReadFile(decisionPath)
	if err != nil || !bytes.HasPrefix(after, before) {
		t.Fatalf("current process rewrote decisions: err=%v", err)
	}
	appended := after[len(before):]
	for _, forbidden := range [][]byte{[]byte(`"kind":"ledger_tampered"`), []byte(`"kind":"tick_error"`)} {
		if bytes.Contains(appended, forbidden) {
			t.Fatalf("current process appended forbidden decision %s: %s", forbidden, appended)
		}
	}
	if fixture == "f5a" && !bytes.Contains(appended, []byte(`"kind":"cleaner_marker_taken_over"`)) {
		t.Fatalf("F5a marker was not taken over on the first current tick: %s", appended)
	}
	finalState, err := scheduler.ReadState(scheduler.StatePathFor(root))
	if err != nil || len(finalState.LedgerTampered) != len(state.LedgerTampered) {
		t.Fatalf("current state tamper holds = %+v err=%v", finalState.LedgerTampered, err)
	}
	list, err := store.List()
	if err != nil || len(list) == 0 {
		t.Fatalf("current task list = %+v err=%v", list, err)
	}
	_, afterCancel, unresolvedAfter := task.UnresolvedCancellation(list, "qa")
	if fixture == "f5a" {
		if !unresolvedAfter {
			t.Fatalf("F5a current ticks unexpectedly released the cancellation: %+v", afterCancel)
		}
		reply := manualCleanDecision(store, rollbackGoneLiveness{}, rollbackAttemptCleaner{store: store, now: time.Unix(114, 0).UTC()})("lead", CleanRequest{Seat: "qa"})
		if !reply.OK {
			t.Fatalf("F5a seat clean did not free the held seat: %+v", reply)
		}
		cleaned, err := store.List()
		if err != nil {
			t.Fatal(err)
		}
		if _, attempt, held := task.UnresolvedCancellation(cleaned, "qa"); held {
			t.Fatalf("F5a seat clean left the cancellation held: %+v", attempt)
		}
		t.Logf("F5a marker wait: 0s (dead-pid marker taken over on first tick); seat clean: %s", reply.Detail)
	}
	if fixture == "f5b" && unresolvedAfter {
		t.Fatalf("F5b current process recreated an uncleaned hold: %+v", afterCancel)
	}
	t.Logf("restored ledger tamper holds: %d", len(state.LedgerTampered))
	t.Logf("scheduler recover needed: no; answer without a held alarm: %s: %s", recoverReply.Code, recoverReply.Detail)
	t.Logf("current decisions appended: %s", appended)
	t.Logf("current task state: %s", list[0].State)
}
