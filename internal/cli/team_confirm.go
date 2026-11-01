package cli

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// R55's confirmation record (brief 31-3, sections 2, 3b, 3c). The lead
// proposes a team as a squad.yaml digest; the owner says yes at one of three
// doors, and each door writes the same record:
//
//	<root>/extensions/amq-squad/confirm/<content hex>.json
//	{schema, digest, door, at, by, proof}
//
// where <root> is the solo lead's AMQ root for the repository (soloHome:
// ~/.amq-squad/solo/<dir>-<digest>/root, the lead's AM_ROOT in the solo
// phase), so every door and the apply find it from the repository alone.
// The doors:
//
//	tui:   `/squad confirm <prefix>` in the lead's pane; the extension's
//	       command handler (pi hands a slash command to the extension, never
//	       to the model) writes it; proof: the pane and the pid.
//	shell: `amq-squad team confirm D` from the owner's own shell; refused
//	       lead_descendant when an amq-squad lead or a seat process is an
//	       ancestor (the parent chain read from the kernel); proof: the pid
//	       chain.
//	buzz:  a signed Buzz DM `yes <16-hex prefix>` through the mailbox
//	       handler's typed path; proof: the event id.
//
// `team apply --digest D` refuses without a record for D that is younger
// than 30 minutes and carries its door's proof, naming the three doors.

// ConfirmTTL is how long a confirmation stands.
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
	ReasonConfirmUnproved  = "confirmation_unproved"
	ReasonLeadDescendant   = "lead_descendant"
	ReasonAncestryUnknown  = "ancestry_unknown"
	ReasonNoTerminal       = "no_terminal"
	ReasonNoProposal       = "no_proposal"
	ReasonProposalAmbig    = "proposal_ambiguous"
	ReasonDigestMalformed  = "digest_malformed"
	confirmDirName         = "confirm"
	proposedDirName        = "proposed"
	confirmFutureSkew      = time.Minute
	confirmMaxHops         = 64
	confirmRecordSchema    = 1
	confirmProposalSchema  = 1
	confirmProposalMaxSize = 1 << 20
)

// confirmHop is one ancestor in the shell door's proof: its pid and its
// argv[0]'s base name (never the full argv, which can hold a secret).
type confirmHop struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
}

// confirmProof is how the record was made (3c): the TUI door's pane and
// pid, the shell door's pid chain, the Buzz door's event id.
type confirmProof struct {
	Pane     string       `json:"pane,omitempty"`
	PID      int          `json:"pid,omitempty"`
	PIDChain []confirmHop `json:"pid_chain,omitempty"`
	EventID  string       `json:"event_id,omitempty"`
}

// confirmRecord is R55's record.
type confirmRecord struct {
	Schema int          `json:"schema"`
	Digest string       `json:"digest"`
	Door   string       `json:"door"`
	At     time.Time    `json:"at"`
	By     string       `json:"by"`
	Proof  confirmProof `json:"proof"`
}

// teamProposal is what `team propose --from PLAN` leaves beside the
// records: the proposal the Buzz door looks a prefix up in, and the
// squad.yaml text the apply plans again (nothing else is written).
type teamProposal struct {
	Schema    int       `json:"schema"`
	Digest    string    `json:"digest"`
	Project   string    `json:"project"`
	SquadYAML string    `json:"squad_yaml"`
	Plan      []string  `json:"plan"`
	At        time.Time `json:"at"`
}

var digestTokenRE = regexp.MustCompile(`^sha256:([0-9a-f]{64})@([0-9a-f]{64}|absent)$`)

// digestHex is the content hex of an apply token, the record's file stem.
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

// confirmNow is the clock of the doors and the apply (a test seam).
var confirmNow = time.Now

// writeConfirmRecord writes rec under root by temp name and rename.
func writeConfirmRecord(root string, rec confirmRecord) (string, error) {
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

// checkConfirmation is the apply's check: a record for digest, younger
// than ConfirmTTL, carrying its door's proof. It returns the record.
func checkConfirmation(root, digest, project string) (confirmRecord, error) {
	var rec confirmRecord
	h, err := digestHex(digest)
	if err != nil {
		return rec, err
	}
	raw, err := os.ReadFile(confirmPath(root, h))
	if errors.Is(err, os.ErrNotExist) {
		return rec, fmt.Errorf("%s: no confirmation of %s; %s", ReasonConfirmMissing, digest, doorsText(digest, project))
	}
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(raw, &rec); err != nil || rec.Schema != confirmRecordSchema || rec.Digest != strings.TrimSpace(digest) {
		return rec, fmt.Errorf("%s: the confirmation record of %s is unreadable or names another digest; %s", ReasonConfirmMissing, digest, doorsText(digest, project))
	}
	now := confirmNow()
	if age := now.Sub(rec.At); age > ConfirmTTL || age < -confirmFutureSkew {
		return rec, fmt.Errorf("%s: the confirmation of %s was made at %s, older than %s (or dated ahead); %s", ReasonConfirmStale, digest, rec.At.UTC().Format(time.RFC3339), ConfirmTTL, doorsText(digest, project))
	}
	if why := proofMissing(rec); why != "" {
		return rec, fmt.Errorf("%s: the %s door's record of %s carries no %s; %s", ReasonConfirmUnproved, rec.Door, digest, why, doorsText(digest, project))
	}
	return rec, nil
}

var eventIDRE = regexp.MustCompile(`^[0-9a-f]{64}$`)

// proofMissing names the door's proof a record lacks, "" when it has it.
func proofMissing(rec confirmRecord) string {
	switch rec.Door {
	case DoorTUI:
		if rec.Proof.PID <= 1 {
			return "pane and pid"
		}
	case DoorShell:
		if len(rec.Proof.PIDChain) == 0 {
			return "pid chain"
		}
	case DoorBuzz:
		if !eventIDRE.MatchString(rec.Proof.EventID) {
			return "event id"
		}
	default:
		return "known door"
	}
	return ""
}

// writeProposal records a proposal beside the records (the one write of
// `team propose --from PLAN`).
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

// Seams of door (a)'s ancestry check: the kernel's records of this process
// and its ancestors (procinfo), and whether it has a controlling terminal.
var (
	confirmSelf     = os.Getpid
	confirmParent   = procinfo.ParentPID
	confirmArgv     = procinfo.Argv
	confirmEnviron  = procinfo.Environ
	confirmSession  = procinfo.SessionID
	confirmTerminal = hasControllingTerminal
)

// hasControllingTerminal reports whether this process has a controlling
// terminal: /dev/tty opens only then. A process the lead detaches (setsid,
// a daemon) has none.
func hasControllingTerminal() bool {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

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

// confirmAncestry is door (a)'s kernel-level refusal (3b (i)): the process
// must have a controlling terminal; its parent chain, walked to pid 1 from
// the kernel's records, must reach its session leader (a process the lead
// orphaned out of the chain does not) and must hold no amq-squad lead or
// seat process (by argv) and no process carrying an AMQ identity (by its
// initial environment). An ancestor whose records cannot be read (another
// user's: login, sshd) is passed over; the lead and its seats are the
// owner's own processes, always readable. It returns the chain.
func confirmAncestry() ([]confirmHop, error) {
	if !confirmTerminal() {
		return nil, fmt.Errorf("%s: this process has no controlling terminal; run amq-squad team confirm from your own shell", ReasonNoTerminal)
	}
	self := confirmSelf()
	sid, ok := confirmSession(self)
	if !ok {
		return nil, fmt.Errorf("%s: the session of pid %d cannot be read", ReasonAncestryUnknown, self)
	}
	var chain []confirmHop
	leader := sid == self
	p, ok := confirmParent(self)
	if !ok {
		return nil, fmt.Errorf("%s: the parent of pid %d cannot be read", ReasonAncestryUnknown, self)
	}
	for hops := 0; p > 1; hops++ {
		if hops >= confirmMaxHops {
			return nil, fmt.Errorf("%s: the parent chain of pid %d is longer than %d hops", ReasonAncestryUnknown, self, confirmMaxHops)
		}
		hop := confirmHop{PID: p}
		if argv, ok := confirmArgv(p); ok {
			hop.Name = filepath.Base(argv[0])
			if leadOrSeatArgv(argv) {
				return nil, fmt.Errorf("%s: pid %d (%s) is an amq-squad lead or seat process and an ancestor of this command; only the owner's own shell confirms (from the lead's pane type /squad confirm instead)", ReasonLeadDescendant, p, strings.Join(argv[:min(2, len(argv))], " "))
			}
		}
		if env, ok := confirmEnviron(p); ok && amqIdentityEnv(env) {
			return nil, fmt.Errorf("%s: pid %d (%s) carries an AMQ seat identity (AM_ME) and is an ancestor of this command; only the owner's own shell confirms", ReasonLeadDescendant, p, firstNonEmptyStr(hop.Name, "?"))
		}
		chain = append(chain, hop)
		leader = leader || p == sid
		next, ok := confirmParent(p)
		if !ok {
			return nil, fmt.Errorf("%s: the parent of pid %d cannot be read", ReasonAncestryUnknown, p)
		}
		p = next
	}
	if !leader {
		return nil, fmt.Errorf("%s: the parent chain of pid %d does not reach its session leader (pid %d): the command was orphaned out of the shell that ran it", ReasonAncestryUnknown, self, sid)
	}
	return chain, nil
}

const teamConfirmUsage = `amq-squad team confirm - the owner's yes to a team the lead proposed (R55, door 2)

Usage:
  amq-squad team confirm DIGEST [--project DIR] [--json]

Run it from your own shell: it refuses lead_descendant when an amq-squad lead
or a seat process is its ancestor, and no_terminal without a controlling
terminal. It writes the confirmation record team apply --digest DIGEST needs
(younger than 30 minutes). The other doors: /squad confirm <prefix> in the
lead's pane, and the signed Buzz DM "yes <prefix>".
`

// runTeamConfirm is door (a).
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
	if _, err := digestHex(digest); err != nil {
		return err
	}
	dir, err := teamProjectDir(*project)
	if err != nil {
		return err
	}
	chain, err := confirmAncestry()
	if err != nil {
		return err
	}
	root, err := soloRootOf(dir)
	if err != nil {
		return err
	}
	rec := confirmRecord{Digest: strings.TrimSpace(digest), Door: DoorShell, At: confirmNow().UTC(), By: "uid " + strconv.Itoa(os.Getuid()), Proof: confirmProof{PIDChain: chain}}
	p, err := writeConfirmRecord(root, rec)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("team_confirm", map[string]any{"record": p, "confirmation": rec})
	}
	fmt.Printf("confirmed %s (door shell) at %s; it stands for %s: %s\n", rec.Digest, rec.At.Format(time.RFC3339), ConfirmTTL, p)
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

// confirmByPrefix is the Buzz door's write (wired into the mailbox
// handler): the owner's signed `yes <prefix>`, its signature already
// verified, confirms the one proposal younger than ConfirmTTL whose digest
// starts with prefix, among every solo root under ~/.amq-squad/solo/. It
// returns the confirmed digest.
func confirmByPrefix(prefix, eventID, owner string, at time.Time) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if _, err := hex.DecodeString(prefix); err != nil || len(prefix) != 16 {
		return "", fmt.Errorf("%s: %q is not a 16-hex prefix", ReasonNoProposal, prefix)
	}
	matches, _ := filepath.Glob(filepath.Join(home, ".amq-squad", "solo", "*", "root", "extensions", "amq-squad", confirmDirName, proposedDirName, prefix+"*.json"))
	sort.Strings(matches)
	type hit struct {
		root string
		p    teamProposal
	}
	var hits []hit
	for _, m := range matches {
		root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(m)))))
		h := strings.TrimSuffix(filepath.Base(m), ".json")
		p, err := readProposal(root, proposalDigestFor(m, h))
		if err != nil || at.Sub(p.At) > ConfirmTTL {
			continue
		}
		hits = append(hits, hit{root, p})
	}
	switch len(hits) {
	case 0:
		return "", fmt.Errorf("%s: no proposal younger than %s starts with %s", ReasonNoProposal, ConfirmTTL, prefix)
	case 1:
	default:
		return "", fmt.Errorf("%s: %d proposals start with %s; confirm at the lead's pane or the shell door with the full digest", ReasonProposalAmbig, len(hits), prefix)
	}
	rec := confirmRecord{Digest: hits[0].p.Digest, Door: DoorBuzz, At: at.UTC(), By: "owner key " + owner, Proof: confirmProof{EventID: eventID}}
	if _, err := writeConfirmRecord(hits[0].root, rec); err != nil {
		return "", err
	}
	return rec.Digest, nil
}

// proposalDigestFor reads a proposal file's digest field (the file stem is
// only its content hex).
func proposalDigestFor(path, _ string) string {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > confirmProposalMaxSize {
		return ""
	}
	var p struct {
		Digest string `json:"digest"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.Digest
}
