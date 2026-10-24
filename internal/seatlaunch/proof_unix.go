//go:build unix

package seatlaunch

import (
	"bufio"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// proofServer is the launcher's end of the seat proof (item 4, lead ruling):
// one Unix socket per launch, a one-time token per seat, and the only
// source from which a seat counts up. It accepts exactly one proof per
// token, within the token's window, from a kernel-identified sender whose
// parent chain reaches the seat's pane shell. A second valid proof (a
// replay) or a valid token from a process that is not the one it claims
// raises identity_forged; a wrong token is logged and dropped, never a
// refusal or a close (fix 3b (b)). The socket stays open for the whole token
// window, so a seat-exec arriving after a same-pane racer is still heard
// and raises the alarm (fix 3b (c)).
//
// Limits (recorded, not fixed): a process adopted by init or launchd loses
// its ancestry, so it is neither up nor alarmed; Linux pid namespaces are
// not translated; SO_PEERCRED describes the peer at connect time; and a
// racer inside the pane's own chain that proves first is detected only by
// the duplicate-proof alarm (the same-UID limit of plan 7.3).
type proofServer struct {
	ln       *net.UnixListener
	dir      *fsanchor.AnchoredDir
	name     string
	dev, ino uint64
	tmp      string // the darwin fallback directory, removed at close
	root     string
	session  string
	launchID string
	now      func() time.Time // record timestamps only
	clock    func() time.Time // the token window: a monotonic clock (time.Now)

	paneShell func(handle string) (pid int, start string, ok bool)
	closeProc func(pid int, start string) error

	mu       sync.Mutex
	seats    map[string]*proofSeat
	wg       sync.WaitGroup
	done     chan struct{}
	closeOne sync.Once
}

type proofSeat struct {
	requestID, token string
	issued           time.Time
	deadline         time.Time // issued + ProofTTL, on the monotonic clock
	consumed         bool
	pid              int
	start            string
	refused, detail  string // a refusal that ends this seat's launch
}

// proofDir opens the directory for the socket through the anchored walk and
// checks it is owned by this user and mode 0700: the core-owned launch
// directory, or, when its path is too long for sun_path (104 bytes on
// darwin; Linux binds through /proc/self/fd and never needs it), a private
// directory under the system temp dir.
func proofDir(root, name string) (*fsanchor.AnchoredDir, string, error) {
	d, err := fsanchor.OpenAnchored(root, []string{"extensions", "amq-squad", "launch"}, true)
	if err != nil {
		return nil, "", err
	}
	tmp := ""
	if runtime.GOOS != "linux" && len(filepath.Join(d.Path(), name)) >= 100 {
		d.Close()
		if tmp, err = os.MkdirTemp("", "amqsq-"); err != nil {
			return nil, "", err
		}
		if tmp, err = filepath.EvalSymlinks(tmp); err != nil { // the walk refuses a symlinked /var
			return nil, "", err
		}
		if d, err = fsanchor.OpenAnchored(tmp, nil, false); err != nil {
			return nil, "", err
		}
		unix.Fchmod(d.Fd(), 0o700)
	}
	var st unix.Stat_t
	if err := unix.Fstat(d.Fd(), &st); err != nil || int(st.Uid) != os.Getuid() || st.Mode&0o077 != 0 {
		d.Close()
		return nil, "", fmt.Errorf("%s is not a private (0700, own) directory", d.Path())
	}
	return d, tmp, nil
}

// socketAddr is the address of name inside d: through the directory's own
// descriptor on Linux, by its walked path elsewhere (darwin has no bindat).
func socketAddr(d *fsanchor.AnchoredDir, name string) string {
	if runtime.GOOS == "linux" {
		return "/proc/self/fd/" + strconv.Itoa(d.Fd()) + "/" + name
	}
	return filepath.Join(d.Path(), name)
}

var umaskMu sync.Mutex

func startProofServer(q Squad, launchID string, o Options) (*proofServer, error) {
	name := "proof-" + launchID[:12] + ".sock"
	d, tmp, err := proofDir(q.SessionRoot(), name)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*proofServer, error) {
		d.Close()
		if tmp != "" {
			os.RemoveAll(tmp)
		}
		return nil, err
	}
	// Never unlink a live socket: an existing node is removed only after a
	// connect to it fails with ECONNREFUSED (a stale socket).
	var st unix.Stat_t
	if unix.Fstatat(d.Fd(), name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil {
		c, cerr := net.DialTimeout("unix", socketAddr(d, name), time.Second)
		if cerr == nil {
			c.Close()
			return fail(fmt.Errorf("a live socket already answers at %s", name))
		}
		if !errors.Is(cerr, syscall.ECONNREFUSED) {
			return fail(fmt.Errorf("%s exists and is not a stale socket: %v", name, cerr))
		}
		if err := unix.Unlinkat(d.Fd(), name, 0); err != nil {
			return fail(err)
		}
	}
	umaskMu.Lock()
	old := syscall.Umask(0o077)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketAddr(d, name), Net: "unix"})
	syscall.Umask(old)
	umaskMu.Unlock()
	if err != nil {
		return fail(err)
	}
	ln.SetUnlinkOnClose(false) // removed by close, through the directory descriptor
	if err := unix.Fstatat(d.Fd(), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil || st.Mode&unix.S_IFMT != unix.S_IFSOCK {
		ln.Close()
		return fail(fmt.Errorf("the bound socket %s is not where it was bound", name))
	}
	s := &proofServer{ln: ln, dir: d, name: name, dev: uint64(st.Dev), ino: uint64(st.Ino), tmp: tmp,
		root: q.SessionRoot(), session: q.Session, launchID: launchID, now: o.Now, clock: o.ProofClock,
		paneShell: o.PaneShell, closeProc: o.CloseProcess, seats: map[string]*proofSeat{}, done: make(chan struct{})}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.paneShell == nil {
		s.paneShell = func(handle string) (int, string, bool) { return tmuxPaneShell(handle, o.Env) }
	}
	if s.closeProc == nil {
		s.closeProc = func(pid int, start string) error { return closeProcess(pid, start, 5*time.Second) }
	}
	s.wg.Add(1)
	go s.serve()
	// The window: the socket stays open for the full token window from
	// minting, whatever arrives early, then marks unused tokens expired.
	go func() {
		t := time.NewTimer(launch.ProofTTL + time.Second)
		defer t.Stop()
		select {
		case <-t.C:
		case <-s.done:
		}
		s.close()
	}()
	return s, nil
}

// ticket mints a seat's one-time token.
func (s *proofServer) ticket(handle, requestID string) *launch.ProofTicket {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	start, _ := procinfo.StartToken(os.Getpid())
	issued := s.clock()
	t := &launch.ProofTicket{Socket: filepath.Join(s.dir.Path(), s.name), Dir: s.dir.Path(), Name: s.name, Dev: s.dev, Ino: s.ino,
		Token: hex.EncodeToString(b), IssuedAt: s.now().UTC(), LauncherPID: os.Getpid(), LauncherStart: start}
	s.mu.Lock()
	// The deadline is on the monotonic clock from minting (fix 3b (f)): a
	// wall-clock step does not move it.
	s.seats[handle] = &proofSeat{requestID: requestID, token: t.Token, issued: issued, deadline: issued.Add(launch.ProofTTL)}
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
				peer, known := peerOf(conn)
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
		// Anyone who can reach the socket can send this: log and drop it.
		s.reject(m, peer, "proof_token_invalid")
		return launch.ProofReply{Code: "proof_token_invalid", Detail: "not this seat's token; dropped"}
	}
	if seat.consumed {
		return s.forged(m.Handle, seat, "proof_replayed", fmt.Sprintf("a second valid proof for a consumed token (first pid %d, now pid %d)", seat.pid, m.PID))
	}
	if seat.refused != "" {
		return launch.ProofReply{Code: seat.refused, Detail: seat.detail}
	}
	if !s.clock().Before(seat.deadline) {
		seat.refused, seat.detail = "proof_expired", fmt.Sprintf("the token's %s window has passed", launch.ProofTTL)
		s.markToken(m.Handle, launch.TokenExpired)
		return launch.ProofReply{Code: seat.refused, Detail: seat.detail}
	}
	if !peerKnown {
		return launch.ProofReply{Code: "proof_origin_unknown", Detail: "the kernel did not report the sender; the proof cannot be bound to a process"}
	}
	if peer != m.PID {
		return s.forged(m.Handle, seat, "proof_origin", fmt.Sprintf("a valid token claims pid %d but was sent by pid %d", m.PID, peer))
	}
	if m.RequestID != seat.requestID {
		return s.forged(m.Handle, seat, "proof_origin", "a valid token names another request id")
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
	if !inChain(m.PID, m.Start, shell, shellStart) {
		return s.forged(m.Handle, seat, "proof_origin", fmt.Sprintf("pid %d does not descend from the pane shell pid %d", m.PID, shell))
	}
	seat.consumed, seat.pid, seat.start = true, m.PID, m.Start
	if err := launch.WriteProof(s.root, launch.ProofEntry{Handle: m.Handle, LaunchID: s.launchID, RequestID: m.RequestID, PID: m.PID, Start: m.Start, At: s.now().UTC()}); err != nil {
		seat.consumed = false
		return launch.ProofReply{Code: "proof_ledger_failed", Detail: err.Error()}
	}
	s.markToken(m.Handle, launch.TokenConsumed)
	return launch.ProofReply{OK: true}
}

// forged refuses a valid-token proof as identity_forged: the alarm goes to
// the owner (status needs_you), and a seat that already proved is closed.
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

// reject logs a dropped proof in the core-owned launch directory.
func (s *proofServer) reject(m launch.ProofMessage, peer int, code string) {
	line, _ := json.Marshal(map[string]any{"at": s.now().UTC(), "code": code, "handle": m.Handle, "claimed_pid": m.PID, "peer_pid": peer})
	if f, err := os.OpenFile(filepath.Join(s.root, "extensions", "amq-squad", "launch", "proof-rejects.log"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
		f.Write(append(line, '\n'))
		f.Close()
	}
}

// markToken overwrites a seat's token in its staged record with a marker
// (fix 3b (e)), so the used-up token is no longer readable there.
func (s *proofServer) markToken(handle, marker string) {
	path := filepath.Join(launch.StagedDir(s.root, handle), launch.FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	rec, err := launch.Decode(b)
	if err != nil || rec.Proof == nil {
		return
	}
	rec.Proof.Token = marker
	_ = launch.WriteFile(path, rec)
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

// close ends the window: unused tokens are marked expired, the socket is
// removed through the directory descriptor.
func (s *proofServer) close() {
	s.closeOne.Do(func() {
		close(s.done)
		s.ln.Close()
		s.wg.Wait()
		s.mu.Lock()
		for handle, seat := range s.seats {
			if !seat.consumed {
				s.markToken(handle, launch.TokenExpired)
			}
		}
		s.mu.Unlock()
		unix.Unlinkat(s.dir.Fd(), s.name, 0)
		s.dir.Close()
		if s.tmp != "" {
			os.RemoveAll(s.tmp)
		}
	})
}

// wait blocks until the window has closed.
func (s *proofServer) wait() { <-s.done }

// inChain walks pid's parent chain (at most 64 hops) to shell. At every hop
// it reads the pid's start stamp before and after reading its parent, so a
// hop that was replaced mid-walk breaks the chain; the shell's stamp must be
// shellStart, and the sender's stamp is re-checked after the walk (fix 3b (d)).
func inChain(pid int, start string, shell int, shellStart string) bool {
	p := pid
	for i := 0; i < 64 && p > 1; i++ {
		before, ok := startTokenOf(p)
		if !ok {
			return false
		}
		if p == shell {
			if before != shellStart {
				return false
			}
			now, ok := startTokenOf(pid)
			return ok && now == start
		}
		next, ok := parentOf(p)
		after, ok2 := startTokenOf(p)
		if !ok || !ok2 || after != before || next == p {
			return false
		}
		p = next
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

// Test seams: the process table and the socket peer.
var (
	startTokenOf = procinfo.StartToken
	parentOf     = procinfo.ParentPID
	peerOf       = peerPID
)
