package cli

import (
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v3-5-verifier rule 2: RecordVerdict is reachable only from the harness. A
// seat process cannot record a verdict: guard denies its call of the commit
// step or the scheduler process (harness_only), the commit step refuses to
// run inside a seat (AMQ_SQUAD_SEAT), and no task verb exposes a verdict.
func TestSeatCannotRecordAVerdict(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	root := t.TempDir()
	id := hookIdentity{Handle: "s1", Root: root, Session: "s", AgentDir: filepath.Join(root, "agents", "s1")}
	for _, command := range []string{
		"amq-squad seat commit --seat s1",
		"/usr/local/bin/amq-squad seat commit --seat s1 --root " + root,
		"sh -c 'amq-squad scheduler run --project . --session s'",
		"env FOO=1 amq-squad seat commit --seat s1 --json",
	} {
		v := evaluateGuard(id, guardCall{Tool: "Bash", Args: map[string]string{"command": command}, CWD: t.TempDir()}, time.Now())
		if v.Verdict != "deny" || v.ReasonCode != "harness_only" {
			t.Errorf("guard on a seat's %q: %s %s", command, v.Verdict, v.ReasonCode)
		}
	}
	if v := evaluateGuard(id, guardCall{Tool: "Bash", Args: map[string]string{"command": "amq-squad task done t1 --me s1"}, CWD: t.TempDir()}, time.Now()); v.Verdict != "allow" {
		t.Fatalf("a seat's own done report: %s %s", v.Verdict, v.ReasonCode)
	}
	t.Setenv("AMQ_SQUAD_SEAT", "s1")
	if err := runSeatCommit([]string{"--seat", "s1", "--root", root}, io.Discard); err == nil || !strings.Contains(err.Error(), "harness_only") {
		t.Fatalf("seat commit inside a seat: %v", err)
	}
	for _, verb := range []string{"verdict", "record-verdict", "snapshot"} {
		if err := runTask([]string{verb, "t1", "--me", "s1"}); err == nil || !strings.Contains(err.Error(), "unknown") {
			t.Fatalf("task %s is a verb: %v", verb, err)
		}
	}
}
