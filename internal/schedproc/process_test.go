package schedproc

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

var sonnet = task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}

// fakeLauncher announces every requested seat at once, as a launch whose
// identity proof passed would.
type fakeLauncher struct {
	mu       sync.Mutex
	s        *scheduler.Scheduler
	requests []scheduler.SeatRequest
	closed   []string
}

func (f *fakeLauncher) RequestSeat(r scheduler.SeatRequest) error {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	s := f.s
	f.mu.Unlock()
	s.Post(scheduler.Event{Kind: scheduler.SeatUp, At: time.Now(), Handle: "seat-" + r.TaskID, Pool: r.Pool, RequestID: r.ID})
	return nil
}
func (f *fakeLauncher) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (f *fakeLauncher) CloseSeat(h, _ string) error {
	f.mu.Lock()
	f.closed = append(f.closed, h)
	f.mu.Unlock()
	return nil
}

type bell struct {
	mu   sync.Mutex
	rung []string
}

func (b *bell) Ring(h string, t task.Task) error {
	b.mu.Lock()
	b.rung = append(b.rung, h+":"+t.ID)
	b.mu.Unlock()
	return nil
}

// lockedBuf is a decisions log safe across the process goroutine.
type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// Ruling 15, row 1: the process starts, dispatches a seeded task to a fake
// launcher (a seat requested, joined and rung), and stops on a usage-limit
// event: the next task waits with usage_limit and no new seat is requested;
// a second process for the same squad is refused; the process stops on
// cancel.
func TestProcessDispatchesAndStopsOnAUsageLimit(t *testing.T) {
	root := t.TempDir()
	store, _ := task.Open(root)
	if _, err := store.Add(task.AddInput{Title: "first", Class: "implement", Pool: &sonnet}, time.Now()); err != nil {
		t.Fatal(err)
	}
	l, b, log := &fakeLauncher{}, &bell{}, &lockedBuf{}
	var limitOn sync.Once
	limited := make(chan struct{})
	limits := scheduler.UsageLimitFunc(func(since time.Time) ([]scheduler.UsageLimitEvent, error) {
		select {
		case <-limited:
			var out []scheduler.UsageLimitEvent
			limitOn.Do(func() {
				out = []scheduler.UsageLimitEvent{{At: time.Now(), Provider: "claude", Source: "test"}}
			})
			return out, nil
		default:
			return nil, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan []scheduler.Decision, 1000)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Root: root, Launcher: l, Doorbell: b, Limits: limits, Log: log, Tick: 10 * time.Millisecond,
			Config:  scheduler.Config{MaxSeats: 4},
			Started: func(s *scheduler.Scheduler) { l.mu.Lock(); l.s = s; l.mu.Unlock() },
			OnTick:  func(ds []scheduler.Decision) { ticks <- ds }})
	}()
	waitFor(t, ticks, func(d scheduler.Decision) bool { return d.Kind == "dispatched" && d.TaskID == "t1" })
	// A second process for the same squad is refused.
	ctx2, cancel2 := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel2()
	if err := Run(ctx2, Options{Root: root, Launcher: l, Log: &lockedBuf{}}); !errors.Is(err, ErrRunning) {
		t.Fatalf("a second process: %v", err)
	}
	close(limited)
	waitFor(t, ticks, func(d scheduler.Decision) bool { return d.Kind == "usage_limit" })
	if _, err := store.Add(task.AddInput{Title: "second", Class: "implement", Pool: &sonnet}, time.Now()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := store.Get("t2")
		if got.WaitReason == task.WaitUsageLimit {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("t2 never waited on the usage limit: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.requests) != 1 || l.requests[0].TaskID != "t1" {
		t.Fatalf("seat requests after the usage limit: %+v", l.requests)
	}
	b.mu.Lock()
	if len(b.rung) != 1 || b.rung[0] != "seat-t1:t1" {
		t.Fatalf("doorbell: %v", b.rung)
	}
	b.mu.Unlock()
	for _, want := range []string{`"kind":"scheduler_started"`, `"kind":"seat_requested"`, `"kind":"dispatched"`, `"kind":"usage_limit"`, `"kind":"scheduler_stopped"`} {
		if !strings.Contains(log.String(), want) {
			t.Fatalf("decision log lacks %s:\n%s", want, log.String())
		}
	}
	if st, _ := scheduler.ReadState(scheduler.StatePathFor(root)); st.UsageLimitSince == nil {
		t.Fatalf("state file: %+v", st)
	}
}

// A usage limit from before the process started is history, not a stop.
func TestUsageLimitBeforeTheStartIsHistory(t *testing.T) {
	root := t.TempDir()
	store, _ := task.Open(root)
	if _, err := store.Add(task.AddInput{Title: "first", Class: "implement", Pool: &sonnet}, time.Now()); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	limits := scheduler.UsageLimitFunc(func(since time.Time) ([]scheduler.UsageLimitEvent, error) {
		if since.After(old) {
			return nil, nil
		}
		return []scheduler.UsageLimitEvent{{At: old, Provider: "claude"}}, nil
	})
	l := &fakeLauncher{}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan []scheduler.Decision, 1000)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Root: root, Launcher: l, Limits: limits, Log: &lockedBuf{}, Tick: 10 * time.Millisecond,
			Started: func(s *scheduler.Scheduler) { l.mu.Lock(); l.s = s; l.mu.Unlock() },
			OnTick:  func(ds []scheduler.Decision) { ticks <- ds }})
	}()
	waitFor(t, ticks, func(d scheduler.Decision) bool { return d.Kind == "dispatched" })
	cancel()
	<-done
}

// External runners post events through files; the process drains them.
func TestPostedEventsReachTheScheduler(t *testing.T) {
	root := t.TempDir()
	store, _ := task.Open(root)
	if _, err := store.Add(task.AddInput{Title: "first", Class: "implement", Pool: &sonnet}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ext := &recording{}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan []scheduler.Decision, 1000)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Root: root, Launcher: ext, Log: &lockedBuf{}, Tick: 10 * time.Millisecond,
			OnTick: func(ds []scheduler.Decision) { ticks <- ds }})
	}()
	waitFor(t, ticks, func(d scheduler.Decision) bool { return d.Kind == "seat_requested" })
	ext.mu.Lock()
	id := ext.reqs[0].ID
	ext.mu.Unlock()
	if err := PostEvent(root, Event{Kind: "seat_up", Handle: "pi-1", Pool: sonnet, RequestID: id}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, ticks, func(d scheduler.Decision) bool { return d.Kind == "dispatched" && d.Seat == "pi-1" })
	if err := PostEvent(root, Event{Kind: "bogus", Handle: "x"}); err == nil {
		t.Fatal("an unknown event kind was posted")
	}
	cancel()
	<-done
}

type recording struct {
	mu   sync.Mutex
	reqs []scheduler.SeatRequest
}

func (r *recording) RequestSeat(q scheduler.SeatRequest) error {
	r.mu.Lock()
	r.reqs = append(r.reqs, q)
	r.mu.Unlock()
	return nil
}
func (r *recording) CancelSeatRequest(scheduler.SeatRequest) error { return nil }
func (r *recording) CloseSeat(string, string) error                { return nil }

func waitFor(t *testing.T, ticks <-chan []scheduler.Decision, match func(scheduler.Decision) bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case ds := <-ticks:
			for _, d := range ds {
				if match(d) {
					return
				}
			}
		case <-deadline:
			t.Fatal("timed out waiting for a decision")
		}
	}
}
