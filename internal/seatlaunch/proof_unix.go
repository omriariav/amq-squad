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
	paneID    func(shell int) string // the pane id of a shell (Options.PaneID; default a read-only tmux query)
	closeProc func(pid int, start string) error

	mu       sync.Mutex
	seats    map[string]*proofSeat
	overflow int // connections refused because lim.conns were busy
	lim      proofLimits
	rejMu    sync.Mutex
	rejects  map[string]*rejectCount
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

// bindMu serializes this process's socket binds (the umask is process-wide).
var bindMu sync.Mutex

// afterStaleProbe is a test seam between the stale probe and the unlink.
var afterStaleProbe func(d *fsanchor.AnchoredDir, name string)

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
	ln, st, err := bindPrivate(d, name)
	if err != nil {
		return fail(err)
	}
	s := &proofServer{ln: ln, dir: d, name: name, dev: uint64(st.Dev), ino: uint64(st.Ino), tmp: tmp,
		root: q.SessionRoot(), session: q.Session, launchID: launchID, now: o.Now, clock: o.ProofClock,
		paneShell: o.PaneShell, paneID: o.PaneID, closeProc: o.CloseProcess, seats: map[string]*proofSeat{}, done: make(chan struct{}),
		lim: proofLimits{conns: maxProofConns, read: proofReadTimeout, logCap: rejectLogCap, judge: judgeHook, mark: markTokenHook}}
	if s.clock == nil {
		s.clock = time.Now
	}
	if s.paneShell == nil {
		s.paneShell = func(handle string) (int, string, bool) { return tmuxPaneShell(handle, o.Env) }
	}
	if s.paneID == nil {
		s.paneID = paneIDOf
	}
	if s.closeProc == nil {
		s.closeProc = func(pid int, start string) error { _, err := closeProcess(pid, start, closeGrace); return err }
	}
	s.wg.Add(1)
	go s.serve()
	// The window, per ticket from its minting (fix 3c (h)): the socket stays
	// open, whatever arrives early, until every ticket's deadline has passed
	// on the monotonic clock; then unused tokens are marked expired.
	go func() {
		tick := time.NewTicker(windowPoll)
		defer tick.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-tick.C:
			}
			if s.windowOver() {
				s.close()
				return
			}
		}
	}()
	return s, nil
}

// bindPrivate binds a Unix socket named name inside the private directory d
// (the proof socket's bind, shared with the scheduler's decision socket).
// One lock covers the stale probe, the unlink and the bind (fix 3c (a)). An
// existing node is removed only after a connect to it fails with
// ECONNREFUSED and only if it is still the node probed (same device and
// inode); a live socket, a replaced node or a bind that finds the name taken
// is socket_occupied: a same-UID pre-bind is a detected denial, never a
// silent takeover. The socket is created 0600 and removed by the caller
// through the directory descriptor.
func bindPrivate(d *fsanchor.AnchoredDir, name string) (*net.UnixListener, unix.Stat_t, error) {
	occupied := func(detail string) (*net.UnixListener, unix.Stat_t, error) {
		return nil, unix.Stat_t{}, fmt.Errorf("socket_occupied: %s (%s)", detail, name)
	}
	bindMu.Lock()
	var st unix.Stat_t
	if unix.Fstatat(d.Fd(), name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil {
		probed := st
		c, cerr := net.DialTimeout("unix", socketAddr(d, name), time.Second)
		if cerr == nil {
			c.Close()
			bindMu.Unlock()
			return occupied("a live socket already answers")
		}
		if !errors.Is(cerr, syscall.ECONNREFUSED) {
			bindMu.Unlock()
			return occupied("the name exists and is not a stale socket: " + cerr.Error())
		}
		if afterStaleProbe != nil {
			afterStaleProbe(d, name)
		}
		if unix.Fstatat(d.Fd(), name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Dev != probed.Dev || st.Ino != probed.Ino {
			bindMu.Unlock()
			return occupied("the stale socket was replaced after the probe")
		}
		if err := unix.Unlinkat(d.Fd(), name, 0); err != nil {
			bindMu.Unlock()
			return nil, st, err
		}
	}
	old := syscall.Umask(0o077)
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketAddr(d, name), Net: "unix"})
	syscall.Umask(old)
	bindMu.Unlock()
	if errors.Is(err, syscall.EADDRINUSE) {
		return occupied("another process bound the name")
	}
	if err != nil {
		return nil, st, err
	}
	ln.SetUnlinkOnClose(false) // removed by close, through the directory descriptor
	if err := unix.Fstatat(d.Fd(), name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil || st.Mode&unix.S_IFMT != unix.S_IFSOCK {
		ln.Close()
		return nil, st, fmt.Errorf("the bound socket %s is not where it was bound", name)
	}
	return ln, st, nil
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
	slots := make(chan struct{}, s.lim.conns)
	for {
		conn, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		select {
		case slots <- struct{}{}:
		default:
			// At most lim.conns at once (fix 3c (d)): refused and counted.
			s.mu.Lock()
			s.overflow++
			s.mu.Unlock()
			conn.Close()
			s.reject(launch.ProofMessage{}, 0, "proof_connections_full")
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-slots }()
			defer conn.Close()
			conn.SetReadDeadline(time.Now().Add(s.lim.read))
			conn.SetWriteDeadline(time.Now().Add(s.lim.read))
			var msg launch.ProofMessage
			line, err := bufio.NewReader(&limitedReader{conn, 64 << 10}).ReadBytes('\n')
			reply := launch.ProofReply{Code: "proof_malformed", Detail: "no proof message"}
			if err == nil && json.Unmarshal(line, &msg) == nil {
				peer, known := peerOf(conn)
				if s.lim.judge != nil {
					s.lim.judge()
				}
				reply = s.judge(msg, peer, known)
			}
			b, _ := json.Marshal(reply)
			conn.Write(append(b, '\n'))
		}()
	}
}

// paneOrigin is the pane lookup of one proof: the seat's pane shell and its
// start stamp, and the pane id.
type paneOrigin struct {
	shell      int
	shellStart string
	id         string
	ok         bool
}

// judge decides one proof. The pane lookup (tmux, bounded by
// paneLookupTimeout) runs before the server mutex is taken (phase 5 re-pass
// P2-6), so a hung tmux stalls only this proof, never the others or the
// socket's shutdown; it runs only for a proof whose token is live, so a
// wrong-token sender cannot make the server call tmux.
func (s *proofServer) judge(m launch.ProofMessage, peer int, peerKnown bool) launch.ProofReply {
	var pane paneOrigin
	if s.tokenLive(m) {
		pane.shell, pane.shellStart, pane.ok = s.paneShell(m.Handle)
		if pane.ok {
			pane.id = s.paneID(pane.shell)
		}
	}
	return s.judgeLocked(m, peer, peerKnown, pane)
}

// tokenLive reports a proof whose token is this seat's and not yet used or
// refused; judgeLocked decides again under the lock.
func (s *proofServer) tokenLive(m launch.ProofMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	seat := s.seats[m.Handle]
	return seat != nil && m.LaunchID == s.launchID && subtle.ConstantTimeCompare([]byte(m.Token), []byte(seat.token)) == 1 && !seat.consumed && seat.refused == ""
}

func (s *proofServer) judgeLocked(m launch.ProofMessage, peer int, peerKnown bool, pane paneOrigin) launch.ProofReply {
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
		if err := s.markToken(m.Handle, launch.TokenExpired); err != nil {
			seat.detail += "; the expired marker could not be written: " + err.Error()
		}
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
	now, ok := startTokenOf(m.PID)
	if !ok {
		return launch.ProofReply{Code: "proof_origin_unknown", Detail: fmt.Sprintf("the start stamp of pid %d cannot be read", m.PID)}
	}
	if now != m.Start {
		return s.forged(m.Handle, seat, "proof_origin", fmt.Sprintf("pid %d does not carry the claimed start stamp", m.PID))
	}
	if m.Root != s.root || m.Me != m.Handle || m.Session != s.session {
		return s.forged(m.Handle, seat, "proof_origin", "the proved identity is not this seat's")
	}
	shell, shellStart := pane.shell, pane.shellStart
	if !pane.ok {
		return launch.ProofReply{Code: "proof_origin_unknown", Detail: "the seat's pane and its shell are not known yet"}
	}
	in, known := inChain(m.PID, m.Start, shell, shellStart)
	if !known {
		// A process entry vanished or changed mid-walk (fix 3c (e)): not up,
		// not an alarm.
		return launch.ProofReply{Code: "proof_origin_unknown", Detail: "the process chain changed while it was read"}
	}
	if !in {
		return s.forged(m.Handle, seat, "proof_origin", fmt.Sprintf("pid %d does not descend from the pane shell pid %d", m.PID, shell))
	}
	// The consumed marker first (fix 3c (f)): a marker that cannot be
	// written fails the seat's launch, so a readable token never stays
	// valid behind an accepted proof.
	if err := s.markToken(m.Handle, launch.TokenConsumed); err != nil {
		seat.refused, seat.detail = "proof_marker_failed", err.Error()
		return launch.ProofReply{Code: seat.refused, Detail: seat.detail}
	}
	seat.consumed, seat.pid, seat.start = true, m.PID, m.Start
	// R8 of the 2b-2 milestone B third pass: the pane the origin check found
	// (its shell pid and start stamp, and its id) is recorded with the proof;
	// after launch the pane's mutable label is never consulted again (the
	// scheduler's decision socket proves the lead against this record).
	if err := launch.WriteProof(s.root, launch.ProofEntry{Handle: m.Handle, LaunchID: s.launchID, RequestID: m.RequestID, PID: m.PID, Start: m.Start, At: s.now().UTC(),
		PaneShell: shell, PaneShellStart: shellStart, PaneID: pane.id}); err != nil {
		seat.consumed = false
		seat.refused, seat.detail = "proof_ledger_failed", err.Error()
		return launch.ProofReply{Code: seat.refused, Detail: seat.detail}
	}
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

// reject logs a dropped proof in the core-owned launch directory, at most
// one line per (handle, code) per minute with the count it stands for, in a
// file capped at rejectLogCap with one rotation (fix 3c (c)).
func (s *proofServer) reject(m launch.ProofMessage, peer int, code string) {
	s.rejMu.Lock()
	defer s.rejMu.Unlock()
	key := m.Handle + "\x00" + code
	if s.rejects == nil {
		s.rejects = map[string]*rejectCount{}
	}
	rc := s.rejects[key]
	if rc == nil {
		rc = &rejectCount{}
		s.rejects[key] = rc
	}
	rc.n++
	now := s.clock()
	if !rc.last.IsZero() && now.Sub(rc.last) < time.Minute {
		return
	}
	line, _ := json.Marshal(map[string]any{"at": s.now().UTC(), "code": code, "handle": m.Handle, "claimed_pid": m.PID, "peer_pid": peer, "count": rc.n})
	rc.last, rc.n = now, 0
	path := filepath.Join(s.root, "extensions", "amq-squad", "launch", "proof-rejects.log")
	if st, err := os.Stat(path); err == nil && st.Size()+int64(len(line))+1 > s.lim.logCap {
		os.Rename(path, path+".1")
	}
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600); err == nil {
		f.Write(append(line, '\n'))
		f.Close()
	}
}

type rejectCount struct {
	last time.Time
	n    int
}

// markToken overwrites a seat's token in its staged record with a marker
// (fix 3b (e)), so the used-up token is no longer readable there.
func (s *proofServer) markToken(handle, marker string) error {
	if s.lim.mark != nil {
		if err := s.lim.mark(handle, marker); err != nil {
			return err
		}
	}
	return launch.UpdateStaged(s.root, handle, func(rec *launch.Record) error {
		if rec.Proof == nil {
			return fmt.Errorf("the staged record of %s has no ticket", handle)
		}
		rec.Proof.Token = marker
		return nil
	})
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
		s.ln.Close()
		s.wg.Wait() // every handler finishes, its alarm written, before done (fix 3c (g))
		s.mu.Lock()
		for handle, seat := range s.seats {
			if !seat.consumed {
				_ = s.markToken(handle, launch.TokenExpired)
			}
		}
		s.mu.Unlock()
		unix.Unlinkat(s.dir.Fd(), s.name, 0)
		s.dir.Close()
		if s.tmp != "" {
			os.RemoveAll(s.tmp)
		}
		close(s.done)
	})
}

// wait blocks until the window has closed.
func (s *proofServer) wait() { <-s.done }

// inChain walks pid's parent chain (at most 64 hops) to shell. At every hop
// it reads the pid's start stamp before and after reading its parent, so a
// hop that was replaced mid-walk breaks the chain; the shell's stamp must be
// shellStart, and the sender's stamp is re-checked after the walk (fix 3b (d)).
// The result is (in the chain, known): a process entry that vanishes or
// changes mid-walk makes the answer unknown, never "outside" (fix 3c (e)).
func inChain(pid int, start string, shell int, shellStart string) (bool, bool) {
	p := pid
	for i := 0; i < 64 && p > 1; i++ {
		before, ok := startTokenOf(p)
		if !ok {
			return false, false
		}
		if p == shell {
			if before != shellStart {
				return false, true
			}
			now, ok := startTokenOf(pid)
			if !ok || now != start {
				return false, false
			}
			return true, true
		}
		next, ok := parentOf(p)
		after, ok2 := startTokenOf(p)
		if !ok || !ok2 || after != before {
			return false, false
		}
		if next == p {
			return false, true
		}
		p = next
	}
	return false, true
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

// Limits of the proof socket (fix 3c): concurrent connections, the read
// deadline, the reject log's size, the close grace, the window poll.
var (
	maxProofConns    = 8
	proofReadTimeout = 5 * time.Second
	rejectLogCap     = int64(1 << 20)
	windowPoll       = 200 * time.Millisecond
	markTokenHook    func(handle, marker string) error // a test seam
	judgeHook        func()                            // a test seam: runs before a proof is judged
)

// proofLimits are the limits and test hooks a server takes when it starts,
// so a server never reads the package variables from its goroutines.
type proofLimits struct {
	conns  int
	read   time.Duration
	logCap int64
	judge  func()
	mark   func(handle, marker string) error
}

// windowOver reports whether every minted ticket's deadline has passed.
// windowEnd is the latest ticket deadline (zero when no ticket was minted):
// after it no proof is accepted.
func (s *proofServer) windowEnd() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var end time.Time
	for _, seat := range s.seats {
		if seat.deadline.After(end) {
			end = seat.deadline
		}
	}
	return end
}

func (s *proofServer) windowOver() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.seats) == 0 {
		return false
	}
	now := s.clock()
	for _, seat := range s.seats {
		if now.Before(seat.deadline) {
			return false
		}
	}
	return true
}
