// Package launch defines the seat launch record, schema 1 (plan 4.3, R4,
// R52): the core stages it and role.md under the session root (StagedDir);
// seat-exec verifies both, publishes them into the seat's extension directory
// and execs the provider. The record is the only source of the seat identity.
package launch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// SchemaVersion stays 1: 3.0 adds fields, and every older record still
// decodes (unknown fields are ignored, missing ones are zero). LayerName is
// the AMQ extension namespace; FileName and RoleFileName name the files.
const (
	SchemaVersion = 1
	LayerName     = "io.github.omriariav.amq-squad"
	FileName      = "launch.json"
	RoleFileName  = "role.md"
)

// Record is one seat's launch record.
type Record struct {
	Schema   int    `json:"schema"`
	Handle   string `json:"handle"`              // the seat identity (R52) is set in the seat environment from this record only
	Root     string `json:"root"`                // AM_ROOT, the session root
	BaseRoot string `json:"base_root,omitempty"` // AM_BASE_ROOT
	Session  string `json:"session"`             // AM_SESSION

	Runtime        string   `json:"runtime,omitempty"` // claude | codex | pi
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	RolePath       string   `json:"role_path,omitempty"`
	RoleSHA256     string   `json:"role_sha256,omitempty"`
	RulesPath      string   `json:"rules_path,omitempty"`
	RulesSHA256    string   `json:"rules_sha256,omitempty"`
	Branch         string   `json:"branch,omitempty"` // AMQ_SQUAD_BRANCH
	Worktree       string   `json:"worktree,omitempty"`
	AMQPath        string   `json:"amq_path,omitempty"`             // the real amq (R51)
	EnvNeeds       []string `json:"env_needs,omitempty"`            // names the runtime declares it needs
	CredNeeds      []string `json:"credential_needs,omitempty"`     // credential names the registry entry declares
	Resume         bool     `json:"resume,omitempty"`               // the launch asked AMQ to resume
	PermissionMode string   `json:"permission_mode,omitempty"`      // Claude's, checked with the hook plugin as one unit (R36)
	ClaudeConfig   string   `json:"claude_config_sha256,omitempty"` // digest of the seat's CLAUDE_CONFIG_DIR managed files
	ClaudePin      string   `json:"claude_pin_sha256,omitempty"`    // sha256 of the pinned claude the launch probed; seat-exec refuses pin_changed_since_probe when the registry's pin differs
	AllowRules     string   `json:"claude_allow_rules,omitempty"`   // "permit": worktree allow rules do not refuse the seat
	Credential     string   `json:"credential_source,omitempty"`    // where a seat credential comes from, never its value
	PiAgentDir     string   `json:"pi_agent_dir,omitempty"`
	// PiShim is the pi shim the launch wrote and AMQ planned; seat-exec
	// compares argv[0] with it, never with a path from its environment.
	PiShim      string       `json:"pi_shim,omitempty"`
	LaunchNonce string       `json:"launch_nonce,omitempty"`
	RequestID   string       `json:"request_id,omitempty"` // the scheduler's SeatRequest.ID (2b-1)
	LaunchID    string       `json:"launch_id,omitempty"`  // the core's nonce for this launch; a proof carries it
	Proof       *ProofTicket `json:"proof,omitempty"`      // staged only; the published copy has no token
	Apply       *Apply       `json:"apply,omitempty"`
	Exec        *Exec        `json:"exec,omitempty"`

	CWD              string     `json:"cwd"` // older fields live readers still use (3.0 writes CWD, Binary, TeamHome, StartedAt)
	Binary           string     `json:"binary"`
	Argv             []string   `json:"argv,omitempty"`
	SharedWorkstream bool       `json:"shared_workstream,omitempty"`
	Conversation     string     `json:"conversation,omitempty"`
	Role             string     `json:"role,omitempty"`
	Launcher         string     `json:"launcher,omitempty"`
	ToolProfile      string     `json:"tool_profile,omitempty"`
	ToolConfig       string     `json:"tool_config,omitempty"`
	AgentPID         int        `json:"agent_pid,omitempty"`
	StartedAt        time.Time  `json:"started_at"`
	StoppedAt        *time.Time `json:"stopped_at,omitempty"`
	TeamProfile      string     `json:"team_profile,omitempty"`
	TeamHome         string     `json:"team_home,omitempty"`
	Tmux             *TmuxInfo  `json:"tmux,omitempty"` // v2 pane identity, kept-dead fleet registry only
}

// Apply is the AMQ ApplyResult outcome the core saw for this launch (R48).
type Apply struct {
	Outcome       string    `json:"outcome"`
	ReasonCode    string    `json:"reason_code,omitempty"`
	FailureDetail string    `json:"failure_detail,omitempty"`
	At            time.Time `json:"at"`
}

// Exec is seat-exec's outcome: exec (identity proof, pid, raw start stamp) or refused.
type Exec struct {
	At           time.Time `json:"at"`
	Outcome      string    `json:"outcome"` // exec | refused
	ReasonCode   string    `json:"reason_code,omitempty"`
	Detail       string    `json:"detail,omitempty"`
	PID          int       `json:"pid,omitempty"`
	ProcessStart string    `json:"process_start,omitempty"` // procinfo.StartToken: raw ticks or kinfo timeval
	ProviderArgv []string  `json:"provider_argv,omitempty"`
	// LauncherArgv is a pi seat's exec: the embedded runtime's launcher
	// (amq-squad pi run) with the adapter's provider argv after "--".
	LauncherArgv  []string `json:"launcher_argv,omitempty"`
	IdentityProof string   `json:"identity_proof,omitempty"`
}

// TmuxInfo is the v2 tmux pane identity (kept-dead fleet registry only).
type TmuxInfo struct {
	Session    string `json:"session,omitempty"`
	WindowID   string `json:"window_id,omitempty"`
	WindowName string `json:"window_name,omitempty"`
	PaneID     string `json:"pane_id,omitempty"`
	Target     string `json:"target,omitempty"`
}

// Entry is a launch record plus the mailbox directory it was found in.
type Entry struct {
	Record   Record
	AgentDir string
	Source   string
}

// ExtensionDir returns the amq-squad extension directory for an agent mailbox.
func ExtensionDir(agentDir string) string { return filepath.Join(agentDir, "extensions", LayerName) }

// Path is the published record of an agent mailbox; LegacyPath is pre-v0.6.
func Path(agentDir string) string { return filepath.Join(ExtensionDir(agentDir), FileName) }

func LegacyPath(agentDir string) string { return filepath.Join(agentDir, FileName) }

// StagedDir is where the core stages a seat's record and role before the
// launch: <session root>/extensions/amq-squad/launch/<handle>. guard
// protects <root>/extensions/amq-squad/, so a seat cannot rewrite it.
func StagedDir(root, handle string) string {
	return filepath.Join(root, "extensions", "amq-squad", "launch", handle)
}

// ExistingPath prefers the extension record and falls back to the legacy path.
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

// WithRecordLock serializes writers of one agent's published record.
func WithRecordLock(agentDir string, fn func() error) error {
	if err := os.MkdirAll(ExtensionDir(agentDir), 0o700); err != nil {
		return fmt.Errorf("ensure extension dir: %w", err)
	}
	return flock.WithLock(Path(agentDir)+".lock", fn)
}

// WriteUnderRecordLock writes an agent's published record (callers hold WithRecordLock).
func WriteUnderRecordLock(agentDir string, rec Record) error {
	if err := os.MkdirAll(ExtensionDir(agentDir), 0o700); err != nil {
		return fmt.Errorf("ensure extension dir: %w", err)
	}
	return WriteFile(Path(agentDir), rec)
}

// WriteFile writes rec to path atomically: an exclusive random temp file per
// writer (atomicfile), 0600, fsync, rename (2b-2 fix 3c: no shared temp name).
func WriteFile(path string, rec Record) error {
	b, err := Marshal(rec)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, b, 0o600)
}

// Marshal encodes rec as schema 1.
func Marshal(rec Record) ([]byte, error) {
	rec.Schema = SchemaVersion
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal launch record: %w", err)
	}
	return append(b, '\n'), nil
}

// Decode parses a record of any schema-1 vintage.
func Decode(b []byte) (Record, error) {
	var rec Record
	if err := json.Unmarshal(b, &rec); err != nil {
		return Record{}, err
	}
	if rec.Schema > SchemaVersion {
		return Record{}, fmt.Errorf("launch record schema %d is newer than %d", rec.Schema, SchemaVersion)
	}
	return rec, nil
}

// Read loads an agent's published record; os.ErrNotExist when absent.
func Read(agentDir string) (Record, error) {
	path := ExistingPath(agentDir)
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	rec, err := Decode(b)
	if err != nil {
		return Record{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return rec, nil
}

// ScanEntries scans <projectRoot>/.agent-mail (see ScanEntriesInRoot).
func ScanEntries(projectRoot string) ([]Entry, error) {
	return ScanEntriesInRoot(projectRoot, filepath.Join(projectRoot, ".agent-mail"))
}

// ScanEntriesInRoot finds launch records under an AMQ base root, in the
// extension and legacy layouts at the base root, session and profile/session
// levels. A mailbox without a record is not inferred (3.0 drops that).
func ScanEntriesInRoot(_ string, baseRoot string) ([]Entry, error) {
	seen := map[string]bool{}
	var out []Entry
	for _, levels := range [][]string{{"*", "*"}, {"*"}, nil} {
		for _, legacy := range []bool{false, true} {
			parts := append(append([]string{baseRoot}, levels...), "agents", "*")
			if legacy {
				parts = append(parts, FileName)
			} else {
				parts = append(parts, "extensions", LayerName, FileName)
			}
			glob := filepath.Join(parts...)
			matches, err := filepath.Glob(glob)
			if err != nil {
				return nil, fmt.Errorf("glob %s: %w", glob, err)
			}
			for _, m := range matches {
				agentDir := filepath.Dir(m)
				if !legacy {
					agentDir = filepath.Dir(filepath.Dir(agentDir))
				}
				if seen[agentDir] {
					continue
				}
				seen[agentDir] = true
				if rec, err := Read(agentDir); err == nil {
					out = append(out, Entry{Record: rec, AgentDir: agentDir, Source: FileName})
				}
			}
		}
	}
	return out, nil
}
