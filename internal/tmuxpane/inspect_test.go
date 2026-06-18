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

func TestInspectPaneByIDMismatchReturnsFalse(t *testing.T) {
	// tmux `display-message -t <gone-id>` does NOT error — it silently falls
	// back to the client's CURRENT pane and prints that pane's fields. The row
	// parses fine but its pane_id is NOT the one we asked for, so reporting it
	// would be the #156 false positive (pane_alive:true for a closed pane).
	// A mismatched pane_id must resolve to not-found.
	zeroReadBackoff(t)
	row := "main\t0\t1\t1234\tcodex\t/repo\t%999\t@42\tamq:issue-96:cto\tdogfood\n"
	swapCapture(t, row, nil)
	if _, ok := InspectPaneByID("%265"); ok {
		t.Fatal("display-message returning a DIFFERENT pane_id must return false")
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
	// A gone pane: display-message exits non-zero on every retry. This is the
	// -CC case where the global scan fails but a present pane still resolves —
	// here the pane is genuinely absent, so we must report not-found, not error.
	zeroReadBackoff(t)
	swapCapture(t, "", errors.New("can't find pane %9"))
	if _, ok := InspectPaneByID("%9"); ok {
		t.Fatal("a display-message error must return false (pane gone)")
	}
}

func TestInspectPaneByIDMalformedReturnsFalse(t *testing.T) {
	zeroReadBackoff(t)
	swapCapture(t, "main\t0\n", nil) // too few fields -> parsePanes skips the row
	if _, ok := InspectPaneByID("%9"); ok {
		t.Fatal("a malformed row must return false")
	}
}

func TestClosePane(t *testing.T) {
	var got []string
	prev := closePaneExec
	closePaneExec = func(name string, args ...string) error {
		got = append([]string{name}, args...)
		return nil
	}
	t.Cleanup(func() { closePaneExec = prev })

	if err := ClosePane("%265"); err != nil {
		t.Fatalf("ClosePane: %v", err)
	}
	if want := []string{"tmux", "kill-pane", "-t", "%265"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}

	// A blank id must be a no-op that never shells tmux.
	got = nil
	if err := ClosePane("   "); err != nil {
		t.Fatalf("blank id should be a no-op, got %v", err)
	}
	if got != nil {
		t.Errorf("blank id must not shell tmux; got %v", got)
	}
}
