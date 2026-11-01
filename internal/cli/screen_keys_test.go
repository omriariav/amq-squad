//go:build unix

package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// TestScreenKeyVerbHelper is not a test: run as a child (AMQSQ_KEY_VERB set)
// it runs the CLI verb a screen key runs from the pi lead's process (brief
// 31-2) and prints its exit code, as the key's child of pi would.
func TestScreenKeyVerbHelper(t *testing.T) {
	verb := os.Getenv("AMQSQ_KEY_VERB")
	if verb == "" {
		t.Skip("helper process")
	}
	var err error
	switch verb {
	case "clear":
		err = runScheduler([]string{"clear", "--root", os.Getenv("AMQSQ_ROOT"), "--seat", "scribe", "--warning", "seat_stalled_at_dialog", "--launch", "launch-1", "--json"})
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	fmt.Printf("\nexit=%d %s\n", ExitCode(err), msg)
	os.Exit(0)
}

func runKeyVerb(t *testing.T, root, verb string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestScreenKeyVerbHelper$")
	cmd.Env = append(os.Environ(), "AMQSQ_KEY_VERB="+verb, "AMQSQ_ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	return string(out)
}

// Brief 31-2 row 1 (the clear key): the clear runs its verb from the lead's
// process (here this test process is the team-mode lead and the verb runs in
// its child, as the key's child of pi does); the scheduler's socket proves
// it under R4 and records it: one warning_cleared decision and one line in
// clears.jsonl. The same verb from a process that does not descend from the
// lead (a seat's shell) is refused not_lead and records nothing. Reverted
// (the socket without the clear kind), the clear is refused
// decision_malformed even from the lead.
func TestClearKeyIsProvedFromTheLeadsDescendant(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := task.Open(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(launch.StagedDir(root, "scribe"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, "scribe"), launch.FileName), launch.Record{Handle: "scribe", Root: root, Session: "s", Runtime: "claude", LaunchID: "launch-1"}); err != nil {
		t.Fatal(err)
	}
	// the warning the clear names is on the screen: scribe is at a dialog
	if err := os.MkdirAll(filepath.Join(root, "agents", "scribe"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "scribe", ".wake.dialog"), []byte(`{"harness":"claude"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	me := os.Getpid()
	start, ok := procinfo.StartToken(me)
	if !ok {
		t.Skip("no start stamp for this process")
	}
	serve := func(lead func(string, string) (panechain.Pane, error)) *schedproc.DecisionServer {
		// the socket as scheduler run wires it (decisionOptions): the clear
		// reads the seat's warnings through status
		d := decisionOptions(storeSelection{}, root)
		d.Root, d.Lead, d.LeadPane = root, "cto", lead
		s, err := schedproc.ServeDecisions(*d)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "cto", Root: root, PID: me, StartedAt: time.Now(), DecisionSocket: s.Path()}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	cleared := func() (int, int) {
		clears, err := schedproc.ReadClears(root)
		if err != nil {
			t.Fatal(err)
		}
		lines, err := schedproc.ReadDecisionsTail(root)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, d := range lines {
			if d.Kind == schedproc.DecisionWarningCleared {
				n++
				if d.Seat != "scribe" || d.ReasonCode != "seat_stalled_at_dialog" || !strings.Contains(d.Reason, "launch-1") {
					t.Errorf("the decision: %+v", d)
				}
			}
		}
		return len(clears["scribe"]), n
	}
	s := serve(schedproc.SupervisingLead(me, start, procinfo.StartToken, procinfo.Alive))
	if out := runKeyVerb(t, root, "clear"); !strings.Contains(out, "exit=0") {
		t.Fatalf("clear from the lead's descendant:\n%s", out)
	}
	if c, d := cleared(); c != 1 || d != 1 {
		t.Fatalf("recorded: %d clears, %d decisions", c, d)
	}
	s.Close()
	// The lead is another process: the verb's process is a seat's shell
	// as far as the scheduler can tell.
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Process.Kill(); other.Wait() })
	otherStart, _ := procinfo.StartToken(other.Process.Pid)
	serve(schedproc.SupervisingLead(other.Process.Pid, otherStart, procinfo.StartToken, procinfo.Alive))
	out := runKeyVerb(t, root, "clear")
	if !strings.Contains(out, fmt.Sprintf("exit=%d", ExitCommitRefused)) || !strings.Contains(out, schedproc.RefusedNotLead) {
		t.Fatalf("clear from outside the lead's descent:\n%s", out)
	}
	if c, d := cleared(); c != 1 || d != 1 {
		t.Fatalf("a refused clear was recorded: %d clears, %d decisions", c, d)
	}
}

// Brief 31-2 row 3 (the verb's half): the exact argv the add key runs from
// the lead's process (pi/test/keys.test.ts pins it: the lead's AM_ROOT, no
// --root, no --me) adds the task to the store with its declared files and
// its pool. Reverted (the key without --files or --pool), the row in
// keys.test.ts fails; this half fails when the verb stops taking them.
func TestAddKeysArgvAddsTheTaskWithItsFilesAndPool(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if _, err := task.Open(root); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	argv := []string{"add", "--title", "Parser", "--class", "implement", "--files", "src/parse.go,src/parse_test.go", "--pool", "codex:gpt-5.6-sol:high", "--json"}
	if _, _, err := captureOutput(t, func() error { return runTask(argv) }); err != nil {
		t.Fatal(err)
	}
	s, _ := task.Open(root)
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("the store: %v %+v", err, list)
	}
	got := list[0]
	if got.Title != "Parser" || strings.Join(got.DeclaredFiles, ",") != "src/parse.go,src/parse_test.go" || got.Pool == nil || got.Pool.Key() != "codex:gpt-5.6-sol:high" {
		t.Fatalf("the task: %+v pool %+v", got, got.Pool)
	}
}
