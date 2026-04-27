// Package team persists the set of agents the user wants booted for a
// project. It's a thin wrapper around a JSON file at <project>/.amq-squad/team.json.
package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SchemaVersion = 1
	DirName       = ".amq-squad"
	FileName      = "team.json"
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
	CWD     string `json:"cwd,omitempty"`
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
// Workstream is the team's default shared AMQ session. CreatedAt is
// informational. Member sessions are legacy/default workstream hints; the live
// workstream can be overridden at launch time.
type Team struct {
	Schema     int       `json:"schema"`
	Project    string    `json:"-"`
	Workstream string    `json:"workstream,omitempty"`
	Members    []Member  `json:"members"`
	CreatedAt  time.Time `json:"created_at"`
}

// Path returns the team.json path for the given project directory.
func Path(projectDir string) string {
	return filepath.Join(projectDir, DirName, FileName)
}

// Read loads the team config from projectDir. Returns os.ErrNotExist if
// no team.json is present.
func Read(projectDir string) (Team, error) {
	p := Path(projectDir)
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

// Write atomically persists the team config under projectDir.
func Write(projectDir string, t Team) error {
	dir := filepath.Join(projectDir, DirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure %s: %w", dir, err)
	}
	t.Schema = SchemaVersion
	// Project is not serialized (json:"-") so nothing to set here.
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	if err := Validate(t); err != nil {
		return fmt.Errorf("validate team: %w", err)
	}
	b, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal team: %w", err)
	}
	path := Path(projectDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// Exists reports whether a team.json is present under projectDir.
func Exists(projectDir string) bool {
	_, err := os.Stat(Path(projectDir))
	return err == nil
}

func Validate(t Team) error {
	if t.Workstream != "" {
		if err := ValidateSessionName(t.Workstream); err != nil {
			return fmt.Errorf("workstream: %w", err)
		}
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
			if seenHandles[handle] {
				return fmt.Errorf("%s: duplicate handle %q", prefix, handle)
			}
			seenHandles[handle] = true
		}
	}
	return nil
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
	if m.CWD != "" {
		if err := ValidateDisplayValue("cwd", m.CWD); err != nil {
			return fmt.Errorf("%s.cwd: %w", prefix, err)
		}
		if !filepath.IsAbs(m.CWD) {
			return fmt.Errorf("%s.cwd: must be absolute", prefix)
		}
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
