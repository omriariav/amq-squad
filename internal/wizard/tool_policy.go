package wizard

import (
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
)

func recommendedToolProfileAssignments(roles []string, lead string) string {
	values := make(map[string]string, len(roles))
	for _, role := range roles {
		profile := "coding"
		if found := catalog.Lookup(role); found != nil && strings.TrimSpace(found.MinimumToolProfile) != "" {
			profile = found.MinimumToolProfile
		}
		if role == lead {
			profile = "full"
		}
		values[role] = profile
	}
	return renderAssignments(roles, values)
}

func fullToolProfileAssignments(roles []string) string {
	values := make(map[string]string, len(roles))
	for _, role := range roles {
		values[role] = "full"
	}
	return renderAssignments(roles, values)
}

func countFullToolProfiles(assignments string) int {
	count := 0
	for _, value := range parseAssignments(assignments) {
		if value == "full" {
			count++
		}
	}
	return count
}
