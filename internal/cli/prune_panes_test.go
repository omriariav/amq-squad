package cli

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
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

func TestExecutePrunePanesPreviewDeclineClosesNothing(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	seedAgentRecord(t, base, "s1", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "s1",
		Tmux: &launch.TmuxInfo{PaneID: "%1"},
	})
	closed := swapPaneCloser(t)
	swapPaneInspectorMatching(t, "s1", map[string]string{"%2": "qa"})

	out, err := runPrunePanesExec(t, prunePanesExecution{
		ProjectDir:      projectDir,
		Session:         "s1",
		ExplicitSession: true,
		BaseRoot:        base,
		PaneLister: func() ([]tmuxpane.TmuxPane, error) {
			return []tmuxpane.TmuxPane{
				{PaneID: "%1", Title: "amq:s1:cto"},
				{PaneID: "%2", Title: "amq:s1:qa"},
			}, nil
		},
		Confirm: strings.NewReader("n\n"),
	})
	if err != nil {
		t.Fatalf("prune-panes: %v\n%s", err, out)
	}
	if len(*closed) != 0 {
		t.Fatalf("declined prune must close nothing, got %v", *closed)
	}
	for _, want := range []string{"# amq-squad prune-panes", "orphan panes: 1", "CLOSE  %2  amq:s1:qa", "aborted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview missing %q in:\n%s", want, out)
		}
	}
}

func TestExecutePrunePanesYesClosesOnlyIdentityConfirmedOrphans(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	closed := swapPaneCloser(t)
	prevInspect := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		switch id {
		case "%2":
			return tmuxpane.TmuxPane{PaneID: "%2", Title: "amq:s1:qa"}, true
		case "%3":
			return tmuxpane.TmuxPane{PaneID: "%3", Title: "amq:s1:other"}, true
		default:
			return tmuxpane.TmuxPane{}, false
		}
	}
	t.Cleanup(func() { statusPaneInspector = prevInspect })

	out, err := runPrunePanesExec(t, prunePanesExecution{
		ProjectDir:      projectDir,
		Session:         "s1",
		ExplicitSession: true,
		Yes:             true,
		BaseRoot:        base,
		PaneLister: func() ([]tmuxpane.TmuxPane, error) {
			return []tmuxpane.TmuxPane{
				{PaneID: "%2", Title: "amq:s1:qa"},
				{PaneID: "%3", Title: "amq:s1:dev"},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("prune-panes: %v\n%s", err, out)
	}
	if want := []string{"%2"}; !reflect.DeepEqual(*closed, want) {
		t.Fatalf("closed = %v, want %v", *closed, want)
	}
	if !strings.Contains(out, "closed tmux pane %2") || !strings.Contains(out, "title changed") {
		t.Fatalf("output should report close and reused-id skip:\n%s", out)
	}
}

func TestExecutePrunePanesRechecksLaunchRecordsBeforeClose(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	closed := swapPaneCloser(t)
	swapPaneInspectorMatching(t, "s1", map[string]string{"%2": "qa"})

	out, err := runPrunePanesExec(t, prunePanesExecution{
		ProjectDir:      projectDir,
		Session:         "s1",
		ExplicitSession: true,
		BaseRoot:        base,
		PaneLister: func() ([]tmuxpane.TmuxPane, error) {
			return []tmuxpane.TmuxPane{{PaneID: "%2", Title: "amq:s1:qa"}}, nil
		},
		Confirm: seedOnRead(t, "y\n", func() {
			seedAgentRecord(t, base, "s1", "qa", launch.Record{
				Binary: "codex", Handle: "qa", Role: "qa", Session: "s1",
				Tmux: &launch.TmuxInfo{PaneID: "%2"},
			})
		}),
	})
	if err != nil {
		t.Fatalf("prune-panes: %v\n%s", err, out)
	}
	if len(*closed) != 0 {
		t.Fatalf("freshly adopted pane must stay open, closed = %v", *closed)
	}
	if !strings.Contains(out, "now has a live launch record") {
		t.Fatalf("output should explain launch-record recheck skip:\n%s", out)
	}
}

func TestExecutePrunePanesReportsCloseFailure(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	prevCloser := paneCloser
	paneCloser = func(string) error { return errors.New("permission denied") }
	t.Cleanup(func() { paneCloser = prevCloser })
	swapPaneInspectorMatching(t, "s1", map[string]string{"%2": "qa"})

	out, err := runPrunePanesExec(t, prunePanesExecution{
		ProjectDir:      projectDir,
		Session:         "s1",
		ExplicitSession: true,
		Yes:             true,
		BaseRoot:        base,
		PaneLister: func() ([]tmuxpane.TmuxPane, error) {
			return []tmuxpane.TmuxPane{{PaneID: "%2", Title: "amq:s1:qa"}}, nil
		},
	})
	if err != nil {
		t.Fatalf("prune-panes: %v\n%s", err, out)
	}
	if !strings.Contains(out, "close failed: permission denied") {
		t.Fatalf("output should report pane close failure:\n%s", out)
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

func runPrunePanesExec(t *testing.T, e prunePanesExecution) (string, error) {
	t.Helper()
	var b strings.Builder
	e.Out = &b
	err := executePrunePanes(e)
	return b.String(), err
}

type seededReader struct {
	r    *strings.Reader
	seed func()
}

func seedOnRead(t *testing.T, s string, seed func()) *seededReader {
	t.Helper()
	return &seededReader{r: strings.NewReader(s), seed: seed}
}

func (r *seededReader) Read(p []byte) (int, error) {
	if r.seed != nil {
		r.seed()
		r.seed = nil
	}
	return r.r.Read(p)
}
