package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	adoptionModeExternal            = "external"
	adoptionModeExternalProjectLead = "external_project_lead"
)

func projectExecutionMode(mode string) bool {
	return mode == executionModeProjectLead || mode == executionModeProjectTeam
}

func leadRegisterTargetMode(t team.Team, role string) string {
	mode := effectiveTeamExecutionMode(t)
	if !t.Orchestrated && strings.TrimSpace(role) != goalOrchestratorRole {
		return executionModeProjectLead
	}
	return mode
}

func launchRecordMatchesIdentity(rec launch.Record, role, handle, profile, session, root string) bool {
	if strings.TrimSpace(rec.Role) != "" && strings.TrimSpace(rec.Role) != strings.TrimSpace(role) {
		return false
	}
	if strings.TrimSpace(rec.Handle) != "" && strings.TrimSpace(rec.Handle) != strings.TrimSpace(handle) {
		return false
	}
	if strings.TrimSpace(rec.Session) != "" && strings.TrimSpace(rec.Session) != strings.TrimSpace(session) {
		return false
	}
	if !launchRecordProfileMatches(rec, profile) {
		return false
	}
	if strings.TrimSpace(rec.Root) != "" && strings.TrimSpace(root) != "" && !rootsMatch(rec.Root, root) {
		return false
	}
	return true
}

func launchRecordProfileMatches(rec launch.Record, profile string) bool {
	recProfile := squadnamespace.NormalizeProfile(rec.TeamProfile)
	wantProfile := squadnamespace.NormalizeProfile(profile)
	if strings.TrimSpace(rec.TeamProfile) == "" {
		recProfile = team.DefaultProfile
	}
	return recProfile == wantProfile
}

func launchRecordMatchesPane(rec launch.Record, paneID string) bool {
	return rec.Tmux != nil && strings.TrimSpace(rec.Tmux.PaneID) != "" &&
		strings.TrimSpace(rec.Tmux.PaneID) == strings.TrimSpace(paneID)
}

func launchRecordAuthorizesProjectLead(rec launch.Record, role, handle, profile, session, root string) bool {
	if !launchRecordMatchesIdentity(rec, role, handle, profile, session, root) {
		return false
	}
	if !rec.External {
		return true
	}
	switch strings.TrimSpace(rec.AdoptionMode) {
	case adoptionModeExternalProjectLead:
		return true
	default:
		return launchRecordHasGoalBinding(rec)
	}
}

func launchRecordAuthorizesProjectLeadPane(rec launch.Record, role, handle, profile, session, root, paneID string) bool {
	return launchRecordMatchesPane(rec, paneID) &&
		launchRecordAuthorizesProjectLead(rec, role, handle, profile, session, root)
}

func launchRecordMatchesSamePaneIdentity(rec launch.Record, role, handle, profile, session, root, paneID string) bool {
	return launchRecordMatchesPane(rec, paneID) &&
		launchRecordMatchesIdentity(rec, role, handle, profile, session, root)
}

func currentEnvProvesTeamRole(handle, role, root string) bool {
	me := strings.TrimSpace(os.Getenv("AM_ME"))
	if me == "" || (me != strings.TrimSpace(handle) && me != strings.TrimSpace(role)) {
		return false
	}
	envRoot := strings.TrimSpace(os.Getenv("AM_ROOT"))
	if envRoot == "" || strings.TrimSpace(root) == "" {
		return false
	}
	return rootsMatch(envRoot, root)
}

func currentWorkingDirMatches(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	cwd, err := os.Getwd()
	if err != nil {
		return false
	}
	return sameFilesystemPath(cwd, target)
}

func sameFilesystemPath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil {
		a = absA
	}
	if errB == nil {
		b = absB
	}
	if ra, err := filepath.EvalSymlinks(a); err == nil {
		a = ra
	}
	if rb, err := filepath.EvalSymlinks(b); err == nil {
		b = rb
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func findLaunchRecordByPane(root, paneID string) (string, launch.Record, bool) {
	paneID = strings.TrimSpace(paneID)
	if strings.TrimSpace(root) == "" || paneID == "" {
		return "", launch.Record{}, false
	}
	agentsDir := filepath.Join(root, "agents")
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		return "", launch.Record{}, false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentDir := filepath.Join(agentsDir, entry.Name())
		rec, err := launch.Read(agentDir)
		if err != nil {
			continue
		}
		if launchRecordMatchesPane(rec, paneID) {
			return agentDir, rec, true
		}
	}
	return "", launch.Record{}, false
}
