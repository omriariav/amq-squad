package schedproc

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier, the liveness ruling: the commit step runs off the tick. A
// 10-minute verifier stand-in holds seat A's step while seat B's exit check
// is decided within one tick; the step runs once, its running state is in
// the exits queue (the seat stays reserved, a restart finds it), and its
// result is recorded at the tick after it ends.
func TestCommitStepRunsOffTheTick(t *testing.T) {
	root := t.TempDir()
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	a, _ := store.Add(task.AddInput{Title: "long verifier", Class: "summarize", DeclaredFiles: []string{"a.go"}}, now)
	b, _ := store.Add(task.AddInput{Title: "failed", Class: "summarize"}, now)
	if _, err := store.Claim(a.ID, "seat-a", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Done(a.ID, "seat-a", "", "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(b.ID, "seat-b", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Fail(b.ID, "seat-b", "red", now); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var steps atomic.Int32
	var mu sync.Mutex
	var checked []string
	c := &Committer{Inline: 50 * time.Millisecond,
		Step: func(string, task.Task) seatcommit.Result {
			steps.Add(1)
			select { // the stand-in of a 10-minute verifier
			case <-release:
			case <-time.After(10 * time.Minute):
			}
			return seatcommit.Result{OK: true}
		},
		ExitStep: func(handle, _, _ string) seatcommit.Result {
			mu.Lock()
			checked = append(checked, handle)
			mu.Unlock()
			return seatcommit.Result{OK: true}
		}}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	exits := filepath.Join(root, "exits.json")
	s := &scheduler.Scheduler{Store: store, Now: func() time.Time { return now }, Committer: c, Exits: c, Config: scheduler.Config{ExitsPath: exits}}
	tick := func() []scheduler.Decision {
		t.Helper()
		done := make(chan []scheduler.Decision, 1)
		go func() {
			d, err := s.Tick()
			if err != nil {
				t.Error(err)
			}
			done <- d
		}()
		select {
		case d := <-done:
			return d
		case <-time.After(10 * time.Second):
			close(release)
			<-done
			t.Fatal("the tick waited for the running commit step")
			return nil
		}
	}
	d := tick()
	mu.Lock()
	gotB := strings.Join(checked, ",")
	mu.Unlock()
	if gotB != "seat-b" || !hasKind(d, "commit_running") {
		t.Fatalf("one tick: seat B's exit check %q, decisions %+v", gotB, d)
	}
	if raw, _ := os.ReadFile(exits); !strings.Contains(string(raw), `"why": "commit"`) {
		t.Fatal("the running commit step is not in the exits queue")
	}
	tick()
	if n := steps.Load(); n != 1 {
		t.Fatalf("the step ran %d times", n)
	}
	close(release)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if got, _ := store.Get(a.ID); got.State == task.StateCompleted {
			break
		}
		tick()
	}
	if got, _ := store.Get(a.ID); got.State != task.StateCompleted {
		t.Fatalf("the step's result was not recorded: %s", got.State)
	}
	if raw, _ := os.ReadFile(exits); strings.Contains(string(raw), `"why": "commit"`) {
		t.Fatal("the decided commit step is still in the exits queue")
	}
}

func hasKind(ds []scheduler.Decision, kind string) bool {
	for _, d := range ds {
		if d.Kind == kind {
			return true
		}
	}
	return false
}
