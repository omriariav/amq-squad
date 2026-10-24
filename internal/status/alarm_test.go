package status

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// 2b-2 item 4: an identity_forged launch alarm reaches the owner as a
// needs_you attention entry, read from the core-owned alarm directory.
func TestIdentityForgedAlarmIsNeedsYou(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	if err := launch.WriteAlarm(root, launch.Alarm{Kind: "identity_forged", Code: "proof_replayed", Handle: "cto", LaunchID: "l1", Detail: "a second proof", At: now}); err != nil {
		t.Fatal(err)
	}
	in := Collect(CollectOptions{Root: root, Now: now})
	if len(in.LaunchAlarms) != 1 {
		t.Fatalf("collected alarms %+v", in.LaunchAlarms)
	}
	st := Build(Input{Now: now, Scheduler: scheduler.State{MaxSeats: 12}, LaunchAlarms: in.LaunchAlarms})
	for _, a := range st.Attention {
		if a.Kind == "identity_forged" && a.Seat == "cto" && a.Bucket == BucketNeedsYou {
			return
		}
	}
	t.Fatalf("no needs_you entry: %+v", st.Attention)
}
