package cli

import (
	"strings"
	"testing"
)

func TestApplyLaunchVisibilitySiblingTabsRequiresLiveTmuxPane(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	opts := teamLaunchOptions{Terminal: "tmux", Target: "current-window"}
	err := applyLaunchVisibility(&opts, "sibling-tabs", false, false, false, true)
	if err == nil || !strings.Contains(err.Error(), "requires running inside a visible tmux pane") {
		t.Fatalf("want visible tmux pane error, got %v", err)
	}
}

func TestApplyLaunchVisibilitySiblingTabsMapsToNewWindow(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%42")
	opts := teamLaunchOptions{Terminal: "tmux", Target: "current-window"}
	if err := applyLaunchVisibility(&opts, "sibling-tabs", false, false, false, true); err != nil {
		t.Fatalf("apply visibility: %v", err)
	}
	if opts.Terminal != "tmux" || opts.Target != "new-window" {
		t.Fatalf("opts = %+v, want terminal tmux target new-window", opts)
	}
}

func TestApplyLaunchVisibilityDetachedMapsToNewSession(t *testing.T) {
	opts := teamLaunchOptions{Terminal: "tmux", Target: "current-window"}
	if err := applyLaunchVisibility(&opts, "detached", false, false, true, true); err != nil {
		t.Fatalf("apply visibility: %v", err)
	}
	if opts.Target != "new-session" {
		t.Fatalf("target = %q, want new-session", opts.Target)
	}
}

func TestApplyLaunchVisibilityRejectsLowLevelTopologyMix(t *testing.T) {
	opts := teamLaunchOptions{Terminal: "tmux", Target: "new-session"}
	err := applyLaunchVisibility(&opts, "sibling-tabs", false, true, false, true)
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("want conflict error, got %v", err)
	}
}

func TestApplyLaunchVisibilityPlanIsPreviewOnly(t *testing.T) {
	opts := teamLaunchOptions{}
	err := applyLaunchVisibility(&opts, "plan", false, false, false, true)
	if err == nil || !strings.Contains(err.Error(), "preview-only") {
		t.Fatalf("want preview-only error, got %v", err)
	}
}
