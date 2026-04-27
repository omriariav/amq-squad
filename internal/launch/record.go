// Package launch defines the launch record written into each agent's mailbox
// at coop exec time. It is the durable input to `amq-squad restore`.
package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	Schema  int      `json:"schema"`
	CWD     string   `json:"cwd"`
	Binary  string   `json:"binary"`
	Argv    []string `json:"argv"`
	Session string   `json:"session"`
	// SharedWorkstream means Session was chosen as the team-wide workstream,
	// even if the name happens to equal this agent's role or handle.
	SharedWorkstream bool      `json:"shared_workstream,omitempty"`
	Conversation     string    `json:"conversation,omitempty"`
	Handle           string    `json:"handle"`
	Role             string    `json:"role,omitempty"`
	Root             string    `json:"root"`
	StartedAt        time.Time `json:"started_at"`
}

// Entry is a launch record plus the mailbox directory it was discovered in.
type Entry struct {
	Record   Record
	AgentDir string
	Source   string
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

// ScanEntries walks a projectRoot for launch.json records across both AMQ layouts:
//
//	<projectRoot>/.agent-mail/<session>/agents/<handle>/launch.json  (coop exec)
//	<projectRoot>/.agent-mail/agents/<handle>/launch.json            (base root, no session)
//
// Returns every record found. Order is whatever filepath.Glob returns;
// callers that care about ordering should sort the result themselves.
func ScanEntries(projectRoot string) ([]Entry, error) {
	patterns := []string{
		filepath.Join(projectRoot, ".agent-mail", "*", "agents", "*", FileName),
		filepath.Join(projectRoot, ".agent-mail", "agents", "*", FileName),
	}
	seen := map[string]bool{}
	var out []Entry
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", p, err)
		}
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			rec, err := Read(filepath.Dir(m))
			if err != nil {
				continue
			}
			out = append(out, Entry{
				Record:   rec,
				AgentDir: filepath.Dir(m),
				Source:   FileName,
			})
		}
	}
	return out, nil
}

// ScanRestorableEntries returns launch records plus best-effort records
// inferred from older AMQ mailboxes that predate amq-squad launch.json.
func ScanRestorableEntries(projectRoot string) ([]Entry, error) {
	entries, err := ScanEntries(projectRoot)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.AgentDir] = true
	}

	legacy, err := ScanLegacyEntries(projectRoot)
	if err != nil {
		return nil, err
	}
	for _, e := range legacy {
		if seen[e.AgentDir] {
			continue
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ScanLegacyEntries infers restorable launches from AMQ agent mailbox
// directories that do not have launch.json. The binary is inferred from the
// handle, which matches AMQ's default handle derivation for claude/codex.
func ScanLegacyEntries(projectRoot string) ([]Entry, error) {
	agentDirs, err := legacyAgentDirs(projectRoot)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, agentDir := range agentDirs {
		if _, err := os.Stat(filepath.Join(agentDir, FileName)); err == nil {
			continue
		}
		if !hasLegacyActivity(agentDir) {
			continue
		}
		rec, err := legacyRecord(projectRoot, agentDir)
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Record:   rec,
			AgentDir: agentDir,
			Source:   "amq history",
		})
	}
	return out, nil
}

func legacyAgentDirs(projectRoot string) ([]string, error) {
	patterns := []string{
		filepath.Join(projectRoot, ".agent-mail", "*", "agents", "*"),
		filepath.Join(projectRoot, ".agent-mail", "agents", "*"),
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range patterns {
		matches, err := filepath.Glob(p)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", p, err)
		}
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || !info.IsDir() || seen[m] {
				continue
			}
			seen[m] = true
			out = append(out, m)
		}
	}
	return out, nil
}

func hasLegacyActivity(agentDir string) bool {
	if _, err := os.Stat(filepath.Join(agentDir, "presence.json")); err == nil {
		return true
	}
	for _, name := range []string{"inbox", "outbox", "acks", "receipts", "dlq"} {
		if hasFiles(filepath.Join(agentDir, name)) {
			return true
		}
	}
	return false
}

func hasFiles(root string) bool {
	found := false
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		found = true
		return filepath.SkipAll
	})
	return found
}

func legacyRecord(projectRoot, agentDir string) (Record, error) {
	rootDir := filepath.Join(projectRoot, ".agent-mail")
	rel, err := filepath.Rel(rootDir, agentDir)
	if err != nil {
		return Record{}, err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return Record{}, fmt.Errorf("invalid agent dir: %s", agentDir)
	}

	session := ""
	root := rootDir
	handle := filepath.Base(agentDir)
	binary, ok := inferLegacyBinary(handle)
	if !ok {
		return Record{}, fmt.Errorf("cannot infer binary for legacy handle: %s", handle)
	}
	if parts[0] != "agents" {
		session = parts[0]
		root = filepath.Join(rootDir, session)
	}

	return Record{
		CWD:       projectRoot,
		Binary:    binary,
		Session:   session,
		Handle:    handle,
		Role:      inferLegacyRole(handle),
		Root:      root,
		StartedAt: legacyActivityTime(agentDir),
	}, nil
}

func inferLegacyBinary(handle string) (string, bool) {
	switch handle {
	case "claude", "codex":
		return handle, true
	default:
		for _, binary := range []string{"claude", "codex"} {
			if strings.HasPrefix(handle, binary+"-") {
				return binary, true
			}
		}
		return "", false
	}
}

func inferLegacyRole(handle string) string {
	for _, binary := range []string{"claude", "codex"} {
		prefix := binary + "-"
		if strings.HasPrefix(handle, prefix) {
			return strings.TrimPrefix(handle, prefix)
		}
	}
	return ""
}

func legacyActivityTime(agentDir string) time.Time {
	if t, ok := readPresenceLastSeen(filepath.Join(agentDir, "presence.json")); ok {
		return t
	}
	var latest time.Time
	filepath.WalkDir(agentDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime()
		}
		return nil
	})
	return latest
}

func readPresenceLastSeen(path string) (time.Time, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	var parsed struct {
		LastSeen time.Time `json:"last_seen"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil || parsed.LastSeen.IsZero() {
		return time.Time{}, false
	}
	return parsed.LastSeen, true
}

// Scan walks a projectRoot for launch.json records across both AMQ layouts:
//
//	<projectRoot>/.agent-mail/<session>/agents/<handle>/launch.json  (coop exec)
//	<projectRoot>/.agent-mail/agents/<handle>/launch.json            (base root, no session)
//
// Returns every record found. Order is whatever filepath.Glob returns;
// callers that care about ordering should sort the result themselves.
func Scan(projectRoot string) ([]Record, error) {
	entries, err := ScanEntries(projectRoot)
	if err != nil {
		return nil, err
	}
	out := make([]Record, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Record)
	}
	return out, nil
}
