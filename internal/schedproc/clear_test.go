package schedproc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Brief 31-2 row 7 (the record): a clear names a seat handle, a warning
// code and the seat's current launch; anything else is refused by name and
// nothing is recorded. A clear for a seat with no launch record is stale.
// Reverted (no shape check), a warning text or a path-like seat is
// recorded.
func TestRecordClearRefusesWhatIsNotAWarningOnALaunch(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(launch.StagedDir(root, "qa"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(root, "qa"), launch.FileName), launch.Record{Handle: "qa", Root: root, Session: "s", Runtime: "codex", LaunchID: "L1"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		req  ClearRequest
		code string
	}{
		{ClearRequest{Seat: "../qa", Warning: "wake_off", LaunchID: "L1"}, RefusedClearMalformed},
		{ClearRequest{Seat: "qa", Warning: "wake_off: the whole line", LaunchID: "L1"}, RefusedClearMalformed},
		{ClearRequest{Seat: "qa", Warning: "wake_off", LaunchID: ""}, RefusedClearMalformed},
		{ClearRequest{Seat: "dev", Warning: "wake_off", LaunchID: "L1"}, RefusedClearStale},
		{ClearRequest{Seat: "qa", Warning: "wake_off", LaunchID: "L0"}, RefusedClearStale},
	} {
		if r := RecordClear(root, "cto", tc.req); r.OK || r.Code != tc.code {
			t.Errorf("%+v: %+v, want %s", tc.req, r, tc.code)
		}
	}
	if c, _ := ReadClears(root); len(c) != 0 {
		t.Fatalf("a refused clear was recorded: %+v", c)
	}
	if r := RecordClear(root, "cto", ClearRequest{Seat: "qa", Warning: "wake_off", LaunchID: "L1"}); !r.OK {
		t.Fatalf("a clear of the current launch: %+v", r)
	}
	c, _ := ReadClears(root)
	if len(c["qa"]) != 1 || !Cleared(c["qa"], "wake_off", "L1") || Cleared(c["qa"], "wake_off", "L2") || Cleared(c["qa"], "launch_budget_exceeded", "L1") {
		t.Fatalf("recorded: %+v", c)
	}
}
