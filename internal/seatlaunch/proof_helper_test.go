//go:build unix

package seatlaunch

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// TestProofHelperProcess is not a test: run as a child (AMQSQ_PROOF_HELPER=1)
// it plays seat-exec's proof step from its own process: it sends the proof
// for its pid and start stamp, prints the reply, and stays up as "the seat".
func TestProofHelperProcess(t *testing.T) {
	if os.Getenv("AMQSQ_PROOF_HELPER") != "1" {
		t.Skip("helper process")
	}
	if os.Getenv("AMQSQ_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	var msg launch.ProofMessage
	json.Unmarshal([]byte(os.Getenv("AMQSQ_MSG")), &msg)
	if msg.PID == 0 {
		msg.PID = os.Getpid()
	}
	if msg.Start == "" {
		msg.Start, _ = procinfo.StartToken(os.Getpid())
	}
	reply := launch.ProofReply{Code: "helper_dial_failed"}
	if conn, err := net.Dial("unix", os.Getenv("AMQSQ_SOCKET")); err == nil {
		b, _ := json.Marshal(msg)
		conn.Write(append(b, '\n'))
		if line, err := bufio.NewReader(conn).ReadBytes('\n'); err == nil {
			json.Unmarshal(line, &reply)
		}
		conn.Close()
	}
	b, _ := json.Marshal(reply)
	os.Stdout.Write(append(b, '\n'))
	time.Sleep(30 * time.Second)
	os.Exit(0)
}

// prover is a child process that proved (or tried to prove) a seat.
type prover struct {
	cmd   *exec.Cmd
	pid   int
	start string
	reply launch.ProofReply
}

// prove starts a prover for handle with the staged ticket; mutate may change
// the message (a wrong token, another pid). The child is killed at cleanup.
func prove(t *testing.T, q Squad, handle string, ignoreTerm bool, mutate func(*launch.ProofMessage)) *prover {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(launch.StagedDir(q.SessionRoot(), handle), launch.FileName))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := launch.Decode(b)
	if err != nil || rec.Proof == nil {
		t.Fatalf("staged ticket: %v %+v", err, rec.Proof)
	}
	msg := launch.ProofMessage{LaunchID: rec.LaunchID, RequestID: rec.RequestID, Handle: handle, Token: rec.Proof.Token,
		Root: rec.Root, Me: handle, Session: rec.Session}
	if mutate != nil {
		mutate(&msg)
	}
	mb, _ := json.Marshal(msg)
	cmd := exec.Command(os.Args[0], "-test.run=^TestProofHelperProcess$")
	cmd.Env = append(os.Environ(), "AMQSQ_PROOF_HELPER=1", "AMQSQ_SOCKET="+rec.Proof.Socket, "AMQSQ_MSG="+string(mb))
	if ignoreTerm {
		cmd.Env = append(cmd.Env, "AMQSQ_IGNORE_TERM=1")
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	p := &prover{cmd: cmd, pid: cmd.Process.Pid}
	p.start, _ = procinfo.StartToken(p.pid)
	line, err := bufio.NewReader(out).ReadBytes('\n')
	if err != nil {
		t.Fatalf("prover output: %v", err)
	}
	json.Unmarshal(line, &p.reply)
	go cmd.Wait() // reap, so a closed prover is not left a zombie that looks alive
	return p
}

// paneOf is a PaneShell stub (the stubbed AMQ pane lookup): every handle's
// pane shell is pid. With the test process as the shell, a prover (its
// child) is inside the pane chain.
func paneOf(pid int) func(string) (int, string, bool) {
	return func(string) (int, string, bool) {
		start, ok := procinfo.StartToken(pid)
		return pid, start, ok
	}
}

// blindStamp makes one pid's start stamp unreadable to the launcher's
// close path until the returned restore runs.
func blindStamp(pid int) func() {
	prev := startToken
	startToken = func(p int) (string, bool) {
		if p == pid {
			return "", false
		}
		return prev(p)
	}
	return func() { startToken = prev }
}
