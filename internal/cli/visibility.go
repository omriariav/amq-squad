package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
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

func launchVisibilityForFlags(raw string, explicitVisibility, explicitTerminal, explicitTarget, explicitTerminalSession bool) string {
	if explicitVisibility {
		return strings.TrimSpace(raw)
	}
	if explicitTerminal || explicitTarget || explicitTerminalSession {
		return ""
	}
	return visibilitySiblingTabs
}

func applyLaunchVisibility(opts *teamLaunchOptions, visibility string, explicitTerminal, explicitTarget, explicitTerminalSession, live bool) error {
	visibility = strings.TrimSpace(visibility)
	if visibility == "" {
		return nil
	}
	mode, err := normalizeLaunchVisibility(visibility)
	if err != nil {
		return err
	}
	if explicitTerminal || explicitTarget {
		return fmt.Errorf("--visibility cannot be combined with --terminal or --target; choose one topology surface")
	}
	if explicitTerminalSession && mode != visibilityDetached {
		return fmt.Errorf("--terminal-session is only valid with --visibility detached")
	}
	opts.Terminal = "tmux"
	switch mode {
	case visibilitySiblingTabs:
		opts.Target = "new-window"
		if live {
			return requireVisibleTmuxPane("--visibility sibling-tabs")
		}
	case visibilityDetached:
		opts.Target = "new-session"
	case visibilityCurrent:
		opts.Target = "current-window"
		if live {
			return requireVisibleTmuxPane("--visibility current")
		}
	case visibilityPlan:
		if live {
			return fmt.Errorf("--visibility plan is preview-only; pass --dry-run or choose sibling-tabs, detached, or current")
		}
		opts.Target = "new-window"
	}
	return nil
}

func requireVisibleTmuxPane(flag string) error {
	if strings.TrimSpace(os.Getenv("TMUX")) != "" && strings.TrimSpace(os.Getenv("TMUX_PANE")) != "" {
		return nil
	}
	return fmt.Errorf("%s requires running inside a visible tmux pane (TMUX and TMUX_PANE set); attach/open a tmux control-mode session first, or explicitly use --visibility detached", flag)
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
