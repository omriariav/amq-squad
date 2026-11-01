package schedproc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// The clear (brief 31-2, key 4): the owner's acknowledgement, from the
// lead's screen, of a warning line that brief 31-1 put in place of a deleted
// refusal (a silent hook, an external include, a seat stalled at a dialog
// or without its provider's key, a seat over the launch budget). It reaches
// the scheduler over the decision socket like a widening, so it is proved
// under R4 from the lead's process. It names the warning and the seat's
// launch: the line leaves the screen for that launch only, and the same
// condition on a later launch is a new line. A clear is never a gate and
// never changes a seat's state: it is one line in clears.jsonl (what status
// reads) and one warning_cleared decision.

// ClearRequestKind marks the lead's clear of a warning line.
const ClearRequestKind = "amq-squad.clear.v1"

// DecisionWarningCleared is the clear's decision kind.
const DecisionWarningCleared = "warning_cleared"

// ClearsName is the clears log under Dir: one JSON line per clear.
const ClearsName = "clears.jsonl"

// Refusals of a clear.
const (
	RefusedClearMalformed = "clear_malformed"
	// RefusedClearStale: the seat's current launch is not the one the
	// clear names (it relaunched since the screen showed the line, or it has
	// no launch record): the line the owner saw is not the seat's line now.
	RefusedClearStale = "clear_launch_stale"
)

// ClearRequest is the lead's clear: the seat, the warning's code (the text
// before its first colon on the screen) and the seat's launch id.
type ClearRequest struct {
	Kind     string `json:"kind"`
	Seat     string `json:"seat"`
	Warning  string `json:"warning"`
	LaunchID string `json:"launch_id"`
}

// ClearLine is one clear as recorded.
type ClearLine struct {
	At       time.Time `json:"at"`
	Seat     string    `json:"seat"`
	Warning  string    `json:"warning"`
	LaunchID string    `json:"launch_id"`
	Lead     string    `json:"lead"`
}

var (
	clearSeatRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	clearWarningRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	clearLaunchRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// ClearsPath is the root's clears log.
func ClearsPath(root string) string { return filepath.Join(Dir(root), ClearsName) }

// SendClear is the lead's side: one clear over the scheduler's socket.
func SendClear(root, path string, req ClearRequest) (DecideReply, error) {
	req.Kind = ClearRequestKind
	return sendDecision(root, path, req)
}

// clearNow is the clock of a clear (a test seam).
var clearNow = time.Now

// applyClear decodes a proved clear and records it.
func applyClear(root, lead string, line []byte) DecideReply {
	var req ClearRequest
	if json.Unmarshal(line, &req) != nil {
		return DecideReply{Code: RefusedClearMalformed, Detail: "a clear that does not decode"}
	}
	return RecordClear(root, lead, req)
}

// RecordClear is the clear's apply step, run by the decision socket after
// it proved the sender: the request's shape, then the seat's current launch
// (its staged record's launch id) must be the one named.
func RecordClear(root, lead string, req ClearRequest) DecideReply {
	if !clearSeatRe.MatchString(req.Seat) || !clearWarningRe.MatchString(req.Warning) || !clearLaunchRe.MatchString(req.LaunchID) {
		return DecideReply{Code: RefusedClearMalformed, Detail: "a clear names a seat handle, a warning code and the seat's launch id"}
	}
	raw, err := os.ReadFile(filepath.Join(launch.StagedDir(root, req.Seat), launch.FileName))
	if err != nil {
		return DecideReply{Code: RefusedClearStale, Detail: fmt.Sprintf("seat %s has no launch record: %v", req.Seat, err)}
	}
	staged, err := launch.Decode(raw)
	if err != nil || staged.LaunchID != req.LaunchID {
		return DecideReply{Code: RefusedClearStale, Detail: fmt.Sprintf("seat %s's current launch is %s, not %s: the line was of an earlier launch", req.Seat, staged.LaunchID, req.LaunchID)}
	}
	at := clearNow().UTC()
	rec := ClearLine{At: at, Seat: req.Seat, Warning: req.Warning, LaunchID: req.LaunchID, Lead: lead}
	if err := appendClear(root, rec); err != nil {
		return DecideReply{Code: "clear_unrecorded", Detail: err.Error()}
	}
	if err := AppendDecision(root, at, scheduler.Decision{Kind: DecisionWarningCleared, Seat: req.Seat, ReasonCode: req.Warning,
		Reason: fmt.Sprintf("%s on launch %s acknowledged by the lead %s at the screen", req.Warning, req.LaunchID, lead)}); err != nil {
		return DecideReply{Code: "clear_unrecorded", Detail: err.Error()}
	}
	data, _ := json.Marshal(rec)
	return DecideReply{OK: true, Detail: fmt.Sprintf("%s cleared for seat %s on launch %s", req.Warning, req.Seat, req.LaunchID), Data: data}
}

func appendClear(root string, rec ClearLine) error {
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(ClearsPath(root), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(rec)
	if _, err := f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ReadClears returns every recorded clear by seat (none when the log is
// absent); a torn line is skipped. The log holds one line per owner
// acknowledgement, so it is read whole.
func ReadClears(root string) (map[string][]ClearLine, error) {
	raw, err := os.ReadFile(ClearsPath(root))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]ClearLine{}
	for _, ln := range bytes.Split(raw, []byte("\n")) {
		var c ClearLine
		if len(bytes.TrimSpace(ln)) == 0 || json.Unmarshal(ln, &c) != nil || c.Seat == "" {
			continue
		}
		out[c.Seat] = append(out[c.Seat], c)
	}
	return out, nil
}

// Cleared reports whether a clear names this warning code on this launch.
func Cleared(clears []ClearLine, warning, launchID string) bool {
	for _, c := range clears {
		if c.Warning == warning && c.LaunchID == launchID && launchID != "" {
			return true
		}
	}
	return false
}

// WarningCode is a warning line's code: the text before its first colon.
func WarningCode(w string) string {
	code, _, _ := strings.Cut(w, ":")
	return strings.TrimSpace(code)
}
