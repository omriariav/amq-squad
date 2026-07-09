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
	// SharedWorkstream means Session was chosen as the team-wide workstream,
	// even if the name happens to equal this agent's role or handle.
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
	Trust            string   `json:"trust,omitempty"`
	NoDefaultArgs    bool     `json:"no_default_args,omitempty"`
	SpawnOrigin      string   `json:"spawn_origin,omitempty"`
	SpawnDepth       int      `json:"spawn_depth,omitempty"`
	// AdoptionMode records how this agent became part of the visible runtime:
	// managed_window, managed_current_window, managed_session, bare_agent_up,
	// external, unmanaged. Status treats missing/unknown values fail-closed for
	// operator-visible lead claims.
	AdoptionMode string `json:"adoption_mode,omitempty"`
	// LauncherPaneID is the tmux pane that initiated the launch when known.
	// Comparing it to Tmux.PaneID lets status detect same-pane collapse.
	LauncherPaneID string `json:"launcher_pane_id,omitempty"`
	// NoRequireWake records the --no-require-wake opt-out so resume/replay
	// reproduces it: the constraint it answers (wake cannot acquire its lock
	// in this environment) is a property of the execution environment, not a
	// one-shot launch decision.
	NoRequireWake bool `json:"no_require_wake,omitempty"`
	// NoGitignore records the AMQ 0.40.0 --no-gitignore opt-out so resume
	// reproduces launches that intentionally leave .gitignore untouched during
	// AMQ coop auto-init.
	NoGitignore bool `json:"no_gitignore,omitempty"`
	// Symphony records that this Codex launch opted into AMQ's Symphony
	// WORKFLOW.md lifecycle hook integration. The hook patches the user's
	// project working tree and is replayed only when this explicit bit is set.
	Symphony bool `json:"symphony,omitempty"`
	// NoWakeReason records the explicit compatibility reason for adopting a
	// project lead without wake. It is intentionally separate from
	// NoRequireWake: --no-require-wake weakens wake startup strictness, while
	// --no-wake skips the sidecar entirely and needs an auditable reason.
	NoWakeReason string `json:"no_wake_reason,omitempty"`
	External     bool   `json:"external,omitempty"`
	// GoalBinding records launch-time evidence that this agent was started with
	// a native goal command. It is optional and additive: older records omit it,
	// and NOC/status surfaces must fall back to AMQ task + brief binding unless
	// a visible lead record carries native evidence.
	GoalBinding *GoalBinding `json:"goal_binding,omitempty"`
	// PreauthorizedActions records the in-scope worker actions amq-squad
	// pre-authorized at launch (#296) — the Claude --allowedTools patterns that
	// let an orchestrated worker create its PR without a permission prompt. It is
	// audit evidence of exactly what was granted; feature-branch push (future
	// work), main-branch push, tags, releases, and destructive git are never in
	// this list. Additive and omitted for legacy records and launches where no
	// pre-authorization applied.
	PreauthorizedActions []string `json:"preauthorized_actions,omitempty"`
	// WakeInjectVia and WakeInjectArgs record AMQ 0.37.0 external wake
	// injector settings so resume/replay can repair and restart the same
	// digest-bound wake target later.
	WakeInjectVia  string   `json:"wake_inject_via,omitempty"`
	WakeInjectArgs []string `json:"wake_inject_args,omitempty"`
	// WakeInjectCmd records the literal instruction the wake sidecar injects on
	// each durable-message arrival (amq wake --inject-cmd). amq-squad sets it to
	// the standard drain instruction so an inbound directive re-engages a lead
	// even after its native /goal reaches a terminal state, via AMQ's sanctioned
	// injector rather than a raw tmux send-keys. It is set only on the external
	// wake path (lead register / register-orchestrator); amq coop exec has no
	// --inject-cmd, so coop-exec restore cannot replay it. Resume repair is via
	// re-running those register commands, which reapply the instruction; this
	// persisted value is durable evidence of the configured injection. Additive:
	// older records omit it.
	WakeInjectCmd string    `json:"wake_inject_cmd,omitempty"`
	WakePID       int       `json:"wake_pid,omitempty"`
	AgentPID      int       `json:"agent_pid,omitempty"`
	AgentTTY      string    `json:"agent_tty,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	// TeamProfile names the profile the launch was emitted from. Empty
	// means the implicit default profile. Captured so status / bootstrap
	// routing can reuse the same profile without rereading flags.
	TeamProfile string `json:"team_profile,omitempty"`
	// Tmux is the tmux runtime identity captured at launch time when
	// amq-squad runs inside tmux. nil when launched outside tmux (or when
	// tmux metadata could not be resolved). Clients detect runtime-control
	// availability by the presence of this object, not by Schema.
	Tmux *TmuxInfo `json:"tmux,omitempty"`
}

// GoalBinding is launch-time evidence for a visible lead's goal binding.
type GoalBinding struct {
	Mode       string `json:"mode"`
	NativeGoal bool   `json:"native_goal"`
	Source     string `json:"source"`
	Command    string `json:"command,omitempty"`
	Detail     string `json:"detail,omitempty"`
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

// Write atomically writes the record into the agent's amq-squad extension dir.
// The agent mailbox is expected to exist (coop exec creates it), but Write
// also creates missing parents so the record can be written pre-exec.
func Write(agentDir string, rec Record) error {
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

// ScanRestorableEntries returns launch records plus best-effort records
// inferred from older AMQ mailboxes that predate amq-squad launch.json.
func ScanRestorableEntries(projectRoot string) ([]Entry, error) {
	return ScanRestorableEntriesInRoot(projectRoot, filepath.Join(projectRoot, ".agent-mail"))
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

// ScanLegacyEntries infers restorable launches from AMQ agent mailbox
// directories that do not have launch.json. The binary is inferred from the
// handle, which matches AMQ's default handle derivation for claude/codex.
func ScanLegacyEntries(projectRoot string) ([]Entry, error) {
	return ScanLegacyEntriesInRoot(projectRoot, filepath.Join(projectRoot, ".agent-mail"))
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

// Scan walks a projectRoot for launch.json records across AMQ layouts:
//
//	<projectRoot>/.agent-mail/<session>/agents/<handle>/extensions/<layer>/launch.json
//	<projectRoot>/.agent-mail/agents/<handle>/extensions/<layer>/launch.json
//	<projectRoot>/.agent-mail/<session>/agents/<handle>/launch.json
//	<projectRoot>/.agent-mail/agents/<handle>/launch.json
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
