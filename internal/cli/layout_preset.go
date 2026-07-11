package cli

import (
	"fmt"
	"strings"
)

const (
	layoutPresetLeadLeft          = "lead-left"
	layoutPresetLeadTop           = "lead-top"
	layoutPresetEvenGrid          = "even-grid"
	layoutPresetOneWindowPerAgent = "one-window-per-agent"
	launcherPaneCloseAfterStart   = "close-after-start"
	launcherPaneKeep              = "keep"
)

type runStartLayoutSelection struct {
	Visibility     string
	Preset         string
	LauncherPane   string
	Target         string
	SpawnLayout    string
	FinalLayout    string
	MainPaneOption string
	MainPaneValue  string
	LeadMain       bool
}

type runStartLayoutInput struct {
	Visibility      string
	VisibilitySet   bool
	Preset          string
	PresetSet       bool
	LauncherPane    string
	LauncherPaneSet bool
	ExternalLead    bool
}

func resolveRunStartLayout(input runStartLayoutInput) (runStartLayoutSelection, error) {
	preset := strings.TrimSpace(input.Preset)
	launcher := strings.TrimSpace(input.LauncherPane)
	if input.PresetSet && preset == "" {
		return runStartLayoutSelection{}, fmt.Errorf("--layout-preset cannot be empty")
	}
	selection := runStartLayoutSelection{Preset: preset}
	switch preset {
	case "":
		visibility, err := normalizeLaunchVisibility(input.Visibility)
		if err != nil {
			return runStartLayoutSelection{}, err
		}
		selection.Visibility = visibility
		selection.Target = launchTargetForVisibility(visibility)
		selection.SpawnLayout = "vertical"
	case layoutPresetLeadLeft:
		selection.Visibility, selection.Target, selection.SpawnLayout = visibilityCurrent, "current-window", "vertical"
		selection.FinalLayout, selection.MainPaneOption, selection.MainPaneValue, selection.LeadMain = "main-vertical", "main-pane-width", "60%", true
	case layoutPresetLeadTop:
		selection.Visibility, selection.Target, selection.SpawnLayout = visibilityCurrent, "current-window", "horizontal"
		selection.FinalLayout, selection.MainPaneOption, selection.MainPaneValue, selection.LeadMain = "main-horizontal", "main-pane-height", "60%", true
	case layoutPresetEvenGrid:
		selection.Visibility, selection.Target, selection.SpawnLayout = visibilityCurrent, "current-window", "tiled"
		selection.FinalLayout = "tiled"
	case layoutPresetOneWindowPerAgent:
		selection.Visibility, selection.Target, selection.SpawnLayout = visibilitySiblingTabs, "new-window", "tiled"
	default:
		return runStartLayoutSelection{}, fmt.Errorf("unsupported --layout-preset %q (want %s, %s, %s, or %s)", preset, layoutPresetLeadLeft, layoutPresetLeadTop, layoutPresetEvenGrid, layoutPresetOneWindowPerAgent)
	}
	if preset != "" && input.VisibilitySet {
		visibility, err := normalizeLaunchVisibility(input.Visibility)
		if err != nil {
			return runStartLayoutSelection{}, err
		}
		if visibility != selection.Visibility {
			return runStartLayoutSelection{}, fmt.Errorf("--layout-preset %s requires --visibility %s, not %s", preset, selection.Visibility, visibility)
		}
	}
	if input.LauncherPaneSet && launcher != launcherPaneCloseAfterStart && launcher != launcherPaneKeep {
		return runStartLayoutSelection{}, fmt.Errorf("unsupported --launcher-pane %q (want %s or %s)", launcher, launcherPaneCloseAfterStart, launcherPaneKeep)
	}
	if input.ExternalLead {
		if launcher == launcherPaneCloseAfterStart {
			return runStartLayoutSelection{}, fmt.Errorf("--external-lead forces --launcher-pane keep because the launcher pane is the lead")
		}
		launcher = launcherPaneKeep
	} else if selection.Visibility == visibilityDetached {
		if launcher == launcherPaneCloseAfterStart {
			return runStartLayoutSelection{}, fmt.Errorf("--visibility detached forces --launcher-pane keep because the launcher is the only visible control point")
		}
		launcher = launcherPaneKeep
	} else if launcher == "" && preset != "" {
		launcher = launcherPaneCloseAfterStart
	}
	selection.LauncherPane = launcher
	return selection, nil
}

func launchTargetForVisibility(visibility string) string {
	switch visibility {
	case visibilityCurrent:
		return "current-window"
	case visibilityDetached:
		return "new-session"
	default:
		return "new-window"
	}
}

func (s runStartLayoutSelection) requestedFinalization() bool {
	return s.Preset != "" || s.LauncherPane == launcherPaneCloseAfterStart
}
