package scheduler

import (
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
