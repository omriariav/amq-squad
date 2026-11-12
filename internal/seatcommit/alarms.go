package seatcommit

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// The hold release file (3.2.0-0c-B, the plan reviewer's ruling on Codex's
// pass on 8cffafe7): the lead's decisions by alarm, one line per decision,
// at <root>/extensions/amq-squad/commits/<handle>.alarms.jsonl beside the
// seat's ledger, appended under the seat's ledger lock. No released build
// reads it (it is not the router's decisions.jsonl, whose verifier admits
// four tamper kinds only and tags a re-encoded struct, and whose chain head
// one in-memory writer keeps; nor the ledger body, which a chain
// continuation replaces).
//
// A line is the JSON body, a tab, then the hex HMAC-SHA256 under the
// session seed's key of the domain string alarmDomain, a zero byte, and
// the body's bytes exactly as written. A reader verifies the HMAC over the
// bytes as read and never re-encodes the body, so a later build can add
// fields to the body without an older build refusing the line; it then
// decodes what it knows and ignores the rest. There is no head and no
// chain: a deleted, truncated or reordered line only keeps a hold held
// (the safe direction); a forged or edited line fails the HMAC; a line
// from another session root fails under that root's different key; a line
// from another seat's file names another seat and is not this seat's; a
// replayed line is idempotent, since it names one alarm. A last line with
// no newline (a crash mid-write) is a torn tail: skipped and reported, and
// the lines before it still count; the next append terminates it first.
// The file grows by one line per decision by alarm on the seat.

// alarmDomain is the HMAC's domain string for a line of the hold release
// file.
const alarmDomain = "amq-squad scheduler hold release v1"

var (
	syncAlarmFile = func(f *os.File) error { return f.Sync() }
	syncAlarmDir  = atomicfile.SyncDir
)

// AlarmsPath is a seat's hold release file.
func AlarmsPath(root, handle string) string {
	return filepath.Join(LedgerDir(root), handle+".alarms.jsonl")
}

// AlarmDecision is a line's body: the seat, the alarm the lead released,
// why, when, and the lead the decision socket proved.
type AlarmDecision struct {
	Seat   string    `json:"seat"`
	Alarm  string    `json:"alarm"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
	Lead   string    `json:"lead"`
}

// alarmTag is a line's HMAC over the body's exact bytes.
func alarmTag(key, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(alarmDomain))
	m.Write([]byte{0})
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}

// AlarmLog is what a seat's hold release file holds as read: the decisions
// whose lines verify, and one problem per line that does not (a torn tail,
// a line that fails its seal, a line that names another seat, a body that
// does not decode), by its place in the file.
type AlarmLog struct {
	Decisions []AlarmDecision
	Problems  []string
}

// Released reports whether a verified line names alarm.
func (a AlarmLog) Released(alarm string) bool {
	for _, d := range a.Decisions {
		if d.Alarm == alarm {
			return true
		}
	}
	return false
}

// ReadAlarms reads a seat's hold release file, verifying each line over
// its bytes as read. An absent file is empty. With no session seed nothing
// verifies, and every line is a problem.
func ReadAlarms(root, handle string) (AlarmLog, error) {
	var out AlarmLog
	raw, present, err := readNoFollow(AlarmsPath(root, handle))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return out, err
	}
	if !present || len(raw) == 0 {
		return out, nil
	}
	key, ok, err := sealchain.ReadKey(root)
	if err != nil {
		return out, err
	}
	lines := bytes.Split(raw, []byte{'\n'})
	last := len(lines) - 1
	if len(lines[last]) > 0 {
		out.Problems = append(out.Problems, fmt.Sprintf("a torn last line of %d bytes (no newline)", len(lines[last])))
	}
	for i, line := range lines[:last] {
		if len(line) == 0 {
			continue
		}
		n := i + 1
		tab := bytes.LastIndexByte(line, '\t')
		if tab < 0 {
			out.Problems = append(out.Problems, fmt.Sprintf("line %d has no seal", n))
			continue
		}
		body, tag := line[:tab], line[tab+1:]
		if !ok || !hmac.Equal([]byte(alarmTag(key.Key, body)), tag) {
			out.Problems = append(out.Problems, fmt.Sprintf("line %d fails its seal", n))
			continue
		}
		var d AlarmDecision
		if json.Unmarshal(body, &d) != nil || d.Alarm == "" {
			out.Problems = append(out.Problems, fmt.Sprintf("line %d does not decode", n))
			continue
		}
		if d.Seat != handle {
			out.Problems = append(out.Problems, fmt.Sprintf("line %d names seat %s", n, d.Seat))
			continue
		}
		out.Decisions = append(out.Decisions, d)
	}
	return out, nil
}

// appendAlarm writes one decision to a seat's hold release file: the body,
// a tab, its seal, a newline, fsynced. A torn last line is terminated
// first, so the new line stays whole. The caller holds the ledger lock.
func appendAlarm(root string, d AlarmDecision) error {
	key, err := sealchain.SessionKey(root)
	if err != nil {
		return fmt.Errorf("the session seed: %w", err)
	}
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(LedgerDir(root), 0o700); err != nil {
		return err
	}
	path := AlarmsPath(root, d.Seat)
	// one descriptor, no link followed, no FIFO blocked on, a regular file
	// (openAppendNoFollow); the torn-tail check reads through it
	f, created, err := openAppendNoFollow(path)
	if err != nil {
		return err
	}
	defer f.Close()
	var line []byte
	line = append(line, body...)
	line = append(line, '\t')
	line = append(line, alarmTag(key.Key, body)...)
	if err := flock.AppendJSONLLine(f, line); err != nil {
		return err
	}
	if created {
		return errors.Join(syncAlarmFile(f), syncAlarmDir(filepath.Dir(path)))
	}
	return syncAlarmFile(f)
}
