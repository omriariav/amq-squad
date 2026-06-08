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
	// Output order matches the ids-first format: pane_id, window_id, session,
	// window_name.
	got := swapCapture(t, "%5\t@42\tmain\tamq-squad-issue-96\n", nil)

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
	// It must target the explicit $TMUX_PANE, never the active pane, and request
	// the stable ids before the (possibly tab-bearing) window name.
	wantArgs := []string{"display-message", "-p", "-t", "%5", "#{pane_id}\t#{window_id}\t#{session_name}\t#{window_name}"}
	if !reflect.DeepEqual(*got, wantArgs) {
		t.Fatalf("capture argv = %v, want %v", *got, wantArgs)
	}
}

func TestPaneIdentityToleratesTabInWindowName(t *testing.T) {
	// A window name containing a tab must corrupt only the label, never the ids.
	swapCapture(t, "%9\t@7\tmain\tweird\tname\n", nil)
	id, err := PaneIdentityFor("%9")
	if err != nil {
		t.Fatalf("PaneIdentityFor: %v", err)
	}
	if id.PaneID != "%9" || id.WindowID != "@7" || id.Session != "main" {
		t.Fatalf("ids corrupted by tab in name: %+v", id)
	}
	if id.WindowName != "weird\tname" {
		t.Fatalf("window name = %q, want the joined remainder", id.WindowName)
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
	// Full 10-field row: session, window, pane, pid, command, cwd, pane_id,
	// window_id, pane_title, window_name (ids before the labels).
	row := "main\t0\t1\t4242\tcodex\t/repo\t%5\t@42\tamq:issue-96:cto\tsquad"
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

func TestParsePanesTabInWindowNameDoesNotCorruptIDs(t *testing.T) {
	// A tab inside the trailing window_name must not shift the ids; it is
	// absorbed into WindowName.
	row := "main\t0\t1\t4242\tcodex\t/repo\t%5\t@42\tamq:issue-96:cto\tweird\tname"
	p := parsePanes(row)[0]
	if p.PaneID != "%5" || p.WindowID != "@42" || p.Title != "amq:issue-96:cto" {
		t.Fatalf("ids/title corrupted by tab in window name: %+v", p)
	}
	if p.WindowName != "weird\tname" {
		t.Fatalf("window name = %q, want joined remainder", p.WindowName)
	}
}

func TestParsePanesToleratesShortRows(t *testing.T) {
	// A 6-field row (no ids/labels) parses the core fields with empty extras.
	short := parsePanes("main\t0\t1\t4242\tcodex\t/repo")[0]
	if short.PaneID != "" || short.WindowID != "" || short.Title != "" || short.WindowName != "" {
		t.Fatalf("short row should have empty extras, got %+v", short)
	}
	// An 8-field row (through window_id, no labels) parses the ids.
	ids := parsePanes("main\t0\t1\t4242\tcodex\t/repo\t%5\t@42")[0]
	if ids.PaneID != "%5" || ids.WindowID != "@42" || ids.Title != "" {
		t.Fatalf("8-field row should parse ids with empty labels, got %+v", ids)
	}
}
