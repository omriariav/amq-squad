package cli

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// R55's confirmation (brief 31-3, sections 2, 3b, 3c, after the plan
// reviewer's rulings on Codex's findings on #834): no file is authority
// anywhere. The verifier is the solo lead's own process, the one that serves
// the start socket, takes the lead lock and supervises the scheduler. It
// witnesses each confirmation live at one of two doors, checking the door's
// proof itself, and keeps what it witnessed in memory, keyed by digest, for
// 30 minutes from the witness; the start accepts only a witnessed, unexpired
// confirmation of the exact digest. A lead restart forgets them all and the
// owner confirms again.
//
//	tui:   `/squad confirm <prefix>`: the extension sends the digest and its
//	       pane over the start socket; the lead accepts it only from the pi
//	       process it spawned (its pid and the start stamp read at spawn).
//	buzz:  the attachment fetches the owner's signed `yes <prefix>` on its
//	       own connection and hands the lead the raw event bytes; the lead,
//	       which holds no key and opens no connection, verifies them itself
//	       (the id recomputed, the pinned owner's signature, kind, channel,
//	       window, once-only, the prefix).
//
// The shell door (`amq-squad team confirm`) was dropped: under one uid, no
// kernel read tells the owner's shell from a tree the model built (a helper
// can setsid, open its own pty and orphan itself); a shell door needs a key
// or a different uid. With no lead holding the root, the owner's own `team
// apply --digest D` is the confirmation by the act (team.go).
//
// The directory <root>/extensions/amq-squad/confirm/ is the lead's log of
// what it witnessed (atomicfile, 0600): the door and what the lead read,
// never what a claimant asserted. Nothing reads it to grant anything; the
// Buzz door reads its event ids to refuse a replay. The proposal `team
// propose --from PLAN` records under confirm/proposed/ is an index from a
// prefix to a digest and the squad.yaml to plan again, never a confirmation.

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
	ReasonShellDoorRemoved = "shell_door_removed"
	ReasonLeadUnreachable  = "lead_unreachable"
	ReasonApplyBusy        = "apply_busy"
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

// confirmProof is what the lead itself read at the door: the pi process
// and the pane (tui), the event id it verified (buzz).
type confirmProof struct {
	Pane    string `json:"pane,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Stamp   string `json:"stamp,omitempty"`
	EventID string `json:"event_id,omitempty"`
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

// doorsText names the two doors for a refusal.
func doorsText(digest string) string {
	prefix := team.DigestPrefix(digest)
	return fmt.Sprintf("confirm it at one of the two doors: (1) type /squad confirm %s in the lead's pane; (2) send the signed Buzz DM \"yes %s\" (digest %s)", prefix, prefix, digest)
}

// eventsDir is where the lead logs the Buzz event ids it witnessed.
func eventsDir(root string) string { return filepath.Join(confirmDir(root), "events") }

// eventLogged reports whether the lead's log names a witnessed event id
// (read for refusal only: a replay is refused, nothing is granted).
func eventLogged(root, id string) bool {
	_, err := os.Lstat(filepath.Join(eventsDir(root), id+".json"))
	return err == nil
}

// logEvent records a witnessed Buzz event id in the lead's log.
func logEvent(root, id, digest string, at time.Time) error {
	b, _ := json.Marshal(map[string]string{"event_id": id, "digest": digest, "at": at.UTC().Format(time.RFC3339Nano)})
	if err := os.MkdirAll(eventsDir(root), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(eventsDir(root), id+".json"), append(b, '\n'), 0o600)
}

// noLeadHolds is the shell apply's check (the plan reviewer's ruling: no
// kernel read tells the owner's shell from a tree the model built, so the
// fact that matters is whether a model runs at all). It fails closed: a
// solo lead's discovery record that exists is lead_running when its socket
// answers and lead_unreachable on any other outcome (unreadable, malformed,
// a connect that fails); a team lead holding any session's lead lock under
// the repository's .agent-mail is lead_running naming that session. "No
// lead" is only no discovery record and no held lead lock at all.
func noLeadHolds(project, digest string) error {
	root, err := soloRootOf(project)
	if err != nil {
		return err
	}
	rec := filepath.Join(root, "extensions", "amq-squad", leadStartRecordName)
	switch _, err := os.Lstat(rec); {
	case err == nil:
		c, derr := dialLead(root)
		if derr != nil {
			return fmt.Errorf("%s: %s names a solo lead of %s whose socket cannot be reached (%v); if no lead runs, remove %s and apply again; %s", ReasonLeadUnreachable, rec, project, derr, rec, doorsText(digest))
		}
		c.Close()
		return fmt.Errorf("%s: the solo lead of %s is running (its start socket answers), so the apply is the lead's to make on a confirmation it witnessed; %s", schedproc.ReasonLeadRunning, project, doorsText(digest))
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("%s: %s cannot be read (%v); %s", ReasonLeadUnreachable, rec, err, doorsText(digest))
	}
	locks, err := filepath.Glob(filepath.Join(project, ".agent-mail", "*", "extensions", "amq-squad", "scheduler", schedproc.LeadLockName))
	if err != nil {
		return err
	}
	sort.Strings(locks)
	for _, lock := range locks {
		held, err := lockHeld(lock)
		if err != nil {
			return err
		}
		if held {
			session := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(lock)))))
			return fmt.Errorf("%s: a team lead holds session %s (%s); %s", schedproc.ReasonLeadRunning, session, lock, doorsText(digest))
		}
	}
	return nil
}

// applyLockWait is how long the apply lock is tried before a refusal (a
// test seam).
var applyLockWait = 3 * time.Second

// applyAfterProbe runs between the shell apply's probe and its write (a test
// hook: the window a lead could start in).
var applyAfterProbe func()

// takeApplyLock takes the repository's apply lock, under the solo root
// (<root>/extensions/amq-squad/apply.lock): the shell apply holds it from
// its probe through its write, and the lead lock step (UpLeadLock) takes it
// around the lead lock, so no lead starts between the probe and the write.
// Held past applyLockWait it is apply_busy.
func takeApplyLock(project string) (func(), error) {
	root, err := soloRootOf(project)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, "extensions", "amq-squad", "apply.lock")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	for deadline := time.Now().Add(applyLockWait); ; time.Sleep(20 * time.Millisecond) {
		l, ok, err := flock.TryExclusive(path, true)
		switch {
		case errors.Is(err, flock.ErrUnsupported):
			return nil, lockUnsupported(path)
		case err != nil:
			return nil, err
		case ok:
			return func() { _ = l.Close() }, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("%s: %s is held (a lead starting, or another apply); try again", ReasonApplyBusy, path)
		}
	}
}

// withNoLead runs a shell apply's write under the apply lock, after the
// probe that no lead holds the repository.
func withNoLead(project, digest string, write func() error) error {
	unlock, err := takeApplyLock(project)
	if err != nil {
		return err
	}
	defer unlock()
	if err := noLeadHolds(project, digest); err != nil {
		return err
	}
	if applyAfterProbe != nil {
		applyAfterProbe()
	}
	return write()
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

// confirmParent reads a pid's parent from the kernel (a test seam; the
// start socket's caller chain walks it).
var confirmParent = procinfo.ParentPID

// leadStartRecord names the solo lead's start socket for the processes that
// are not its descendants (the Buzz attachment, a shell apply checking
// whether a lead runs). It is no
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
// mailbox handler, after the handler fetched the event on its own connection
// and verified the owner's signature): it finds the solo root whose recent
// proposal the prefix names and hands that root's lead the raw event bytes;
// the lead verifies them itself, with no connection and no key, and
// witnesses the confirmation. It returns the confirmed digest.
func confirmByPrefix(prefix string, raw []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	e, err := nostr.ParseEvent(raw)
	if err != nil {
		return "", fmt.Errorf("%s: the bytes are not an event: %v", ReasonEventUnverified, err)
	}
	at := time.Unix(e.CreatedAt, 0) // which proposal to route to; the lead verifies everything
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
	reply, err := askLead(c, leadStartRequest{Kind: confirmBuzzKind, Event: raw})
	if err != nil {
		return "", err
	}
	if reply.Outcome != "witnessed" {
		return "", errors.New(reply.Refusal + ": " + reply.Detail)
	}
	return reply.Digest, nil
}
