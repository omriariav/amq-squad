package routing

import (
	"fmt"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// BenchmarkRouteStore100 is one Route call against a store of 100 tasks: a
// lock-free snapshot read, the flush scan and one decision.
func BenchmarkRouteStore100(b *testing.B) {
	root := b.TempDir()
	store, err := task.Open(root)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, err := store.Add(task.AddInput{Title: fmt.Sprintf("task %d", i), Class: task.Classes[i%len(task.Classes)],
			DeclaredFiles: []string{fmt.Sprintf("pkg%d/", i)}}, t0); err != nil {
			b.Fatal(err)
		}
	}
	f := &fixture{tbl: Default(), reg: fixtureRegistry()}
	r, err := New(Options{Table: f.tbl, Registry: f.reg, Grants: f.g, Root: root, Now: func() time.Time { return t0 },
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) }})
	if err != nil {
		b.Fatal(err)
	}
	tk, err := store.Get("t50")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if route := r.Route(tk); route.Pool == nil && route.Wait == "" {
			b.Fatal("no answer")
		}
	}
}
