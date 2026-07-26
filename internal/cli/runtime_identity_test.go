package cli

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

func TestLaunchRuntimeIdentityProcessStartSkewBoundary(t *testing.T) {
	recordedAt := time.Now().UTC()
	rec := launch.Record{
		AgentPID: 42, Binary: "codex", AgentTTY: "/dev/ttys007",
		StartedAt: recordedAt,
	}
	startedAt := recordedAt.Add(launchProcessStartSkewEpsilon)
	probe := launchRuntimeProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "/dev/ttys007", true },
		ProcessStartTime: func(int) (time.Time, bool) {
			return startedAt, true
		},
	}
	if got := classifyLaunchRuntimeIdentity(rec, "codex", "", probe); !got.PIDLive {
		t.Fatalf("process at skew boundary classified reused: %+v", got)
	}
	startedAt = startedAt.Add(time.Nanosecond)
	if got := classifyLaunchRuntimeIdentity(rec, "codex", "", probe); got.PIDLive || got.Live {
		t.Fatalf("process just beyond skew boundary classified live: %+v", got)
	}
}
