// Package team is the in-memory form of a repository's squad.yaml (brief
// 2b-5): squad.go holds the schema 1 file, this file the resolved team the
// CLI acts on. There is one squad per repository, at <repo root>/squad.yaml;
// the v2 profile model (.amq-squad/team.json, .amq-squad/teams/<name>.json,
// schema 3 to 6, roster overlays) is gone. A v2 checkout is migrated by
// phase 5's `migrate`.
package team

import (
	"errors"
	"fmt"
	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/grants"
)

const (
	DirName               = ".amq-squad"
	RolesDirName          = "roles"
	DefaultOperatorHandle = "user"
	// defaultProfile is namespace.DefaultProfile (namespace imports team).
	defaultProfile          = "default"
	ActorModeImplementation = "implementation"
	ActorModeReview         = "review"
	LeadModeBuilder         = "builder"
	LeadModePlanner         = "planner"
)

const (
	ToolProfileMinimal = "minimal"
	ToolProfileCoding  = "coding"
	ToolProfileBrowser = "browser"
	ToolProfileData    = "data"
	ToolProfileFull    = "full"
)

const (
	OperatorInteractionUnspecified      = "unspecified"
	OperatorInteractionLeadPane         = "lead_pane"
	OperatorInteractionSeparateTerminal = "separate_terminal"
	OperatorInteractionNOC              = "noc"
	// removedOperatorInteractionSelfOperator names the v2 mode 3.0 rejects.
	removedOperatorInteractionSelfOperator = "self_operator"
)

// ErrNamedProfile is a v2 named profile: 3.0 has one squad per repository.
var ErrNamedProfile = errors.New("named profiles have no squad config in amq-squad 3.0: a repository has one squad, <repo root>/squad.yaml; migrate a v2 profile with `amq-squad migrate` (phase 5)")

// Member is one resolved seat of squad.yaml.
type Member struct {
	Role        string `json:"role"`
	Binary      string `json:"binary"` // the seat runtime: claude, codex or pi
	Handle      string `json:"handle"`
	Session     string `json:"session"`
	Model       string `json:"model,omitempty"`
	Effort      string `json:"effort,omitempty"`
	CWD         string `json:"cwd,omitempty"` // absolute; "" is the repository root
	ActorMode   string `json:"actor_mode,omitempty"`
	ToolProfile string `json:"tool_profile,omitempty"`
}

// EffectiveToolProfile is the seat's tool policy; "" is full.
func (m Member) EffectiveToolProfile() string {
	if p := strings.TrimSpace(m.ToolProfile); p != "" {
		return p
	}
	return ToolProfileFull
}

// EffectiveCWD is the seat's working directory, the project when unset.
func (m Member) EffectiveCWD(projectDir string) string {
	if m.CWD != "" {
		return m.CWD
	}
	return projectDir
}

// Team is squad.yaml resolved against its repository root.
type Team struct {
	Project string `json:"-"` // the repository root that holds squad.yaml
	Session string `json:"session,omitempty"`
	// Lead is the lead seat's role and LeadHandle its handle (two seats may
	// share a role; the handle names the lead). Orchestrated is Lead != "".
	Lead                string   `json:"lead,omitempty"`
	LeadHandle          string   `json:"lead_handle,omitempty"`
	Orchestrated        bool     `json:"orchestrated,omitempty"`
	LeadMode            string   `json:"lead_mode,omitempty"`
	ExecutionMode       string   `json:"execution_mode,omitempty"`
	ControlRoot         string   `json:"control_root,omitempty"`
	TargetProjectRoot   string   `json:"target_project_root,omitempty"`
	SharedCwdException  string   `json:"shared_cwd_exception,omitempty"`
	OperatorInteraction string   `json:"operator_interaction,omitempty"`
	Members             []Member `json:"members"`
	// Squad keeps the file's grant narrowing and defaults for Write.
	Squad Squad `json:"-"`
}

// OperatorView is the owner's mailbox participant. It is never runnable.
type OperatorView struct {
	Enabled         bool   `json:"enabled"`
	Handle          string `json:"handle,omitempty"`
	InteractionMode string `json:"interaction_mode"`
	Participant     bool   `json:"participant"`
	Kind            string `json:"kind,omitempty"`
	Runnable        bool   `json:"runnable"`
	Assignable      bool   `json:"assignable"`
	WakeSupported   bool   `json:"wake_supported"`
	PollRequired    bool   `json:"poll_required"`
}

type OperatorInteractionContract struct {
	Mode            string
	ApprovalSurface string
	Contract        string
	PollRequired    bool
	PollOwner       string
}

// EffectiveOperator is the owner participant: handle user, the squad's
// interaction mode.
func EffectiveOperator(t Team) OperatorView {
	contract := OperatorContractForMode(t.OperatorInteraction)
	return OperatorView{Enabled: true, Handle: DefaultOperatorHandle, InteractionMode: contract.Mode, Participant: true, Kind: "operator", PollRequired: contract.PollRequired}
}

// OperatorContractForMode is the single semantic mapping consumed by team
// views, status, bootstrap, and operator-loop reporting.
func OperatorContractForMode(mode string) OperatorInteractionContract {
	switch mode = strings.TrimSpace(mode); mode {
	case OperatorInteractionLeadPane:
		return OperatorInteractionContract{Mode: mode, ApprovalSurface: "lead pane", Contract: "type in the lead pane; the lead mirrors decisions to gate threads", PollOwner: "none"}
	case OperatorInteractionSeparateTerminal:
		return OperatorInteractionContract{Mode: mode, ApprovalSurface: "separate operator terminal", Contract: "poll durable gates and answer them from the operator terminal", PollRequired: true, PollOwner: "operator"}
	case OperatorInteractionNOC:
		return OperatorInteractionContract{Mode: mode, ApprovalSurface: "NOC/global board", Contract: "the NOC/global orchestrator polls this run and answers durable gates by explicit namespace", PollRequired: true, PollOwner: "noc"}
	default:
		return OperatorInteractionContract{Mode: OperatorInteractionUnspecified, ApprovalSurface: "legacy operator mailbox", Contract: "legacy compatibility: operator or parent orchestrator polls durable AMQ gates", PollRequired: true, PollOwner: "operator_or_parent"}
	}
}

// ValidateOperatorInteractionMode validates an explicit interaction mode.
func ValidateOperatorInteractionMode(mode string) error {
	switch strings.TrimSpace(mode) {
	case OperatorInteractionLeadPane, OperatorInteractionSeparateTerminal, OperatorInteractionNOC:
		return nil
	case removedOperatorInteractionSelfOperator:
		return fmt.Errorf("operator interaction mode %q was removed in amq-squad 3.0: only the owner answers typed gates; use %s, %s, or %s", mode, OperatorInteractionLeadPane, OperatorInteractionSeparateTerminal, OperatorInteractionNOC)
	default:
		return fmt.Errorf("invalid operator interaction mode %q: use %s, %s, or %s", mode, OperatorInteractionLeadPane, OperatorInteractionSeparateTerminal, OperatorInteractionNOC)
	}
}

func EffectiveLeadMode(t Team) string {
	if strings.TrimSpace(t.LeadMode) == "" {
		return LeadModeBuilder
	}
	return strings.TrimSpace(t.LeadMode)
}

// EffectiveActorMode resolves the seat's actor-relative capability: explicit,
// else review for a planner lead, else implementation.
func EffectiveActorMode(t Team, m Member) string {
	if mode := strings.TrimSpace(m.ActorMode); mode != "" {
		return mode
	}
	if IsLead(t, m) && EffectiveLeadMode(t) == LeadModePlanner {
		return ActorModeReview
	}
	return ActorModeImplementation
}

// IsLead reports whether m is the lead seat: by handle when the squad names
// one, else (a hand-built Team) by role.
func IsLead(t Team, m Member) bool {
	if h := strings.TrimSpace(t.LeadHandle); h != "" {
		handle := m.Handle
		if handle == "" {
			handle = m.Role
		}
		return handle == h
	}
	return t.Lead != "" && strings.EqualFold(strings.TrimSpace(m.Role), strings.TrimSpace(t.Lead))
}

// RolesDir is <projectDir>/.amq-squad/roles, the staged custom roles.
func RolesDir(projectDir string) string { return filepath.Join(projectDir, DirName, RolesDirName) }

// CustomRolePath is the staged role.md path for a custom role id.
func CustomRolePath(projectDir, id string) string {
	return filepath.Join(RolesDir(projectDir), id+".md")
}

// FromSquad resolves a parsed squad.yaml against its repository root.
func FromSquad(projectDir string, s Squad) Team {
	t := Team{
		Project: projectDir, Session: s.Session, LeadMode: s.LeadMode, ExecutionMode: s.ExecutionMode,
		ControlRoot: s.ControlRoot, TargetProjectRoot: s.TargetProjectRoot, SharedCwdException: s.SharedCwdException,
		OperatorInteraction: s.OperatorInteraction, Squad: s,
	}
	for _, seat := range s.Resolved() {
		cwd := ""
		if seat.Worktree != "" {
			cwd = SeatWorktree(projectDir, seat)
		}
		if seat.Handle == s.Lead {
			t.Lead, t.LeadHandle, t.Orchestrated = seat.Role, seat.Handle, true
		}
		t.Members = append(t.Members, Member{
			Role: seat.Role, Binary: seat.Runtime, Handle: seat.Handle, Session: s.Session, Model: seat.Model,
			Effort: seat.Effort, CWD: cwd, ActorMode: seat.ActorMode, ToolProfile: seat.Tools,
		})
	}
	return t
}

// ToSquad is the squad.yaml form of t; the grant narrowing comes from
// t.Squad. Explicit seat fields replace the file's defaults.
func (t Team) ToSquad() Squad {
	s := t.Squad
	s.Schema, s.Defaults, s.Seats = SquadSchema, SeatDefaults{}, nil
	s.Session, s.LeadMode, s.ExecutionMode = t.Session, t.LeadMode, t.ExecutionMode
	s.ControlRoot, s.TargetProjectRoot, s.SharedCwdException = t.ControlRoot, t.TargetProjectRoot, t.SharedCwdException
	s.OperatorInteraction, s.Lead = t.OperatorInteraction, ""
	for _, m := range t.Members {
		handle := m.Handle
		if handle == "" {
			handle = m.Role
		}
		if s.Session == "" {
			s.Session = m.Session
		}
		if s.Lead == "" && IsLead(t, m) {
			s.Lead = handle
		}
		worktree := m.CWD
		if worktree != "" && filepath.Clean(worktree) == filepath.Clean(t.Project) {
			worktree = ""
		}
		s.Seats = append(s.Seats, Seat{Handle: handle, Role: m.Role, Runtime: m.Binary, Model: m.Model, Effort: m.Effort,
			Worktree: worktree, Tools: m.ToolProfile, ActorMode: m.ActorMode})
	}
	return s
}

// Read loads <projectDir>/squad.yaml against the owner grant. A missing file
// is fs.ErrNotExist.
func Read(projectDir string) (Team, error) {
	s, err := LoadSquad(projectDir)
	if err != nil {
		return Team{}, err
	}
	return FromSquad(projectDir, s), nil
}

// ReadFor is Read for a namespace profile: only the default profile has a
// squad config (ErrNamedProfile otherwise).
func ReadFor(projectDir, profile string) (Team, error) {
	if err := CheckProfile(profile); err != nil {
		return Team{}, err
	}
	return Read(projectDir)
}

// CheckProfile refuses a v2 named profile.
func CheckProfile(profile string) error {
	if p := strings.TrimSpace(profile); p != "" && p != defaultProfile {
		return fmt.Errorf("profile %q: %w", p, ErrNamedProfile)
	}
	return nil
}

// Exists reports whether <projectDir>/squad.yaml exists.
func Exists(projectDir string) bool {
	_, err := os.Stat(SquadPath(projectDir))
	return err == nil
}

// ExistsFor is Exists for a namespace profile: a named profile has none.
func ExistsFor(projectDir, profile string) bool {
	return CheckProfile(profile) == nil && Exists(projectDir)
}

// IsNotExist reports a missing squad.yaml.
func IsNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }

// Write renders t as squad.yaml, checks it against the owner grant (a
// widening field is an error), and writes it atomically under the lock.
func Write(projectDir string, t Team) error {
	return WithLock(projectDir, func() error { return WriteUnderLock(projectDir, t) })
}

// WithLock serializes squad.yaml writers and read-modify-write sections.
func WithLock(projectDir string, fn func() error) error {
	if err := os.MkdirAll(filepath.Join(projectDir, DirName), 0o755); err != nil {
		return err
	}
	return flock.WithLock(filepath.Join(projectDir, DirName, SquadFileName+".lock"), fn)
}

// WriteUnderLock writes while the caller holds WithLock.
func WriteUnderLock(projectDir string, t Team) error {
	t.Project = projectDir
	text := Render(t.ToSquad())
	owner, err := grants.Load("")
	if err != nil {
		return err
	}
	if _, err := ParseSquad(text, SquadPath(projectDir), owner.Grants); err != nil {
		return err
	}
	return WriteFileAtomic(SquadPath(projectDir), []byte(text))
}

// WriteFileAtomic writes through the shared durable writer (0644).
func WriteFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o644)
}

func ValidateRoleID(s string) error      { return validateSlug("role", s) }
func ValidateHandle(s string) error      { return validateSlug("handle", s) }
func ValidateSessionName(s string) error { return validateSlug("session name", s) }

func validateSlug(label, s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s cannot be empty", label)
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
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
