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
	PaneTTY          func(string) (string, bool)
}

type launchRuntimeIdentity struct {
	Live     bool
	PIDLive  bool
	PaneLive bool
	// PaneTitleMatch is true only when PaneLive was granted on the primary
	// path: the pane's title (or its durable @amq_squad_title option, which the
	// inspector projects through the title) equals the recorded token. False
	// when PaneLive was granted by the process-tty corroboration fallback, so
	// callers can re-stamp the clobbered fingerprint (#655).
	PaneTitleMatch bool
	PIDAlive       bool
	BinaryMatch    bool
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
		if paneID != "" && (rec.External || paneID == currentPane) {
			role := strings.TrimSpace(rec.Role)
			if role == "" {
				role = strings.TrimSpace(rec.Handle)
			}
			session := strings.TrimSpace(rec.Session)
			observedTitle, observedTitleOK := "", false
			if probe.PaneTitle != nil {
				observedTitle, observedTitleOK = probe.PaneTitle(paneID)
				if observedTitleOK && role != "" && session != "" &&
					strings.TrimSpace(observedTitle) == paneTitleToken(session, role) {
					out.PaneLive = true
					out.PaneTitleMatch = true
				}
			}
			// The app inside a pane owns #{pane_title} and rewrites it — an
			// in-place agent restart (e.g. a CLI self-upgrade re-exec) clobbers
			// the stamped token while the pane and the agent stay alive (#655).
			// A stale title alone must therefore not condemn the pane: when the
			// recorded agent process is verified live, tie it to the pane by
			// its controlling pty — same device means the live agent is running
			// in exactly that pane, regardless of what the title says.
			//
			// EXCEPT when the pane carries a valid amq token for a DIFFERENT
			// agent: that pane was deliberately reassigned, and a lingering
			// recorded process on the same pty must not out-vote the current
			// owner's durable token (codex review of #659). Only an absent or
			// non-amq application title is corroborable; a conflicting token
			// fails closed, same policy as paneTitledForDifferentAgent.
			if !out.PaneLive && out.PIDLive && probe.PaneTTY != nil && probe.ProcessTTY != nil &&
				!(observedTitleOK && paneTitledForDifferentAgent(observedTitle, session, role)) {
				if paneTTY, ok := probe.PaneTTY(paneID); ok {
					if agentTTY, ttyOK := probe.ProcessTTY(rec.AgentPID); ttyOK && sameResolvedDir(paneTTY, agentTTY) {
						out.PaneLive = true
					}
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
		PaneTTY: func(paneID string) (string, bool) {
			return statusPaneTTYInspector(paneID)
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
	runtimeProbe.PaneTTY = nil
	return classifyLaunchRuntimeIdentity(rec, expectedBinary, "", runtimeProbe)
}
