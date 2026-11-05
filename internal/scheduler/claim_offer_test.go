package scheduler

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestLapsedHolderIsEligibleAgainOnlyAfterItsCurrentJoin(t *testing.T) {
	joined := t0
	s := &Scheduler{seats: map[string]*seat{
		"seat-a": {pool: sonnet, joinedAt: joined},
	}}
	tk := task.Task{ID: "t1", State: task.StatePending, Pool: &sonnet, Attempts: []task.Attempt{{
		N: 1, Holder: "seat-a", Pool: &sonnet, GrantedAt: joined.Add(time.Second), EndedAt: joined.Add(time.Minute), Outcome: task.OutcomeExpired,
	}}}
	if got := s.idleSeat([]task.Task{tk}, tk, sonnet); got != "" {
		t.Fatalf("same join got its lapsed task again: %q", got)
	}
	s.seats["seat-a"] = &seat{pool: sonnet, joinedAt: joined.Add(2 * time.Minute)}
	if got := s.idleSeat([]task.Task{tk}, tk, sonnet); got != "seat-a" {
		t.Fatalf("relaunched seat did not get the task again: %q", got)
	}
}
