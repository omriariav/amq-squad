//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// TestLeadToolVerbHelper is not a test: run as a child (AMQSQ_TOOL_VERB set)
// it runs the CLI verb the pi lead's tool runs (amq_scheduler_relaunch,
// amq_task_widen) and prints its exit code, as the tool's child would. The
// widening's AMQ record send is a no-op here (no live AMQ).
func TestLeadToolVerbHelper(t *testing.T) {
	verb := os.Getenv("AMQSQ_TOOL_VERB")
	if verb == "" {
		t.Skip("helper process")
	}
	taskWidenSend = func(string, []string) ([]byte, error) { return nil, nil }
	var err error
	switch verb {
	case "relaunch":
		err = runScheduler([]string{"relaunch", "--root", os.Getenv("AMQSQ_ROOT"), "--reason", "rev-1 is gone", "--json"})
	case "widen":
		err = runTask([]string{"widen", "t1", "--root", os.Getenv("AMQSQ_ROOT"), "--files", "b.go", "--reason", "b.go belongs to the parser task", "--json"})
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	fmt.Printf("\nexit=%d %s\n", ExitCode(err), msg)
	os.Exit(0)
}

// runToolVerb runs the verb in a child of this process.
func runToolVerb(t *testing.T, root, verb string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLeadToolVerbHelper$")
	cmd.Env = append(os.Environ(), "AMQSQ_TOOL_VERB="+verb, "AMQSQ_ROOT="+root) // the package's TestMain unsets AM_ROOT
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	return string(out)
}

// t16 round 4 (reviewer ruling A): the two lead tools run the CLI verbs
// from the pi lead's process, a descendant of the team-mode lead the
// scheduler proves. Here this test process is the team-mode lead and the
// verb runs in its child, as the tool's child of pi does: the relaunch and
// the widening are proved over the real decision socket (kernel peer,
// parent chain, stamps) and acted on. The same verbs from a process that
// does not descend from the lead (a roster pane's process) are refused
// not_lead and nothing is applied. Reverted (the ledger lead only), the
// team-mode lead's tools could not be proved at all.
func TestLeadToolVerbsAreProvedFromTheLeadsDescendant(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := task.Open(root); err != nil {
		t.Fatal(err)
	}
	me := os.Getpid()
	start, ok := procinfo.StartToken(me)
	if !ok {
		t.Skip("no start stamp for this process")
	}
	var mu sync.Mutex
	var relaunched []string
	var widened []schedproc.WidenRequest
	serve := func(lead func(string, string) (panechain.Pane, error)) *schedproc.DecisionServer {
		s, err := schedproc.ServeDecisions(schedproc.DecideOptions{Root: root, Lead: "lead", LeadPane: lead,
			Apply: func(_ string, req schedproc.WidenRequest) schedproc.DecideReply {
				mu.Lock()
				widened = append(widened, req)
				mu.Unlock()
				return schedproc.DecideReply{OK: true}
			},
			Relaunch: func(_ string, req schedproc.RelaunchRequest) schedproc.DecideReply {
				mu.Lock()
				relaunched = append(relaunched, req.Reason)
				mu.Unlock()
				return schedproc.DecideReply{OK: true, Detail: "accepted"}
			}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: me, StartedAt: time.Now(), DecisionSocket: s.Path()}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := serve(schedproc.SupervisingLead(me, start, procinfo.StartToken))
	if out := runToolVerb(t, root, "relaunch"); !strings.Contains(out, "exit=0") {
		t.Fatalf("relaunch from the lead's descendant:\n%s", out)
	}
	if out := runToolVerb(t, root, "widen"); !strings.Contains(out, "exit=0") {
		t.Fatalf("widen from the lead's descendant:\n%s", out)
	}
	mu.Lock()
	if len(relaunched) != 1 || relaunched[0] != "rev-1 is gone" || len(widened) != 1 || widened[0].Task != "t1" || strings.Join(widened[0].Files, ",") != "b.go" {
		t.Fatalf("applied: relaunch %v, widen %+v", relaunched, widened)
	}
	mu.Unlock()
	s.Close()
	// The lead is another process; the verb's process does not descend from it.
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })
	otherStart, _ := procinfo.StartToken(other.Process.Pid)
	serve(schedproc.SupervisingLead(other.Process.Pid, otherStart, procinfo.StartToken))
	for _, verb := range []string{"relaunch", "widen"} {
		out := runToolVerb(t, root, verb)
		if !strings.Contains(out, fmt.Sprintf("exit=%d", ExitCommitRefused)) || !strings.Contains(out, schedproc.RefusedNotLead) {
			t.Fatalf("%s from outside the lead's descent:\n%s", verb, out)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(relaunched) != 1 || len(widened) != 1 {
		t.Fatalf("a refused decision was applied: relaunch %v, widen %+v", relaunched, widened)
	}
}
