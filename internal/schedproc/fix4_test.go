package schedproc

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Tests of the 2b-2 milestone B fix commit 4 (the fourth pass on 13c1fe76).

// R8 (decide.go:171-178): the sender was proved before the request was
// read, so an old lead could connect, pass, and have its widening applied
// after the record was replaced within the read window. The ledger is read
// again and the sender proved again after the request is read: a lead
// record replaced in between refuses the decision.
func TestDecisionSocketReprovesTheLeadAfterTheRequest(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	calls := 0
	applied := 0
	s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
		Apply: func(string, WidenRequest) DecideReply { applied++; return DecideReply{OK: true} },
		LeadPane: func(string, string) (panechain.Pane, error) {
			calls++
			if calls == 1 {
				return panechain.Pane{Shell: 100, Start: "old", Agent: "lead"}, nil
			}
			return panechain.Pane{Shell: 200, Start: "new", Agent: "lead"}, nil // relaunched meanwhile
		},
		Reaches: func(_ int, _ string, panes []panechain.Pane) (*panechain.Pane, bool) {
			if panes[0].Shell == 100 {
				return &panes[0], true // the sender descends from the old incarnation only
			}
			return nil, true
		},
		StartToken: func(int) (string, bool) { return "s", true },
		Peer:       func(*net.UnixConn) (int, bool) { return 4242, true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reply, err := SendDecision(s.Path(), WidenRequest{Kind: WidenRequestKind, Task: "t1"})
	if err != nil || reply.OK || reply.Code != RefusedNotLead || applied != 0 || calls != 2 {
		t.Fatalf("a decision whose lead record was replaced in flight: %+v %v (applied %d, ledger reads %d)", reply, err, applied, calls)
	}
}

// R8, the incarnation: the chain only had to reach the recorded pane shell,
// so after a lead relaunch under the same pane shell the previous
// incarnation's descendants still passed. The record names the proved
// process (the lead's incarnation) and the chain must reach it: a sender in
// the same pane that does not descend from the recorded incarnation is
// not_lead.
func TestDecisionFromAPreviousLeadIncarnationIsRefused(t *testing.T) {
	t.Setenv("PATH", t.TempDir()+":/bin:/usr/bin")
	ppid := os.Getppid()
	shellStart, ok := procinfo.StartToken(ppid)
	self, ok2 := procinfo.StartToken(os.Getpid())
	if !ok || !ok2 {
		t.Skip("no start stamps on this host")
	}
	// The relaunched lead: a live process under the same pane shell that is
	// not this sender's ancestor.
	relaunched := exec.Command("sleep", "30")
	if err := relaunched.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { relaunched.Process.Kill(); relaunched.Wait() })
	newStart, ok := procinfo.StartToken(relaunched.Process.Pid)
	if !ok {
		t.Skip("no start stamp for the child")
	}
	for _, tc := range []struct {
		entry launch.ProofEntry
		want  string
	}{
		{launch.ProofEntry{Handle: "lead", LaunchID: "l2", PID: relaunched.Process.Pid, Start: newStart, PaneShell: ppid, PaneShellStart: shellStart}, RefusedNotLead},
		{launch.ProofEntry{Handle: "lead", LaunchID: "l1", PID: os.Getpid(), Start: self, PaneShell: ppid, PaneShellStart: shellStart}, ""},
	} {
		root, _ := filepath.EvalSymlinks(t.TempDir())
		if err := launch.WriteProof(root, tc.entry); err != nil {
			t.Fatal(err)
		}
		applied := 0
		s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
			Apply: func(string, WidenRequest) DecideReply { applied++; return DecideReply{OK: true} }})
		if err != nil {
			t.Fatal(err)
		}
		reply, err := SendDecision(s.Path(), WidenRequest{Kind: WidenRequestKind, Task: "t1"})
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if (tc.want == "" && (!reply.OK || applied != 1)) || (tc.want != "" && (reply.Code != tc.want || applied != 0)) {
			t.Fatalf("incarnation %s: %+v (applied %d)", tc.entry.LaunchID, reply, applied)
		}
	}
}

// asyncFake is the process tests' launcher whose nil close does not confirm.
type asyncFake struct{ *fakeLauncher }

func (asyncFake) CloseUnconfirmed(string) bool { return true }

// The restart barrier (process.go:102, :169; scheduler_run.go:288): the
// decision socket was opened before adoption, so a decision could be applied
// while a seat of the previous incarnation still ran. The socket opens only
// once the exits ledger is loaded and every adopted seat's close is
// confirmed; until then the launch record names no socket (task widen:
// scheduler_starting).
func TestDecisionSocketOpensOnlyAfterTheStartUpBarrier(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	prev := startTokenOf
	startTokenOf = func(pid int) (string, bool) { return "live", pid == 4242 }
	t.Cleanup(func() { startTokenOf = prev })
	if err := os.MkdirAll(launch.StagedDir(root, "c1"), 0o700); err != nil {
		t.Fatal(err)
	}
	launch.WriteFile(filepath.Join(launch.StagedDir(root, "c1"), launch.FileName), launch.Record{Handle: "c1", Root: root, Session: "s", Runtime: "pi", Worktree: "/w", Branch: "seat/c1"})
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: "c1", PID: 4242, Start: "live"}); err != nil {
		t.Fatal(err)
	}
	fl := &fakeLauncher{}
	l := asyncFake{fl}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	socks := make(chan string, 1000)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Root: root, Launcher: l, Log: &lockedBuf{}, Tick: 10 * time.Millisecond,
			Config:    scheduler.Config{Lead: "lead"},
			Decisions: &DecideOptions{Apply: func(string, WidenRequest) DecideReply { return DecideReply{OK: true} }},
			Started:   func(s *scheduler.Scheduler) { fl.mu.Lock(); fl.s = s; fl.mu.Unlock() },
			OnTick: func([]scheduler.Decision) {
				rec, _ := ReadLaunchRecord(root)
				socks <- rec.DecisionSocket
			}})
	}()
	for i := 0; i < 4; i++ {
		if sock := <-socks; sock != "" {
			t.Fatalf("tick %d: the decision socket opened while an adopted seat's close was unconfirmed", i+1)
		}
	}
	if err := PostEvent(root, Event{Kind: "seat_gone", Handle: "c1"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for open := false; !open; {
		select {
		case sock := <-socks:
			open = sock != ""
		case <-deadline:
			t.Fatal("the decision socket never opened after the adopted seat's seat_gone")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
