package schedproc

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex's G on #898: the attempt a lead's `seat clean` archives under is
// the seat's most recently ended one, the rule stated and held here: the
// greatest EndedAt; at one instant the later grant, then the higher
// attempt number under one task, then the task id; another seat's attempts
// never count; a seat with no ended attempt has none.
func TestLatestEndedAttemptRule(t *testing.T) {
	t0 := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	at := func(n int) time.Time { return t0.Add(time.Duration(n) * time.Minute) }
	list := []task.Task{
		{ID: "t1", Attempts: []task.Attempt{
			{N: 1, Holder: "dev", GrantedAt: at(0), EndedAt: at(5), Outcome: task.OutcomeFailed},
			{N: 2, Holder: "dev", GrantedAt: at(1), EndedAt: at(9), Outcome: task.OutcomeCancelled},
		}},
		{ID: "t2", Attempts: []task.Attempt{
			{N: 1, Holder: "dev", GrantedAt: at(6), EndedAt: at(9), Outcome: task.OutcomeExpired},
			{N: 1, Holder: "reviewer", GrantedAt: at(20), EndedAt: at(30), Outcome: task.OutcomeFailed},
		}},
	}
	id, a, ok := latestEndedAttempt(list, "dev")
	if !ok || id != "t2" || a.N != 1 {
		t.Fatalf("the later grant at one instant: %s attempt %d, %v", id, a.N, ok)
	}
	list[1].Attempts[0].GrantedAt = at(1)
	if id, a, ok = latestEndedAttempt(list, "dev"); !ok || id != "t2" || a.N != 1 {
		t.Fatalf("a tie on the grant too falls to the task id: %s attempt %d, %v", id, a.N, ok)
	}
	list[0].Attempts = append(list[0].Attempts, task.Attempt{N: 3, Holder: "dev", GrantedAt: at(1), EndedAt: at(9), Outcome: task.OutcomeCancelled})
	list[1].Attempts = list[1].Attempts[1:]
	if id, a, ok = latestEndedAttempt(list, "dev"); !ok || id != "t1" || a.N != 3 {
		t.Fatalf("the higher attempt number under one task: %s attempt %d, %v", id, a.N, ok)
	}
	list[0].Attempts = append(list[0].Attempts, task.Attempt{N: 4, Holder: "dev", GrantedAt: at(40), Outcome: task.OutcomeCancelled})
	if id, a, ok = latestEndedAttempt(list, "dev"); !ok || id != "t1" || a.N != 3 {
		t.Fatalf("a zero EndedAt sorts before every ended attempt: %s attempt %d, %v", id, a.N, ok)
	}
	if _, _, ok = latestEndedAttempt(list, "nobody"); ok {
		t.Fatal("a seat with no attempts has a latest one")
	}
}
