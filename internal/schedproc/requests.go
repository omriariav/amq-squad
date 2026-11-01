package schedproc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The scheduler process's launch record (R1 of the 2b-2 milestone B first
// pass): written by the core at admission, it names the lead whose
// decisions the process accepts and the socket they arrive on (R4, decide.go).

// LaunchRecordName is the scheduler process's launch record under Dir.
const LaunchRecordName = "launch.json"

// LaunchRecord is what the core records when a scheduler process is
// admitted: the squad it schedules and the lead whose decisions it accepts.
type LaunchRecord struct {
	Schema      int       `json:"schema"`
	Lead        string    `json:"lead"`
	Root        string    `json:"root"`
	Session     string    `json:"session,omitempty"`
	ProjectRoot string    `json:"project_root,omitempty"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	// ProcessStart is PID's raw start stamp (procinfo.StartToken): with the
	// pid and the project root in its argv, the identity `amq-squad up`
	// checks before it signals a scheduler (brief 31-1's second PR).
	ProcessStart string `json:"process_start,omitempty"`
	// Start is the start of the squad's launch (brief 31-1, the launch
	// budget's first mark): when the owner's command began (`amq-squad up`,
	// else `lead --team`), from the AMQ_SQUAD_START it hands the scheduler.
	// Nil when the scheduler was started by hand. StartedAt is this
	// process's own admission, later.
	Start *time.Time `json:"start,omitempty"`
	// DecisionSocket is where the lead's decisions reach this process (R4).
	DecisionSocket string `json:"decision_socket,omitempty"`
}

// WriteLaunchRecord writes the record (0600 in the 0700 scheduler dir).
func WriteLaunchRecord(root string, rec LaunchRecord) error {
	rec.Schema = 1
	return writeJSON(filepath.Join(Dir(root), LaunchRecordName), rec)
}

// ReadLaunchRecord reads the admitted process's record.
func ReadLaunchRecord(root string) (LaunchRecord, error) {
	var rec LaunchRecord
	raw, err := os.ReadFile(filepath.Join(Dir(root), LaunchRecordName))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return rec, err
	}
	if rec.Schema != 1 || rec.Lead == "" || rec.Root != root {
		return rec, fmt.Errorf("the scheduler launch record is not this squad's")
	}
	return rec, nil
}

// WidenRequestKind marks a widening in a message body.
const WidenRequestKind = "amq-squad.widen.v1"

// WidenRequest is the lead's widening, over the decision socket (and in the
// lead's AMQ record of it).
type WidenRequest struct {
	Kind   string   `json:"kind"`
	Task   string   `json:"task"`
	Files  []string `json:"files"`
	Reason string   `json:"reason"`
}

// ParseWidenRequest reads a widening from a message body (false: not a
// widening).
func ParseWidenRequest(body string) (WidenRequest, bool) {
	var r WidenRequest
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &r) != nil || r.Kind != WidenRequestKind {
		return r, false
	}
	return r, true
}
