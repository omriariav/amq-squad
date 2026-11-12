package scheduler

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
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

func TestAdoptionFailsClosedWithoutAReadableSeatStart(t *testing.T) {
	tests := []struct {
		name string
		seed func(*testing.T, string)
	}{
		{"no record", func(*testing.T, string) {}},
		{"unreadable record", func(t *testing.T, root string) {
			p := launch.Path(filepath.Join(root, "agents", "seat-a"))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte("not json\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"zero start", func(t *testing.T, root string) {
			agentDir := filepath.Join(root, "agents", "seat-a")
			if err := launch.WithRecordLock(agentDir, func() error {
				return launch.WriteUnderRecordLock(agentDir, launch.Record{Handle: "seat-a", Root: root})
			}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			store, err := task.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			tt.seed(t, root)
			lapsedAt := t0.Add(time.Minute)
			tk := task.Task{ID: "t1", State: task.StatePending, Pool: &sonnet, Attempts: []task.Attempt{{
				N: 1, Holder: "seat-a", Pool: &sonnet, GrantedAt: lapsedAt, EndedAt: lapsedAt.Add(time.Minute), Outcome: task.OutcomeExpired,
			}}}
			s := &Scheduler{Store: store}
			s.AdoptUpRequest("seat-a", "request-1", sonnet, t0.Add(10*time.Minute))
			if got := s.seats["seat-a"].joinedAt; !got.IsZero() {
				t.Fatalf("unproved adoption start = %s, want zero", got)
			}
			if got := s.idleSeat([]task.Task{tk}, tk, sonnet); got != "" {
				t.Fatalf("unproved adoption cleared the lapsed exclusion: %q", got)
			}
		})
	}
}

func TestAdoptedSeatKeepsItsOwnLaunchAnchorUntilARealRelaunch(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	writeLaunch := func(start time.Time) {
		t.Helper()
		agentDir := filepath.Join(root, "agents", "seat-a")
		if err := launch.WithRecordLock(agentDir, func() error {
			return launch.WriteUnderRecordLock(agentDir, launch.Record{Handle: "seat-a", Root: root, Runtime: sonnet.Runtime, Model: sonnet.Model, Effort: sonnet.Effort, StartedAt: start})
		}); err != nil {
			t.Fatal(err)
		}
	}

	seatStart := t0
	lapsedAt := seatStart.Add(time.Minute)
	adoptedAt := seatStart.Add(10 * time.Minute)
	tk := task.Task{ID: "t1", State: task.StatePending, Pool: &sonnet, Attempts: []task.Attempt{{
		N: 1, Holder: "seat-a", Pool: &sonnet, GrantedAt: lapsedAt, EndedAt: lapsedAt.Add(time.Minute), Outcome: task.OutcomeExpired,
	}}}

	writeLaunch(seatStart)
	restarted := &Scheduler{Store: store}
	restarted.AdoptUpRequest("seat-a", "request-1", sonnet, adoptedAt)
	if got := restarted.seats["seat-a"].joinedAt; !got.Equal(seatStart) {
		t.Fatalf("adopted join anchor = %s, want seat launch %s", got, seatStart)
	}
	if got := restarted.idleSeat([]task.Task{tk}, tk, sonnet); got != "" {
		t.Fatalf("scheduler restart cleared the lapsed exclusion: %q", got)
	}

	relaunchAt := adoptedAt.Add(time.Minute)
	writeLaunch(relaunchAt)
	relaunched := &Scheduler{Store: store}
	relaunched.AdoptUpRequest("seat-a", "request-2", sonnet, relaunchAt.Add(time.Minute))
	if got := relaunched.idleSeat([]task.Task{tk}, tk, sonnet); got != "seat-a" {
		t.Fatalf("real seat relaunch did not clear the lapsed exclusion: %q", got)
	}
}
