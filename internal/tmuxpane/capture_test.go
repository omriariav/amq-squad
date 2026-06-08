package tmuxpane

import (
	"errors"
	"reflect"
	"testing"
)

// swapCapture replaces the captureExec seam and returns a restore func plus a
// pointer to the recorded argv of the last call.
func swapCapture(t *testing.T, out string, retErr error) *[]string {
	t.Helper()
	var got []string
	prev := captureExec
	captureExec = func(args ...string) (string, error) {
		got = append([]string(nil), args...)
		return out, retErr
	}
	t.Cleanup(func() { captureExec = prev })
	return &got
}

func swapEnv(t *testing.T, server, pane string) {
	t.Helper()
	ps, pp := tmuxServerEnv, tmuxPaneEnv
	tmuxServerEnv = func() string { return server }
	tmuxPaneEnv = func() string { return pane }
	t.Cleanup(func() { tmuxServerEnv, tmuxPaneEnv = ps, pp })
}

func TestCurrentPaneIdentityResolvesExactIDs(t *testing.T) {
	swapEnv(t, "/tmp/tmux-1000/default,123,0", "%5")
	got := swapCapture(t, "main\t@42\tamq-squad-issue-96\t%5\n", nil)

	id, err := CurrentPaneIdentity()
	if err != nil {
		t.Fatalf("CurrentPaneIdentity: %v", err)
	}
	if id == nil {
		t.Fatal("expected identity, got nil")
	}
	want := &PaneIdentity{Session: "main", WindowID: "@42", WindowName: "amq-squad-issue-96", PaneID: "%5"}
	if !reflect.DeepEqual(id, want) {
		t.Fatalf("identity = %+v, want %+v", id, want)
	}
	// It must target the explicit $TMUX_PANE, never the active pane.
	wantArgs := []string{"display-message", "-p", "-t", "%5", "#{session_name}\t#{window_id}\t#{window_name}\t#{pane_id}"}
	if !reflect.DeepEqual(*got, wantArgs) {
		t.Fatalf("capture argv = %v, want %v", *got, wantArgs)
	}
}

func TestCurrentPaneIdentityNotInTmux(t *testing.T) {
	swapEnv(t, "", "%5") // $TMUX empty
	got := swapCapture(t, "", nil)
	id, err := CurrentPaneIdentity()
	if err != nil || id != nil {
		t.Fatalf("outside tmux want (nil,nil), got (%v,%v)", id, err)
	}
	if len(*got) != 0 {
		t.Fatalf("must not query tmux when $TMUX unset; argv=%v", *got)
	}
}

func TestCurrentPaneIdentityNoPaneEnv(t *testing.T) {
	swapEnv(t, "/tmp/tmux-1000/default", "") // in tmux but no $TMUX_PANE
	got := swapCapture(t, "", nil)
	id, err := CurrentPaneIdentity()
	if err != nil || id != nil {
		t.Fatalf("no pane env want (nil,nil), got (%v,%v)", id, err)
	}
	if len(*got) != 0 {
		t.Fatalf("must not query tmux when $TMUX_PANE unset; argv=%v", *got)
	}
}

func TestPaneIdentityForPropagatesError(t *testing.T) {
	swapCapture(t, "", errors.New("no server running"))
	if _, err := PaneIdentityFor("%5"); err == nil {
		t.Fatal("expected error when tmux query fails")
	}
}

func TestPaneIdentityForRejectsShortOutput(t *testing.T) {
	swapCapture(t, "main\t@42\n", nil) // only 2 fields
	if _, err := PaneIdentityFor("%5"); err == nil {
		t.Fatal("expected error on malformed display-message output")
	}
}

func TestParsePanesCapturesExactIDs(t *testing.T) {
	// 10-field row including pane_id (#{pane_id}) and window_id (#{window_id}).
	row := "main\t0\t1\t4242\tcodex\t/repo\tamq:issue-96:cto\tsquad\t%5\t@42"
	panes := parsePanes(row)
	if len(panes) != 1 {
		t.Fatalf("want 1 pane, got %d", len(panes))
	}
	p := panes[0]
	if p.PaneID != "%5" || p.WindowID != "@42" {
		t.Fatalf("exact ids not parsed: PaneID=%q WindowID=%q", p.PaneID, p.WindowID)
	}
	if p.Session != "main" || p.Title != "amq:issue-96:cto" || p.WindowName != "squad" {
		t.Fatalf("row fields wrong: %+v", p)
	}
}

func TestParsePanesToleratesOldEightFieldRow(t *testing.T) {
	// Older 8-field row (no pane_id/window_id) must still parse, with empty ids.
	row := "main\t0\t1\t4242\tcodex\t/repo\tamq:issue-96:cto\tsquad"
	panes := parsePanes(row)
	if len(panes) != 1 {
		t.Fatalf("want 1 pane, got %d", len(panes))
	}
	if panes[0].PaneID != "" || panes[0].WindowID != "" {
		t.Fatalf("old row should have empty exact ids, got PaneID=%q WindowID=%q", panes[0].PaneID, panes[0].WindowID)
	}
}
