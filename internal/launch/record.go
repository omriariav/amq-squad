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
	"strings"
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

	Runtime     string `json:"runtime,omitempty"` // claude | codex | pi
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	RolePath    string `json:"role_path,omitempty"`
	RoleSHA256  string `json:"role_sha256,omitempty"`
	RulesPath   string `json:"rules_path,omitempty"`
	RulesSHA256 string `json:"rules_sha256,omitempty"`
	Branch      string `json:"branch,omitempty"` // AMQ_SQUAD_BRANCH
	Base        string `json:"base,omitempty"`   // the base branch's tip the launch put the worktree and branch on (3.0.2)
	Worktree    string `json:"worktree,omitempty"`
	// MainCheckout is the repository root Claude Code reads a Claude seat's
	// local settings from, as seat-exec found it at the launch
	// (claudeseat.MainCheckout); on the exec record only (3.3.1 item 5).
	MainCheckout   string   `json:"main_checkout,omitempty"`
	AMQPath        string   `json:"amq_path,omitempty"`          // the real amq (R51)
	EnvNeeds       []string `json:"env_needs,omitempty"`         // names the runtime declares it needs
	CredNeeds      []string `json:"credential_needs,omitempty"`  // credential names the registry entry declares
	Resume         bool     `json:"resume,omitempty"`            // the launch asked AMQ to resume
	PermissionMode string   `json:"permission_mode,omitempty"`   // Claude's, checked with the hook plugin as one unit (R36)
	Credential     string   `json:"credential_source,omitempty"` // where a seat credential comes from, never its value
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
	WakeRequired     bool       `json:"wake_required,omitempty"` // the launch compiled a required AMQ wake (3.0.1); a fact, never re-checked
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
	// LauncherArgv is a pi seat's exec: the embedded runtime's launcher (amq-squad pi run) with the adapter's provider argv after "--".
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

// StagedDir is where the core stages a seat's record and role before the launch:
// <root>/extensions/amq-squad/launch/<handle>, which guard protects from the seat.
func StagedDir(root, handle string) string {
	return filepath.Join(root, "extensions", "amq-squad", "launch", handle)
}

// C5a (3.3.x): every record a decision trusts lives under StagedDir, which no
// seat can write: a Claude or pi seat's write there is refused by the guard
// (internal/cli/hook_guard_env.go protects the session root's
// extensions/amq-squad/); a Codex seat writes only its worktree (the
// workspace-write sandbox with writable_roots=[], internal/seatexec/codex.go),
// and compile refuses a worktree that holds the session root
// (internal/seatlaunch/compile.go). The published record in the seat's own
// agent directory (Path, ExistingPath, Read) is still written, for a 3.3.0
// binary after a rollback, and no 3.3.x reader trusts it.

// StagedPath is the record the core staged for a seat's launch
// (seatlaunch.Stage): the record a decision trusts (its handle, role, pool,
// rules source and start).
func StagedPath(root, handle string) string { return filepath.Join(StagedDir(root, handle), FileName) }

// ExecDir is where seat-exec writes the seat's exec record and its role and
// rules snapshots: <StagedDir>/exec.
func ExecDir(root, handle string) string { return filepath.Join(StagedDir(root, handle), "exec") }

// ExecPath is the seat's exec record: the staged record with seat-exec's
// outcome (pid, start stamp, provider argv), never the proof token.
func ExecPath(root, handle string) string { return filepath.Join(ExecDir(root, handle), FileName) }

// ReadStaged reads the record the core staged for a seat's launch; its
// handle must be the one asked for. os.ErrNotExist when there is none.
func ReadStaged(root, handle string) (Record, error) {
	return readSeatRecord(StagedPath(root, handle), handle)
}

// ReadExec reads a seat's exec record (written by seat-exec at its launch);
// its handle must be the one asked for. os.ErrNotExist when there is none (a
// seat launched before 3.3.x, or one seat-exec has not run for).
func ReadExec(root, handle string) (Record, error) {
	return readSeatRecord(ExecPath(root, handle), handle)
}

func readSeatRecord(path, handle string) (Record, error) {
	if handle == "" || handle == "." || handle == ".." || strings.ContainsAny(handle, `/\`) {
		return Record{}, fmt.Errorf("%q is not a seat handle", handle)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Record{}, err
	}
	rec, err := Decode(b)
	if err != nil {
		return Record{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if rec.Handle != handle {
		return Record{}, fmt.Errorf("%s names handle %q, not %q", path, rec.Handle, handle)
	}
	return rec, nil
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

// ScanEntriesInRoot finds the records the core staged for seat launches
// under an AMQ base root (<session root>/extensions/amq-squad/launch/<h>/
// launch.json), at the base root, session and profile/session levels. Each
// Entry's AgentDir is that session root's agents/<h> (its mailbox, presence
// and activity). A record a seat wrote in its own agent directory is never
// read (C5a); a mailbox without a staged record is not inferred; and a
// record is kept only where it names, as its root, the session root it was
// found under (the core stages Root: the session root), so a record placed
// under another directory of the base root never speaks for a session
// there (Codex pass 2, finding 2).
func ScanEntriesInRoot(_ string, baseRoot string) ([]Entry, error) {
	seen := map[string]bool{}
	var out []Entry
	for _, levels := range [][]string{{"*", "*"}, {"*"}, nil} {
		parts := append(append([]string{baseRoot}, levels...), "extensions", "amq-squad", "launch", "*", FileName)
		glob := filepath.Join(parts...)
		matches, err := filepath.Glob(glob)
		if err != nil {
			return nil, fmt.Errorf("glob %s: %w", glob, err)
		}
		for _, m := range matches {
			handle := filepath.Base(filepath.Dir(m))
			root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(m)))))
			agentDir := filepath.Join(root, "agents", handle)
			if seen[agentDir] {
				continue
			}
			seen[agentDir] = true
			if rec, err := ReadStaged(root, handle); err == nil && sameDir(recordRoot(rec), root) {
				out = append(out, Entry{Record: rec, AgentDir: agentDir, Source: FileName})
			}
		}
	}
	return out, nil
}

// recordRoot is rec.Root, a relative one taken against rec.CWD as the
// snapshot does (state.absoluteLaunchRoot).
func recordRoot(rec Record) string {
	root, cwd := strings.TrimSpace(rec.Root), strings.TrimSpace(rec.CWD)
	if root != "" && !filepath.IsAbs(root) && filepath.IsAbs(cwd) {
		return filepath.Join(cwd, root)
	}
	return root
}

// sameDir reports that a and b (both non-empty) name one directory, by their
// cleaned absolute paths or their resolved ones.
func sameDir(a, b string) bool {
	if a == "" || b == "" || !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}
