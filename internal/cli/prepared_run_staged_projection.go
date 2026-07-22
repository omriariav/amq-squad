package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// projectPreparedRunStagedTeamForRecord overlays only the immutable effective
// staged authority on an in-memory team. The persisted prepared manifest and
// profile remain unchanged.
func projectPreparedRunStagedTeamForRecord(t team.Team, rec launch.Record) (team.Team, error) {
	project := strings.TrimSpace(rec.TeamHome)
	if project == "" {
		project = strings.TrimSpace(rec.CWD)
	}
	manifest, digest, prepared, err := preparedRunManifestForProjection(project, rec.TeamProfile, rec.Session)
	if err != nil {
		return team.Team{}, err
	}
	if !prepared || !containsRole(manifest.StagedRoster, rec.Role) {
		return t, nil
	}
	staged, ok := manifest.StagedMembers[rec.Role]
	if !ok || staged.Role != rec.Role || staged.Handle != rec.Handle {
		return team.Team{}, preparedRunIdentityMismatchf("staged launch record actor %s/%s differs from accepted staged identity", rec.Role, rec.Handle)
	}
	token := preparedRunTokenFromRecord(rec)
	if !token.complete() || strings.TrimSpace(token.LaunchAttempt) == "" {
		return team.Team{}, preparedRunIdentityMismatchf("staged launch record %s/%s lacks a complete claim-bound prepared token", rec.Role, rec.Handle)
	}
	if err := validatePreparedRunToken(token, manifest, digest); err != nil {
		return team.Team{}, err
	}
	claim, err := currentPreparedRunStagedClaim(project, rec.TeamProfile, rec.Session, token.generationRef(), rec.Role)
	if err != nil {
		return team.Team{}, err
	}
	if claim.ClaimID != token.LaunchAttempt || claim.Handle != rec.Handle {
		return team.Team{}, preparedRunIdentityMismatchf("staged launch record does not match authoritative current claim")
	}
	return projectPreparedRunStagedMember(t, claim), nil
}

func projectPreparedRunStagedTeamForTarget(project, profile, session, role string, t team.Team) (team.Team, error) {
	manifest, digest, prepared, err := preparedRunManifestForProjection(project, profile, session)
	if err != nil {
		return team.Team{}, err
	}
	if !prepared || !containsRole(manifest.StagedRoster, role) {
		return t, nil
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	claim, err := currentPreparedRunStagedClaim(project, profile, session, token, role)
	if err != nil {
		return team.Team{}, preparedRunIdentityMismatchf("staged dispatch target %s has no authoritative active claim: %v", role, err)
	}
	return projectPreparedRunStagedMember(t, claim), nil
}

// preparedRunManifestForProjection distinguishes a genuinely non-prepared
// session from a damaged accepted state. Once a generation directory exists,
// losing or corrupting the current pointer is an authority failure, not a
// request to fall back to the implementation profile.
func preparedRunManifestForProjection(project, profile, session string) (preparedRunManifest, string, bool, error) {
	manifest, digest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if err == nil {
		return manifest, digest, true, nil
	}
	if !os.IsNotExist(err) {
		return preparedRunManifest{}, "", false, err
	}
	_, generationsErr := os.Stat(preparedRunGenerationsPath(project, profile, session))
	switch {
	case generationsErr == nil:
		return preparedRunManifest{}, "", false, preparedRunIdentityMismatchf("prepared session %s/%s has immutable generations but no readable current pointer", profile, session)
	case os.IsNotExist(generationsErr):
		return preparedRunManifest{}, "", false, nil
	default:
		return preparedRunManifest{}, "", false, fmt.Errorf("inspect prepared session generations: %w", generationsErr)
	}
}

func projectPreparedRunStagedMember(t team.Team, claim preparedRunStagedClaim) team.Team {
	projected := t
	projected.Members = append([]team.Member(nil), t.Members...)
	for index := range projected.Members {
		member := &projected.Members[index]
		if member.Role != claim.Role || memberHandle(*member) != claim.Handle {
			continue
		}
		member.ActorMode = claim.Effective.ActorMode
		member.ToolProfile = claim.Effective.ToolProfile
		member.ToolConfig = claim.Effective.ToolConfig
		member.ToolMCPConfig = claim.Effective.ToolMCPConfig
		member.ToolAllowlist = append([]string(nil), claim.Effective.ToolAllowlist...)
		member.ToolBlocklist = append([]string(nil), claim.Effective.ToolBlocklist...)
		member.PermissionAllowlist = append([]string(nil), claim.Effective.PermissionAllowlist...)
		return projected
	}
	return projected
}

func preparedRunStagedProjectedMember(t team.Team, claim preparedRunStagedClaim) (team.Member, error) {
	projected := projectPreparedRunStagedMember(t, claim)
	for _, member := range projected.Members {
		if member.Role == claim.Role && memberHandle(member) == claim.Handle {
			return member, nil
		}
	}
	return team.Member{}, fmt.Errorf("staged claim actor %s/%s is absent from current team", claim.Role, claim.Handle)
}
