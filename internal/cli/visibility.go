package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

const (
	visibilitySiblingTabs = "sibling-tabs"
	visibilityDetached    = "detached"
	visibilityCurrent     = "current"
	visibilityPlan        = "plan"
)

func normalizeLaunchVisibility(raw string) (string, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return visibilitySiblingTabs, nil
	}
	switch v {
	case visibilitySiblingTabs, visibilityDetached, visibilityCurrent, visibilityPlan:
		return v, nil
	default:
		return "", fmt.Errorf("unsupported visibility %q: use sibling-tabs, detached, current, or plan", raw)
	}
}

func visibilityPreviewLaunchCommand(session, profile, visibility string) string {
	visibility = strings.TrimSpace(visibility)
	if visibility == "" {
		return ""
	}
	mode, err := normalizeLaunchVisibility(visibility)
	if err != nil {
		return ""
	}
	launchMode := mode
	extra := ""
	if mode == visibilityPlan {
		launchMode = visibilitySiblingTabs
		extra = " --dry-run"
	}
	cmd := "amq-squad up " + shellQuote(session)
	if strings.TrimSpace(profile) != "" && profile != team.DefaultProfile {
		cmd += " --profile " + shellQuote(profile)
	}
	cmd += " --visibility " + shellQuote(launchMode) + extra
	return cmd
}
