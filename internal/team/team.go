// Package team persists the set of agents the user wants booted for a
// project. It's a thin wrapper around a JSON file at <project>/.amq-squad/team.json.
package team

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
type Member struct {
	Role         string `json:"role"`    // catalog role ID, e.g. "cpo"
	Binary       string `json:"binary"`  // "claude" or "codex"
	Handle       string `json:"handle"`  // AMQ handle, defaults to Role
	Session      string `json:"session"` // AMQ session name, defaults to Role
	Conversation string `json:"conversation,omitempty"`
	CWD          string `json:"cwd,omitempty"`
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
// CreatedAt is informational.
type Team struct {
	Schema    int       `json:"schema"`
	Project   string    `json:"-"`
	Members   []Member  `json:"members"`
	CreatedAt time.Time `json:"created_at"`
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
