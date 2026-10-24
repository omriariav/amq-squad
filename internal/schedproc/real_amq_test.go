package schedproc

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The doorbell against the installed amq (skipped without one): the ring is
// a real AMQ message in the seat's inbox, from the scheduler mailbox; it
// moves nothing else.
func TestDoorbellLandsInTheSeatInboxWithTheInstalledAMQ(t *testing.T) {
	amq, err := exec.LookPath("amq")
	if err != nil {
		t.Skip("no installed amq on PATH")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(dir, ".agent-mail", "s1")
	init := exec.Command(amq, "init", "--root", root, "--agents", "s1,lead")
	init.Dir, init.Env = dir, append(os.Environ(), "AMQ_NO_UPDATE_CHECK=1")
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("amq init: %v\n%s", err, out)
	}
	s := Sender{AMQ: amq, Root: root}
	if err := (Doorbell{Sender: s}).Ring("s1", task.Task{ID: "t7", Title: "do it", Class: "implement"}); err != nil {
		t.Fatal(err)
	}
	list := exec.Command(amq, "list", "--me", "s1", "--new", "--json")
	list.Dir, list.Env = dir, append(scrubAMQ(os.Environ()), "AM_ROOT="+root, "AMQ_NO_UPDATE_CHECK=1")
	out, err := list.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "task t7: do it") || !strings.Contains(string(out), `"scheduler"`) {
		t.Fatalf("seat inbox: %v\n%s", err, out)
	}
}
