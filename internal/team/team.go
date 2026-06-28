// Package team persists the set of agents the user wants booted for a
// project. It's a thin wrapper around a JSON file at <project>/.amq-squad/team.json.
package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func sortStrings(s []string) { sort.Strings(s) }

const (
	SchemaVersion         = 3
	DirName               = ".amq-squad"
	FileName              = "team.json"
	TeamsDirName          = "teams"
	RolesDirName          = "roles"
	DefaultOperatorHandle = "user"
	// DefaultProfile names the implicit project-default profile. It maps to
	// .amq-squad/team.json; a file at .amq-squad/teams/default.json is never
	// created (the on-disk encoding is the project root, not the teams dir).
	DefaultProfile = "default"
)

// Member is one row of the team: a role picked from the catalog plus the
// overrides the user chose at team init time.
//
// CWD is the working directory this agent runs from. Empty means "same as
// the team's project dir". Members can live in different directories; the
// team-home (where team.json lives) is just one of them.
//
// Session stores the member's default workstream hint. Current launch commands
// may override it with a shared workstream for the whole team run.
type Member struct {
	Role    string `json:"role"`    // catalog role ID, e.g. "cpo"
	Binary  string `json:"binary"`  // "claude" or "codex"
	Handle  string `json:"handle"`  // AMQ handle, defaults to Role
	Session string `json:"session"` // AMQ workstream session name
	Model   string `json:"model,omitempty"`
	CWD     string `json:"cwd,omitempty"`
	// SpawnOrigin and SpawnDepth record how runtime composition added this
	// member. Seeded/static members default to depth 0. Runtime children added
	// by the orchestration lead are depth 1 under the default cap.
	SpawnOrigin string `json:"spawn_origin,omitempty"`
	SpawnDepth  int    `json:"spawn_depth,omitempty"`
	// Launcher is an optional wrapper command exec'd in place of Binary while
	// the member still receives AMQ identity, bootstrap, and a launch record.
	// LauncherArgs precede the agent's normal child args; the launcher is
	// expected to forward the trailing args to Binary so bootstrap survives.
	Launcher     string   `json:"launcher,omitempty"`
	LauncherArgs []string `json:"launcher_args,omitempty"`
	// ClaudeArgs / CodexArgs are optional per-member native CLI args (e.g. a
	// `--settings` overlay that trims a worker's plugin/hook surface). They
	// are appended AFTER the team-level binary_args for the member's binary,
	// so the member-specific value wins by position. Only the field matching
	// the member's binary may be set — claude_args on a codex member (and
	// vice versa) is rejected at validation so flipping a member's binary can
	// never silently apply stale flags.
	ClaudeArgs []string `json:"claude_args,omitempty"`
	CodexArgs  []string `json:"codex_args,omitempty"`
}

// ExtraArgs returns the per-member native CLI args that match the member's
// binary (claude_args for claude, codex_args for codex). Nil when the binary
// matches neither or the matching field is empty; otherwise a copy, so
// callers can append without mutating the profile.
func (m Member) ExtraArgs() []string {
	var args []string
	switch strings.ToLower(strings.TrimSpace(m.Binary)) {
	case "claude":
		args = m.ClaudeArgs
	case "codex":
		args = m.CodexArgs
	}
	if len(args) == 0 {
		return nil
	}
	return append([]string(nil), args...)
}

// OperatorConfig describes the optional human/operator participant for a
// profile. The operator is a mailbox participant only, never a runnable member.
type OperatorConfig struct {
	Enabled bool   `json:"enabled"`
	Handle  string `json:"handle,omitempty"`
}

// OperatorView is the JSON/output shape used by callers that need the
// effective operator contract without interpreting on-disk schema details.
type OperatorView struct {
	Enabled  bool   `json:"enabled"`
	Handle   string `json:"handle,omitempty"`
	Runnable bool   `json:"runnable"`
}

const (
	CompositionSeeded     = "seeded"
	CompositionAutonomous = "autonomous"
)

type AutonomousPolicy struct {
	MaxActiveAgents    int             `json:"max_active_agents"`
	MaxTotalSpawns     int             `json:"max_total_spawns"`
	AllowedRoles       []string        `json:"allowed_roles,omitempty"`
	AllowedRoleClasses []string        `json:"allowed_role_classes,omitempty"`
	BudgetTurns        int             `json:"budget_turns"`
	IdleReapMinutes    int             `json:"idle_reap_minutes,omitempty"`
	Paused             bool            `json:"paused,omitempty"`
	Disabled           bool            `json:"disabled,omitempty"`
	State              AutonomousState `json:"state,omitempty"`
}

type AutonomousState struct {
	TotalSpawns     int `json:"total_spawns,omitempty"`
	BudgetTurnsUsed int `json:"budget_turns_used,omitempty"`
}

type AutonomousStatus struct {
	Composition      string            `json:"composition"`
	Enabled          bool              `json:"enabled"`
	Paused           bool              `json:"paused,omitempty"`
	Disabled         bool              `json:"disabled,omitempty"`
	Policy           *AutonomousPolicy `json:"policy,omitempty"`
	ActiveAgents     int               `json:"active_agents"`
	MaxActiveAgents  int               `json:"max_active_agents,omitempty"`
	TotalSpawns      int               `json:"total_spawns,omitempty"`
	MaxTotalSpawns   int               `json:"max_total_spawns,omitempty"`
	BudgetTurnsUsed  int               `json:"budget_turns_used,omitempty"`
	BudgetTurns      int               `json:"budget_turns,omitempty"`
	BudgetTurnsLeft  int               `json:"budget_turns_left,omitempty"`
	OperatorRequired []string          `json:"operator_required,omitempty"`
}

// Capabilities is derived client metadata. It is intentionally not persisted
// in team.json so hand-edited configs cannot drift.
type Capabilities struct {
	OperatorGates bool `json:"operator_gates"`
	// RuntimeActions advertises that this amq-squad build exposes the tmux
	// runtime contract clients (amq-noc) consume: per-member tmux identity +
	// pane_alive in status/history/resume --json, the status `actions` array,
	// and the focus/open/send control verbs. Always true since v1.5.0; a client
	// can gate its runtime-action UI on it instead of sniffing for fields.
	RuntimeActions bool `json:"runtime_actions"`
	// AutonomousGuardrails advertises that runtime composition carries
	// bodies-are-data guidance plus spawn-depth/origin metadata.
	AutonomousGuardrails bool `json:"autonomous_guardrails"`
}

// EffectiveCWD returns the member's working directory, falling back to the
// team's project dir when CWD is empty.
func (m Member) EffectiveCWD(projectDir string) string {
	if m.CWD != "" {
		return m.CWD
	}
	return projectDir
}

// Team is the persisted team config.
//
// Project is not serialized: it's always the directory that contains
// .amq-squad/team.json, derived at Read time. Persisting an absolute path
// would leak local paths into shared repos and break when the repo moves.
// CreatedAt is informational. Member sessions are legacy/default workstream
// hints; the live workstream can be overridden at launch time. Trust controls
// Codex trust defaults for generated launch commands ("sandboxed" or
// "trusted"; empty means sandboxed). BinaryArgs stores extra native CLI args
// by binary name, for example codex or claude.
//
// Workstream is a DEPRECATED SHIM (scheduled for removal in 2.1). It once
// pinned the team's default shared AMQ session, but it is no longer
// auto-stamped on init and member-session inference now wins over it. The
// field is still read so old team.json files keep loading; when it is the
// resolved source the CLI emits a deprecation notice steering operators to
// pass --session explicitly or re-init.
type Team struct {
	Schema  int    `json:"schema"`
	Project string `json:"-"`
	// Workstream: deprecated shim, see the Team doc comment. Removal in 2.1.
	Workstream string              `json:"workstream,omitempty"`
	Trust      string              `json:"trust,omitempty"`
	Operator   *OperatorConfig     `json:"operator,omitempty"`
	BinaryArgs map[string][]string `json:"binary_args,omitempty"`
	Members    []Member            `json:"members"`
	// Orchestrated marks this squad as lead-agent orchestrated: one member (Lead)
	// drives the others as children over amq-squad's runtime primitives. Optional,
	// default off; the generated team-rules.md gains the orchestration reporting
	// norm only when it is true. Lead names the lead role (a member role, never the
	// operator/NOC) and is required when Orchestrated is set.
	Orchestrated bool              `json:"orchestrated,omitempty"`
	Lead         string            `json:"lead,omitempty"`
	Composition  string            `json:"composition,omitempty"`
	Autonomous   *AutonomousPolicy `json:"autonomous,omitempty"`
	// ExecutionMode records the operator-visible ownership contract for this
	// profile. Empty means callers apply the compatibility default.
	ExecutionMode     string `json:"execution_mode,omitempty"`
	ControlRoot       string `json:"control_root,omitempty"`
	TargetProjectRoot string `json:"target_project_root,omitempty"`
	TargetContract    string `json:"target_contract,omitempty"`
	// MaxSpawnDepth caps runtime composition fan-out. Zero means the safe
	// default of 1: the operator-launched lead may add direct children, but
	// children cannot add grandchildren.
	MaxSpawnDepth int       `json:"max_spawn_depth,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

func DefaultOperator() OperatorConfig {
	return OperatorConfig{Enabled: true, Handle: DefaultOperatorHandle}
}

func DisabledOperator() OperatorConfig {
	return OperatorConfig{Enabled: false}
}

// EffectiveOperator returns the handle users should expect when a profile has
// no explicit operator config. Missing operator means the compatibility
// default: an implicit non-runnable "user" mailbox. Schema-3 profiles opt out
// explicitly with operator.enabled=false.
func EffectiveOperator(t Team) OperatorView {
	if t.Operator == nil {
		return OperatorView{Enabled: true, Handle: DefaultOperatorHandle, Runnable: false}
	}
	op := *t.Operator
	if !op.Enabled {
		return OperatorView{Enabled: false, Runnable: false}
	}
	handle := strings.TrimSpace(op.Handle)
	if handle == "" {
		handle = DefaultOperatorHandle
	}
	return OperatorView{Enabled: true, Handle: handle, Runnable: false}
}

// SupportsOperatorGates reports whether this profile speaks the operator-gate
// protocol. Legacy schema-1/2 files have no operator field, so they keep the
// implicit "user" gate until rewritten. Schema-3 profiles use
// operator.enabled=false as the explicit opt-out.
func SupportsOperatorGates(t Team) bool {
	return t.Operator == nil || t.Operator.Enabled
}

func EffectiveCapabilities(t Team) Capabilities {
	return Capabilities{
		OperatorGates:        SupportsOperatorGates(t),
		RuntimeActions:       true, // every v1.5.0+ build exposes the runtime contract
		AutonomousGuardrails: true,
	}
}

func EffectiveMaxSpawnDepth(t Team) int {
	if t.MaxSpawnDepth > 0 {
		return t.MaxSpawnDepth
	}
	return 1
}

func EffectiveComposition(t Team) string {
	if strings.TrimSpace(t.Composition) == "" {
		return CompositionSeeded
	}
	return t.Composition
}

func EffectiveAutonomousStatus(t Team) AutonomousStatus {
	composition := EffectiveComposition(t)
	status := AutonomousStatus{
		Composition:      composition,
		Enabled:          composition == CompositionAutonomous && t.Autonomous != nil && !t.Autonomous.Disabled,
		ActiveAgents:     len(t.Members),
		OperatorRequired: []string{"merge", "push", "release", "destructive-filesystem", "external-communication", "provider-side-effect"},
	}
	if t.Autonomous == nil {
		return status
	}
	p := *t.Autonomous
	p.AllowedRoles = append([]string(nil), p.AllowedRoles...)
	p.AllowedRoleClasses = append([]string(nil), p.AllowedRoleClasses...)
	status.Policy = &p
	status.Paused = p.Paused
	status.Disabled = p.Disabled
	status.MaxActiveAgents = p.MaxActiveAgents
	status.TotalSpawns = p.State.TotalSpawns
	status.MaxTotalSpawns = p.MaxTotalSpawns
	status.BudgetTurnsUsed = p.State.BudgetTurnsUsed
	status.BudgetTurns = p.BudgetTurns
	if p.BudgetTurns > 0 {
		left := p.BudgetTurns - p.State.BudgetTurnsUsed
		if left < 0 {
			left = 0
		}
		status.BudgetTurnsLeft = left
	}
	return status
}

// Path returns the team.json path for the default profile under projectDir.
// It is preserved for compatibility with callers that don't care about
// non-default profiles; use ProfilePath when a profile name is in play.
func Path(projectDir string) string {
	return ProfilePath(projectDir, DefaultProfile)
}

// ProfilePath returns the team.json path for the given profile under
// projectDir. The default profile (or empty string) maps to
// <projectDir>/.amq-squad/team.json. Named profiles map to
// <projectDir>/.amq-squad/teams/<profile>.json.
func ProfilePath(projectDir, profile string) string {
	if profile == "" || profile == DefaultProfile {
		return filepath.Join(projectDir, DirName, FileName)
	}
	return filepath.Join(projectDir, DirName, TeamsDirName, profile+".json")
}

// RolesDir returns the directory that holds authored custom-role documents,
// <projectDir>/.amq-squad/roles. Each custom role staged from a file lands at
// <RolesDir>/<id>.md and is consulted when seeding an agent's role.md.
func RolesDir(projectDir string) string {
	return filepath.Join(projectDir, DirName, RolesDirName)
}

// CustomRolePath returns the staged role.md path for a custom role id.
func CustomRolePath(projectDir, id string) string {
	return filepath.Join(RolesDir(projectDir), id+".md")
}

// ValidateProfileName enforces the profile-name slug rules: lowercase a-z,
// 0-9, hyphen, and underscore. The implicit "default" profile name is
// permitted by callers but does not need to match these rules; the on-disk
// encoding for "default" lives outside the teams/ directory.
func ValidateProfileName(s string) error {
	return validateSlug("profile name", s, true)
}

// Read loads the default-profile team config from projectDir. Returns
// os.ErrNotExist if no team.json is present.
func Read(projectDir string) (Team, error) {
	return ReadProfile(projectDir, DefaultProfile)
}

// ReadProfile loads a named profile from projectDir.
func ReadProfile(projectDir, profile string) (Team, error) {
	if profile != "" && profile != DefaultProfile {
		if err := ValidateProfileName(profile); err != nil {
			return Team{}, err
		}
	}
	p := ProfilePath(projectDir, profile)
	b, err := os.ReadFile(p)
	if err != nil {
		return Team{}, err
	}
	var t Team
	if err := json.Unmarshal(b, &t); err != nil {
		return Team{}, fmt.Errorf("parse %s: %w", p, err)
	}
	t.Project = projectDir
	if err := Validate(t); err != nil {
		return Team{}, fmt.Errorf("validate %s: %w", p, err)
	}
	return t, nil
}

// Write atomically persists the default-profile team config under projectDir.
func Write(projectDir string, t Team) error {
	return WriteProfile(projectDir, DefaultProfile, t)
}

// NormalizeForWrite returns the exact profile shape amq-squad persists:
// current schema, default operator filled in, created_at set, and validated.
func NormalizeForWrite(projectDir, profile string, t Team) (Team, error) {
	if profile != "" && profile != DefaultProfile {
		if err := ValidateProfileName(profile); err != nil {
			return Team{}, err
		}
	}
	t.Schema = SchemaVersion
	t.Project = projectDir
	if t.Operator == nil {
		op := DefaultOperator()
		t.Operator = &op
	} else if t.Operator.Enabled && strings.TrimSpace(t.Operator.Handle) == "" {
		t.Operator.Handle = DefaultOperatorHandle
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if err := Validate(t); err != nil {
		return Team{}, fmt.Errorf("validate team: %w", err)
	}
	return t, nil
}

// WriteProfile atomically persists a named profile under projectDir. The
// schema field is unconditionally set to the current SchemaVersion so
// reading a schema 1 file and writing it back upgrades the on-disk shape.
func WriteProfile(projectDir, profile string, t Team) error {
	path := ProfilePath(projectDir, profile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure %s: %w", filepath.Dir(path), err)
	}
	var err error
	t, err = NormalizeForWrite(projectDir, profile, t)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal team: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Exists reports whether a default-profile team.json is present.
func Exists(projectDir string) bool {
	return ExistsProfile(projectDir, DefaultProfile)
}

// ExistsProfile reports whether a named profile config exists.
func ExistsProfile(projectDir, profile string) bool {
	_, err := os.Stat(ProfilePath(projectDir, profile))
	return err == nil
}

// DeleteProfile removes one persisted team profile. It only deletes the profile
// JSON file: AMQ sessions, briefs, team-rules.md, and managed pointer stubs are
// intentionally left for their own explicit commands.
func DeleteProfile(projectDir, profile string) error {
	if profile != "" && profile != DefaultProfile {
		if err := ValidateProfileName(profile); err != nil {
			return err
		}
	}
	path := ProfilePath(projectDir, profile)
	if err := os.Remove(path); err != nil {
		return err
	}
	if profile != "" && profile != DefaultProfile {
		_ = os.Remove(filepath.Dir(path))
	}
	return nil
}

// ListProfiles returns the named profiles present under projectDir, sorted
// alphabetically. The default profile is NOT included; callers prepend
// "default" themselves when the default file exists. Returns nil with no
// error when no teams/ directory exists.
func ListProfiles(projectDir string) ([]string, error) {
	dir := filepath.Join(projectDir, DirName, TeamsDirName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		profile := strings.TrimSuffix(name, ".json")
		if profile == "" || profile == DefaultProfile {
			continue
		}
		if err := ValidateProfileName(profile); err != nil {
			continue
		}
		out = append(out, profile)
	}
	sortStrings(out)
	return out, nil
}

func Validate(t Team) error {
	if t.Workstream != "" {
		if err := ValidateSessionName(t.Workstream); err != nil {
			return fmt.Errorf("workstream: %w", err)
		}
	}
	if t.Trust != "" && t.Trust != "sandboxed" && t.Trust != "approve-for-me" && t.Trust != "trusted" {
		return fmt.Errorf("trust: invalid trust mode %q: use sandboxed, approve-for-me, or trusted", t.Trust)
	}
	if t.MaxSpawnDepth < 0 {
		return fmt.Errorf("max_spawn_depth: cannot be negative")
	}
	if err := validateComposition(t); err != nil {
		return err
	}
	operatorHandle := ""
	if t.Operator == nil {
		operatorHandle = DefaultOperatorHandle
	} else {
		if t.Operator.Enabled {
			operatorHandle = strings.TrimSpace(t.Operator.Handle)
			if operatorHandle == "" {
				operatorHandle = DefaultOperatorHandle
			}
			if err := ValidateHandle(operatorHandle); err != nil {
				return fmt.Errorf("operator.handle: %w", err)
			}
		} else if strings.TrimSpace(t.Operator.Handle) != "" {
			return fmt.Errorf("operator.handle: set enabled=true before handle")
		}
	}
	for binary, args := range t.BinaryArgs {
		if err := ValidateDisplayValue("binary_args key", binary); err != nil {
			return fmt.Errorf("binary_args[%q]: %w", binary, err)
		}
		for i, arg := range args {
			if err := ValidateDisplayValue("arg", arg); err != nil {
				return fmt.Errorf("binary_args[%q][%d]: %w", binary, i, err)
			}
		}
	}
	if err := validateOrchestration(t); err != nil {
		return err
	}
	seenHandles := map[string]bool{}
	for i, m := range t.Members {
		prefix := fmt.Sprintf("members[%d]", i)
		if err := validateMember(prefix, m); err != nil {
			return err
		}
		handle := m.Handle
		if handle == "" {
			handle = m.Role
		}
		if handle != "" {
			if operatorHandle != "" {
				conflictsOperator := handle == operatorHandle
				if operatorHandle == DefaultOperatorHandle && m.Role == DefaultOperatorHandle {
					conflictsOperator = true
				}
				if conflictsOperator {
					return fmt.Errorf("%s: runnable member %q conflicts with non-runnable operator handle %q", prefix, handle, operatorHandle)
				}
			}
			if seenHandles[handle] {
				return fmt.Errorf("%s: duplicate handle %q", prefix, handle)
			}
			seenHandles[handle] = true
		}
	}
	return nil
}

func validateComposition(t Team) error {
	mode := EffectiveComposition(t)
	switch mode {
	case CompositionSeeded:
		if t.Autonomous != nil {
			return fmt.Errorf("autonomous: set composition=%q before configuring autonomous policy", CompositionAutonomous)
		}
		return nil
	case CompositionAutonomous:
		if !t.Orchestrated {
			return fmt.Errorf("composition: autonomous requires orchestrated=true")
		}
		if t.Autonomous == nil {
			return fmt.Errorf("autonomous: policy is required when composition=autonomous")
		}
		return ValidateAutonomousPolicy(*t.Autonomous)
	default:
		return fmt.Errorf("composition: invalid mode %q: use %s or %s", t.Composition, CompositionSeeded, CompositionAutonomous)
	}
}

func ValidateAutonomousPolicy(p AutonomousPolicy) error {
	if p.MaxActiveAgents <= 0 {
		return fmt.Errorf("autonomous.max_active_agents: must be positive")
	}
	if p.MaxTotalSpawns <= 0 {
		return fmt.Errorf("autonomous.max_total_spawns: must be positive")
	}
	if p.BudgetTurns <= 0 {
		return fmt.Errorf("autonomous.budget_turns: must be positive")
	}
	if p.IdleReapMinutes < 0 {
		return fmt.Errorf("autonomous.idle_reap_minutes: cannot be negative")
	}
	if len(p.AllowedRoles) == 0 && len(p.AllowedRoleClasses) == 0 {
		return fmt.Errorf("autonomous.allowed_roles: at least one allowed role or role class is required")
	}
	for i, role := range p.AllowedRoles {
		if err := ValidateRoleID(role); err != nil {
			return fmt.Errorf("autonomous.allowed_roles[%d]: %w", i, err)
		}
	}
	for i, class := range p.AllowedRoleClasses {
		if err := validateSlug("role class", class, true); err != nil {
			return fmt.Errorf("autonomous.allowed_role_classes[%d]: %w", i, err)
		}
	}
	if p.State.TotalSpawns < 0 {
		return fmt.Errorf("autonomous.state.total_spawns: cannot be negative")
	}
	if p.State.BudgetTurnsUsed < 0 {
		return fmt.Errorf("autonomous.state.budget_turns_used: cannot be negative")
	}
	return nil
}

// validateOrchestration enforces the lead/orchestrated contract: a lead must be
// a valid role slug that names an actual team member, and an orchestrated team
// must declare exactly one lead. The lead is always a runnable member role, so
// it can never be the non-runnable operator/NOC handle (that conflict is already
// rejected by the member/operator handle check).
func validateOrchestration(t Team) error {
	if t.Orchestrated && strings.TrimSpace(t.Lead) == "" {
		return fmt.Errorf("orchestrated: a lead role is required when orchestrated is true")
	}
	if t.Lead == "" {
		return nil
	}
	// A lead is only meaningful for an orchestrated team. Reject the half-state
	// (lead set, orchestrated off) so it cannot persist via hand-edit/API and
	// surface a lead in JSON plans while the orchestration norm stays off.
	if !t.Orchestrated {
		return fmt.Errorf("lead: set orchestrated=true to name a lead")
	}
	// Validate the persisted value directly (not a trimmed copy) so a
	// non-canonical lead (surrounding whitespace, uppercase) cannot be written
	// or loaded and then leak into JSON plans. The CLI already lowercases and
	// trims --lead before write, so this only bites hand-edited/API configs.
	if err := ValidateRoleID(t.Lead); err != nil {
		return fmt.Errorf("lead: %w", err)
	}
	// Exactly one lead: count matching member roles so a hand-authored config
	// with a duplicated role can't make `lead` name two runnable members.
	matches := 0
	for _, m := range t.Members {
		if m.Role == t.Lead {
			matches++
		}
	}
	switch matches {
	case 0:
		return fmt.Errorf("lead: role %q is not a team member", t.Lead)
	case 1:
		return nil
	default:
		return fmt.Errorf("lead: role %q names %d members; the lead must be exactly one member", t.Lead, matches)
	}
}

func validateMember(prefix string, m Member) error {
	if m.Role == "" {
		return fmt.Errorf("%s.role: cannot be empty", prefix)
	}
	if err := ValidateRoleID(m.Role); err != nil {
		return fmt.Errorf("%s.role: %w", prefix, err)
	}
	if m.Handle != "" {
		if err := ValidateHandle(m.Handle); err != nil {
			return fmt.Errorf("%s.handle: %w", prefix, err)
		}
	}
	if m.Session != "" {
		if err := ValidateSessionName(m.Session); err != nil {
			return fmt.Errorf("%s.session: %w", prefix, err)
		}
	}
	if m.Binary != "" {
		if err := ValidateDisplayValue("binary", m.Binary); err != nil {
			return fmt.Errorf("%s.binary: %w", prefix, err)
		}
	}
	if m.Model != "" {
		if err := ValidateDisplayValue("model", m.Model); err != nil {
			return fmt.Errorf("%s.model: %w", prefix, err)
		}
	}
	if m.CWD != "" {
		if err := ValidateDisplayValue("cwd", m.CWD); err != nil {
			return fmt.Errorf("%s.cwd: %w", prefix, err)
		}
		if !filepath.IsAbs(m.CWD) {
			return fmt.Errorf("%s.cwd: must be absolute", prefix)
		}
	}
	if m.SpawnOrigin != "" {
		if err := ValidateDisplayValue("spawn_origin", m.SpawnOrigin); err != nil {
			return fmt.Errorf("%s.spawn_origin: %w", prefix, err)
		}
	}
	if m.SpawnDepth < 0 {
		return fmt.Errorf("%s.spawn_depth: cannot be negative", prefix)
	}
	if m.Launcher != "" {
		if err := ValidateDisplayValue("launcher", m.Launcher); err != nil {
			return fmt.Errorf("%s.launcher: %w", prefix, err)
		}
		if !filepath.IsAbs(m.Launcher) {
			return fmt.Errorf("%s.launcher: must be absolute", prefix)
		}
	}
	for i, a := range m.LauncherArgs {
		if err := ValidateDisplayValue("launcher_args", a); err != nil {
			return fmt.Errorf("%s.launcher_args[%d]: %w", prefix, i, err)
		}
	}
	if m.Launcher == "" && len(m.LauncherArgs) > 0 {
		return fmt.Errorf("%s.launcher_args: set launcher before launcher_args", prefix)
	}
	for i, a := range m.ClaudeArgs {
		if err := ValidateDisplayValue("claude_args", a); err != nil {
			return fmt.Errorf("%s.claude_args[%d]: %w", prefix, i, err)
		}
	}
	for i, a := range m.CodexArgs {
		if err := ValidateDisplayValue("codex_args", a); err != nil {
			return fmt.Errorf("%s.codex_args[%d]: %w", prefix, i, err)
		}
	}
	// Binary-match contract: per-member args are bound to the binary they
	// configure. Rejecting the mismatch (instead of silently ignoring it)
	// means a member whose binary flips later can never carry stale flags.
	bin := strings.ToLower(strings.TrimSpace(m.Binary))
	if len(m.ClaudeArgs) > 0 && bin != "claude" {
		return fmt.Errorf("%s.claude_args: member binary is %q; claude_args applies only to claude members", prefix, m.Binary)
	}
	if len(m.CodexArgs) > 0 && bin != "codex" {
		return fmt.Errorf("%s.codex_args: member binary is %q; codex_args applies only to codex members", prefix, m.Binary)
	}
	return nil
}

func ValidateRoleID(s string) error {
	return validateSlug("role", s, true)
}

func ValidateHandle(s string) error {
	return validateSlug("handle", s, true)
}

func ValidateSessionName(s string) error {
	return validateSlug("session name", s, true)
}

func validateSlug(label, s string, allowHyphen bool) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || allowHyphen && r == '-' {
			continue
		}
		return fmt.Errorf("invalid %s %q: use lowercase a-z, 0-9, - and _ only", label, s)
	}
	return nil
}

func ValidateDisplayValue(label, s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains control characters", label)
		}
	}
	return nil
}
