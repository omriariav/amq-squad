package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// The seat proof (2b-2 item 4, lead ruling): the launcher owns a Unix socket
// per launch, mints a one-time token per seat into the staged record, and
// counts a seat up only from a proof seat-exec sends over that socket. The
// seat's published record is evidence only. Accepted proofs and alarms are
// written here, in the core-owned launch directory (StagedDir's parent),
// which guard protects from seats.

// ProofTTL is how long a proof token is valid after staging.
const ProofTTL = 60 * time.Second

// ProofTicket is a seat's one-time proof ticket in the staged record. The
// published copy never carries the token.
//
// Recorded limit (same UID, plan 7.3): darwin has no connectat, so seat-exec
// stats the socket through its directory descriptor and then connects by
// path; a same-UID process can replace the node in between. The defence is
// the peer check after connect: the kernel-reported peer must be exactly
// LauncherPID with LauncherStart, so a replaced socket is never trusted.
type ProofTicket struct {
	Socket   string    `json:"socket"` // Dir/Name, for display; seat-exec connects through Dir's descriptor walk
	Dir      string    `json:"dir"`    // the 0700 directory that holds the socket (no symlink in the path)
	Name     string    `json:"name"`
	Dev      uint64    `json:"dev"` // the socket node's device and inode, checked before connecting
	Ino      uint64    `json:"ino"`
	Token    string    `json:"token,omitempty"` // TokenConsumed or TokenExpired once used up
	IssuedAt time.Time `json:"issued_at"`
	// The launcher process that serves the socket: seat-exec connects only
	// to a peer with this pid and raw start stamp.
	LauncherPID   int    `json:"launcher_pid"`
	LauncherStart string `json:"launcher_start"`
}

// Markers the launcher writes over a used-up token in the staged record.
const (
	TokenConsumed = "consumed"
	TokenExpired  = "expired"
)

// ProofMessage is seat-exec's proof: the ticket, the identity `amq env`
// resolved, and the process that will exec the provider.
type ProofMessage struct {
	LaunchID  string `json:"launch_id"`
	RequestID string `json:"request_id,omitempty"`
	Handle    string `json:"handle"`
	Token     string `json:"token"`
	Root      string `json:"root"`
	Me        string `json:"me"`
	Session   string `json:"session"`
	PID       int    `json:"pid"`
	Start     string `json:"start"` // procinfo.StartToken
}

// ProofReply is the launcher's answer; seat-exec execs only on OK.
type ProofReply struct {
	OK     bool   `json:"ok"`
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ProofEntry is the launch ledger entry of an accepted proof: the identity
// a later close compares against (never the seat's own record).
type ProofEntry struct {
	Handle    string    `json:"handle"`
	LaunchID  string    `json:"launch_id"`
	RequestID string    `json:"request_id,omitempty"`
	PID       int       `json:"pid"`
	Start     string    `json:"start"`
	At        time.Time `json:"at"`
	// CloseEscalated: the seat did not exit within the close grace after
	// SIGTERM and was killed (SIGKILL, fix 3c item 2a).
	CloseEscalated bool       `json:"close_escalated,omitempty"`
	ClosedAt       *time.Time `json:"closed_at,omitempty"`
}

// ProofLedgerPath is <root>/extensions/amq-squad/launch/<handle>/proof.json.
func ProofLedgerPath(root, handle string) string {
	return filepath.Join(StagedDir(root, handle), "proof.json")
}

// WriteProof records an accepted proof in the launch ledger (0600).
func WriteProof(root string, e ProofEntry) error {
	b, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(StagedDir(root, e.Handle), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(ProofLedgerPath(root, e.Handle), append(b, '\n'), 0o600)
}

// ReadProof reads a seat's ledger entry; os.ErrNotExist when there is none.
func ReadProof(root, handle string) (ProofEntry, error) {
	var e ProofEntry
	b, err := os.ReadFile(ProofLedgerPath(root, handle))
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(b, &e); err != nil {
		return e, fmt.Errorf("parse %s: %w", ProofLedgerPath(root, handle), err)
	}
	return e, nil
}

// Alarm is a launch alarm routed to the owner (status needs_you), such as
// identity_forged: a replayed token or a proof from outside the pane.
type Alarm struct {
	Kind     string    `json:"kind"` // identity_forged
	Code     string    `json:"code"` // the refusal: proof_replayed, proof_token_invalid, proof_origin, ...
	Handle   string    `json:"handle"`
	LaunchID string    `json:"launch_id"`
	Detail   string    `json:"detail"`
	At       time.Time `json:"at"`
}

// AlarmDir is <root>/extensions/amq-squad/launch/alarms.
func AlarmDir(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "launch", "alarms")
}

// WriteAlarm records one alarm (one file per handle, launch and code).
func WriteAlarm(root string, a Alarm) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(AlarmDir(root), 0o700); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%s-%s.json", a.Handle, a.LaunchID, a.Code)
	return atomicfile.Write(filepath.Join(AlarmDir(root), name), append(b, '\n'), 0o600)
}

// ReadAlarms lists the session's alarms, oldest first; none is not an error.
func ReadAlarms(root string) ([]Alarm, error) {
	entries, err := os.ReadDir(AlarmDir(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Alarm
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		var a Alarm
		if b, err := os.ReadFile(filepath.Join(AlarmDir(root), e.Name())); err == nil && json.Unmarshal(b, &a) == nil {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

// UpdateStaged changes a seat's staged record under one lock with a
// read-modify-write (2b-2 fix 3c (f)): every writer of the staged record
// (the post-Apply rewrite, the token markers) goes through it, so a marker is
// never overwritten with a stale token.
func UpdateStaged(root, handle string, fn func(*Record) error) error {
	path := filepath.Join(StagedDir(root, handle), FileName)
	return flock.WithLock(path+".lock", func() error {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rec, err := Decode(b)
		if err != nil {
			return err
		}
		if err := fn(&rec); err != nil {
			return err
		}
		return WriteFile(path, rec)
	})
}
