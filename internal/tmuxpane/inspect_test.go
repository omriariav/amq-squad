package tmuxpane

import (
	"errors"
	"reflect"
	"testing"
)

func TestInspectPaneByIDResolvesSinglePane(t *testing.T) {
	// One full paneListFormat row (session, window_index, pane_index, pane_pid,
	// command, path, pane_id, window_id, pane_title, window_name).
	row := "main\t0\t1\t1234\tcodex\t/repo\t%265\t@42\tamq:issue-96:cto\tdogfood\n"
	got := swapCapture(t, row, nil)

	p, ok := InspectPaneByID("%265")
	if !ok {
		t.Fatal("a valid display-message row should resolve")
	}
	if p.PaneID != "%265" || p.CWD != "/repo" || p.Command != "codex" ||
		p.Session != "main" || p.Title != "amq:issue-96:cto" || p.WindowID != "@42" {
		t.Fatalf("unexpected pane parsed: %+v", p)
	}
	// display-message takes the format as a trailing positional arg, NOT -F.
	want := []string{"display-message", "-p", "-t", "%265", paneListFormat}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("argv = %v, want %v", *got, want)
	}
}

func TestInspectPaneByIDEmptyIDDoesNotShell(t *testing.T) {
	got := swapCapture(t, "ignored", nil)
	if _, ok := InspectPaneByID("   "); ok {
		t.Fatal("empty pane id must not resolve")
	}
	if len(*got) != 0 {
		t.Errorf("empty pane id must not shell tmux; got argv %v", *got)
	}
}

func TestInspectPaneByIDErrorReturnsFalse(t *testing.T) {
	// A gone pane: display-message exits non-zero. This is exactly the -CC case
	// where the global scan fails but a present pane still resolves — here the
	// pane is genuinely absent, so we must report not-found, not error.
	swapCapture(t, "", errors.New("can't find pane %9"))
	if _, ok := InspectPaneByID("%9"); ok {
		t.Fatal("a display-message error must return false (pane gone)")
	}
}

func TestInspectPaneByIDMalformedReturnsFalse(t *testing.T) {
	swapCapture(t, "main\t0\n", nil) // too few fields -> parsePanes skips the row
	if _, ok := InspectPaneByID("%9"); ok {
		t.Fatal("a malformed row must return false")
	}
}
