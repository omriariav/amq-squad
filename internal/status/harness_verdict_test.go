package status

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// harnessPass records the harness's pass verdict on the task's current
// attempt, as the commit step's test run or the probe would: a class with a
// mechanical verifier completes on nothing else (phase 5 re-pass, ruling 1).
func harnessPass(t testing.TB, s task.Store, id string, now time.Time) {
	t.Helper()
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	v := task.ClassVerifier(got.Class)
	if !task.MechanicalVerifier(v) {
		return
	}
	// The harness records the tree first, then the sealed verdict on it.
	if _, err := s.RecordSnapshot(id, "test-tree", now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(id, task.Verdict{Verifier: v, Result: task.VerdictPass, Source: "test harness", Tree: "test-tree", CommandDigest: task.CommandDigest("test")}, now); err != nil {
		t.Fatal(err)
	}
}
