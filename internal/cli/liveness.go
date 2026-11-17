package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
)

// agentLivenessVerdict is the closed seat-state vocabulary (manifest C10).
// 3.0 (2a-2) cuts the pane and wake inputs: the classifier produces only
// agent-live, presence-live, stale and missing; wake-live and
// replacement-live stay named states of the vocabulary.
type agentLivenessVerdict string

const (
	livenessAgentLive       agentLivenessVerdict = "agent-live"
	livenessWakeLive        agentLivenessVerdict = "wake-live"
	livenessPresenceLive    agentLivenessVerdict = "presence-live"
	livenessReplacementLive agentLivenessVerdict = "replacement-live"
	livenessStale           agentLivenessVerdict = "stale"
	livenessMissing         agentLivenessVerdict = "missing"
)

// agentLiveness is the classifier's output: the verdict plus what status
// renders without re-reading disk. SourceError keeps a non-ENOENT
// launch-record read failure visible.
type agentLiveness struct {
	Verdict         agentLivenessVerdict
	SourceError     string
	Status          statusState
	Detail          string
	Signals         statusSignals
	PresenceLive    bool
	LaunchRecord    launch.Record
	LaunchFound     bool
	RuntimeIdentity launchRuntimeIdentity
}

// Live reports whether the verdict is any of the live sub-states.
func (l agentLiveness) Live() bool {
	switch l.Verdict {
	case livenessAgentLive, livenessWakeLive, livenessPresenceLive, livenessReplacementLive:
		return true
	default:
		return false
	}
}

// classifyAgentLivenessFromDisk reads launch.json and presence.json once:
//   - agent-live: launch AgentPID alive with binary/start-time/tty identity;
//   - presence-live: fresh, active, same-handle presence whose recorded
//     writer is not known dead (the zombie-heartbeat guard).
func classifyAgentLivenessFromDisk(agentDir, root, expectedProfile, handle, binary string, probe duplicateLaunchProbe) agentLiveness {
	out := agentLiveness{}
	var launchIdentity launchRuntimeIdentity

	launchRec, launchErr := launch.Read(agentDir)
	if launchErr == nil {
		out.LaunchFound = true
		out.LaunchRecord = launchRec
		if !squadnamespace.ProfilesEqual(expectedProfile, launchRec.TeamProfile) {
			out.Verdict = livenessStale
			out.Status = statusStateStale
			out.Detail = fmt.Sprintf("launch record profile %q does not match requested profile %q", squadnamespace.NormalizeProfile(launchRec.TeamProfile), squadnamespace.NormalizeProfile(expectedProfile))
			return out
		}
		launchIdentity = classifyLaunchRuntimeIdentity(launchRec, binary, launchRuntimeProbeFromDuplicate(probe))
		out.RuntimeIdentity = launchIdentity
		if launchRec.StoppedAt != nil && !launchRec.StoppedAt.IsZero() {
			out.Verdict = livenessStale
			out.Status = statusStateStale
			out.Detail = fmt.Sprintf("launch record explicitly stopped at %s", launchRec.StoppedAt.UTC().Format(time.RFC3339))
			return out
		}
	} else if !os.IsNotExist(launchErr) {
		out.SourceError = "read launch record: " + launchErr.Error()
	}
	presence, presenceErr := readPresenceForEntry(agentDir)
	hasLaunchPID := launchErr == nil && launchRec.AgentPID > 0
	if hasLaunchPID {
		out.Signals.AgentPID = launchRec.AgentPID
		out.Signals.AgentAlive = launchIdentity.PIDAlive
		out.Signals.BinaryMatch = launchIdentity.BinaryMatch
	}

	// A fresh presence only proves something wrote it within 90s;
	// presenceWriterIsKnownDead demotes it when the launch writer is dead.
	presenceLive := false
	presenceMismatched := false
	if presenceErr == nil {
		out.Signals.Presence = presence.Status
		out.Signals.LastSeen = presence.LastSeen
		fresh := !presence.LastSeen.IsZero() && probe.Now().Sub(presence.LastSeen) <= presenceFreshness
		active := strings.EqualFold(presence.Status, "active")
		handleOK := presence.Handle == "" || presence.Handle == handle
		switch {
		case fresh && active && handleOK && !presenceWriterIsKnownDead(agentDir, binary, probe):
			presenceLive = true
		case fresh && active && !handleOK:
			presenceMismatched = true
		}
	}
	out.PresenceLive = presenceLive

	switch {
	case launchIdentity.PIDLive:
		out.Verdict = livenessAgentLive
		out.Status = statusStateLive
		out.Detail = fmt.Sprintf("agent pid %d alive (%s)", out.Signals.AgentPID, binary)
	case presenceLive:
		out.Verdict = livenessPresenceLive
		out.Status = statusStateLive
		out.Detail = fmt.Sprintf("fresh active presence, no verified pid (last seen %s)", presence.LastSeen.UTC().Format(time.RFC3339))
	case !hasLaunchPID && !presenceMismatched:
		out.Verdict = livenessMissing
		out.Status = statusStateMissing
		out.Detail = "no live signals for this handle"
	default:
		out.Verdict = livenessStale
		out.Status = statusStateStale
		out.Detail = staleDetail(out.Signals, presenceMismatched) + "; relaunch via amq-squad to re-register"
	}
	return out
}
