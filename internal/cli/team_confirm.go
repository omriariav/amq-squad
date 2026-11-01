package cli

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// R55's confirmation (brief 31-3, sections 2, 3b, 3c, after the plan
// reviewer's ruling on Codex's P1 on #834): no file is authority anywhere.
// The verifier is the solo lead's own process, the one that serves the start
// socket, takes the lead lock and supervises the scheduler. It witnesses
// each confirmation live at one of three doors, checking the door's proof
// itself, and keeps what it witnessed in memory, keyed by digest, for 30
// minutes from the witness; the start accepts only a witnessed, unexpired
// confirmation of the exact digest. A lead restart forgets them all and the
// owner confirms again.
//
//	tui:   `/squad confirm <prefix>`: the extension sends the digest and its
//	       pane over the start socket; the lead accepts it only from the pi
//	       process it spawned (its pid and the start stamp read at spawn).
//	shell: `amq-squad team confirm D` connects to the start socket; the lead
//	       reads the peer from the kernel: not a descendant of the lead or a
//	       seat, a controlling terminal, its chain through its session
//	       leader, every pid with its start stamp.
//	buzz:  the attachment hands the lead the event id of the owner's signed
//	       `yes <prefix>`; the lead fetches the event by that id itself and
//	       runs the typed path's checks.
//
// The directory <root>/extensions/amq-squad/confirm/ is the lead's log of
// what it witnessed (atomicfile, 0600): the door and what the lead read,
// never what a claimant asserted. Nothing reads it for authority. The
// proposal `team propose --from PLAN` records under confirm/proposed/ is an
// index from a prefix to a digest and the squad.yaml to plan again, never a
// confirmation.

// ConfirmTTL is how long a witnessed confirmation stands.
const ConfirmTTL = 30 * time.Minute

// The doors.
const (
	DoorTUI   = "tui"
	DoorShell = "shell"
	DoorBuzz  = "buzz"
)

// Refusals of the confirmation.
const (
	ReasonConfirmMissing   = "confirmation_missing"
	ReasonConfirmStale     = "confirmation_stale"
	ReasonLeadDescendant   = "lead_descendant"
	ReasonAncestryUnknown  = "ancestry_unknown"
	ReasonNoTerminal       = "no_terminal"
	ReasonNoProposal       = "no_proposal"
	ReasonProposalAmbig    = "proposal_ambiguous"
	ReasonDigestMalformed  = "digest_malformed"
	ReasonNoLead           = "no_lead"
	ReasonPeerNotLeadPi    = "peer_not_lead_pi"
	ReasonEventUnverified  = "event_unverified"
	confirmDirName         = "confirm"
	proposedDirName        = "proposed"
	leadStartRecordName    = "lead-start.json"
	confirmMaxHops         = 64
	confirmRecordSchema    = 1
	confirmProposalSchema  = 1
	confirmProposalMaxSize = 1 << 20
)

// confirmHop is one process of a chain the lead read: its pid, its start
// stamp and its argv[0]'s base name (never the full argv, which can hold a
// secret).
type confirmHop struct {
	PID   int    `json:"pid"`
	Stamp string `json:"stamp,omitempty"`
	Name  string `json:"name"`
}

// confirmProof is what the lead itself read at the door: the pi process
// and the pane (tui), the peer's chain (shell), the event id it verified
// (buzz).
type confirmProof struct {
	Pane     string       `json:"pane,omitempty"`
	PID      int          `json:"pid,omitempty"`
	Stamp    string       `json:"stamp,omitempty"`
	PIDChain []confirmHop `json:"pid_chain,omitempty"`
	EventID  string       `json:"event_id,omitempty"`
}

// confirmRecord is one witnessed confirmation: in the lead's memory, and a
// line of its log.
type confirmRecord struct {
	Schema int          `json:"schema"`
	Digest string       `json:"digest"`
	Door   string       `json:"door"`
	At     time.Time    `json:"at"`
	By     string       `json:"by"`
	Proof  confirmProof `json:"proof"`
}

// teamProposal is what `team propose --from PLAN` leaves under the solo
// root: an index from the prefix to the digest (the Buzz door) and the
// squad.yaml text the start plans again.
type teamProposal struct {
	Schema    int       `json:"schema"`
	Digest    string    `json:"digest"`
	Project   string    `json:"project"`
	SquadYAML string    `json:"squad_yaml"`
	Plan      []string  `json:"plan"`
	At        time.Time `json:"at"`
}

var digestTokenRE = regexp.MustCompile(`^sha256:([0-9a-f]{64})@([0-9a-f]{64}|absent)$`)

// digestHex is the content hex of an apply token, the log's file stem.
func digestHex(digest string) (string, error) {
	m := digestTokenRE.FindStringSubmatch(strings.TrimSpace(digest))
	if m == nil {
		return "", fmt.Errorf("%s: %q is not a team propose digest (sha256:<64 hex>@<64 hex or absent>)", ReasonDigestMalformed, digest)
	}
	return m[1], nil
}

// soloRootOf is the solo lead's AMQ root for a repository.
var soloRootOf = func(project string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir, _ := soloHome(home, project)
	return filepath.Join(dir, "root"), nil
}

func confirmDir(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", confirmDirName)
}

func confirmPath(root, hexDigest string) string {
	return filepath.Join(confirmDir(root), hexDigest+".json")
}

func proposalPath(root, hexDigest string) string {
	return filepath.Join(confirmDir(root), proposedDirName, hexDigest+".json")
}

// confirmNow is the clock of the witness (a test seam).
var confirmNow = time.Now

// writeConfirmLog writes the lead's log line of a witnessed confirmation
// (atomicfile, 0600). Nothing reads it for authority.
func writeConfirmLog(root string, rec confirmRecord) (string, error) {
	h, err := digestHex(rec.Digest)
	if err != nil {
		return "", err
	}
	rec.Schema = confirmRecordSchema
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(confirmDir(root), 0o700); err != nil {
		return "", err
	}
	p := confirmPath(root, h)
	return p, atomicfile.Write(p, append(b, '\n'), 0o600)
}

// doorsText names the three doors for a refusal.
func doorsText(digest, project string) string {
	prefix := team.DigestPrefix(digest)
	return fmt.Sprintf("confirm it at one of the three doors: (1) type /squad confirm %s in the lead's pane; (2) run amq-squad team confirm %s --project %s from your own shell; (3) send the signed Buzz DM \"yes %s\"", prefix, digest, project, prefix)
}

// writeProposal records a proposal (the one write of `team propose --from
// PLAN`).
func writeProposal(root string, p teamProposal) error {
	h, err := digestHex(p.Digest)
	if err != nil {
		return err
	}
	p.Schema = confirmProposalSchema
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(proposalPath(root, h)), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(proposalPath(root, h), append(b, '\n'), 0o600)
}

// readProposal reads the proposal of digest under root.
func readProposal(root, digest string) (teamProposal, error) {
	var p teamProposal
	h, err := digestHex(digest)
	if err != nil {
		return p, err
	}
	raw, err := os.ReadFile(proposalPath(root, h))
	if err != nil {
		return p, fmt.Errorf("%s: no proposal of %s under %s (run team propose --from PLAN): %w", ReasonNoProposal, digest, confirmDir(root), err)
	}
	if len(raw) > confirmProposalMaxSize || json.Unmarshal(raw, &p) != nil || p.Schema != confirmProposalSchema || p.Digest != strings.TrimSpace(digest) {
		return p, fmt.Errorf("%s: the proposal of %s is unreadable", ReasonNoProposal, digest)
	}
	return p, nil
}

// proposalByPrefix is the one proposal under root whose digest starts with
// prefix and that is younger than ConfirmTTL at at.
func proposalByPrefix(root, prefix string, at time.Time) (teamProposal, error) {
	if _, err := hex.DecodeString(prefix); err != nil || len(prefix) != 16 {
		return teamProposal{}, fmt.Errorf("%s: %q is not a 16-hex prefix", ReasonNoProposal, prefix)
	}
	matches, _ := filepath.Glob(filepath.Join(confirmDir(root), proposedDirName, prefix+"*.json"))
	sort.Strings(matches)
	var hits []teamProposal
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil || len(raw) > confirmProposalMaxSize {
			continue
		}
		var p teamProposal
		if json.Unmarshal(raw, &p) != nil || team.DigestPrefix(p.Digest) != prefix || at.Sub(p.At) > ConfirmTTL {
			continue
		}
		hits = append(hits, p)
	}
	switch len(hits) {
	case 0:
		return teamProposal{}, fmt.Errorf("%s: no proposal younger than %s starts with %s", ReasonNoProposal, ConfirmTTL, prefix)
	case 1:
		return hits[0], nil
	}
	return teamProposal{}, fmt.Errorf("%s: %d proposals start with %s; confirm at the lead's pane or the shell door with the full digest", ReasonProposalAmbig, len(hits), prefix)
}

// Seams of the kernel reads the lead (the shell door) and the owner's apply
// make: this process, the parent chain, argv, initial environment, session
// leader and controlling terminal of any pid (the start stamp is
// confirmStamp).
var (
	confirmSelf    = os.Getpid
	confirmParent  = procinfo.ParentPID
	confirmArgv    = procinfo.Argv
	confirmEnviron = procinfo.Environ
	confirmSession = procinfo.SessionID
	confirmHasTTY  = procinfo.HasTTY
)

// leadOrSeatArgv reports whether argv is an amq-squad lead or a seat
// process: amq-squad bare (the solo lead), lead, seat-exec, pi (a pi seat
// or the lead's pi), or scheduler.
func leadOrSeatArgv(argv []string) bool {
	if len(argv) == 0 || filepath.Base(argv[0]) != "amq-squad" {
		return false
	}
	return len(argv) == 1 || slices.Contains([]string{"lead", "seat-exec", "pi", "scheduler"}, argv[1])
}

// amqIdentityEnv reports whether an environment carries an AMQ seat
// identity (AM_ME): seat-exec sets it before it execs a provider, and the
// lead's pi runs with it.
func amqIdentityEnv(env []string) bool {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "AM_ME="); ok && strings.TrimSpace(v) != "" {
			return true
		}
	}
	return false
}

// ownerAncestry is the kernel-level check of a process that claims to be
// the owner's (3b (i)), read entirely from the kernel's records (procinfo):
// pid has a controlling terminal (its own, read for it, never the reader's);
// its parent chain, walked to pid 1, passes through its session leader (a
// process orphaned out of its shell does not), never passes lead (the solo
// lead's pid, 0 for none), and holds no amq-squad lead or seat process (by
// argv) and no process carrying an AMQ identity (by its initial
// environment). Every pid's start stamp is read when the pid is first read,
// the child's parent and stamp again after each hop, and every stamp again
// at the end (caller_unproved on a change: a reused pid). An ancestor whose
// argv cannot be read (another user's: login, sshd) is passed over; the lead
// and its seats are the owner's own processes, always readable. It returns
// the chain as read, pid first.
func ownerAncestry(pid, lead int) ([]confirmHop, error) {
	first, ok := confirmStamp(pid)
	if !ok {
		return nil, fmt.Errorf("%s: the start stamp of pid %d cannot be read", ReasonAncestryUnknown, pid)
	}
	switch has, ok := confirmHasTTY(pid); {
	case !ok:
		return nil, fmt.Errorf("%s: the terminal of pid %d cannot be read", ReasonAncestryUnknown, pid)
	case !has:
		return nil, fmt.Errorf("%s: pid %d has no controlling terminal; confirm from your own shell", ReasonNoTerminal, pid)
	}
	sid, ok := confirmSession(pid)
	if !ok {
		return nil, fmt.Errorf("%s: the session of pid %d cannot be read", ReasonAncestryUnknown, pid)
	}
	name := "?"
	if argv, ok := confirmArgv(pid); ok {
		name = filepath.Base(argv[0])
	}
	chain := []confirmHop{{PID: pid, Stamp: first, Name: name}}
	leader := sid == pid
	for p, hops := pid, 0; ; hops++ {
		if hops >= confirmMaxHops {
			return nil, fmt.Errorf("%s: the parent chain of pid %d is longer than %d hops", ReasonAncestryUnknown, pid, confirmMaxHops)
		}
		parent, ok := confirmParent(p)
		if !ok {
			return nil, fmt.Errorf("%s: the parent of pid %d cannot be read", ReasonAncestryUnknown, p)
		}
		if parent <= 1 {
			break
		}
		stamp, ok := confirmStamp(parent)
		if !ok {
			return nil, fmt.Errorf("%s: the start stamp of pid %d cannot be read", ReasonAncestryUnknown, parent)
		}
		if again, ok := confirmParent(p); !ok || again != parent {
			return nil, fmt.Errorf("%s: pid %d was reparented while it was read", ReasonCallerUnproved, p)
		}
		if err := restampHops(chain[len(chain)-1:]); err != nil {
			return nil, err
		}
		hop := confirmHop{PID: parent, Stamp: stamp}
		if parent == lead {
			return nil, fmt.Errorf("%s: pid %d descends from the solo lead (pid %d); only the owner's own shell confirms (from the lead's pane type /squad confirm instead)", ReasonLeadDescendant, pid, lead)
		}
		if argv, ok := confirmArgv(parent); ok {
			hop.Name = filepath.Base(argv[0])
			if leadOrSeatArgv(argv) {
				return nil, fmt.Errorf("%s: pid %d (%s) is an amq-squad lead or seat process and an ancestor of pid %d; only the owner's own shell confirms", ReasonLeadDescendant, parent, strings.Join(argv[:min(2, len(argv))], " "), pid)
			}
		}
		if env, ok := confirmEnviron(parent); ok && amqIdentityEnv(env) {
			return nil, fmt.Errorf("%s: pid %d (%s) carries an AMQ seat identity (AM_ME) and is an ancestor of pid %d; only the owner's own shell confirms", ReasonLeadDescendant, parent, firstNonEmptyStr(hop.Name, "?"), pid)
		}
		chain = append(chain, hop)
		leader = leader || parent == sid
		p = parent
	}
	if !leader {
		return nil, fmt.Errorf("%s: the parent chain of pid %d does not reach its session leader (pid %d): it was orphaned out of the shell that ran it", ReasonAncestryUnknown, pid, sid)
	}
	return chain, restampHops(chain)
}

// restampHops is restamp over a chain the lead read.
func restampHops(chain []confirmHop) error {
	var s []stampedPID
	for _, h := range chain {
		s = append(s, stampedPID{h.PID, h.Stamp})
	}
	return restamp(s)
}

// leadStartRecord names the solo lead's start socket for the processes that
// are not its descendants (team confirm, the Buzz attachment). It is no
// authority: the dial checks the socket is private and the owner's, and the
// lead proves every peer itself.
type leadStartRecord struct {
	Schema int    `json:"schema"`
	Socket string `json:"socket"`
	PID    int    `json:"pid"`
}

// dialLead connects to the solo lead of root: no_lead when none serves it.
func dialLead(root string) (io.ReadWriteCloser, error) {
	raw, err := os.ReadFile(filepath.Join(root, "extensions", "amq-squad", leadStartRecordName))
	var rec leadStartRecord
	if err != nil || json.Unmarshal(raw, &rec) != nil || rec.Socket == "" {
		return nil, fmt.Errorf("%s: no solo lead serves %s", ReasonNoLead, root)
	}
	c, err := seatlaunch.DialPrivate(root, rec.Socket, leadStartSocketName)
	if err != nil {
		return nil, fmt.Errorf("%s: the solo lead's socket %s: %v", ReasonNoLead, rec.Socket, err)
	}
	c.SetDeadline(time.Now().Add(60 * time.Second))
	return c, nil
}

// askLead sends one request to a lead and reads its reply.
func askLead(c io.ReadWriter, req any) (leadStartReply, error) {
	var reply leadStartReply
	b, _ := json.Marshal(req)
	if _, err := c.Write(append(b, '\n')); err != nil {
		return reply, err
	}
	line, err := bufio.NewReader(io.LimitReader(c, 4<<20)).ReadBytes('\n')
	if err != nil {
		return reply, fmt.Errorf("%s: no reply from the lead: %w", ReasonNoLead, err)
	}
	return reply, json.Unmarshal(line, &reply)
}

const teamConfirmUsage = `amq-squad team confirm - the owner's yes to a team the lead proposed (R55, door 2)

Usage:
  amq-squad team confirm DIGEST [--project DIR] [--json]

Run it from your own shell while the solo lead runs: it asks the lead, which
reads this process from the kernel (no lead or seat ancestor, a controlling
terminal, the chain through its session leader) and witnesses the
confirmation for 30 minutes. With no lead running it refuses no_lead; apply
from your own shell instead (team apply --digest DIGEST). The other doors:
/squad confirm <prefix> in the lead's pane, and the signed Buzz DM
"yes <prefix>".
`

// runTeamConfirm is door (a): the lead witnesses it, the claimant asserts
// nothing.
func runTeamConfirm(args []string) error {
	fs := flag.NewFlagSet("team confirm", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, teamConfirmUsage) }
	project := fs.String("project", "", "the repository root (default: the repository of the working directory)")
	jsonOut := fs.Bool("json", false, "JSON output")
	var digest string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		digest, args = args[0], args[1:]
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if digest == "" && fs.NArg() == 1 {
		digest = fs.Arg(0)
	} else if fs.NArg() != 0 || digest == "" {
		return usageErrorf("team confirm takes one DIGEST")
	}
	digest = strings.TrimSpace(digest)
	if _, err := digestHex(digest); err != nil {
		return err
	}
	dir, err := teamProjectDir(*project)
	if err != nil {
		return err
	}
	root, err := soloRootOf(dir)
	if err != nil {
		return err
	}
	c, err := dialLead(root)
	if err != nil {
		return fmt.Errorf("%v; start the lead (amq-squad lead) and confirm again, or apply from your own shell: amq-squad team apply --digest %s --project %s", err, digest, dir)
	}
	defer c.Close()
	reply, err := askLead(c, leadStartRequest{Kind: confirmShellKind, Digest: digest, Project: dir})
	if err != nil {
		return err
	}
	if *jsonOut {
		if err := printJSONEnvelope("team_confirm", reply); err != nil {
			return err
		}
	} else if reply.Outcome == "witnessed" {
		fmt.Printf("confirmed %s (door shell, witnessed by the lead); it stands for %s\n", digest, ConfirmTTL)
	}
	if reply.Outcome != "witnessed" {
		return fmt.Errorf("%s: %s", reply.Refusal, reply.Detail)
	}
	return nil
}

// teamProjectDir is --project, else the repository of the working directory.
func teamProjectDir(project string) (string, error) {
	if strings.TrimSpace(project) == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		return projectTop(cwd), nil
	}
	return filepath.Abs(project)
}

// confirmByPrefix is the attachment's half of the Buzz door (wired into the
// mailbox handler, after the handler verified the owner's signature): it
// finds the solo root whose recent proposal the prefix names and hands that
// root's lead the event id only; the lead fetches and verifies the event
// itself and witnesses the confirmation. It returns the confirmed digest.
func confirmByPrefix(prefix, eventID, _ string, at time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	roots, _ := filepath.Glob(filepath.Join(home, ".amq-squad", "solo", "*", "root"))
	sort.Strings(roots)
	var found []string
	for _, root := range roots {
		if _, err := proposalByPrefix(root, prefix, at); err == nil {
			found = append(found, root)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("%s: no proposal younger than %s starts with %s", ReasonNoProposal, ConfirmTTL, prefix)
	case 1:
	default:
		return "", fmt.Errorf("%s: proposals in %d repositories start with %s", ReasonProposalAmbig, len(found), prefix)
	}
	c, err := dialLead(found[0])
	if err != nil {
		return "", err
	}
	defer c.Close()
	reply, err := askLead(c, leadStartRequest{Kind: confirmBuzzKind, EventID: eventID})
	if err != nil {
		return "", err
	}
	if reply.Outcome != "witnessed" {
		return "", errors.New(reply.Refusal + ": " + reply.Detail)
	}
	return reply.Digest, nil
}
