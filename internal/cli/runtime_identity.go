package cli

import (
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
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
	PaneTitle        func(string) (string, bool)
}

type launchRuntimeIdentity struct {
	Live        bool
	PIDLive     bool
	PaneLive    bool
	PIDAlive    bool
	BinaryMatch bool
}

// classifyLaunchRuntimeIdentity is the single launch-record runtime identity
// predicate. Context resolution, implicit TeamHome bootstrap, status/resume,
// and cleanup all consume this result rather than maintaining lookalike
// liveness checks.
func classifyLaunchRuntimeIdentity(rec launch.Record, expectedBinary, currentPane string, probe launchRuntimeProbe) launchRuntimeIdentity {
	var out launchRuntimeIdentity
	if rec.StoppedAt != nil && !rec.StoppedAt.IsZero() {
		return out
	}

	if rec.AgentPID > 0 && probe.PIDAlive != nil && probe.PIDAlive(rec.AgentPID) {
		out.PIDAlive = true
		binary := strings.TrimSpace(rec.Binary)
		if binary == "" {
			binary = strings.TrimSpace(expectedBinary)
		}
		launcher := strings.TrimSpace(rec.Launcher)
		if (binary != "" || launcher != "") && probe.ProcessMatch != nil && probe.ProcessMatch(rec.AgentPID, launchRecordProcessMatcher(binary, launcher)) {
			out.BinaryMatch = true
			reusedPID := false
			if !rec.StartedAt.IsZero() && probe.ProcessStartTime != nil {
				if processStartedAt, ok := probe.ProcessStartTime(rec.AgentPID); ok &&
					processStartedAt.After(rec.StartedAt.Add(launchProcessStartSkewEpsilon)) {
					reusedPID = true
				}
			}
			if !reusedPID {
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
			}
		}
	}

	if rec.Tmux != nil {
		paneID := strings.TrimSpace(rec.Tmux.PaneID)
		currentPane = strings.TrimSpace(currentPane)
		if paneID != "" && (rec.External || paneID == currentPane) && probe.PaneTitle != nil {
			role := strings.TrimSpace(rec.Role)
			if role == "" {
				role = strings.TrimSpace(rec.Handle)
			}
			session := strings.TrimSpace(rec.Session)
			if role != "" && session != "" {
				if title, ok := probe.PaneTitle(paneID); ok && strings.TrimSpace(title) == paneTitleToken(session, role) {
					out.PaneLive = true
				}
			}
		}
	}
	out.Live = out.PIDLive || out.PaneLive
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
		PaneTitle: func(paneID string) (string, bool) {
			pane, ok := statusPaneInspector(paneID)
			return pane.Title, ok
		},
	}
}

// classifyLaunchPIDRuntimeIdentity is the launch-record PID adapter for
// callers that do not need pane identity. It still delegates the entire
// decision to classifyLaunchRuntimeIdentity so binary, birth time, TTY, and
// StoppedAt semantics cannot drift.
func classifyLaunchPIDRuntimeIdentity(rec launch.Record, expectedBinary string, probe duplicateLaunchProbe) launchRuntimeIdentity {
	runtimeProbe := launchRuntimeProbeFromDuplicate(probe)
	runtimeProbe.PaneTitle = nil
	return classifyLaunchRuntimeIdentity(rec, expectedBinary, "", runtimeProbe)
}
