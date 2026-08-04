package cli

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestFindOrphanPanesExcludesLiveLaunchRecordsAndFiltersSession(t *testing.T) {
	panes := []tmuxpane.TmuxPane{
		{PaneID: "%1", Title: "amq:s1:cto"},
		{PaneID: "%2", Title: "amq:s1:qa"},
		{PaneID: "%3", Title: "amq:s2:cto"},
		{PaneID: "%4", Title: "shell"},
	}
	live := map[string]bool{
		launchPaneKey("%1", "amq:s1:cto"): true,
	}

	got := findOrphanPanes(panes, live, "s1")
	want := []orphanPane{{PaneID: "%2", Title: "amq:s1:qa", Session: "s1", Role: "qa"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("orphans = %+v, want %+v", got, want)
	}
}

func TestLiveLaunchPaneTokensReadsCurrentRecords(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	seedAgentRecord(t, base, "s1", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "s1",
		Tmux: &launch.TmuxInfo{PaneID: "%1"},
	})
	got, err := liveLaunchPaneTokens(projectDir, base)
	if err != nil {
		t.Fatalf("liveLaunchPaneTokens: %v", err)
	}
	if !got[launchPaneKey("%1", "amq:s1:cto")] {
		t.Fatalf("expected token for launch record under %s", filepath.Join(base, "s1"))
	}
}
