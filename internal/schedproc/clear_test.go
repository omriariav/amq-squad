package schedproc

import (
	"errors"
	"testing"
)

// Brief 31-2 row 7 (the record) and Codex's P2 on #831: a clear names a
// seat handle, a warning code and the seat's current launch, and it is
// accepted only for a seat warning that status shows for that seat on that
// launch now. A refusal code is never clearable (clear_not_a_warning); a
// warning that is not showing is not cleared ahead of it
// (clear_not_showing); a launch that is not the seat's current one is
// stale. Nothing refused is recorded. Reverted (the launch id checked, the
// code not), seat_stalled_at_dialog is pre-cleared and exit_refused is
// recorded as an acknowledgement.
func TestRecordClearAcceptsOnlyAWarningShowingOnTheCurrentLaunch(t *testing.T) {
	root := t.TempDir()
	current := func(_ string, seat string) (string, []string, error) {
		if seat != "qa" {
			return "", nil, errors.New("seat " + seat + " has no readable launch record")
		}
		return "L1", []string{"wake_off: amq-squad: no AMQ wake for this Codex seat"}, nil
	}
	for _, tc := range []struct {
		req     ClearRequest
		current CurrentWarnings
		code    string
	}{
		{ClearRequest{Seat: "../qa", Warning: "wake_off", LaunchID: "L1"}, current, RefusedClearMalformed},
		{ClearRequest{Seat: "qa", Warning: "wake_off: the whole line", LaunchID: "L1"}, current, RefusedClearMalformed},
		{ClearRequest{Seat: "qa", Warning: "wake_off", LaunchID: ""}, current, RefusedClearMalformed},
		{ClearRequest{Seat: "qa", Warning: "exit_refused", LaunchID: "L1"}, current, RefusedClearNotAWarning},
		{ClearRequest{Seat: "qa", Warning: "ledger_tampered", LaunchID: "L1"}, current, RefusedClearNotAWarning},
		{ClearRequest{Seat: "qa", Warning: "seat_stalled_at_dialog", LaunchID: "L1"}, current, RefusedClearNotShowing},
		{ClearRequest{Seat: "qa", Warning: "wake_off", LaunchID: "L1"}, nil, RefusedClearNotShowing},
		{ClearRequest{Seat: "dev", Warning: "wake_off", LaunchID: "L1"}, current, RefusedClearStale},
		{ClearRequest{Seat: "qa", Warning: "wake_off", LaunchID: "L0"}, current, RefusedClearStale},
	} {
		if r := RecordClear(root, "cto", tc.req, tc.current); r.OK || r.Code != tc.code {
			t.Errorf("%+v: %+v, want %s", tc.req, r, tc.code)
		}
	}
	if c, _ := ReadClears(root); len(c) != 0 {
		t.Fatalf("a refused clear was recorded: %+v", c)
	}
	if r := RecordClear(root, "cto", ClearRequest{Seat: "qa", Warning: "wake_off", LaunchID: "L1"}, current); !r.OK {
		t.Fatalf("a clear of a showing warning on the current launch: %+v", r)
	}
	c, _ := ReadClears(root)
	if len(c["qa"]) != 1 || !Cleared(c["qa"], "wake_off", "L1") || Cleared(c["qa"], "wake_off", "L2") || Cleared(c["qa"], "launch_budget_exceeded", "L1") {
		t.Fatalf("recorded: %+v", c)
	}
}
