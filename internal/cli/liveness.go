package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"
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
// launch-record read failure visible; InspectSignal and LaunchapiObservation
// (gh#737/#766) are set only by classifyAgentLivenessForRollup and never
// change Verdict/Status except through applyLaunchapiInspectionFloor.
type agentLiveness struct {
	Verdict              agentLivenessVerdict
	SourceError          string
	Status               statusState
	Detail               string
	Signals              statusSignals
	PresenceLive         bool
	LaunchRecord         launch.Record
	LaunchFound          bool
	RuntimeIdentity      launchRuntimeIdentity
	InspectSignal        launchapiInspectSignal
	LaunchapiObservation *launchapiObservationJSON
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

// launchapiInspectOutcome classifies one session-level launchapi.Inspect
// call (gh#737). AMQ owns liveness in 3.0, so this floor is kept.
type launchapiInspectOutcome int

const (
	// NotApplicable: binding_missing or an unidentifiable target (not
	// launchapi-launched); per-seat classification stands unchanged.
	launchapiInspectNotApplicable launchapiInspectOutcome = iota
	// CallFailed: the Inspect call errored; never clamp, surface a warning.
	launchapiInspectCallFailed
	// Floor: State unknown or action required; cap every live verdict.
	launchapiInspectFloor
	// PresentSignal: corroborates per-seat evidence, never overrides it.
	launchapiInspectPresentSignal
	// AbsentSignal: a live seat conflicts with it and is capped at stale.
	launchapiInspectAbsentSignal
)

const (
	launchapiInspectStatePresent = "present"
	launchapiInspectStateAbsent  = "absent"
)

// launchapiInspectSignal is the session Inspect result, its evidence text,
// and the per-participant observations callers match by handle.
type launchapiInspectSignal struct {
	Outcome      launchapiInspectOutcome
	Evidence     string
	Observations []launchapi.ParticipantObservationV1
}

// launchapiObservationJSON is one member's matched ParticipantObservationV1
// on statusRecord (gh#766).
type launchapiObservationJSON struct {
	Handle      string `json:"handle"`
	Runnable    bool   `json:"runnable"`
	Execution   string `json:"execution,omitempty"`
	ReasonCode  string `json:"reason_code,omitempty"`
	Disposition string `json:"disposition,omitempty"`
}

// launchapiObservationForHandle finds handle's observation, if any.
func launchapiObservationForHandle(observations []launchapi.ParticipantObservationV1, handle string) *launchapiObservationJSON {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return nil
	}
	for _, obs := range observations {
		if strings.TrimSpace(obs.Handle) == handle {
			return &launchapiObservationJSON{
				Handle:      obs.Handle,
				Runnable:    obs.Runnable,
				Execution:   obs.Execution,
				ReasonCode:  obs.ReasonCode,
				Disposition: obs.Disposition,
			}
		}
	}
	return nil
}

// launchapiObservationIsNotable: not runnable or a reason code; only these
// are appended to Detail.
func launchapiObservationIsNotable(obs *launchapiObservationJSON) bool {
	if obs == nil {
		return false
	}
	return !obs.Runnable || strings.TrimSpace(obs.ReasonCode) != ""
}

// launchapiInspect is a seam so tests stub the call.
var launchapiInspect = launchapi.Inspect

// launchapiSessionInspect calls Inspect once per (project, base root,
// session) for the process lifetime. projectRoot is the team's canonical
// root, not a member worktree (launchapi requires the canonical path).
func launchapiSessionInspect(projectRoot, baseRoot, session string) launchapiInspectSignal {
	projectRoot = strings.TrimSpace(projectRoot)
	baseRoot = strings.TrimSpace(baseRoot)
	session = strings.TrimSpace(session)
	if projectRoot == "" || baseRoot == "" || session == "" {
		return launchapiInspectSignal{Outcome: launchapiInspectNotApplicable, Evidence: "incomplete session target"}
	}
	key := projectRoot + "\x00" + baseRoot + "\x00" + session
	launchapiInspectMemoMu.Lock()
	if cached, ok := launchapiInspectMemo[key]; ok {
		launchapiInspectMemoMu.Unlock()
		return cached
	}
	launchapiInspectMemoMu.Unlock()

	signal := launchapiSessionInspectUncached(projectRoot, baseRoot, session)

	launchapiInspectMemoMu.Lock()
	launchapiInspectMemo[key] = signal
	launchapiInspectMemoMu.Unlock()
	return signal
}

var (
	launchapiInspectMemoMu sync.Mutex
	launchapiInspectMemo   = map[string]launchapiInspectSignal{}
)

// launchapiSessionInspectUncached: SessionRoot must be BaseRoot's direct
// child named Session, and ProjectRoot/BaseRoot must be canonical, or
// launchapi refuses (base_root_relation_invalid / base_root_unauthorized).
// Those two refusals mean no launchapi target here, not a call failure.
func launchapiSessionInspectUncached(projectRoot, baseRoot, session string) launchapiInspectSignal {
	result, err := launchapiInspect(context.Background(), launchapi.InspectRequestV1{
		RequestVersion: launchapi.RequestVersionV1,
		Target: launchapi.TargetV1{
			ProjectRoot: canonicalFilesystemPath(projectRoot),
			BaseRoot:    canonicalFilesystemPath(baseRoot),
			SessionRoot: filepath.Join(canonicalFilesystemPath(baseRoot), session),
			Session:     session,
		},
	})
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "base_root_unauthorized") || strings.Contains(msg, "base_root_relation_invalid") {
			return launchapiInspectSignal{Outcome: launchapiInspectNotApplicable, Evidence: msg}
		}
		return launchapiInspectSignal{Outcome: launchapiInspectCallFailed, Evidence: msg}
	}
	if result.ReasonCode == "binding_missing" {
		return launchapiInspectSignal{Outcome: launchapiInspectNotApplicable, Evidence: result.ReasonCode, Observations: result.Observations}
	}
	switch result.State {
	case launchapiInspectStatePresent:
		return launchapiInspectSignal{Outcome: launchapiInspectPresentSignal, Evidence: result.State, Observations: result.Observations}
	case launchapiInspectStateAbsent:
		return launchapiInspectSignal{Outcome: launchapiInspectAbsentSignal, Evidence: result.State, Observations: result.Observations}
	default:
		evidence := result.State
		if result.ReasonCode != "" {
			evidence = result.State + ": " + result.ReasonCode
		}
		return launchapiInspectSignal{Outcome: launchapiInspectFloor, Evidence: evidence, Observations: result.Observations}
	}
}

// applyLaunchapiInspectionFloor applies the session Inspect result to one
// member's verdict. Pure, so the floor rules are unit-testable.
func applyLaunchapiInspectionFloor(live agentLiveness, signal launchapiInspectSignal, handle string) agentLiveness {
	live.InspectSignal = signal
	live.LaunchapiObservation = launchapiObservationForHandle(signal.Observations, handle)
	switch signal.Outcome {
	case launchapiInspectNotApplicable:
		return live
	case launchapiInspectPresentSignal:
		live.Detail = launchapiNoLaunchRecordDetail(live)
		live.Detail = launchapiAppendObservationDetail(live)
		return live
	case launchapiInspectCallFailed:
		live.Detail = launchapiNoLaunchRecordDetail(live)
		live.Detail = launchapiAppendObservationDetail(live)
		return live
	case launchapiInspectFloor:
		if live.Live() {
			live.Verdict = livenessStale
			live.Status = statusStateStale
			live.Detail = fmt.Sprintf("session Inspect reports %s; capping below live regardless of this seat's own signals (unknown never reads as clear)", signal.Evidence)
		}
		live.Detail = launchapiAppendObservationDetail(live)
		return live
	case launchapiInspectAbsentSignal:
		if live.Live() {
			live.Verdict = livenessStale
			live.Status = statusStateStale
			live.Detail = fmt.Sprintf("session Inspect reports absent while this seat's own signals report live (%s); treating as stale pending reconciliation", live.Detail)
		}
		live.Detail = launchapiAppendObservationDetail(live)
		return live
	default:
		return live
	}
}

// launchapiAppendObservationDetail appends a notable observation to Detail.
func launchapiAppendObservationDetail(live agentLiveness) string {
	if !launchapiObservationIsNotable(live.LaunchapiObservation) {
		return live.Detail
	}
	obs := live.LaunchapiObservation
	note := fmt.Sprintf("launchapi observes: runnable=%t", obs.Runnable)
	if obs.Execution != "" {
		note += " execution=" + obs.Execution
	}
	if obs.ReasonCode != "" {
		note += " reason=" + obs.ReasonCode
	}
	if strings.TrimSpace(live.Detail) == "" {
		return note
	}
	return live.Detail + "; " + note
}

// launchapiNoLaunchRecordDetail explains a launchapi-launched seat's missing
// launch record (launchapi seats never write one).
func launchapiNoLaunchRecordDetail(live agentLiveness) string {
	if live.LaunchFound {
		return live.Detail
	}
	note := "no launch record (launchapi-launched); classified from wake/presence + session Inspect"
	if strings.TrimSpace(live.Detail) == "" {
		return note
	}
	return note + " (" + live.Detail + ")"
}

// classifyAgentLivenessForRollup is the disk classifier plus the session
// Inspect floor; status uses it.
func classifyAgentLivenessForRollup(projectRoot, agentDir, root, expectedProfile, handle, role, binary, workstream, cwd string, probe duplicateLaunchProbe) agentLiveness {
	live := classifyAgentLivenessFromDisk(agentDir, root, expectedProfile, handle, binary, probe)
	signal := launchapiSessionInspect(projectRoot, root, workstream)
	return applyLaunchapiInspectionFloor(live, signal, handle)
}
