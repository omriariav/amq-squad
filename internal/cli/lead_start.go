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

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
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
	Digest  string `json:"digest,omitempty"`
	Project string `json:"project,omitempty"`
	Pane    string `json:"pane,omitempty"`  // the TUI door's pane
	Event   []byte `json:"event,omitempty"` // the Buzz door's raw event bytes, as the attachment fetched them
}

// The confirmation kinds on the same socket (the three doors).
const (
	confirmTUIKind   = "amq-squad.confirm-tui.v1"
	confirmShellKind = "amq-squad.confirm-shell.v1" // dropped: refused by name
	phaseKind        = "amq-squad.phase.v1"         // R53: the lead's pi asks whether the team started
	confirmBuzzKind  = "amq-squad.confirm-buzz.v1"
)

// leadStartReply is the lead's answer.
type leadStartReply struct {
	Outcome    string     `json:"outcome"` // started | witnessed | refused | team | solo
	Digest     string     `json:"digest,omitempty"`
	Refusal    string     `json:"refusal,omitempty"`
	Detail     string     `json:"detail,omitempty"`
	Steps      []upStep   `json:"steps,omitempty"`
	Roster     []string   `json:"roster,omitempty"`
	OwnerSteps [][]string `json:"owner_steps,omitempty"` // R40's fallback: what the owner runs
	// Team is the team lead's identity and the confirmed digest, in the
	// reply of a start that succeeded and of a phase query after it (R53,
	// brief 31-3 PR 2): the lead's pi switches on this reply, never on a file
	Team *leadTeam `json:"team,omitempty"`
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

	// the witness (R55 after the plan reviewer's ruling): the pi process the
	// lead spawned, and the confirmations it witnessed, in memory only
	wmu       sync.Mutex
	team      *leadTeam // set when this lead's start succeeded (R53)
	piPID     int
	piStamp   string
	witnessed map[string]confirmRecord
	seen      map[string]bool // Buzz event ids witnessed (once-only)
}

// setPi records the pi process the lead spawned, with its start stamp read
// at the spawn: the TUI door's only accepted peer.
func (s *leadStarter) setPi(pid int, stamp string) {
	s.wmu.Lock()
	s.piPID, s.piStamp = pid, stamp
	s.wmu.Unlock()
}

// newLeadStarter is the starter of the solo lead whose AMQ root is root.
func newLeadStarter(root, project, version string, pools []string) *leadStarter {
	return &leadStarter{root: root, project: project, version: version, pools: pools, self: os.Getpid(), wait: 120 * time.Second}
}

// serve binds the socket and answers requests until close.
func (s *leadStarter) serve() (string, error) {
	// solo startup (the socket's bind and the discovery record's publish)
	// is serialized on the repository's apply lock, as the lead lock step
	// is: a solo lead starting while a shell apply holds it (between its
	// probe and its write) waits for the write, up to applyLockWait, then
	// refuses apply_busy; the lock is released once the record is published
	unlock, err := takeApplyLock(s.project)
	if err != nil {
		return "", err
	}
	defer unlock()
	sock, err := seatlaunch.ListenPrivate(s.root, leadStartSocketName)
	if err != nil {
		return "", err
	}
	s.sock = sock
	// where the processes that are not the lead's descendants (team
	// confirm, the Buzz attachment) find the socket; no authority
	start, _ := confirmStamp(s.self)
	rec, _ := json.Marshal(leadStartRecord{Schema: 1, Socket: sock.Path, PID: s.self, Start: start})
	if err := atomicfile.Write(filepath.Join(s.root, "extensions", "amq-squad", leadStartRecordName), append(rec, '\n'), 0o600); err != nil {
		sock.Close()
		return "", err
	}
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
		// the discovery record goes with the socket, so a shell apply after
		// a clean exit finds no lead (one left by a crash is lead_unreachable
		// until the owner removes it)
		_ = os.Remove(filepath.Join(s.root, "extensions", "amq-squad", leadStartRecordName))
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
	var req leadStartRequest
	line, rerr := bufio.NewReader(io.LimitReader(conn, 64<<10)).ReadBytes('\n')
	switch {
	case !ok:
		reply.Refusal, reply.Detail = "origin_unknown", "the kernel did not name the requesting process"
	case rerr != nil || json.Unmarshal(line, &req) != nil:
		reply.Refusal, reply.Detail = "request_malformed", "not a request"
	case req.Kind == leadStartKind:
		chain, err := callerChain(pid, s.self)
		if err != nil {
			reply = refusedBy(err)
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
	case req.Kind == phaseKind:
		// R53: the lead's pi asks, at a session start, whether this lead
		// started its team; only a descendant of the lead is answered
		if _, err := callerChain(pid, s.self); err != nil {
			reply = refusedBy(err)
			break
		}
		s.wmu.Lock()
		team := s.team
		s.wmu.Unlock()
		reply = leadStartReply{Outcome: "solo"}
		if team != nil {
			reply = leadStartReply{Outcome: "team", Team: team}
		}
	case req.Kind == confirmTUIKind:
		reply = s.witnessTUI(pid, req)
	case req.Kind == confirmShellKind:
		// the shell door was dropped (no kernel read tells the owner's shell
		// from a tree the model built); its kind is refused by name
		reply.Refusal, reply.Detail = ReasonShellDoorRemoved, "the shell door was dropped: under one uid, no kernel read tells the owner's shell from a tree the model built; confirm at the lead's pane (/squad confirm) or over Buzz (yes <prefix>)"
	case req.Kind == confirmBuzzKind:
		reply = s.witnessBuzz(req)
	default:
		reply.Refusal, reply.Detail = "request_malformed", "an unknown request kind"
	}
	conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	b, _ := json.Marshal(reply)
	conn.Write(append(b, '\n'))
}

// refusedBy is a refusal whose error text starts with its name.
func refusedBy(err error) leadStartReply {
	name, _, _ := strings.Cut(err.Error(), ":")
	return leadStartReply{Outcome: "refused", Refusal: name, Detail: strings.TrimSpace(strings.TrimPrefix(err.Error(), name+":"))}
}

// witness keeps a confirmation the lead witnessed and logs it.
func (s *leadStarter) witness(rec confirmRecord) leadStartReply {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.witnessLocked(rec)
}

// witnessLocked is witness with wmu held.
func (s *leadStarter) witnessLocked(rec confirmRecord) leadStartReply {
	if s.witnessed == nil {
		s.witnessed = map[string]confirmRecord{}
	}
	s.witnessed[rec.Digest] = rec
	detail := "witnessed by the lead (pid " + fmt.Sprint(s.self) + "); it stands for " + ConfirmTTL.String()
	if _, err := writeConfirmLog(s.root, rec); err != nil {
		detail += "; the log line was not written: " + err.Error()
	}
	return leadStartReply{Outcome: "witnessed", Digest: rec.Digest, Detail: detail}
}

// witnessFor is the start's check: a confirmation of exactly digest that
// this lead witnessed, younger than ConfirmTTL.
func (s *leadStarter) witnessFor(digest string) (confirmRecord, error) {
	s.wmu.Lock()
	rec, ok := s.witnessed[digest]
	s.wmu.Unlock()
	switch {
	case !ok:
		return rec, fmt.Errorf("%s: this lead witnessed no confirmation of %s (a lead restart forgets them); %s", ReasonConfirmMissing, digest, doorsText(digest))
	case confirmNow().Sub(rec.At) > ConfirmTTL:
		return rec, fmt.Errorf("%s: the confirmation of %s was witnessed at %s, older than %s; %s", ReasonConfirmStale, digest, rec.At.UTC().Format(time.RFC3339), ConfirmTTL, doorsText(digest))
	}
	return rec, nil
}

// witnessTUI is the TUI door: accepted only from the pi process the lead
// spawned, its pid and the start stamp read at the spawn (the model's
// commands are pi's children, never pi itself; a reused pid carries another
// stamp).
func (s *leadStarter) witnessTUI(pid int, req leadStartRequest) leadStartReply {
	if _, err := digestHex(req.Digest); err != nil {
		return refusedBy(err)
	}
	s.wmu.Lock()
	piPID, piStamp := s.piPID, s.piStamp
	s.wmu.Unlock()
	if piPID <= 1 || pid != piPID {
		return leadStartReply{Outcome: "refused", Refusal: ReasonPeerNotLeadPi, Detail: fmt.Sprintf("pid %d is not the pi process this lead spawned (pid %d); /squad confirm is typed in the lead's pane", pid, piPID)}
	}
	if now, ok := confirmStamp(pid); !ok || now != piStamp {
		return leadStartReply{Outcome: "refused", Refusal: ReasonPeerNotLeadPi, Detail: fmt.Sprintf("pid %d does not carry the start stamp the lead read when it spawned pi", pid)}
	}
	return s.witness(confirmRecord{Digest: strings.TrimSpace(req.Digest), Door: DoorTUI, At: confirmNow().UTC(), By: "the lead's pi, pane " + firstNonEmptyStr(req.Pane, "-"),
		Proof: confirmProof{PID: pid, Stamp: piStamp, Pane: req.Pane}})
}

// witnessBuzz is the Buzz door: the attachment fetched the owner's event on
// its own connection and hands the lead the raw bytes; the lead holds no key
// and opens no connection (the plan reviewer: it signs in with nothing,
// ever). It verifies the bytes itself: the id recomputed from them and
// matched, the signature under the pinned owner key, the DM kind, the pinned
// channel, the routing window, `yes <prefix>` against its own recent
// proposals, once-only per event id; only then is the confirmation
// witnessed, with the event id it verified.
func (s *leadStarter) witnessBuzz(req leadStartRequest) leadStartReply {
	refusedAs := func(name, format string, args ...any) leadStartReply {
		return leadStartReply{Outcome: "refused", Refusal: name, Detail: fmt.Sprintf(format, args...)}
	}
	_, pin, err := gateverify.LoadBinding(gateBindingPath())
	if err != nil {
		return refusedAs(ReasonEventUnverified, "the owner's gate binding (the pinned key and channel): %v", err)
	}
	e, err := nostr.ParseEvent(req.Event)
	now := confirmNow()
	switch {
	case err != nil:
		return refusedAs(ReasonEventUnverified, "the bytes are not an event: %v", err)
	case !e.CheckID():
		return refusedAs(gateverify.RejectBadID, "the event's id does not recompute from its bytes")
	case e.PubKey != pin.Owner:
		return refusedAs(gateverify.RejectWrongPubkey, "event %s is not signed by the owner's pinned key", e.ID[:12])
	case e.VerifySignature() != nil:
		return refusedAs(gateverify.RejectBadSignature, "event %s", e.ID[:12])
	case e.Kind != buzz.KindDM:
		return refusedAs(gateverify.RejectWrongKind, "event %s is kind %d", e.ID[:12], e.Kind)
	case !slices.Equal(nostr.TagValues(e, "h"), []string{pin.Channel}):
		return refusedAs(gateverify.RejectWrongChannel, "event %s is not in the owner's DM channel", e.ID[:12])
	case e.CreatedAt < now.Add(-buzz.RouteWindow).Unix() || e.CreatedAt > now.Add(buzz.FutureSkew).Unix():
		return refusedAs(gateverify.RejectWindow, "event %s was created at %d", e.ID[:12], e.CreatedAt)
	}
	prefix, ok := gateverify.ParseTeamYes(e.Content)
	if !ok {
		return refusedAs(ReasonEventUnverified, "event %s is not yes <16 hex>", e.ID[:12])
	}
	// once-only, restart-proof and atomic: the check and the mark under one
	// lock; the event ids this lead or an earlier one witnessed are in the
	// lead's log, read here to refuse a replay and for nothing else
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.seen[e.ID] || eventLogged(s.root, e.ID) {
		return refusedAs(gatestore.RejectReplayed, "event %s was witnessed already", e.ID[:12])
	}
	prop, err := proposalByPrefix(s.root, prefix, now)
	if err != nil {
		return refusedBy(err)
	}
	if err := logEvent(s.root, e.ID, prop.Digest, now); err != nil {
		return refusedAs("event_unrecorded", "event %s could not be logged as witnessed, so it is not witnessed: %v", e.ID[:12], err)
	}
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	s.seen[e.ID] = true
	return s.witnessLocked(confirmRecord{Digest: prop.Digest, Door: DoorBuzz, At: now.UTC(), By: "owner key " + e.PubKey, Proof: confirmProof{EventID: e.ID}})
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
// read; at each hop the child's parent is read again after the parent's
// stamp (a reparent mid-hop breaks it); at the end every stamp is read again
// (a reused pid never carries its old stamp again, so the end's read sees
// any reuse during the walk). Any change is caller_unproved; a chain that ends at
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
	rec, err := s.witnessFor(strings.TrimSpace(req.Digest))
	if err != nil {
		r := refusedBy(err)
		r.Steps = s.steps
		return r
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
	// R53 (Codex on #837): the team lead's identity is resolved from the
	// confirmed squad before anything starts, so a started team always
	// carries it in the reply and the lead's pi always switches; an
	// identity that cannot be resolved refuses here, with nothing started.
	id, err := piruntime.NewTeamIdentity(sq.Lead, filepath.Join(s.project, ".agent-mail"), sq.Session)
	if err != nil {
		return refused(ReasonTeamIdentity, "the team lead's identity cannot be resolved from the confirmed squad (%v); nothing was started", err)
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
		// R53 (brief 31-3 PR 2): the owner's yes switches this lead to team
		// phase on this reply (the team identity and the digest), never on
		// a file. The marker is a record only: a failed write is a
		// phase_record_failed decision and changes nothing else.
		detail := l.outcome
		team := s.teamOf(id, req.Digest)
		if err := writePhaseMarker(s.root, s.project, team, s.self); err != nil {
			detail += "; the phase record was not written (" + KindPhaseRecordFailed + "): " + err.Error()
			_ = schedproc.AppendDecision(s.root, confirmNow(), scheduler.Decision{Kind: KindPhaseRecordFailed, Reason: phaseMarkerPath(s.root) + ": " + err.Error()})
		}
		return leadStartReply{Outcome: "started", Detail: detail, Steps: s.steps, Roster: l.roster, Team: team}
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
			lock, err := UpLeadLock(s.project, root)
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

// ReasonUnderSoloLead: a team-starting verb (up, lead --team, scheduler
// run) run under a lead, by whatever name the binary carries.
const ReasonUnderSoloLead = "under_solo_lead"

// Seams of the under-a-lead check: this process, an ancestor's argv and its
// initial environment.
var (
	underLeadSelf   = os.Getpid
	ancestorArgv    = procinfo.Argv
	ancestorEnviron = procinfo.Environ
)

// amqIdentityEnv reports whether an environment carries AMQ's identity: a
// non-empty AM_ME, or the solo lead's start socket path (what the lead
// gives its pi).
func amqIdentityEnv(env []string) bool {
	for _, kv := range env {
		for _, name := range []string{"AM_ME=", leadStartSocketEnv + "="} {
			if v, ok := strings.CutPrefix(kv, name); ok && strings.TrimSpace(v) != "" {
				return true
			}
		}
	}
	return false
}

// leadArgv reports whether argv is an amq-squad lead: bare amq-squad, or
// amq-squad lead with or without --team.
func leadArgv(argv []string) bool {
	return len(argv) > 0 && filepath.Base(argv[0]) == "amq-squad" && (len(argv) == 1 || argv[1] == "lead")
}

// piRuntimeDir is the user's unpacked pi runtimes, ~/.amq-squad/runtime (a
// test seam).
var piRuntimeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".amq-squad", "runtime")
}

// piArgv reports whether argv is a pi process by its executable alone
// (Codex's narrow pass on #834: no substring of any argument decides):
// argv[0]'s base name is exactly pi, or argv[0] is an absolute path inside
// the user's own pi runtimes (the runtime's node, which PiArgv puts first).
// Whether that pi refuses is decided by AMQ's identity in its environment
// (amqIdentityEnv), never by its argv.
func piArgv(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	exe := filepath.Clean(argv[0])
	if filepath.Base(exe) == "pi" {
		return true
	}
	dir := piRuntimeDir()
	return dir != "" && filepath.IsAbs(exe) && strings.HasPrefix(exe, dir+string(filepath.Separator))
}

// underSoloLead is the in-binary refusal of the renamed-binary residual
// (Codex's third pass on #834: `cp "$AMQ_SQUAD_BIN" /tmp/squadctl;
// /tmp/squadctl up` passes both string layers), by the plan reviewer's
// rule. verb refuses under_solo_lead when its parent chain (read from the
// kernel, each pid with its start stamp, re-read at the end) holds a pi
// process carrying AMQ's identity (AM_ME or the lead's start socket in its
// environment: the solo lead's pi, under which the model's commands run;
// the owner's own pi editor carries neither), and,
// with leadRefuses, an amq-squad lead too, naming the ancestor; with
// checkEnv, also when AM_ME is in its environment. up and lead --team pass
// leadRefuses (neither is ever legitimately a lead's child: the confirmed
// start runs the shared functions in-process, not the up verb); scheduler
// run does not (lead --team starts it). An orphan (setsid, reparented to
// pid 1, env -u AM_ME) has no such ancestor and evades it; only the 3.2
// sandbox closes that.
func underSoloLead(verb string, leadRefuses, checkEnv bool) error {
	if v := strings.TrimSpace(os.Getenv("AM_ME")); checkEnv && v != "" {
		return fmt.Errorf("%s: %s runs with AM_ME=%q in its environment (a seat's, or the lead's pi and its commands); the owner starts a team from their own shell, a lead on the owner's confirmation", ReasonUnderSoloLead, verb, v)
	}
	chain, err := callerChain(underLeadSelf(), 1)
	if err != nil {
		return fmt.Errorf("%s: %s cannot read its parent chain: %v", ReasonUnderSoloLead, verb, err)
	}
	for _, hop := range chain[1:] {
		if hop.PID <= 1 {
			continue
		}
		argv, ok := ancestorArgv(hop.PID)
		if !ok {
			continue
		}
		what := ""
		switch {
		case piArgv(argv) && func() bool { env, ok := ancestorEnviron(hop.PID); return ok && amqIdentityEnv(env) }():
			// a pi carrying AMQ's identity: the owner's own pi editor is not
			// the lead's pi
			what = "the lead's pi"
		case leadRefuses && leadArgv(argv):
			what = "a lead"
		}
		if what != "" {
			return fmt.Errorf("%s: %s runs under pid %d (%s), %s; the owner starts a team from their own shell, a lead on the owner's confirmation", ReasonUnderSoloLead, verb, hop.PID, strings.Join(argv[:min(2, len(argv))], " "), what)
		}
	}
	return restamp(chain)
}

// leadTeam is the team lead's identity as lead --team resolves it from
// squad.yaml (resolveTeamLeadIdentity), with the confirmed digest.
type leadTeam struct {
	Root     string `json:"root"`
	BaseRoot string `json:"base_root"`
	Session  string `json:"session"`
	Handle   string `json:"handle"`
	Digest   string `json:"digest"`
}

// teamOf records the team lead's identity (resolved before the start) as
// this lead's phase (the answer to a later phase query).
func (s *leadStarter) teamOf(id piruntime.TeamIdentity, digest string) *leadTeam {
	t := &leadTeam{Root: id.Root, BaseRoot: id.BaseRoot, Session: id.Session, Handle: id.Handle, Digest: digest}
	s.wmu.Lock()
	s.team = t
	s.wmu.Unlock()
	return t
}

// ReasonTeamIdentity refuses a start whose team lead identity cannot be
// resolved from the confirmed squad (nothing is started).
const ReasonTeamIdentity = "team_identity"

// KindPhaseRecordFailed is the decision of a phase record (the marker) the
// lead could not write after its team started: a record failure and
// nothing else (the switch rode the start's reply).
const KindPhaseRecordFailed = "phase_record_failed"

// The phase marker (brief 31-3 PR 2, R53, after the plan reviewer: the
// marker is never authority). The solo lead's process writes it in its own
// AMQ root (<root>/extensions/amq-squad/phase.json) when the start of a
// confirmed team succeeds, for the record only: the lead's pi switches on
// the lead's reply over its start socket, and a solo lead restarted in a
// marked repository comes up solo and offers the two ways back (the owner's
// amq-squad up, or a fresh yes at either door). Nobody reads it to grant
// anything: under one uid a model with write could plant it.
const phaseMarkerName = "phase.json"

// PhaseTeam is the one phase a marker records.
const PhaseTeam = "team"

type phaseMarker struct {
	Schema   int       `json:"schema"`
	Phase    string    `json:"phase"`
	Project  string    `json:"project"`
	Root     string    `json:"root"`
	BaseRoot string    `json:"base_root"`
	Session  string    `json:"session"`
	Handle   string    `json:"handle"`
	Digest   string    `json:"digest"`
	At       time.Time `json:"at"`
	PID      int       `json:"pid"`
	Start    string    `json:"start"`
}

func phaseMarkerPath(soloRoot string) string {
	return filepath.Join(soloRoot, "extensions", "amq-squad", phaseMarkerName)
}

// writePhaseMarker writes the record of a team started from project's solo
// lead.
func writePhaseMarker(soloRoot, project string, t *leadTeam, pid int) error {
	start, _ := confirmStamp(pid)
	m := phaseMarker{Schema: 1, Phase: PhaseTeam, Project: project, Root: t.Root, BaseRoot: t.BaseRoot, Session: t.Session, Handle: t.Handle,
		Digest: t.Digest, At: confirmNow().UTC(), PID: pid, Start: start}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(phaseMarkerPath(soloRoot), append(b, '\n'), 0o600)
}

// markerInfo reads the marker as information only (a regular file, not
// followed, for this project): what the restarted lead offers the owner.
func markerInfo(soloRoot, project string) (phaseMarker, bool) {
	var m phaseMarker
	raw, err := readDiscoveryRecord(phaseMarkerPath(soloRoot))
	if err != nil || json.Unmarshal(raw, &m) != nil {
		return m, false
	}
	return m, m.Schema == 1 && m.Phase == PhaseTeam && m.Project == project
}

// resumeOffer is what a solo lead restarted in a marked repository says:
// the team it started, and the two ways back, the owner's up (the resume by
// the act) or a fresh yes at either door (the witness path).
func resumeOffer(m phaseMarker) string {
	return fmt.Sprintf("this session's team was started on %s with digest %s; run `amq-squad up` to resume it, or confirm it again at your pane (/squad confirm) or over Buzz", m.At.UTC().Format(time.RFC3339), m.Digest)
}
