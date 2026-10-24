package schedproc

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// TestTickBudget measures one scheduler tick over a store of 100 pending
// tasks with the DefaultRouter, 4 seats up and the cap reached (the
// scheduler process's tick budget; 2b-4's router adds its Route cost per
// pending task). Run with AMQ_SQUAD_TIMING=1.
func TestTickBudget(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_TIMING") != "1" {
		t.Skip("timing run: set AMQ_SQUAD_TIMING=1")
	}
	store, _ := task.Open(t.TempDir())
	for i := 0; i < 100; i++ {
		if _, err := store.Add(task.AddInput{Title: fmt.Sprintf("t%d", i), Class: "implement", Pool: &sonnet, DeclaredFiles: []string{fmt.Sprintf("pkg%d/", i)}}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	l := &fakeLauncher{}
	s, err := scheduler.New(scheduler.Options{Store: store, Launcher: l, Config: scheduler.Config{MaxSeats: 4}})
	if err != nil {
		t.Fatal(err)
	}
	l.s = s
	var d []time.Duration
	for i := 0; i < 12; i++ {
		start := time.Now()
		if _, err := s.Tick(); err != nil {
			t.Fatal(err)
		}
		d = append(d, time.Since(start))
	}
	d = d[2:] // the first ticks request and admit the seats
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	t.Logf("tick over 100 tasks (96 pending, 4 leased, cap reached): median %v of 10 (min %v, max %v)", (d[4]+d[5])/2, d[0], d[9])
}
