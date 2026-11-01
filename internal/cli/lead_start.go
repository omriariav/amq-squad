package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// The start of a confirmed team from the lead's conversation (brief 31-3,
// section 3). The solo lead (`amq-squad lead` with no squad) serves a
// private socket in its own process, the parent of its pi. `team apply
// --digest D` run by the lead's amq_team_apply tool (a descendant of that
// process, which the socket proves from the kernel) asks it to start the
// team; the lead's process then
//
//  1. checks the owner's confirmation of D (R55: a record younger than 30
//     minutes carrying its door's proof);
//  2. runs the mayor's library checks (internal/fleet's Mayor.Start, R40:
//     the fleet grant, the permitted repositories, max squads, the cost cap
//     fail-closed), which write squad.yaml through the digest flow for D
//     only and call the launcher below; a root outside the fleet grant is
//     R40's fallback: the lead proposes, the owner starts;
//  3. the launcher runs up's shared steps (up.go): the compile, the
//     session, the doctor, the lead lock (taken here and held for the
//     lead's life: lead_running when a team lead holds the root), the
//     scheduler (started in this process, as `lead --team` starts it), and
//     the roster's outcome. No install, no tmux: `up` alone does those. The
//     launch budget starts at the confirmation's moment.

const (
	leadStartSocketName = "lead-start.sock"
	// leadStartSocketEnv names the socket in the lead's pi environment, so
	// the apply its tool runs can reach the lead's process.
	leadStartSocketEnv = "AMQ_SQUAD_LEAD_START_SOCKET"
	leadStartKind      = "amq-squad.team-start.v1"
	leadStartWait      = 10 * time.Minute
)

// leadStartRequest is the apply's request.
type leadStartRequest struct {
	Kind    string `json:"kind"`
	Digest  string `json:"digest"`
	Project string `json:"project"`
}

// leadStartReply is the lead's answer.
type leadStartReply struct {
	Outcome    string     `json:"outcome"` // started | refused
	Refusal    string     `json:"refusal,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	Steps      []upStep   `json:"steps,omitempty"`
	Roster     []string   `json:"roster,omitempty"`
	OwnerSteps [][]string `json:"owner_steps,omitempty"` // R40's fallback: what the owner runs
}

// Seams of the start for tests: the grant the mayor loads, the fleet
// registry, the repository identity, this binary.
var (
	leadMayorGrant    = func() (grants.Resolved, error) { return grants.Load("") }
	leadMayorRegistry = fleet.DefaultPath
	leadMayorIdentify func(string, grants.Fleet) (string, string, error) // nil: fleet.RepositoryIdentity
	leadSelfBinary    = func() (string, error) {
		self, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(self)
	}
	leadStartPeer = seatlaunch.PeerPID
)

// leadStarter is the solo lead's start of a confirmed team.
type leadStarter struct {
	root, project, version string
	pools                  []string
	self                   int
	wait                   time.Duration

	mu    sync.Mutex
	lock  *schedproc.LeadLock
	sup   *supervisor
	sock  *seatlaunch.PrivateSocket
	steps []upStep
}

// newLeadStarter is the starter of the solo lead whose AMQ root is root.
func newLeadStarter(root, project, version string, pools []string) *leadStarter {
	return &leadStarter{root: root, project: project, version: version, pools: pools, self: os.Getpid(), wait: 120 * time.Second}
}

// serve binds the socket and answers requests until close.
func (s *leadStarter) serve() (string, error) {
	sock, err := seatlaunch.ListenPrivate(s.root, leadStartSocketName)
	if err != nil {
		return "", err
	}
	s.sock = sock
	go func() {
		for {
			conn, err := sock.Listener.AcceptUnix()
			if err != nil {
				return
			}
			go s.answer(conn)
		}
	}()
	return sock.Path, nil
}

// close stops the scheduler, drops the lead lock and closes the socket.
func (s *leadStarter) close() {
	if s.sock != nil {
		s.sock.Close()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sup != nil {
		s.sup.stop()
	}
	s.lock.Release()
}

// answer proves the peer descends from the lead's process (the kernel names
// it), reads one request and answers it.
func (s *leadStarter) answer(conn *net.UnixConn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	reply := leadStartReply{Outcome: "refused"}
	pid, ok := leadStartPeer(conn)
	var chain []stampedPID
	var walkErr error
	if ok {
		chain, walkErr = callerChain(pid, s.self)
	}
	switch {
	case !ok:
		reply.Refusal, reply.Detail = "origin_unknown", "the kernel did not name the requesting process"
	case walkErr != nil:
		name, _, _ := strings.Cut(walkErr.Error(), ":")
		reply.Refusal, reply.Detail = name, strings.TrimSpace(strings.TrimPrefix(walkErr.Error(), name+":"))
	default:
		var req leadStartRequest
		line, err := bufio.NewReader(io.LimitReader(conn, 64<<10)).ReadBytes('\n')
		if err != nil || json.Unmarshal(line, &req) != nil || req.Kind != leadStartKind {
			reply.Refusal, reply.Detail = "request_malformed", "not a team start request"
			break
		}
		if leadStartBeforeAct != nil {
			leadStartBeforeAct()
		}
		// the caller is proved again just before the lead acts
		if err := restamp(chain); err != nil {
			reply.Refusal, reply.Detail = ReasonCallerUnproved, err.Error()
			break
		}
		conn.SetDeadline(time.Time{}) // the start is the lead's own work
		reply = s.start(context.Background(), req)
	}
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	b, _ := json.Marshal(reply)
	conn.Write(append(b, '\n'))
}

// leadStartBeforeAct runs between the request's read and the last proof of
// the caller (a test hook: the window a pid can be reused in).
var leadStartBeforeAct func()

// ReasonCallerUnproved: a pid of the caller's chain changed its start stamp
// while the lead read it (the process ended and the pid was reused).
const ReasonCallerUnproved = "caller_unproved"

// stampedPID is a pid with the start stamp read for it.
type stampedPID struct {
	PID   int    `json:"pid"`
	Stamp string `json:"stamp"`
}

// confirmStamp reads a pid's raw start stamp (a test seam).
var confirmStamp = procinfo.StartToken

// callerChain proves pid descends from anc, the pattern of
// panechain.Reaches: every pid's start stamp is read when the pid is first
// read; at each hop the child's parent and stamp are read again after the
// parent's stamp (a reparent or a reuse mid-hop breaks it); at the end every
// stamp is read again. Any change is caller_unproved; a chain that ends at
// pid 1 without anc is origin_not_lead. It returns the chain, anc included.
func callerChain(pid, anc int) ([]stampedPID, error) {
	first, ok := confirmStamp(pid)
	if !ok {
		return nil, fmt.Errorf("%s: the start stamp of pid %d cannot be read", ReasonCallerUnproved, pid)
	}
	chain := []stampedPID{{pid, first}}
	for p, i := pid, 0; ; i++ {
		if p == anc {
			return chain, restamp(chain)
		}
		if p <= 1 || i >= confirmMaxHops {
			return nil, fmt.Errorf("origin_not_lead: pid %d does not descend from the lead's process %d", pid, anc)
		}
		parent, ok := confirmParent(p)
		if !ok {
			return nil, fmt.Errorf("%s: the parent of pid %d cannot be read", ReasonCallerUnproved, p)
		}
		stamp, ok := confirmStamp(parent)
		if !ok && parent > 1 {
			return nil, fmt.Errorf("%s: the start stamp of pid %d cannot be read", ReasonCallerUnproved, parent)
		}
		if again, ok := confirmParent(p); !ok || again != parent {
			return nil, fmt.Errorf("%s: pid %d was reparented while the lead read it", ReasonCallerUnproved, p)
		}
		if err := restamp(chain[len(chain)-1:]); err != nil {
			return nil, err
		}
		chain = append(chain, stampedPID{parent, stamp})
		p = parent
	}
}

// restamp reads every pid's start stamp again: caller_unproved when one
// changed (its process ended and the pid was reused) or cannot be read.
func restamp(chain []stampedPID) error {
	for _, h := range chain {
		if h.PID <= 1 {
			continue
		}
		if now, ok := confirmStamp(h.PID); !ok || now != h.Stamp {
			return fmt.Errorf("%s: pid %d no longer carries the start stamp %q the lead read for it", ReasonCallerUnproved, h.PID, h.Stamp)
		}
	}
	return nil
}

// r40Fallback are the mayor's outcomes for a root outside the fleet grant
// (or a grant that does not let the mayor start): the lead proposes, the
// owner starts.
var r40Fallback = []string{fleet.ReasonNoFleetGrant, fleet.ReasonRepository, fleet.ReasonUnidentified, fleet.ReasonManualMode, fleet.ReasonProvisioning}

// start is one confirmed team's start.
func (s *leadStarter) start(ctx context.Context, req leadStartRequest) leadStartReply {
	s.mu.Lock()
	defer s.mu.Unlock()
	refused := func(name, format string, args ...any) leadStartReply {
		return leadStartReply{Outcome: "refused", Refusal: name, Detail: fmt.Sprintf(format, args...), Steps: s.steps}
	}
	s.steps = nil
	if s.lock != nil {
		return refused(schedproc.ReasonLeadRunning, "this lead already started its team and holds the lead lock")
	}
	if filepath.Clean(req.Project) != s.project {
		return refused("project_mismatch", "the lead leads %s, not %s", s.project, req.Project)
	}
	rec, err := checkConfirmation(s.root, req.Digest, s.project)
	if err != nil {
		name, _, _ := strings.Cut(err.Error(), ":")
		return refused(name, "%s", strings.TrimSpace(strings.TrimPrefix(err.Error(), name+":")))
	}
	prop, err := readProposal(s.root, req.Digest)
	if err != nil {
		return refused(ReasonNoProposal, "%v", err)
	}
	owner, err := grants.Load("")
	if err != nil {
		return refused("grant_invalid", "%v", err)
	}
	sq, err := team.ParseSquad(prop.SquadYAML, team.SquadPath(s.project), owner.Grants)
	if err != nil {
		return refused("squad_invalid", "%v", err)
	}
	l := &leadLauncher{s: s, at: rec.At}
	m := &fleet.Mayor{
		Grant: leadMayorGrant, RegistryPath: leadMayorRegistry(), Identify: leadMayorIdentify,
		Squads:   &confirmedSquadWriter{project: s.project, digest: req.Digest},
		Launcher: l,
		// up's session step provisions the root (amq coop init), so the
		// launch API's .amqrc precondition (ask 2) does not apply here
		NeedsProvisioning: func(string) (bool, string) { return false, "" },
	}
	name := strings.Trim(tmuxNameRE.ReplaceAllString(strings.ToLower(filepath.Base(s.project)), "-"), "-")
	if name == "" {
		name = "repo"
	}
	fsq := fleet.Squad{Name: name + "-" + sq.Session, Root: filepath.Join(s.project, ".agent-mail", sq.Session), ProjectPath: s.project, Lead: sq.Lead, Session: sq.Session}
	res, err := m.Start(ctx, fleet.StartRequest{Squad: fsq, SquadYAML: prop.SquadYAML})
	if err != nil {
		return refused("mayor_failed", "%v", err)
	}
	switch {
	case res.Outcome == fleet.OutcomeStarted:
		return leadStartReply{Outcome: "started", Detail: l.outcome, Steps: s.steps, Roster: l.roster}
	case slices.Contains(r40Fallback, res.Reason):
		r := refused(res.Reason, "%s; the lead proposes, the owner starts: from your own shell run the two commands below", res.Detail)
		r.OwnerSteps = [][]string{{"amq-squad", "team", "apply", "--digest", req.Digest, "--project", s.project}, {"amq-squad", "up", "--project", s.project}}
		return r
	case l.refusal != "":
		return refused(l.refusal, "%s", l.detail)
	}
	return refused(res.Reason, "%s", res.Detail)
}

// confirmedSquadWriter is the mayor's squad.yaml writer for the start of a
// confirmed team: the digest flow over the repository the owner confirmed
// (never the mayor's canonical path, which can spell it another way), and
// only for the confirmed digest.
type confirmedSquadWriter struct {
	teamSquadWriter
	project, digest string
}

func (w *confirmedSquadWriter) Propose(_ string, text string) (string, error) {
	d, err := w.teamSquadWriter.Propose(w.project, text)
	if err != nil {
		return "", err
	}
	if d != w.digest {
		return "", fmt.Errorf("%s: the proposal plans %s now, not the confirmed %s; propose again and confirm the new digest", team.CodeDigestMismatch, d, w.digest)
	}
	return d, nil
}

func (w *confirmedSquadWriter) Apply(_ string, digest string) error {
	return w.teamSquadWriter.Apply(w.project, digest)
}

func (w *confirmedSquadWriter) Rollback(_ string, digest string) error {
	return w.teamSquadWriter.Rollback(w.project, digest)
}

// leadLauncher is the mayor's roster launcher for the start of a confirmed
// team: up's shared steps in the lead's process.
type leadLauncher struct {
	s               *leadStarter
	at              time.Time // the confirmation's moment: the launch budget's start
	outcome         string
	roster          []string
	refusal, detail string
}

func (l *leadLauncher) Launch(ctx context.Context, _ fleet.LaunchRequest) error {
	s := l.s
	step := func(name string, fn func() (string, error)) error {
		detail, err := fn()
		st := upStep{Name: name, Outcome: "ok", Detail: detail}
		if err != nil {
			st.Outcome, st.Detail = "refused", err.Error()
			var r *upRefusal
			if errors.As(err, &r) {
				st.Refusal, st.Detail = r.Name, r.Detail
			}
			l.refusal, l.detail = firstNonEmptyStr(st.Refusal, name+"_failed"), st.Detail
		}
		s.steps = append(s.steps, st)
		return err
	}
	self, err := leadSelfBinary()
	if err != nil {
		return err
	}
	var q seatlaunch.Squad
	var lead string
	if err := step("compile", func() (string, error) {
		var err error
		q, lead, err = UpCompile(s.project, self)
		return fmt.Sprintf("%d seats, lead %s, session %s", len(q.Seats), lead, q.Session), err
	}); err != nil {
		return err
	}
	root := q.SessionRoot()
	for _, st := range []struct {
		name string
		fn   func() (string, error)
	}{
		{"session", func() (string, error) { return UpSession(ctx, q, lead) }},
		{"doctor", func() (string, error) { return UpDoctor(ctx, s.project, self) }},
		{"lead", func() (string, error) {
			lock, err := UpLeadLock(root)
			if err != nil {
				return "", refuse(schedproc.ReasonLeadRunning, "%v", err)
			}
			s.lock = lock
			return "the lead lock of " + root + " is held by this lead (pid " + fmt.Sprint(s.self) + ")", nil
		}},
	} {
		if err := step(st.name, st.fn); err != nil {
			return err
		}
	}
	from := schedproc.DecisionsSize(root)
	logFrom := fileSize(filepath.Join(schedproc.Dir(root), "scheduler.log"))
	if err := step("scheduler", func() (string, error) {
		// the launch budget counts from the owner's confirmation, as the
		// add key's counts from its press
		os.Setenv(leadStartEnv, l.at.UTC().Format(time.RFC3339Nano))
		sup, err := UpScheduler(s.project, root, s.pools)
		if err != nil {
			return "", refuse("scheduler_failed", "%v", err)
		}
		s.sup = sup
		return "scheduler run --project " + s.project + " supervised by the lead", nil
	}); err != nil {
		s.lock.Release()
		s.lock = nil
		return err
	}
	var seats []string
	err = step("roster", func() (string, error) {
		detail, roster, err := UpRosterOutcome(root, s.project, s.version, from, logFrom, s.wait, nil, "the lead's screen shows the roster")
		for _, r := range roster {
			seats = append(seats, fmt.Sprintf("seat %s %s/%s %s", r.Handle, r.Runtime, r.Model, r.State))
		}
		return detail, err
	})
	if err != nil {
		s.sup.stop()
		s.sup = nil
		s.lock.Release()
		s.lock = nil
		return err
	}
	l.outcome, l.roster = s.steps[len(s.steps)-1].Detail, seats
	return nil
}

// Close is the mayor's close of a fenced launch: the scheduler stops (the
// roster's seats close with it) and the lead lock drops.
func (l *leadLauncher) Close(context.Context, fleet.LaunchRequest) error {
	if l.s.sup != nil {
		l.s.sup.stop()
		l.s.sup = nil
	}
	l.s.lock.Release()
	l.s.lock = nil
	return nil
}

// applyThroughLead is `team apply --digest D` under the solo lead: the
// request over the lead's socket, the reply printed.
func applyThroughLead(sock, project, digest string, jsonOut bool) error {
	root := os.Getenv("AM_ROOT")
	c, err := seatlaunch.DialPrivate(root, sock, leadStartSocketName)
	if err != nil {
		return fmt.Errorf("lead_unreachable: the lead's start socket %s: %w", sock, err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(leadStartWait))
	b, _ := json.Marshal(leadStartRequest{Kind: leadStartKind, Digest: digest, Project: project})
	if _, err := c.Write(append(b, '\n')); err != nil {
		return err
	}
	line, err := bufio.NewReader(io.LimitReader(c, 4<<20)).ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("lead_unreachable: no reply from the lead: %w", err)
	}
	var reply leadStartReply
	if err := json.Unmarshal(line, &reply); err != nil {
		return err
	}
	if jsonOut {
		if err := printJSONEnvelope("team_apply", reply); err != nil {
			return err
		}
	} else {
		writeLeadStartText(os.Stdout, reply)
	}
	if reply.Outcome != "started" {
		return fmt.Errorf("%s: %s", reply.Refusal, reply.Detail)
	}
	return nil
}

func writeLeadStartText(out io.Writer, r leadStartReply) {
	for _, s := range r.Steps {
		line := fmt.Sprintf("start: %-9s %s", s.Name, s.Outcome)
		if s.Refusal != "" {
			line += " " + s.Refusal
		}
		if s.Detail != "" {
			line += ": " + s.Detail
		}
		fmt.Fprintln(out, line)
	}
	for _, seat := range r.Roster {
		fmt.Fprintln(out, seat)
	}
	if r.Outcome == "started" {
		fmt.Fprintln(out, "team started: "+r.Detail)
	}
	for _, step := range r.OwnerSteps {
		fmt.Fprintln(out, "  owner runs: "+termSafe(strings.Join(step, " ")))
	}
}
