package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/runtimecontrol"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// agentLivenessVerdict is the single shared liveness vocabulary that BOTH
// `status` and `resume` consume so they can never disagree about whether an
// agent is alive. It is finer-grained than statusState (it distinguishes the
// three "live" sub-reasons) so callers can map it to their own surface:
// status collapses agent/presence/replacement to "live"; resume treats all
// four live verdicts as "skip-live".
type agentLivenessVerdict string

const (
	// livenessAgentLive: the launch-record AgentPID is alive AND its binary
	// matches. The agent process itself is verified running.
	livenessAgentLive agentLivenessVerdict = "agent-live"
	// livenessWakeLive: the wake helper is verified live for this handle/root,
	// but the agent PID itself is not verified.
	livenessWakeLive agentLivenessVerdict = "wake-live"
	// livenessPresenceLive: a fresh, active presence.json for this handle, with
	// no verified PID. The agent is heartbeating even though no PID is proven.
	livenessPresenceLive agentLivenessVerdict = "presence-live"
	// livenessReplacementLive: the recorded PID is dead, but a live tmux pane
	// resolves to this member (the relaunched-outside-amq-squad case).
	livenessReplacementLive agentLivenessVerdict = "replacement-live"
	// livenessStale: live-pointing disk signals exist (launch record, wake
	// lock, or mismatched fresh presence) but none verify as usable for this
	// handle.
	livenessStale agentLivenessVerdict = "stale"
	// livenessMissing: no launch record, no wake lock, no usable presence for
	// this handle. The member is configured but has never run (or its artifacts
	// are gone) in the resolved session.
	livenessMissing agentLivenessVerdict = "missing"
)

// agentLiveness is the shared classifier's output: the verdict plus everything
// status and resume need to render their respective surfaces without
// re-reading disk or re-running the probe.
type agentLiveness struct {
	Verdict agentLivenessVerdict
	// Status is the statusState this verdict maps to, so classifyMemberStatus
	// can adopt it directly.
	Status statusState
	// Detail is the human-readable one-line explanation, identical to the
	// detail string status emitted before unification.
	Detail string
	// Signals is the populated status signal block (agent pid/alive/match,
	// wake pid/alive, presence/last-seen).
	Signals statusSignals
	// PresenceLive is true when a fresh, active, same-handle presence is a real
	// live signal (i.e. it passed the zombie-heartbeat guard). It is kept
	// separate from the single Verdict so resume can list EVERY live source in
	// its note (e.g. "wake+launch+presence"), matching the pre-unification
	// blocker summary.
	PresenceLive bool
	// LaunchRecord is the parsed launch.json (zero value when none/unreadable).
	LaunchRecord launch.Record
	// LaunchFound is true when launch.json parsed successfully.
	LaunchFound bool
	// Tmux is the persisted tmux runtime identity from the launch record, when
	// any. nil when the record carried no tmux block or no record was found.
	Tmux *launch.TmuxInfo
	// ReplacementTarget is the live tmux pane jump target when the verdict is
	// replacement-live; empty otherwise.
	ReplacementTarget string
}

// Live reports whether the verdict is any of the live sub-states. Both status
// (live/wake-live) and resume (skip-live) branch on this.
func (l agentLiveness) Live() bool {
	switch l.Verdict {
	case livenessAgentLive, livenessWakeLive, livenessPresenceLive, livenessReplacementLive:
		return true
	default:
		return false
	}
}

// classifyAgentLiveness performs ONE read of launch.json / wake.lock /
// presence plus the probe checks and returns the single shared verdict that
// status and resume both consume. It reproduces the exact signal+state logic
// that classifyMemberStatus used before unification, including:
//   - agent: launch AgentPID alive AND binary matches,
//   - wake: wake-lock PID alive AND an `amq wake` for this handle/root,
//   - presence: fresh + active + handle rules (the zombie-writer guard is
//     applied by the caller's freshness check exactly as before — see below),
//   - the live-replacement-pane fallback (relaunched-outside-amq-squad).
//
// agentDir is the resolved mailbox dir; root is its AMQ root; expectedProfile
// is the selected team profile; handle is the resolved handle; role/binary/
// workstream identify the member for the replacement-pane resolver. probe
// abstracts liveness/process inspection so tests inject deterministic behavior.
func classifyAgentLiveness(agentDir, root, expectedProfile, handle, role, binary, workstream, cwd string, probe duplicateLaunchProbe) agentLiveness {
	out := agentLiveness{}

	launchRec, launchErr := launch.Read(agentDir)
	if launchErr == nil {
		out.LaunchFound = true
		out.LaunchRecord = launchRec
		out.Tmux = launchRec.Tmux
		if !squadnamespace.ProfilesEqual(expectedProfile, launchRec.TeamProfile) {
			out.Tmux = nil
			out.Verdict = livenessStale
			out.Status = statusStateStale
			out.Detail = fmt.Sprintf("launch record profile %q does not match requested profile %q", squadnamespace.NormalizeProfile(launchRec.TeamProfile), squadnamespace.NormalizeProfile(expectedProfile))
			return out
		}
	}
	wakeLock, wakeErr := readWakeLock(agentDir)
	presence, presenceErr := readPresenceForEntry(agentDir)

	hasLaunchPID := launchErr == nil && launchRec.AgentPID > 0
	hasWakePID := wakeErr == nil && wakeLock.PID > 0

	if hasLaunchPID {
		out.Signals.AgentPID = launchRec.AgentPID
		if probe.PIDAlive(launchRec.AgentPID) {
			out.Signals.AgentAlive = true
			b := strings.TrimSpace(launchRec.Binary)
			if b == "" {
				b = binary
			}
			if b != "" && probe.ProcessMatch(launchRec.AgentPID, agentProcessMatcher(b)) {
				out.Signals.BinaryMatch = true
			}
		}
	}
	if hasWakePID {
		out.Signals.WakePID = wakeLock.PID
		if probe.PIDAlive(wakeLock.PID) {
			expectedRoot := root
			if wakeLock.Root != "" {
				expectedRoot = wakeLock.Root
			}
			if probe.ProcessMatch(wakeLock.PID, wakeProcessMatcher(handle, expectedRoot)) {
				out.Signals.WakeAlive = true
			}
		}
	}

	// Presence freshness/active/handle rules, plus the preflight's
	// zombie-heartbeat guard (#38/#44). A fresh presence only proves SOMETHING
	// wrote the file in the last 90s; if both the launch and wake writer records
	// exist and both PIDs are confirmed dead, the file is a leftover heartbeat,
	// not a live agent. presenceWriterIsKnownDead is the SAME guard the launch
	// preflight applies, so status, resume, and preflight agree. It is
	// conservative: only a both-records-present, both-dead case demotes
	// presence; a missing/unknown writer keeps presence as live (unchanged).
	presenceLive := false
	presenceMismatched := false
	if presenceErr == nil {
		out.Signals.Presence = presence.Status
		out.Signals.LastSeen = presence.LastSeen
		fresh := !presence.LastSeen.IsZero() && probe.Now().Sub(presence.LastSeen) <= presenceFreshness
		active := strings.EqualFold(presence.Status, "active")
		handleOK := presence.Handle == "" || presence.Handle == handle
		switch {
		case fresh && active && handleOK && !presenceWriterIsKnownDead(agentDir, root, handle, binary, probe):
			presenceLive = true
		case fresh && active && !handleOK:
			presenceMismatched = true
		}
	}
	out.PresenceLive = presenceLive

	if out.Signals.AgentAlive && out.Signals.BinaryMatch {
		out.Verdict = livenessAgentLive
		out.Status = statusStateLive
		out.Detail = fmt.Sprintf("agent pid %d alive (%s)", out.Signals.AgentPID, binary)
		return out
	}
	if launchErr == nil && launchRec.External && launchRec.Tmux != nil && strings.TrimSpace(launchRec.Tmux.PaneID) != "" {
		if _, ok := statusPaneInspector(launchRec.Tmux.PaneID); ok {
			out.Verdict = livenessAgentLive
			out.Status = statusStateLive
			out.Detail = fmt.Sprintf("external pane %s live (registered lead)", launchRec.Tmux.PaneID)
			return out
		}
	}
	if presenceLive {
		out.Verdict = livenessPresenceLive
		out.Status = statusStateLive
		out.Detail = fmt.Sprintf("fresh active presence, no verified pid (last seen %s)", presence.LastSeen.UTC().Format(time.RFC3339))
		return out
	}
	if out.Signals.WakeAlive {
		out.Verdict = livenessWakeLive
		out.Status = statusStateWakeLive
		out.Detail = wakeLiveDetail(out.Signals)
		return out
	}

	// Not live. Stale requires a live-pointing disk signal for this handle.
	// Lone stale/inactive/old presence does not count; it collapses to missing.
	hasLiveSignal := hasLaunchPID || hasWakePID || presenceMismatched
	if !hasLiveSignal {
		out.Verdict = livenessMissing
		out.Status = statusStateMissing
		out.Detail = "no live signals for this handle"
		return out
	}

	// Before settling on stale: the recorded PID may be dead because the agent
	// was relaunched OUTSIDE amq-squad, leaving a live replacement process the
	// launch record never learned about. Look for a live tmux pane that
	// resolves to this member.
	if replacementPaneAllowedForRecord(launchErr, launchRec) {
		if target, ok := classifierReplacementPane(role, handle, binary, cwd, workstream); ok {
			out.Verdict = livenessReplacementLive
			out.Status = statusStateLive
			out.ReplacementTarget = target
			out.Detail = fmt.Sprintf("recorded pid dead; live %s at %s — relaunch via amq-squad to re-register", binary, target)
			return out
		}
	}

	out.Verdict = livenessStale
	out.Status = statusStateStale
	out.Detail = staleDetail(out.Signals, presenceMismatched) + "; relaunch via amq-squad to re-register"
	return out
}

func replacementPaneAllowedForRecord(launchErr error, rec launch.Record) bool {
	if launchErr != nil || rec.Terminal == nil {
		return true
	}
	switch strings.TrimSpace(rec.Terminal.Backend) {
	case "", runtimecontrol.BackendTmux:
		return true
	default:
		return false
	}
}

// classifierReplacementPane is the verdict-level live-replacement detector. It
// delegates to liveReplacementPane (the single neutral tmux resolver shared
// with status) so there is exactly one replacement-detection implementation and
// its existing tests stay authoritative. The classifier carries bare identity
// fields, so it assembles the minimal team.Member + statusRecord the resolver
// needs.
func classifierReplacementPane(role, handle, binary, cwd, workstream string) (string, bool) {
	m := team.Member{Role: role, Handle: handle, Binary: binary}
	rec := statusRecord{Role: role, Handle: handle, Binary: binary, CWD: cwd}
	return liveReplacementPane(m, rec, workstream)
}
