package status

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex review of bc48a0e1, finding 6: null passes only at nullable paths;
// a null document or a required value replaced by null fails.
func TestFinding6CheckAdditiveRejectsNullForRequiredValues(t *testing.T) {
	golden := readGolden(t)
	if err := CheckAdditive(golden, []byte("null")); err == nil {
		t.Error("a null document passed")
	}
	fail := map[string]func(m map[string]any){
		"counts":         func(m map[string]any) { m["counts"] = nil },
		"actions":        func(m map[string]any) { m["actions"] = nil },
		"seats":          func(m map[string]any) { m["seats"] = nil },
		"schema_version": func(m map[string]any) { m["schema_version"] = nil },
		"new_task argv":  func(m map[string]any) { m["actions"].(map[string]any)["new_task"].(map[string]any)["argv"] = nil },
		"task state":     func(m map[string]any) { m["tasks"].([]any)[0].(map[string]any)["state"] = nil },
	}
	for name, fn := range fail {
		if err := CheckAdditive(golden, mutate(t, golden, fn)); err == nil || !strings.Contains(err.Error(), "null") {
			t.Errorf("%s replaced by null passed (err %v)", name, err)
		}
	}
	pass := map[string]func(m map[string]any){
		"tokens_in":         func(m map[string]any) { m["seats"].([]any)[0].(map[string]any)["tokens_in"] = nil },
		"pool":              func(m map[string]any) { m["tasks"].([]any)[0].(map[string]any)["pool"] = nil },
		"usage_limit_until": func(m map[string]any) { m["scheduler"].(map[string]any)["usage_limit_until"] = nil },
		"attention action":  func(m map[string]any) { m["attention"].([]any)[0].(map[string]any)["action"] = nil },
	}
	for name, fn := range pass {
		if err := CheckAdditive(golden, mutate(t, golden, fn)); err != nil {
			t.Errorf("nullable %s: %v", name, err)
		}
	}
}

// Codex review of bc48a0e1, finding 7: a seat whose unclaimed lease reached
// expires_at is stale, not waiting (expiry first, as in the task classifier).
func TestFinding7ExpiredUnclaimedSeatLeaseIsStale(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	tk := task.Task{ID: "t1", Title: "x", Class: "script", State: task.StateInProgress, Attempt: 1,
		Lease: &task.Lease{Holder: "seat-a", GrantedAt: now.Add(-10 * time.Minute), ExpiresAt: now}}
	st := Build(Input{Now: now, Tasks: []task.Task{tk}, Seats: []SeatInput{{Handle: "seat-a", Live: true, HasSignal: true}}})
	seat, row := st.Seats[0], st.Tasks[0]
	if seat.State != StateStale || seat.ReasonCode != "lease_expired" || seat.Bucket != BucketInactive {
		t.Fatalf("seat at expires_at: %s/%s %s", seat.State, seat.ReasonCode, seat.Bucket)
	}
	if row.State != StateStale || row.ReasonCode != "lease_expired" {
		t.Fatalf("task at expires_at: %s/%s", row.State, row.ReasonCode)
	}
}
