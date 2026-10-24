package schedproc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
)

// The lead's decisions reach the scheduler over the scheduler's own Unix
// socket (R4 of the 2b-2 milestone B second pass): the AMQ mailbox is
// same-UID writable and AMQ derives a message's sender from the caller's
// AM_ME, so a message carries no authority. The socket is bound like the
// seat proof socket (seatlaunch.ListenPrivate); the kernel names the
// connecting process, and the scheduler accepts a decision only when that
// process's parent chain, with the start stamp re-read at every hop,
// reaches the shell of the pane AMQ labelled with the lead's handle (the
// lead in the scheduler's launch record). A chain that reaches another pane
// or none is not_lead; an origin that cannot be read (no kernel peer, a
// vanished hop, no or two lead panes, an unreadable tmux server) is
// origin_unknown, a refusal and never an alarm. The lead's AMQ message
// stays its record of the decision.

// DecisionSocketName is the socket's name in the launch directory.
const DecisionSocketName = "scheduler-decide.sock"

// Refusal codes of the decision socket.
const (
	RefusedNotLead       = "not_lead"
	RefusedOriginUnknown = "origin_unknown"
)

// DecideReply is the scheduler's answer to one decision.
type DecideReply struct {
	OK     bool            `json:"ok"`
	Code   string          `json:"code,omitempty"`
	Detail string          `json:"detail,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

// DecideOptions configure the decision socket.
type DecideOptions struct {
	Root string
	Lead string
	// Apply applies a widening the lead decided (the verb's core).
	Apply func(lead string, req WidenRequest) DecideReply
	// Seams for tests: the panes, the chain walk, a pid's start stamp and the
	// kernel peer of a connection.
	Panes      func() ([]panechain.Pane, error)
	Reaches    func(pid int, start string, panes []panechain.Pane) (*panechain.Pane, bool)
	StartToken func(pid int) (string, bool)
	Peer       func(*net.UnixConn) (int, bool)
}

// DecisionServer is the running socket.
type DecisionServer struct {
	sock *seatlaunch.PrivateSocket
	o    DecideOptions
	wg   sync.WaitGroup
	once sync.Once
}

// ServeDecisions binds the socket and serves it until Close.
func ServeDecisions(o DecideOptions) (*DecisionServer, error) {
	if o.Lead == "" || o.Apply == nil {
		return nil, errors.New("the decision socket needs the lead and the apply step")
	}
	if o.Panes == nil {
		o.Panes = panechain.List
	}
	if o.Reaches == nil {
		o.Reaches = panechain.Reaches
	}
	if o.StartToken == nil {
		o.StartToken = procinfo.StartToken
	}
	if o.Peer == nil {
		o.Peer = seatlaunch.PeerPID
	}
	sock, err := seatlaunch.ListenPrivate(o.Root, DecisionSocketName)
	if err != nil {
		return nil, err
	}
	s := &DecisionServer{sock: sock, o: o}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Path is where the socket is bound (recorded in the launch record).
func (s *DecisionServer) Path() string { return s.sock.Path }

// Close stops the socket and waits for the open connections.
func (s *DecisionServer) Close() {
	s.once.Do(func() {
		s.sock.Close()
		s.wg.Wait()
	})
}

func (s *DecisionServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.sock.Listener.AcceptUnix()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			reply := DecideReply{Code: "decision_malformed", Detail: "no decision"}
			line, err := bufio.NewReader(io.LimitReader(conn, 64<<10)).ReadBytes('\n')
			var req WidenRequest
			if err == nil && json.Unmarshal(line, &req) == nil && req.Kind == WidenRequestKind {
				reply = s.judge(conn, req)
			}
			b, _ := json.Marshal(reply)
			conn.Write(append(b, '\n'))
		}()
	}
}

// judge is R4's check of the sender, then the apply step.
func (s *DecisionServer) judge(conn *net.UnixConn, req WidenRequest) DecideReply {
	unknown := func(why string) DecideReply {
		return DecideReply{Code: RefusedOriginUnknown, Detail: "the decision's origin cannot be established: " + why + "; refused"}
	}
	pid, known := s.o.Peer(conn)
	if !known || pid <= 1 {
		return unknown("the kernel did not report the sender")
	}
	start, ok := s.o.StartToken(pid)
	if !ok {
		return unknown(fmt.Sprintf("the start stamp of pid %d is unreadable", pid))
	}
	panes, err := s.o.Panes()
	if err != nil {
		return unknown(err.Error())
	}
	var lead []panechain.Pane
	for _, p := range panes {
		if p.Agent == s.o.Lead {
			lead = append(lead, p)
		}
	}
	if len(lead) != 1 {
		return unknown(fmt.Sprintf("%d panes carry the lead's handle %s", len(lead), s.o.Lead))
	}
	pane, known := s.o.Reaches(pid, start, lead)
	if !known {
		return unknown(fmt.Sprintf("the parent chain of pid %d cannot be read", pid))
	}
	if pane == nil {
		return DecideReply{Code: RefusedNotLead, Detail: fmt.Sprintf("pid %d does not descend from the lead %s's pane", pid, s.o.Lead)}
	}
	return s.o.Apply(s.o.Lead, req)
}

// SendDecision is the lead's side: one decision over the scheduler's
// socket, the reply read back.
func SendDecision(path string, req WidenRequest) (DecideReply, error) {
	var reply DecideReply
	c, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return reply, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(60 * time.Second))
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return reply, err
	}
	line, err := bufio.NewReader(io.LimitReader(c, 4<<20)).ReadBytes('\n')
	if err != nil {
		return reply, err
	}
	return reply, json.Unmarshal(line, &reply)
}
