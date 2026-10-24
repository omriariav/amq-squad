//go:build unix

package seatlaunch

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// proofServer is the launcher's end of the seat proof (item 4, lead ruling):
// one Unix socket per launch, a one-time token per seat, and the only
// source from which a seat counts up. It accepts exactly one proof per
// launch id and token, from the process whose parent chain reaches the
// seat's pane shell; a second proof or a proof from elsewhere is refused and
// raised as identity_forged (closing the seat that did prove, if any).
//
// Residual (plan 7.3, the same-UID limit): a racer inside the pane's own
// process chain that reads the staged token first can prove instead of
// seat-exec; seat-exec's own proof then arrives second and raises the
// duplicate-proof alarm, which is the detection.
type proofServer struct {
	ln        *net.UnixListener
	path, tmp string
	root      string
	session   string
	launchID  string
	now       func() time.Time
	paneShell func(handle string) (pid int, start string, ok bool)
	closeProc func(pid int, start string) error

	mu    sync.Mutex
	seats map[string]*proofSeat
	wg    sync.WaitGroup
}

type proofSeat struct {
	requestID, token string
	issued           time.Time
	consumed         bool
	pid              int
	start            string
	refused, detail  string // a refusal that ends this seat's launch
}

// proofSocketPath is the socket under the core-owned launch directory, or,
// when that path is too long for sun_path (104 bytes on darwin), a 0700
// directory under the system temp dir; the staged ticket names the one used.
func proofSocketPath(root, launchID string) (path, tmp string, err error) {
	dir := filepath.Join(root, "extensions", "amq-squad", "launch")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	path = filepath.Join(dir, "proof-"+launchID[:12]+".sock")
	if len(path) < 100 {
		return path, "", nil
	}
	tmp, err = os.MkdirTemp("", "amqsq-")
	if err != nil {
		return "", "", err
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		return "", "", err
	}
	return filepath.Join(tmp, "proof.sock"), tmp, nil
}

func startProofServer(q Squad, launchID string, o Options) (*proofServer, error) {
	path, tmp, err := proofSocketPath(q.SessionRoot(), launchID)
	if err != nil {
		return nil, err
	}
	os.Remove(path)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	s := &proofServer{ln: ln, path: path, tmp: tmp, root: q.SessionRoot(), session: q.Session, launchID: launchID,
		now: o.Now, paneShell: o.PaneShell, closeProc: o.CloseProcess, seats: map[string]*proofSeat{}}
	if s.paneShell == nil {
		s.paneShell = func(handle string) (int, string, bool) { return tmuxPaneShell(handle, o.Env) }
	}
	if s.closeProc == nil {
		s.closeProc = func(pid int, start string) error { return closeProcess(pid, start, 5*time.Second) }
	}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// ticket mints a seat's one-time token.
func (s *proofServer) ticket(handle, requestID string) *launch.ProofTicket {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	t := &launch.ProofTicket{Socket: s.path, Token: hex.EncodeToString(b), IssuedAt: s.now().UTC()}
	s.mu.Lock()
	s.seats[handle] = &proofSeat{requestID: requestID, token: t.Token, issued: t.IssuedAt}
	s.mu.Unlock()
	return t
}

func (s *proofServer) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(30 * time.Second))
			var msg launch.ProofMessage
			line, err := bufio.NewReader(&limitedReader{conn, 64 << 10}).ReadBytes('\n')
			reply := launch.ProofReply{Code: "proof_malformed", Detail: "no proof message"}
			if err == nil && json.Unmarshal(line, &msg) == nil {
				peer, known := peerPID(conn)
				reply = s.judge(msg, peer, known)
			}
			b, _ := json.Marshal(reply)
			conn.Write(append(b, '\n'))
		}()
	}
}

// judge decides one proof.
func (s *proofServer) judge(m launch.ProofMessage, peer int, peerKnown bool) launch.ProofReply {
	s.mu.Lock()
	defer s.mu.Unlock()
	seat := s.seats[m.Handle]
	if seat == nil || m.LaunchID != s.launchID {
		return launch.ProofReply{Code: "proof_unknown", Detail: "no ticket for this handle in this launch"}
	}
	if subtle.ConstantTimeCompare([]byte(m.Token), []byte(seat.token)) != 1 {
		return s.forged(m.Handle, seat, "proof_token_invalid", "a proof with a token that is not this seat's")
	}
	if seat.consumed {
		return s.forged(m.Handle, seat, "proof_replayed", fmt.Sprintf("a second proof for a consumed token (first pid %d, now pid %d)", seat.pid, m.PID))
	}
	if seat.refused != "" {
		return launch.ProofReply{Code: seat.refused, Detail: seat.detail}
	}
	if m.RequestID != seat.requestID {
		return s.forged(m.Handle, seat, "proof_origin", "the proof names another request id")
	}
	if age := s.now().Sub(seat.issued); age > launch.ProofTTL {
		seat.refused, seat.detail = "proof_expired", fmt.Sprintf("the token was staged %s ago (valid %s)", age.Round(time.Second), launch.ProofTTL)
		return launch.ProofReply{Code: seat.refused, Detail: seat.detail}
	}
	if peerKnown && peer != m.PID {
		return s.forged(m.Handle, seat, "proof_origin", fmt.Sprintf("the proof claims pid %d but was sent by pid %d", m.PID, peer))
	}
	if now, ok := procinfo.StartToken(m.PID); !ok || now != m.Start {
		return s.forged(m.Handle, seat, "proof_origin", fmt.Sprintf("pid %d does not carry the claimed start stamp", m.PID))
	}
	if m.Root != s.root || m.Me != m.Handle || m.Session != s.session {
		return s.forged(m.Handle, seat, "proof_origin", "the proved identity is not this seat's")
	}
	shell, shellStart, ok := s.paneShell(m.Handle)
	if !ok {
		return launch.ProofReply{Code: "proof_origin_unknown", Detail: "the seat's pane and its shell are not known yet"}
	}
	if !inChain(m.PID, shell, shellStart) {
		return s.forged(m.Handle, seat, "proof_origin", fmt.Sprintf("pid %d does not descend from the pane shell pid %d", m.PID, shell))
	}
	seat.consumed, seat.pid, seat.start = true, m.PID, m.Start
	if err := launch.WriteProof(s.root, launch.ProofEntry{Handle: m.Handle, LaunchID: s.launchID, RequestID: m.RequestID, PID: m.PID, Start: m.Start, At: s.now().UTC()}); err != nil {
		seat.consumed = false
		return launch.ProofReply{Code: "proof_ledger_failed", Detail: err.Error()}
	}
	return launch.ProofReply{OK: true}
}

// forged refuses a proof as identity_forged: the alarm goes to the owner
// (status needs_you), and a seat that already proved is closed.
func (s *proofServer) forged(handle string, seat *proofSeat, code, detail string) launch.ProofReply {
	_ = launch.WriteAlarm(s.root, launch.Alarm{Kind: "identity_forged", Code: code, Handle: handle, LaunchID: s.launchID, Detail: detail, At: s.now().UTC()})
	if seat.consumed && seat.refused == "" {
		if err := s.closeProc(seat.pid, seat.start); err != nil {
			detail += "; closing the proved seat failed: " + err.Error()
		}
	}
	seat.refused, seat.detail = "identity_forged", code+": "+detail
	return launch.ProofReply{Code: "identity_forged", Detail: code + ": " + detail}
}

// state reports a seat's proof: up (with its identity) or a refusal.
func (s *proofServer) state(handle string) (up bool, pid int, start, refused, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	seat := s.seats[handle]
	if seat == nil {
		return false, 0, "", "", ""
	}
	return seat.consumed && seat.refused == "", seat.pid, seat.start, seat.refused, seat.detail
}

func (s *proofServer) close() {
	s.ln.Close()
	s.wg.Wait()
	os.Remove(s.path)
	if s.tmp != "" {
		os.RemoveAll(s.tmp)
	}
}

// inChain walks pid's parent chain (at most 64 steps) and reports whether it
// reaches shell, whose start stamp must still be shellStart (pid reuse).
func inChain(pid, shell int, shellStart string) bool {
	for i := 0; i < 64 && pid > 1; i++ {
		if pid == shell {
			now, ok := procinfo.StartToken(pid)
			return ok && now == shellStart
		}
		next, ok := procinfo.ParentPID(pid)
		if !ok || next == pid {
			return false
		}
		pid = next
	}
	return false
}

type limitedReader struct {
	c net.Conn
	n int
}

func (r *limitedReader) Read(p []byte) (int, error) {
	if r.n <= 0 {
		return 0, fmt.Errorf("proof message too long")
	}
	if len(p) > r.n {
		p = p[:r.n]
	}
	n, err := r.c.Read(p)
	r.n -= n
	return n, err
}
