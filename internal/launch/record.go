// Package launch defines the launch record written into each agent's mailbox
// at coop exec time. It is the durable input to `amq-squad restore`.
package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// SchemaVersion is bumped on any breaking change to the on-disk record.
	SchemaVersion = 1

	// FileName is the name of the launch record inside an agent's mailbox dir.
	FileName = "launch.json"
)

// Record is the persisted launch invocation for a single agent. It lives at
// <AM_ROOT>/agents/<handle>/launch.json.
type Record struct {
	Schema    int       `json:"schema"`
	CWD       string    `json:"cwd"`
	Binary    string    `json:"binary"`
	Argv      []string  `json:"argv"`
	Session   string    `json:"session"`
	Handle    string    `json:"handle"`
	Role      string    `json:"role,omitempty"`
	Root      string    `json:"root"`
	StartedAt time.Time `json:"started_at"`
}

// Write atomically writes the record into the agent's mailbox directory.
// The agent mailbox is expected to exist (coop exec creates it), but Write
// also creates missing parents so the record can be written pre-exec.
func Write(agentDir string, rec Record) error {
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		return fmt.Errorf("ensure agent dir: %w", err)
	}
	path := filepath.Join(agentDir, FileName)
	tmp := path + ".tmp"

	rec.Schema = SchemaVersion
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal launch record: %w", err)
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Read loads a launch record from an agent's mailbox directory. Returns
// os.ErrNotExist if no launch.json is present.
func Read(agentDir string) (Record, error) {
	path := filepath.Join(agentDir, FileName)
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return Record{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return rec, nil
}

// Scan walks <projectRoot>/.agent-mail/*/agents/*/ for launch.json records
// and returns every record found. Order is whatever filepath.Glob returns;
// callers that care about ordering should sort the result themselves.
func Scan(projectRoot string) ([]Record, error) {
	pattern := filepath.Join(projectRoot, ".agent-mail", "*", "agents", "*", FileName)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob: %w", err)
	}
	out := make([]Record, 0, len(matches))
	for _, m := range matches {
		rec, err := Read(filepath.Dir(m))
		if err != nil {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}
