package scheduler

import (
	"fmt"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

type attemptArchiveRecorder struct{ calls []task.Attempt }

func (*attemptArchiveRecorder) PrepareSeat(string) error { return nil }
func (r *attemptArchiveRecorder) ArchiveAttempt(_ string, _ task.Task, a task.Attempt) error {
	r.calls = append(r.calls, a)
	return nil
}

type attemptCleanRecorder struct {
	cleaned []string
}

func (*attemptCleanRecorder) PrepareSeat(string) error { return nil }
func (r *attemptCleanRecorder) CleanAttempt(handle, taskID string, attempt int, outcome string) error {
	r.cleaned = append(r.cleaned, fmt.Sprintf("%s/%s/%d/%s", handle, taskID, attempt, outcome))
	return nil
}

func TestCancelledAttemptClosesThenArchivesAndCleansBeforeSlotRelease(t *testing.T) {
	h := newHarness(t, Config{IdleTimeout: time.Hour})
	launcher := &holdLauncher{fakeLauncher: h.l}
	h.s.Launcher = launcher
	h.s.Exits = &exitRecorder{bad: map[string]bool{}}
	cleaner := &attemptCleanRecorder{}
	h.s.Preparer = cleaner
	h.add("cancel me", &sonnet)
	h.up("seat-a", sonnet)
	h.tick()
	if _, err := h.store.Claim("t1", "seat-a", nil, time.Hour, h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Cancel("t1", "lead", "stop", h.clk.Now()); err != nil {
		t.Fatal(err)
	}
	first := h.tick()
	if len(cleaner.cleaned) != 0 || !has(first, "seat_closed", "", "seat-a") || len(launcher.reserved) != 1 {
		t.Fatalf("cancel close pass: decisions=%+v cleaned=%v reserved=%v", first, cleaner.cleaned, launcher.reserved)
	}
	second := h.tick()
	if len(cleaner.cleaned) != 1 || cleaner.cleaned[0] != "seat-a/t1/1/cancelled" || !has(second, "seat_cleaned", "t1", "seat-a") || len(launcher.released) != 1 {
		t.Fatalf("cancel clean pass: decisions=%+v cleaned=%v released=%v", second, cleaner.cleaned, launcher.released)
	}
}

func TestScanAttemptsArchivesTypedBlockOnce(t *testing.T) {
	for _, reason := range []string{task.ReasonWidenRequested, task.ReasonGrowthGateRequired} {
		t.Run(reason, func(t *testing.T) {
			s := task.Store{Dir: t.TempDir()}
			now := time.Now()
			tk, err := s.Add(task.AddInput{Title: "typed", Class: "implement", DeclaredFiles: []string{"a.go"}}, now)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Grant(tk.ID, "seat", nil, time.Minute, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Claim(tk.ID, "seat", nil, time.Minute, now); err != nil {
				t.Fatal(err)
			}
			if _, err := s.BlockWithPaths(tk.ID, "seat", task.ReasonWidenRequested, "", task.RouteLead, []string{"b.go"}, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if reason == task.ReasonGrowthGateRequired {
				if _, err := s.Escalate(tk.ID, reason, "gate", now.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			list, _ := s.List()
			recorder := &attemptArchiveRecorder{}
			scheduler := Scheduler{Preparer: recorder}
			if err := scheduler.scanAttempts(list, now.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := scheduler.scanAttempts(list, now.Add(4*time.Second)); err != nil {
				t.Fatal(err)
			}
			if len(recorder.calls) != 1 || recorder.calls[0].N != list[0].BlockAttempt {
				t.Fatalf("archive calls = %+v, block attempt %d", recorder.calls, list[0].BlockAttempt)
			}
		})
	}
}
