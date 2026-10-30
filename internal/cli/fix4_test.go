package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Fourth pass, the restart barrier: a scheduler whose launch record names
// no decision socket yet is starting (the socket opens after the exits
// ledger is loaded and every adopted seat's close is confirmed): the
// widening is refused scheduler_starting, and nothing is sent.
func TestTaskWidenBeforeTheBarrierIsSchedulerStarting(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"a.go"}, ProjectPath: proj}, time.Now())
	sent := 0
	prev := taskWidenSend
	taskWidenSend = func(string, []string) ([]byte, error) { sent++; return nil, nil }
	t.Cleanup(func() { taskWidenSend = prev })
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, ProjectRoot: proj}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runTaskWiden([]string{tk.ID, "--root", root, "--files", "b.go", "--reason", "r"}, os.Stdout)
	})
	if err == nil || !strings.Contains(err.Error(), schedproc.RefusedStarting) || sent != 0 {
		t.Fatalf("a widen before the barrier: %v (sent %d)", err, sent)
	}
}
