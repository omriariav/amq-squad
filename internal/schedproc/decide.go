package schedproc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
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
// process's parent chain, with the start stamp re-read at every hop, reaches
// the lead's pane shell as the launch ledger recorded it when the lead
// proved up (R8 of the third pass: the pane's @amq_pane_agent label is
// mutable and never consulted after launch). A chain that reaches another
// process or none is not_lead; an origin that cannot be read (no kernel
// peer, a vanished hop, no recorded lead pane, a recorded shell that is
// gone or reused) is origin_unknown, a refusal and never an alarm. The
// lead's AMQ message stays its record of the decision.
//
// The socket admits at most MaxDecisionConns connections at once (excess
// ones are refused and counted), proves the sender before it reads a byte,
// and reads the decision under a 5 s deadline.
//
// The fourth pass: the chain must reach the lead's incarnation, the process
// whose proof the ledger recorded (its pid and start stamp, the seat-exec
// that proved and then exec'd the agent in place), not only the pane shell:
// a lead relaunched under the same pane shell writes its new incarnation
// into the record, and the previous incarnation's descendants are not_lead.
// And the sender is proved again, against the ledger read again, after the
// request is read and before it is applied: a record replaced while the
// request was in flight refuses it.

// DecisionSocketName is the socket's name in the launch directory.
const DecisionSocketName = "scheduler-decide.sock"

// Limits of the decision socket.
const (
	MaxDecisionConns    = 8
	DecisionReadTimeout = 5 * time.Second
)

// decisionReadTimeout is DecisionReadTimeout (a test seam).
var decisionReadTimeout = DecisionReadTimeout

// Refusal codes of the decision socket.
const (
	RefusedNotLead       = "not_lead"
	RefusedOriginUnknown = "origin_unknown"
	RefusedBusy          = "decision_connections_full"
	// RefusedStarting: the scheduler has not passed its start-up barrier
	// (the exits ledger loaded, every adopted seat closed and confirmed
	// gone); its decision socket is not open yet.
	RefusedStarting = "scheduler_starting"
	// RefusedLeadGone: the lead's recorded incarnation (the team-mode lead
	// that started this scheduler) is gone or its pid reused; the scheduler
	// exits rather than serve a dead lead's socket (3.0.1 t16).
	RefusedLeadGone = "lead_gone"
)

// ErrLeadGone is SupervisingLead's error for a recorded lead incarnation
// that is gone (its pid dead or carrying another start stamp).
var ErrLeadGone = errors.New("lead_gone")

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
	// Relaunch takes the lead's relaunch of the roster (3.0.1 t16); nil
	// refuses it (relaunch_unsupported: the scheduler runs no roster).
	Relaunch func(lead string, req RelaunchRequest) DecideReply
	// Seams for tests: the lead's recorded pane, the chain walk, a pid's
	// start stamp and the kernel peer of a connection.
	LeadPane   func(root, lead string) (panechain.Pane, error)
	Reaches    func(pid int, start string, panes []panechain.Pane) (*panechain.Pane, bool)
	StartToken func(pid int) (string, bool)
	Peer       func(*net.UnixConn) (int, bool)
}

// RecordedLeadPane is the lead's incarnation as the launch ledger recorded
// it at the lead's proof (R8 and the fourth pass): the proved process (pid
// and start stamp) under its recorded pane shell, the shell checked to be
// still that process. The chain walk then has to reach the proved process
// itself, so the descendants of an earlier incarnation in the same pane are
// not the lead.
func RecordedLeadPane(root, lead string) (panechain.Pane, error) {
	e, err := launch.ReadProof(root, lead)
	if err != nil {
		return panechain.Pane{}, fmt.Errorf("no launch ledger entry for the lead %s: %v", lead, err)
	}
	if e.PaneShell <= 1 || e.PaneShellStart == "" {
		return panechain.Pane{}, fmt.Errorf("the lead %s's proof recorded no pane shell", lead)
	}
	if now, ok := procinfo.StartToken(e.PaneShell); !ok || now != e.PaneShellStart {
		return panechain.Pane{}, fmt.Errorf("the lead %s's recorded pane shell pid %d is gone or reused", lead, e.PaneShell)
	}
	if e.PID <= 1 || e.Start == "" {
		return panechain.Pane{}, fmt.Errorf("the lead %s's proof recorded no incarnation", lead)
	}
	return panechain.Pane{Shell: e.PID, Start: e.Start, Agent: lead}, nil
}

// SupervisingLead is the lead's incarnation when the scheduler was started
// by the lead's team mode (3.0.1 t16): the scheduler's own parent process,
// pid and raw start stamp read once at the scheduler's start. The team-mode
// lead runs outside the roster's tmux session (a lead inside it keeps the
// binding present, and AMQ then refuses every relaunch), so it has no proof
// ledger entry under the session root; the kernel's parent link is its
// proof instead, and its commands (pi runs as its child) descend from it.
// The stamp is read again at every decision: a parent that is gone or
// reused is origin_unknown.
//
// The proof is then panechain.Reaches as for a ledger lead: an ancestor of
// the sender (the walk bounded at 64 hops, the stamp re-read at every hop)
// whose pid and raw start stamp both equal the recorded incarnation. The
// stamp is what makes it sound: the recorded pid can sit in another chain
// only after the lead died and the pid was reused, and that pid then carries
// another stamp. The two proofs never both apply to one scheduler: a
// team-mode start uses this one, every other start the launch ledger's.
func SupervisingLead(pid int, start string, stamp func(int) (string, bool)) func(root, lead string) (panechain.Pane, error) {
	return func(_, lead string) (panechain.Pane, error) {
		if gone, why := LeadGone(pid, start, stamp); gone {
			return panechain.Pane{}, fmt.Errorf("%w: %s", ErrLeadGone, why)
		}
		return panechain.Pane{Shell: pid, Start: start, Agent: lead}, nil
	}
}

// LeadGone reports whether the recorded lead incarnation (pid and raw start
// stamp) is gone: not recorded, dead, or its pid carrying another stamp.
func LeadGone(pid int, start string, stamp func(int) (string, bool)) (bool, string) {
	if pid <= 1 || start == "" {
		return true, "the team-mode lead process that started this scheduler was not recorded"
	}
	if now, ok := stamp(pid); !ok || now != start {
		return true, fmt.Sprintf("the team-mode lead process %d that started this scheduler is gone or its pid reused", pid)
	}
	return false, ""
}

// DecisionServer is the running socket.
type DecisionServer struct {
	sock     *seatlaunch.PrivateSocket
	o        DecideOptions
	wg       sync.WaitGroup
	once     sync.Once
	slots    chan struct{}
	overflow atomic.Int64
}

// ServeDecisions binds the socket and serves it until Close.
func ServeDecisions(o DecideOptions) (*DecisionServer, error) {
	if o.Lead == "" || o.Apply == nil {
		return nil, errors.New("the decision socket needs the lead and the apply step")
	}
	if o.LeadPane == nil {
		o.LeadPane = RecordedLeadPane
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
	s := &DecisionServer{sock: sock, o: o, slots: make(chan struct{}, MaxDecisionConns)}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// Path is where the socket is bound (recorded in the launch record).
func (s *DecisionServer) Path() string { return s.sock.Path }

// Root is the session root the socket is bound under (SendDecision dials
// through it).
func (s *DecisionServer) Root() string { return s.o.Root }

// Overflow is how many connections were refused because the socket was full.
func (s *DecisionServer) Overflow() int64 { return s.overflow.Load() }

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
		select {
		case s.slots <- struct{}{}:
		default:
			s.overflow.Add(1)
			conn.SetWriteDeadline(time.Now().Add(time.Second))
			b, _ := json.Marshal(DecideReply{Code: RefusedBusy, Detail: fmt.Sprintf("%d decisions are open; refused", MaxDecisionConns)})
			conn.Write(append(b, '\n'))
			conn.Close()
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.slots }()
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(decisionReadTimeout))
			// The sender is proved before any byte is read.
			reply := s.origin(conn)
			if reply.Code == "" {
				reply = DecideReply{Code: "decision_malformed", Detail: "no decision"}
				line, err := bufio.NewReader(io.LimitReader(conn, 64<<10)).ReadBytes('\n')
				var kind struct {
					Kind string `json:"kind"`
				}
				if err == nil && json.Unmarshal(line, &kind) == nil && (kind.Kind == WidenRequestKind || kind.Kind == RelaunchRequestKind) {
					// The fourth pass: the ledger is read again and the
					// sender proved again after the request is read; a lead
					// record replaced meanwhile refuses it.
					if again := s.origin(conn); again.Code != "" {
						reply = again
					} else {
						conn.SetDeadline(time.Time{}) // the apply step is the scheduler's own work
						reply = s.apply(kind.Kind, line)
					}
				}
			}
			conn.SetWriteDeadline(time.Now().Add(decisionReadTimeout))
			b, _ := json.Marshal(reply)
			conn.Write(append(b, '\n'))
		}()
	}
}

// apply runs a proved decision of a known kind.
func (s *DecisionServer) apply(kind string, line []byte) DecideReply {
	switch kind {
	case RelaunchRequestKind:
		var req RelaunchRequest
		if json.Unmarshal(line, &req) != nil {
			return DecideReply{Code: "decision_malformed", Detail: "a relaunch that does not decode"}
		}
		if s.o.Relaunch == nil {
			return DecideReply{Code: RefusedRelaunchUnsupported, Detail: "this scheduler runs no roster; there is nothing to relaunch"}
		}
		return s.o.Relaunch(s.o.Lead, req)
	default:
		var req WidenRequest
		if json.Unmarshal(line, &req) != nil {
			return DecideReply{Code: "decision_malformed", Detail: "a widening that does not decode"}
		}
		return s.o.Apply(s.o.Lead, req)
	}
}

// origin is R4 and R8's check of the sender; an empty code is the lead.
func (s *DecisionServer) origin(conn *net.UnixConn) DecideReply {
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
	lead, err := s.o.LeadPane(s.o.Root, s.o.Lead)
	if errors.Is(err, ErrLeadGone) {
		return DecideReply{Code: RefusedLeadGone, Detail: err.Error() + "; refused, and the scheduler exits"}
	}
	if err != nil {
		return unknown(err.Error())
	}
	pane, known := s.o.Reaches(pid, start, []panechain.Pane{lead})
	if !known {
		return unknown(fmt.Sprintf("the parent chain of pid %d cannot be read", pid))
	}
	if pane == nil {
		return DecideReply{Code: RefusedNotLead, Detail: fmt.Sprintf("pid %d does not descend from the lead %s's recorded incarnation", pid, s.o.Lead)}
	}
	return DecideReply{}
}

// SendDecision is the lead's side: one decision over the scheduler's
// socket (recorded at path, bound under root), the reply read back. The
// dial goes through seatlaunch.DialPrivate, bounded like the bind: a
// recorded path past sun_path (a deep root on Linux) is still dialable.
func SendDecision(root, path string, req WidenRequest) (DecideReply, error) {
	return sendDecision(root, path, req)
}

// SendRelaunch is the lead's relaunch of the roster over the scheduler's
// socket (3.0.1 t16), proved and answered like a widening.
func SendRelaunch(root, path string, req RelaunchRequest) (DecideReply, error) {
	req.Kind = RelaunchRequestKind
	return sendDecision(root, path, req)
}

// RelaunchRequestKind marks the lead's relaunch of the roster.
const RelaunchRequestKind = "amq-squad.relaunch.v1"

// RefusedRelaunchUnsupported: the scheduler runs no roster.
const RefusedRelaunchUnsupported = "relaunch_unsupported"

// RelaunchRequest is the lead's decision to relaunch the roster: the
// remaining seats finish what they hold and close, and the roster launches
// again in one apply (a replacement while AMQ cannot join a fresh seat into
// a present binding, ask 20).
type RelaunchRequest struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`
}

func sendDecision(root, path string, req any) (DecideReply, error) {
	var reply DecideReply
	c, err := seatlaunch.DialPrivate(root, path, DecisionSocketName)
	if err != nil {
		return reply, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(60 * time.Second))
	b, _ := json.Marshal(req)
	// A refused connection (decision_connections_full) is answered and
	// closed before the request is read, so the write can fail while the
	// refusal waits in the socket: the reply is read either way.
	_, werr := c.Write(append(b, '\n'))
	line, err := bufio.NewReader(io.LimitReader(c, 4<<20)).ReadBytes('\n')
	if err != nil {
		if werr != nil {
			return reply, werr
		}
		return reply, err
	}
	return reply, json.Unmarshal(line, &reply)
}
