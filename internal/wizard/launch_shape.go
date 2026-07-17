package wizard

import (
	"fmt"
	"strings"
)

const (
	LaunchShapeWorkingTeamTogether = "working-team-together"
	LaunchShapeLeadOnlyStaged      = "lead-only-staged"
)

func (s *Spec) ApplyLaunchShape() error {
	shape := strings.TrimSpace(s.LaunchShape)
	if shape == "" {
		return fmt.Errorf("launch shape must be explicitly selected")
	}
	s.captureAuthoredComposition()
	roles := splitAssignmentsList(s.AuthoredRoles)
	if len(roles) == 0 {
		return fmt.Errorf("launch shape requires a non-empty role composition")
	}
	lead := strings.TrimSpace(s.Lead)
	if !containsString(roles, lead) {
		return fmt.Errorf("lead %q is not in the selected role composition", lead)
	}
	staged := splitAssignmentsList(s.StagedRoles)
	switch shape {
	case LaunchShapeWorkingTeamTogether:
		// Roles moved to staged automatically by a prior lead-only selection are
		// restored to the initial roster. Truly separate staged roles remain.
		staged = withoutRoles(staged, roles)
	case LaunchShapeLeadOnlyStaged:
		for _, role := range roles {
			if role != lead && !containsString(staged, role) {
				staged = append(staged, role)
			}
		}
		roles = []string{lead}
	default:
		return fmt.Errorf("unsupported launch shape %q", shape)
	}
	s.Binary = renderAssignments(roles, parseAssignments(s.AuthoredBinary))
	s.Model = renderAssignments(roles, parseAssignments(s.AuthoredModel))
	s.Effort = renderAssignments(roles, parseAssignments(s.AuthoredEffort))
	s.ToolProfile = renderAssignments(roles, parseAssignments(s.AuthoredToolProfile))
	for _, role := range staged {
		if containsString(roles, role) {
			return fmt.Errorf("staged role %q is also in the initial launch roster", role)
		}
	}
	s.Roles = strings.Join(roles, ",")
	s.StagedRoles = strings.Join(staged, ",")
	return nil
}

func (s *Spec) captureAuthoredComposition() {
	if strings.TrimSpace(s.AuthoredRoles) != "" {
		return
	}
	s.AuthoredRoles = strings.Join(splitAssignmentsList(s.Roles), ",")
	s.AuthoredBinary = s.Binary
	s.AuthoredModel = s.Model
	s.AuthoredEffort = s.Effort
	s.AuthoredToolProfile = s.ToolProfile
}

func withoutRoles(values, removed []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !containsString(removed, value) {
			out = append(out, value)
		}
	}
	return out
}

func (s Spec) LaunchRosterReview() string {
	initial := splitAssignmentsList(s.Roles)
	staged := splitAssignmentsList(s.StagedRoles)
	return fmt.Sprintf("Initial launch: %d members - %s\nStaged for later: %d roles - %s\nLaunch shape: %s",
		len(initial), displayRoles(initial), len(staged), displayRoles(staged), defaultString(s.LaunchShape, "legacy/unspecified"))
}

func displayRoles(roles []string) string {
	if len(roles) == 0 {
		return "none"
	}
	return strings.Join(roles, ", ")
}
