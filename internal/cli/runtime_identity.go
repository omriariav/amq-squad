package cli

import (
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// launchProcessStartSkewEpsilon absorbs the wall-clock reconstruction error in
// Linux procfs start times (btime is only second-granularity) plus small clock
// adjustments. A process born more than two seconds after the launch record is
// a reused PID; at or inside the boundary it must still pass binary and TTY
// identity checks.
const launchProcessStartSkewEpsilon = 2 * time.Second

type launchRuntimeProbe struct {
	PIDAlive         func(int) bool
	ProcessMatch     func(int, func(string) bool) bool
	ProcessTTY       func(int) (string, bool)
	ProcessStartTime func(int) (time.Time, bool)
}

// launchRuntimeIdentity is the launch-record PID identity. 3.0 (2a-2): the v2
// pane inputs (PaneLive, PaneTitleMatch, the pane-title and pane-tty
// corroboration) are cut with the tmux pane driver (manifest C10/C2).
type launchRuntimeIdentity struct {
	PIDLive     bool
	PIDAlive    bool
	BinaryMatch bool
}

// classifyLaunchRuntimeIdentity is the single launch-record runtime identity
// predicate: the recorded agent PID is alive, runs the recorded binary or
// launcher, was not born after the record (PID reuse), and holds the recorded
// tty. Context resolution, status and doctor all consume this result.
func classifyLaunchRuntimeIdentity(rec launch.Record, expectedBinary string, probe launchRuntimeProbe) launchRuntimeIdentity {
	var out launchRuntimeIdentity
	if rec.StoppedAt != nil && !rec.StoppedAt.IsZero() {
		return out
	}
	if rec.AgentPID <= 0 || probe.PIDAlive == nil || !probe.PIDAlive(rec.AgentPID) {
		return out
	}
	out.PIDAlive = true
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = strings.TrimSpace(expectedBinary)
	}
	launcher := strings.TrimSpace(rec.Launcher)
	if (binary == "" && launcher == "") || probe.ProcessMatch == nil || !probe.ProcessMatch(rec.AgentPID, launchRecordProcessMatcher(binary, launcher)) {
		return out
	}
	out.BinaryMatch = true
	if !rec.StartedAt.IsZero() && probe.ProcessStartTime != nil {
		if processStartedAt, ok := probe.ProcessStartTime(rec.AgentPID); ok &&
			processStartedAt.After(rec.StartedAt.Add(launchProcessStartSkewEpsilon)) {
			return out // reused PID
		}
	}
	recordedTTY := strings.TrimSpace(rec.AgentTTY)
	ttyMatches := recordedTTY == "" || recordedTTY == "unknown"
	if !ttyMatches {
		observedTTY, ok := "", false
		if probe.ProcessTTY != nil {
			observedTTY, ok = probe.ProcessTTY(rec.AgentPID)
		}
		ttyMatches = !ok || sameResolvedDir(recordedTTY, observedTTY)
	}
	out.PIDLive = ttyMatches
	return out
}

// launchRecordProcessMatcher recognizes either supported image for the
// recorded child PID. A custom launcher is exec'd in place of the configured
// binary and may remain the long-running process, while forwarding launchers
// may replace themselves with the binary. Both are exact recorded identities.
func launchRecordProcessMatcher(binary, launcher string) func(args string) bool {
	binaryMatch := agentProcessMatcher(binary)
	launcherMatch := agentProcessMatcher(launcher)
	return func(args string) bool {
		return binaryMatch(args) || launcherMatch(args)
	}
}

func launchRuntimeProbeFromDuplicate(probe duplicateLaunchProbe) launchRuntimeProbe {
	return launchRuntimeProbe{
		PIDAlive:         probe.PIDAlive,
		ProcessMatch:     probe.ProcessMatch,
		ProcessTTY:       probe.ProcessTTY,
		ProcessStartTime: probe.ProcessStartTime,
	}
}
