// Package worktreeplan owns the durable contract for isolated worker
// worktrees. The team profile remains the source of truth for member identity;
// this package records only the session-scoped Git materialization state.
package worktreeplan

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	SchemaVersion = 1
	DirName       = "worktrees"

	StatePlanned      State = "planned"
	StateMaterialized State = "materialized"
	StateActive       State = "active"
	StateHandoff      State = "handoff"
	StateCleaned      State = "cleaned"
	// StateUnplanned is an inspection-only projection. It is never accepted
	// in a durable Set record.
	StateUnplanned State = "unplanned"
)

type State string

func (s State) valid() bool {
	switch s {
	case StatePlanned, StateMaterialized, StateActive, StateHandoff, StateCleaned:
		return true
	default:
		return false
	}
}

// SharedCWDException is an explicit operator-authored exception to the
// otherwise mandatory isolated-index policy.
type SharedCWDException struct {
	Enabled   bool       `json:"enabled"`
	Reason    string     `json:"reason,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// Record is one mutation-capable member's durable worktree lifecycle.
type Record struct {
	Role       string   `json:"role"`
	Handle     string   `json:"handle"`
	Path       string   `json:"path"`
	Branch     string   `json:"branch"`
	BaseSHA    string   `json:"base_sha"`
	TaskID     string   `json:"task_id"`
	Scope      []string `json:"scope"`
	State      State    `json:"state"`
	HandoffSHA string   `json:"handoff_sha,omitempty"`

	RepoRoot         string     `json:"repo_root"`
	PreviousCWD      string     `json:"previous_cwd,omitempty"`
	PreviousExplicit bool       `json:"previous_cwd_explicit,omitempty"`
	CleanupDecision  string     `json:"cleanup_decision,omitempty"`
	CleanupStartedAt *time.Time `json:"cleanup_started_at,omitempty"`
	CreatedAt        *time.Time `json:"created_at,omitempty"`
	UpdatedAt        *time.Time `json:"updated_at,omitempty"`
}

// Set is the complete session-scoped materialization contract.
type Set struct {
	Schema             int                `json:"schema"`
	Profile            string             `json:"profile"`
	Session            string             `json:"session"`
	TeamHome           string             `json:"team_home"`
	ControlRoot        string             `json:"control_root"`
	AMQRoot            string             `json:"amq_root"`
	AcceptedBaseSHA    string             `json:"accepted_base_sha,omitempty"`
	SharedCWDException SharedCWDException `json:"shared_cwd_exception"`
	Plans              []Record           `json:"plans"`
	CreatedAt          *time.Time         `json:"created_at,omitempty"`
	UpdatedAt          *time.Time         `json:"updated_at,omitempty"`
}

// Request describes one deterministic plan/materialization request.
type Request struct {
	Role     string
	TaskID   string
	Base     string
	Scope    []string
	Path     string
	Branch   string
	RepoRoot string
	AMQRoot  string
}

type CleanupRequest struct {
	Role     string
	Decision string
}

type MemberStatus struct {
	Role                     string   `json:"role"`
	Handle                   string   `json:"handle"`
	CWD                      string   `json:"cwd"`
	Worktree                 string   `json:"worktree,omitempty"`
	Branch                   string   `json:"branch,omitempty"`
	BaseSHA                  string   `json:"base_sha,omitempty"`
	CurrentHEAD              string   `json:"current_head,omitempty"`
	Clean                    bool     `json:"clean"`
	Dirty                    bool     `json:"dirty"`
	Drifted                  bool     `json:"drifted"`
	Scope                    []string `json:"scope,omitempty"`
	TaskID                   string   `json:"task_id,omitempty"`
	State                    State    `json:"state,omitempty"`
	HandoffSHA               string   `json:"handoff_sha,omitempty"`
	HandoffValid             bool     `json:"handoff_valid"`
	Registered               bool     `json:"registered"`
	Index                    string   `json:"index,omitempty"`
	Detail                   string   `json:"detail,omitempty"`
	CoordinationRootDiverged bool     `json:"coordination_root_diverged"`
	Orphaned                 bool     `json:"orphaned"`
}

type DiagnosticStatus string

const (
	DiagnosticOK   DiagnosticStatus = "ok"
	DiagnosticWarn DiagnosticStatus = "warn"
	DiagnosticFail DiagnosticStatus = "fail"
)

type Diagnostic struct {
	Kind   string           `json:"kind"`
	Status DiagnosticStatus `json:"status"`
	Role   string           `json:"role,omitempty"`
	Detail string           `json:"detail"`
}

type Inspection struct {
	StorePath   string         `json:"store_path"`
	Exists      bool           `json:"exists"`
	Set         Set            `json:"set"`
	Members     []MemberStatus `json:"members"`
	Diagnostics []Diagnostic   `json:"diagnostics"`
}

func ControlRoot(t team.Team) string {
	if root := strings.TrimSpace(t.ControlRoot); root != "" {
		if !filepath.IsAbs(root) && strings.TrimSpace(t.Project) != "" {
			root = filepath.Join(t.Project, root)
		}
		return filepath.Clean(root)
	}
	return filepath.Clean(t.Project)
}

func StorePath(t team.Team, profile, session string) string {
	profile = squadnamespace.NormalizeProfile(profile)
	return filepath.Join(ControlRoot(t), team.DirName, DirName, profile, strings.TrimSpace(session)+".json")
}

func LockPath(t team.Team, profile, session string) string {
	return StorePath(t, profile, session) + ".lock"
}

func Read(t team.Team, profile, session string) (Set, bool, error) {
	path := StorePath(t, profile, session)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Set{}, false, nil
	}
	if err != nil {
		return Set{}, false, fmt.Errorf("read worktree plan %s: %w", path, err)
	}
	var set Set
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&set); err != nil {
		return Set{}, true, fmt.Errorf("parse worktree plan %s: %w", path, err)
	}
	if err := validateSet(set, profile, session); err != nil {
		return Set{}, true, fmt.Errorf("validate worktree plan %s: %w", path, err)
	}
	return set, true, nil
}

func WithLock(t team.Team, profile, session string, fn func() error) error {
	path := StorePath(t, profile, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure worktree plan directory: %w", err)
	}
	return flock.WithLock(LockPath(t, profile, session), fn)
}

func WriteUnderLock(t team.Team, profile, session string, set Set) error {
	path := StorePath(t, profile, session)
	set.Schema = SchemaVersion
	set.Profile = squadnamespace.NormalizeProfile(profile)
	set.Session = strings.TrimSpace(session)
	sort.Slice(set.Plans, func(i, j int) bool {
		if set.Plans[i].Role == set.Plans[j].Role {
			return set.Plans[i].Handle < set.Plans[j].Handle
		}
		return set.Plans[i].Role < set.Plans[j].Role
	})
	for i := range set.Plans {
		set.Plans[i].Scope = normalizedScope(set.Plans[i].Scope)
	}
	if err := validateSet(set, profile, session); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal worktree plan: %w", err)
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure worktree plan directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".worktree-plan-*.tmp")
	if err != nil {
		return fmt.Errorf("create worktree plan temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod worktree plan temp file: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return fmt.Errorf("write worktree plan temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync worktree plan temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close worktree plan temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install worktree plan: %w", err)
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open worktree plan directory for sync: %w", err)
	}
	if err := dir.Sync(); err != nil {
		dir.Close()
		return fmt.Errorf("sync worktree plan directory: %w", err)
	}
	if err := dir.Close(); err != nil {
		return fmt.Errorf("close worktree plan directory: %w", err)
	}
	return nil
}

func validateSet(set Set, profile, session string) error {
	if set.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d (want %d)", set.Schema, SchemaVersion)
	}
	if set.Profile != squadnamespace.NormalizeProfile(profile) {
		return fmt.Errorf("profile mismatch: got %q want %q", set.Profile, squadnamespace.NormalizeProfile(profile))
	}
	if set.Session != strings.TrimSpace(session) || set.Session == "" {
		return fmt.Errorf("session mismatch: got %q want %q", set.Session, strings.TrimSpace(session))
	}
	for _, field := range []struct {
		name, value string
	}{
		{"team_home", set.TeamHome},
		{"control_root", set.ControlRoot},
		{"amq_root", set.AMQRoot},
	} {
		if !filepath.IsAbs(field.value) {
			return fmt.Errorf("%s must be absolute", field.name)
		}
	}
	if set.SharedCWDException.Enabled && strings.TrimSpace(set.SharedCWDException.Reason) == "" {
		return fmt.Errorf("shared_cwd_exception.reason is required when enabled")
	}
	roles := map[string]bool{}
	paths := map[string]string{}
	branches := map[string]string{}
	for _, plan := range set.Plans {
		if strings.TrimSpace(plan.Role) == "" || strings.TrimSpace(plan.Handle) == "" {
			return fmt.Errorf("plan role and handle are required")
		}
		if roles[plan.Role] {
			return fmt.Errorf("duplicate role %q", plan.Role)
		}
		roles[plan.Role] = true
		if !filepath.IsAbs(plan.Path) || !filepath.IsAbs(plan.RepoRoot) {
			return fmt.Errorf("plan %s path and repo_root must be absolute", plan.Role)
		}
		if previous, ok := paths[filepath.Clean(plan.Path)]; ok {
			return fmt.Errorf("duplicate worktree path for %s and %s", previous, plan.Role)
		}
		paths[filepath.Clean(plan.Path)] = plan.Role
		if previous, ok := branches[plan.Branch]; ok {
			return fmt.Errorf("duplicate branch for %s and %s", previous, plan.Role)
		}
		branches[plan.Branch] = plan.Role
		if strings.TrimSpace(plan.Branch) == "" || strings.TrimSpace(plan.BaseSHA) == "" || strings.TrimSpace(plan.TaskID) == "" {
			return fmt.Errorf("plan %s branch, base_sha, and task_id are required", plan.Role)
		}
		if !plan.State.valid() {
			return fmt.Errorf("plan %s has invalid state %q", plan.Role, plan.State)
		}
		if plan.State == StateHandoff && strings.TrimSpace(plan.HandoffSHA) == "" {
			return fmt.Errorf("plan %s is in handoff without handoff_sha", plan.Role)
		}
		if plan.State != StateHandoff && strings.TrimSpace(plan.HandoffSHA) != "" {
			return fmt.Errorf("plan %s has handoff_sha outside handoff state", plan.Role)
		}
	}
	return nil
}

func normalizedScope(scope []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(scope))
	for _, item := range scope {
		item = strings.TrimSpace(item)
		if item != "" && !seen[item] {
			seen[item] = true
			out = append(out, item)
		}
	}
	sort.Strings(out)
	return out
}
