package namespace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

const RootSessionID = "_root"

// DefaultProfile is the namespace profile of a repository's one squad
// (squad.yaml). The profile stays a namespace dimension (IDs, AMQ roots);
// a repository has one squad config (team.ErrNamedProfile).
const DefaultProfile = "default"

// ValidateProfile enforces the profile slug rules: lowercase a-z, 0-9, - and _.
func ValidateProfile(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("invalid profile name %q: use lowercase a-z, 0-9, - and _ only", s)
	}
	return nil
}

// Ref is the canonical identity for a team workstream inside one project.
// The default profile preserves the legacy session-only storage paths. Named
// profiles are storage-isolated under their profile name so two profiles can
// safely reuse the same human session label.
type Ref struct {
	TeamHome   string `json:"team_home,omitempty"`
	Profile    string `json:"profile"`
	Session    string `json:"session"`
	ID         string `json:"id"`
	Display    string `json:"display"`
	AMQSession string `json:"amq_session"`
	AMQRoot    string `json:"amq_root,omitempty"`
	Paths      Paths  `json:"paths,omitempty"`
}

type Paths struct {
	ProfileConfig string `json:"profile_config,omitempty"` // the squad.yaml path
	AMQRoot       string `json:"amq_root,omitempty"`
	Brief         string `json:"brief,omitempty"`
	Tasks         string `json:"tasks,omitempty"`
}

func NormalizeProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return DefaultProfile
	}
	return profile
}

func ProfilesEqual(a, b string) bool {
	return NormalizeProfile(a) == NormalizeProfile(b)
}

func ID(profile, session string) string {
	profile = NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	if session == "" {
		session = RootSessionID
	}
	return profile + "/" + session
}

func Resolve(teamHome, profile, session string) Ref {
	profile = NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	id := ID(profile, session)
	displaySession := session
	if displaySession == "" {
		displaySession = "<root>"
	}
	ref := Ref{
		TeamHome:   strings.TrimSpace(teamHome),
		Profile:    profile,
		Session:    session,
		ID:         id,
		Display:    profile + "/" + displaySession,
		AMQSession: session,
	}
	if ref.TeamHome != "" {
		ref.Paths.ProfileConfig = team.SquadPath(ref.TeamHome)
		if session != "" {
			ref.AMQRoot = AMQRoot(ref.TeamHome, profile, session)
			ref.Paths.AMQRoot = ref.AMQRoot
			ref.Paths.Brief = BriefPath(ref.TeamHome, profile, session)
			ref.Paths.Tasks = TasksPath(ref.TeamHome, profile, session)
		}
	}
	return ref
}

func BriefPath(teamHome, profile, session string) string {
	teamHome = strings.TrimSpace(teamHome)
	session = strings.TrimSpace(session)
	if teamHome == "" || session == "" {
		return ""
	}
	base := filepath.Join(teamHome, team.DirName, "briefs")
	if NormalizeProfile(profile) != DefaultProfile {
		base = filepath.Join(base, NormalizeProfile(profile))
	}
	return filepath.Join(base, session+".md")
}

func TasksPath(teamHome, profile, session string) string {
	teamHome = strings.TrimSpace(teamHome)
	session = strings.TrimSpace(session)
	if teamHome == "" || session == "" {
		return ""
	}
	base := filepath.Join(teamHome, team.DirName, "tasks")
	if NormalizeProfile(profile) != DefaultProfile {
		base = filepath.Join(base, NormalizeProfile(profile))
	}
	return filepath.Join(base, session)
}

func AMQRoot(teamHome, profile, session string) string {
	teamHome = strings.TrimSpace(teamHome)
	session = strings.TrimSpace(session)
	if teamHome == "" || session == "" {
		return ""
	}
	base := filepath.Join(teamHome, ".agent-mail")
	if NormalizeProfile(profile) != DefaultProfile {
		base = filepath.Join(base, NormalizeProfile(profile))
	}
	return filepath.Join(base, session)
}
