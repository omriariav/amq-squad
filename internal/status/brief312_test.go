package status

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
)

// Brief 31-2 row 7 (status's half): a clear of a seat's warning line on its
// current launch moves that warning to acknowledged_warnings, leaves the
// seat's state, reason code, bucket and launch outcome as they were, and
// touches no other warning or seat; the same condition on the seat's next
// launch is a warning again, and a clear naming the earlier launch is then
// refused clear_launch_stale. Reverted (no split in collectSeat), the
// cleared warning stays in warnings.
func TestAClearedWarningLeavesTheScreenForItsLaunchOnly(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	start := now.Add(-time.Minute)
	sec := time.Second
	// scribe: 11 s to seat live, over the 10 s ceiling, and stalled at a dialog
	budgetSeat(t, root, "scribe", "claude", "sonnet", start, 2*sec, sec, sec, 7*sec, nil)
	if err := os.WriteFile(filepath.Join(root, "agents", "scribe", ".wake.dialog"), []byte(`{"harness":"claude"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	budgetSeat(t, root, "pi-nokey", "pi", "anthropic/claude-sonnet-5", start, sec, sec, sec, sec, nil)
	collect := func() Status {
		return Build(Collect(CollectOptions{Root: root, Now: now, PIDAlive: func(int) bool { return true }}))
	}
	before := collect()
	scribe0 := seatByHandle(t, before, "scribe")
	if hasWarning(scribe0, WarnStalledAtDialog) == "" || hasWarning(scribe0, WarnLaunchBudget) == "" {
		t.Fatalf("fixture: scribe's warnings %q", scribe0.Warnings)
	}
	if r := schedproc.RecordClear(root, "cto", schedproc.ClearRequest{Seat: "scribe", Warning: WarnStalledAtDialog, LaunchID: "launch-1"}, CurrentSeatWarnings); !r.OK {
		t.Fatalf("clear: %+v", r)
	}
	after := collect()
	scribe := seatByHandle(t, after, "scribe")
	if hasWarning(scribe, WarnStalledAtDialog) != "" {
		t.Errorf("the cleared warning is still a warning: %q", scribe.Warnings)
	}
	if len(scribe.AcknowledgedWarnings) != 1 || !strings.HasPrefix(scribe.AcknowledgedWarnings[0], WarnStalledAtDialog+": ") {
		t.Errorf("acknowledged: %q", scribe.AcknowledgedWarnings)
	}
	if hasWarning(scribe, WarnLaunchBudget) == "" {
		t.Errorf("a warning that was not cleared left too: %q", scribe.Warnings)
	}
	if scribe.State != scribe0.State || scribe.ReasonCode != scribe0.ReasonCode || scribe.Bucket != scribe0.Bucket || scribe.Live != scribe0.Live || scribe.Launch.Outcome != scribe0.Launch.Outcome {
		t.Errorf("the clear changed the seat: before %s/%s/%s live=%v %s, after %s/%s/%s live=%v %s",
			scribe0.State, scribe0.ReasonCode, scribe0.Bucket, scribe0.Live, scribe0.Launch.Outcome, scribe.State, scribe.ReasonCode, scribe.Bucket, scribe.Live, scribe.Launch.Outcome)
	}
	if pi0, pi := seatByHandle(t, before, "pi-nokey"), seatByHandle(t, after, "pi-nokey"); !slices.Equal(pi0.Warnings, pi.Warnings) || len(pi.AcknowledgedWarnings) != 0 {
		t.Errorf("another seat changed: %q -> %q (%q)", pi0.Warnings, pi.Warnings, pi.AcknowledgedWarnings)
	}
	// The seat's next launch: the same condition is a new line.
	if err := launch.UpdateStaged(root, "scribe", func(r *launch.Record) error { r.LaunchID = "launch-2"; return nil }); err != nil {
		t.Fatal(err)
	}
	next := seatByHandle(t, collect(), "scribe")
	if hasWarning(next, WarnStalledAtDialog) == "" || len(next.AcknowledgedWarnings) != 0 {
		t.Errorf("a later launch's same condition: warnings %q, acknowledged %q", next.Warnings, next.AcknowledgedWarnings)
	}
	if r := schedproc.RecordClear(root, "cto", schedproc.ClearRequest{Seat: "scribe", Warning: WarnStalledAtDialog, LaunchID: "launch-1"}, CurrentSeatWarnings); r.OK || r.Code != schedproc.RefusedClearStale {
		t.Errorf("a clear of the earlier launch: %+v", r)
	}
}

// Codex's P2 on #831 (status's half): the scheduler reads the seat's
// warnings through CurrentSeatWarnings, the computation status shows, so a
// warning that is not on the screen cannot be cleared ahead of it: a clear
// of seat_stalled_at_dialog before any dialog is refused
// clear_not_showing, and when the dialog then opens on the same launch the
// line is a warning, not hidden. Reverted (the code unchecked), the
// pre-clear is recorded and the later dialog never shows.
func TestAWarningCannotBeClearedBeforeItShows(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "root")
	now := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	sec := time.Second
	budgetSeat(t, root, "scribe", "claude", "sonnet", now.Add(-time.Minute), sec, sec, sec, sec, nil)
	if r := schedproc.RecordClear(root, "cto", schedproc.ClearRequest{Seat: "scribe", Warning: WarnStalledAtDialog, LaunchID: "launch-1"}, CurrentSeatWarnings); r.OK || r.Code != schedproc.RefusedClearNotShowing {
		t.Fatalf("a clear ahead of the dialog: %+v", r)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "scribe", ".wake.dialog"), []byte(`{"harness":"claude"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	scribe := seatByHandle(t, Build(Collect(CollectOptions{Root: root, Now: now, PIDAlive: func(int) bool { return true }})), "scribe")
	if hasWarning(scribe, WarnStalledAtDialog) == "" || len(scribe.AcknowledgedWarnings) != 0 {
		t.Fatalf("the dialog that opened later: warnings %q, acknowledged %q", scribe.Warnings, scribe.AcknowledgedWarnings)
	}
}

// The clearable codes are exactly the seat warnings status shows: a new
// seat warning is clearable only once it is added to
// schedproc.ClearableWarnings, and no refusal code is in that list.
func TestTheClearableCodesAreStatusSeatWarnings(t *testing.T) {
	shown := []string{claudeseat.HooksSilent, claudeseat.AllowRulesPresent, claudeseat.ExternalIncludesPending,
		WarnLaunchBudget, WarnWakeOff, WarnStalledAtDialog, WarnPiProviderNoKey}
	slices.Sort(shown)
	clearable := slices.Clone(schedproc.ClearableWarnings)
	slices.Sort(clearable)
	if !slices.Equal(shown, clearable) {
		t.Fatalf("status's seat warnings %q, clearable %q", shown, clearable)
	}
}
