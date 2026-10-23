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

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

const (
	// SchemaVersion is bumped on any breaking change to the on-disk record.
	SchemaVersion = 1

	// LayerName is the AMQ extension namespace used for amq-squad metadata.
	LayerName = "io.github.omriariav.amq-squad"

	// FileName is the name of the launch record inside an agent's mailbox dir.
	FileName = "launch.json"
)

// Record is the persisted launch invocation for a single agent. New records
// live under the amq-squad extension namespace inside the agent mailbox.
type Record struct {
	Schema  int      `json:"schema"`
	CWD     string   `json:"cwd"`
	Binary  string   `json:"binary"`
	Argv    []string `json:"argv"`
	Session string   `json:"session"`
	// SharedWorkstream: Session is the team-wide workstream, even when it
	// equals this agent's role or handle.
	SharedWorkstream bool     `json:"shared_workstream,omitempty"`
	Conversation     string   `json:"conversation,omitempty"`
	Handle           string   `json:"handle"`
	Role             string   `json:"role,omitempty"`
	Root             string   `json:"root"`
	BaseRoot         string   `json:"base_root,omitempty"`
	RootSource       string   `json:"root_source,omitempty"`
	AMQVersion       string   `json:"amq_version,omitempty"`
	CodexArgs        []string `json:"codex_args,omitempty"`
	ClaudeArgs       []string `json:"claude_args,omitempty"`
	Launcher         string   `json:"launcher,omitempty"`
	LauncherArgs     []string `json:"launcher_args,omitempty"`
	Model            string   `json:"model,omitempty"`
	ToolProfile      string   `json:"tool_profile,omitempty"`
	ToolConfig       string   `json:"tool_config,omitempty"`
	ToolMCPConfig    string   `json:"tool_mcp_config,omitempty"`
	ToolAllowlist    []string `json:"tool_allowlist,omitempty"`
	ToolBlocklist    []string `json:"tool_blocklist,omitempty"`
	Trust            string   `json:"trust,omitempty"`
	SpawnOrigin      string   `json:"spawn_origin,omitempty"`
	SpawnDepth       int      `json:"spawn_depth,omitempty"`
	// NoGitignore records AMQ's --no-gitignore opt-out for coop auto-init.
	NoGitignore bool `json:"no_gitignore,omitempty"`
	// PreauthorizedActions is audit evidence of the Claude --allowedTools
	// patterns granted at launch (#296).
	PreauthorizedActions []string  `json:"preauthorized_actions,omitempty"`
	AgentPID             int       `json:"agent_pid,omitempty"`
	AgentTTY             string    `json:"agent_tty,omitempty"`
	StartedAt            time.Time `json:"started_at"`
	// StoppedAt marks a record resumable but no longer live; written only
	// after the runtime is confirmed stopped.
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	// TeamProfile is the launch's profile; empty means the default profile.
	TeamProfile string `json:"team_profile,omitempty"`
	TeamHome    string `json:"team_home,omitempty"`
	// Tmux is the v2 tmux identity, read only by the kept-dead fleet registry
	// (global_noc.go); 3.0 launches do not write it.
	Tmux *TmuxInfo `json:"tmux,omitempty"`
}

// WakeRetryTransition records one active-registration policy migration. An
// omitted legacy value is normalized to "drained" in From; Source preserves
// whether that old policy was implicit or explicitly persisted.
type WakeRetryTransition struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Source string    `json:"source"`
	At     time.Time `json:"at"`
}

// OrchestratorRegistration is additive launch-record provenance for the
// external orchestrator companion bound to one project run.
type OrchestratorRegistration struct {
	Policy                 string    `json:"policy"`
	State                  string    `json:"state"`
	Handle                 string    `json:"handle"`
	ExternalRegistrationID string    `json:"external_registration_id,omitempty"`
	ExternalGeneration     uint64    `json:"external_generation,omitempty"`
	NOCControlRoot         string    `json:"noc_control_root,omitempty"`
	NOCLaunchID            string    `json:"noc_launch_id,omitempty"`
	NOCGeneration          uint64    `json:"noc_generation,omitempty"`
	NOCRunRegistrationID   string    `json:"noc_run_registration_id,omitempty"`
	RegisteredAt           time.Time `json:"registered_at"`
}

// GoalBinding is launch-time evidence for a visible lead's goal binding.
type GoalBinding struct {
	Mode       string `json:"mode"`
	NativeGoal bool   `json:"native_goal"`
	Source     string `json:"source"`
	Command    string `json:"command,omitempty"`
	// DeliveryState separates accepted/prepared bootstrap evidence from a
	// reserved control action and a control action whose delivery completed.
	// Empty is the legacy state and remains readable for pre-v2.22 records.
	DeliveryState string `json:"delivery_state,omitempty"`
	// Goal and AttemptID are additive typed identity for binary-specific goal
	// delivery. Legacy native bindings omit them and remain readable through the
	// strict generated-/goal parser; prompt_goal bindings require them so resume
	// never has to infer structured prompt contents.
	Goal      string `json:"goal,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// TmuxInfo is the exact tmux identity of the pane an agent was launched into.
// Pane and window ids (e.g. "%265", "@42") are stable tmux control addresses
// and are the only values that should be used to target follow-up control.
// WindowName is a human label that can collide and must never be treated as an
// address.
type TmuxInfo struct {
	Session    string `json:"session,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
	WindowName string `json:"window_name,omitempty"`
	PaneID     string `json:"pane_id,omitempty"`
	// Target records how the pane was created relative to the launching
	// environment: "current-window", "new-window", or "new-session". Empty
	// when not known to the launcher.
	Target string `json:"target,omitempty"`
}

// TerminalInfo is the backend-neutral runtime identity for terminal control.
// For tmux-backed launches it intentionally duplicates the existing tmux ids
// instead of nesting TmuxInfo, keeping the JSON shape simple for clients that
// select behavior by backend first.
type TerminalInfo struct {
	Backend    string `json:"backend,omitempty"`
	Session    string `json:"session,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
	WindowName string `json:"window_name,omitempty"`
	TabID      string `json:"tab_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	PaneID     string `json:"pane_id,omitempty"`
	TTY        string `json:"tty,omitempty"`
	Target     string `json:"target,omitempty"`
}

// Entry is a launch record plus the mailbox directory it was discovered in.
type Entry struct {
	Record   Record
	AgentDir string
	Source   string
}

// ExtensionDir returns the amq-squad extension directory for an agent mailbox.
func ExtensionDir(agentDir string) string {
	return filepath.Join(agentDir, "extensions", LayerName)
}

// Path returns the v0.6+ launch record path for an agent mailbox.
func Path(agentDir string) string {
	return filepath.Join(ExtensionDir(agentDir), FileName)
}

// LegacyPath returns the pre-v0.6 launch record path for an agent mailbox.
func LegacyPath(agentDir string) string {
	return filepath.Join(agentDir, FileName)
}

// ExistingPath returns the launch record path that should be read, preferring
// the extension namespace and falling back to the legacy direct-agent path.
func ExistingPath(agentDir string) string {
	p := Path(agentDir)
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if _, err := os.Stat(LegacyPath(agentDir)); err == nil {
		return LegacyPath(agentDir)
	}
	return p
}

// HasRecord reports whether either the extension or legacy record exists.
func HasRecord(agentDir string) bool {
	if _, err := os.Stat(Path(agentDir)); err == nil {
		return true
	}
	if _, err := os.Stat(LegacyPath(agentDir)); err == nil {
		return true
	}
	return false
}

// RecordLockPath is the shared writer lock for one launch record.
func RecordLockPath(agentDir string) string { return Path(agentDir) + ".lock" }

// WithRecordLock serializes launch record writers and narrow conditional
// read/write control-plane operations.
func WithRecordLock(agentDir string, fn func() error) error {
	if err := os.MkdirAll(ExtensionDir(agentDir), 0o700); err != nil {
		return fmt.Errorf("ensure extension dir: %w", err)
	}
	return flock.WithLock(RecordLockPath(agentDir), fn)
}

// WriteUnderRecordLock writes a record while the caller holds WithRecordLock.
// It is exported only for compound compare-and-write sections.
func WriteUnderRecordLock(agentDir string, rec Record) error {
	dir := ExtensionDir(agentDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("ensure extension dir: %w", err)
	}
	path := Path(agentDir)
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
	path := ExistingPath(agentDir)
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

// ScanEntries walks a projectRoot for launch.json records across AMQ layouts:
//
//	<projectRoot>/.agent-mail/<profile>/<session>/agents/<handle>/extensions/<layer>/launch.json
//	<projectRoot>/.agent-mail/<session>/agents/<handle>/extensions/<layer>/launch.json
//	<projectRoot>/.agent-mail/agents/<handle>/extensions/<layer>/launch.json
//	<projectRoot>/.agent-mail/<profile>/<session>/agents/<handle>/launch.json
//	<projectRoot>/.agent-mail/<session>/agents/<handle>/launch.json
//	<projectRoot>/.agent-mail/agents/<handle>/launch.json
//
// Returns every record found. Order is whatever filepath.Glob returns;
// callers that care about ordering should sort the result themselves.
func ScanEntries(projectRoot string) ([]Entry, error) {
	return ScanEntriesInRoot(projectRoot, filepath.Join(projectRoot, ".agent-mail"))
}

// ScanEntriesInRoot walks an AMQ base root for launch.json records across
// extension and legacy layouts.
func ScanEntriesInRoot(projectRoot, baseRoot string) ([]Entry, error) {
	patterns := []struct {
		glob     string
		agentDir func(string) string
	}{
		{
			glob: filepath.Join(baseRoot, "*", "*", "agents", "*", "extensions", LayerName, FileName),
			agentDir: func(path string) string {
				return filepath.Dir(filepath.Dir(filepath.Dir(path)))
			},
		},
		{
			glob: filepath.Join(baseRoot, "*", "agents", "*", "extensions", LayerName, FileName),
			agentDir: func(path string) string {
				return filepath.Dir(filepath.Dir(filepath.Dir(path)))
			},
		},
		{
			glob: filepath.Join(baseRoot, "agents", "*", "extensions", LayerName, FileName),
			agentDir: func(path string) string {
				return filepath.Dir(filepath.Dir(filepath.Dir(path)))
			},
		},
		{
			glob: filepath.Join(baseRoot, "*", "agents", "*", FileName),
			agentDir: func(path string) string {
				return filepath.Dir(path)
			},
		},
		{
			glob: filepath.Join(baseRoot, "*", "*", "agents", "*", FileName),
			agentDir: func(path string) string {
				return filepath.Dir(path)
			},
		},
		{
			glob: filepath.Join(baseRoot, "agents", "*", FileName),
			agentDir: func(path string) string {
				return filepath.Dir(path)
			},
		},
	}
	seen := map[string]bool{}
	var out []Entry
	for _, p := range patterns {
		matches, err := filepath.Glob(p.glob)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", p.glob, err)
		}
		for _, m := range matches {
			agentDir := p.agentDir(m)
			if seen[agentDir] {
				continue
			}
			seen[agentDir] = true
			rec, err := Read(agentDir)
			if err != nil {
				continue
			}
			out = append(out, Entry{
				Record:   rec,
				AgentDir: agentDir,
				Source:   FileName,
			})
		}
	}
	return out, nil
}

// ScanRestorableEntriesInRoot returns launch records plus best-effort records
// inferred from older AMQ mailboxes under a resolved AMQ base root.
func ScanRestorableEntriesInRoot(projectRoot, baseRoot string) ([]Entry, error) {
	entries, err := ScanEntriesInRoot(projectRoot, baseRoot)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.AgentDir] = true
	}

	legacy, err := ScanLegacyEntriesInRoot(projectRoot, baseRoot)
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

// ScanLegacyEntriesInRoot infers restorable launches under a resolved AMQ base root.
func ScanLegacyEntriesInRoot(projectRoot, baseRoot string) ([]Entry, error) {
	agentDirs, err := legacyAgentDirs(baseRoot)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, agentDir := range agentDirs {
		if HasRecord(agentDir) {
			continue
		}
		if !hasLegacyActivity(agentDir) {
			continue
		}
		rec, err := legacyRecord(projectRoot, baseRoot, agentDir)
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

func legacyAgentDirs(baseRoot string) ([]string, error) {
	patterns := []string{
		filepath.Join(baseRoot, "*", "*", "agents", "*"),
		filepath.Join(baseRoot, "*", "agents", "*"),
		filepath.Join(baseRoot, "agents", "*"),
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
	for _, name := range []string{"inbox", "outbox", "receipts", "dlq"} {
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

func legacyRecord(projectRoot, baseRoot, agentDir string) (Record, error) {
	rel, err := filepath.Rel(baseRoot, agentDir)
	if err != nil {
		return Record{}, err
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) < 2 {
		return Record{}, fmt.Errorf("invalid agent dir: %s", agentDir)
	}

	session := ""
	root := baseRoot
	handle := filepath.Base(agentDir)
	binary, ok := inferLegacyBinary(handle)
	if !ok {
		return Record{}, fmt.Errorf("cannot infer binary for legacy handle: %s", handle)
	}
	if parts[0] != "agents" {
		if len(parts) >= 4 && parts[2] == "agents" {
			session = parts[1]
			root = filepath.Join(baseRoot, parts[0], session)
		} else {
			session = parts[0]
			root = filepath.Join(baseRoot, session)
		}
	}

	rec := Record{
		CWD:       projectRoot,
		Binary:    binary,
		Session:   session,
		Handle:    handle,
		Role:      inferLegacyRole(handle),
		Root:      root,
		BaseRoot:  baseRoot,
		StartedAt: legacyActivityTime(agentDir),
	}
	if len(parts) >= 4 && parts[2] == "agents" {
		rec.TeamProfile = parts[0]
	}
	return rec, nil
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
