package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

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

// replacementPaneResolver finds a live replacement pane for one stale member.
// Single-member callers use classifierReplacementPane directly. Roster-level
// callers provide a command-scoped allocator so one physical pane cannot make
// several same-cwd/same-engine roles appear live.
type replacementPaneResolver func(role, handle, binary, cwd, workstream string) (string, bool)

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
	// SourceError preserves a non-ENOENT launch-record read failure. Callers
	// must surface it as unknown/degraded evidence rather than normalizing the
	// unreadable identity to an ordinary stopped member.
	SourceError string
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
	// RuntimeIdentity is the shared launch-record identity result. Downstream
	// JSON rendering must consume this rather than re-promoting a recorded PID
	// or pane from weaker observations.
	RuntimeIdentity launchRuntimeIdentity
	// InspectSignal is gh#737's session-level launchapi Inspect outcome, set
	// only by classifyAgentLivenessForRollup (zero value -- Outcome
	// launchapiInspectNotApplicable -- for every other caller of the
	// classifier). Callers that need to surface a session-level warning for
	// launchapiInspectCallFailed read it from here rather than re-deriving
	// the session Target a second time.
	InspectSignal launchapiInspectSignal
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
	return classifyAgentLivenessWithReplacementResolver(agentDir, root, expectedProfile, handle, role, binary, workstream, cwd, probe, classifierReplacementPane)
}

func classifyAgentLivenessWithReplacementResolver(agentDir, root, expectedProfile, handle, role, binary, workstream, cwd string, probe duplicateLaunchProbe, replacement replacementPaneResolver) agentLiveness {
	out := agentLiveness{}
	var launchIdentity launchRuntimeIdentity

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
		launchIdentity = classifyLaunchRuntimeIdentity(launchRec, binary, "", launchRuntimeProbeFromDuplicate(probe))
		out.RuntimeIdentity = launchIdentity
		if launchRec.StoppedAt != nil && !launchRec.StoppedAt.IsZero() {
			out.Tmux = nil
			out.Verdict = livenessStale
			out.Status = statusStateStale
			out.Detail = fmt.Sprintf("launch record explicitly stopped at %s", launchRec.StoppedAt.UTC().Format(time.RFC3339))
			return out
		}
	} else if !os.IsNotExist(launchErr) {
		out.SourceError = "read launch record: " + launchErr.Error()
	}
	wakeLock, wakeErr := readWakeLock(agentDir)
	presence, presenceErr := readPresenceForEntry(agentDir)

	hasLaunchPID := launchErr == nil && launchRec.AgentPID > 0
	hasWakePID := wakeErr == nil && wakeLock.PID > 0

	if hasLaunchPID {
		out.Signals.AgentPID = launchRec.AgentPID
		out.Signals.AgentAlive = launchIdentity.PIDAlive
		out.Signals.BinaryMatch = launchIdentity.BinaryMatch
	}
	if hasWakePID {
		out.Signals.WakePID = wakeLock.PID
		// A wake lock is positive liveness evidence only for the exact root
		// being classified. Never let a lock planted under this agent directory
		// substitute its own foreign root into the process matcher: notifier
		// delivery reserves the message ID before consulting this signal, so a
		// false positive here would permanently suppress the one fallback input.
		if strings.TrimSpace(wakeLock.Root) != "" && rootsMatch(wakeLock.Root, root) && probe.PIDAlive(wakeLock.PID) {
			if probe.ProcessMatch(wakeLock.PID, wakeProcessMatcher(handle, wakeLock.Root)) {
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

	if launchIdentity.PIDLive {
		out.Verdict = livenessAgentLive
		out.Status = statusStateLive
		out.Detail = fmt.Sprintf("agent pid %d alive (%s)", out.Signals.AgentPID, binary)
		return out
	}
	if launchIdentity.PaneLive {
		out.Verdict = livenessAgentLive
		out.Status = statusStateLive
		out.Detail = fmt.Sprintf("external pane %s live (registered lead)", launchRec.Tmux.PaneID)
		return out
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
	if replacement != nil && replacementPaneAllowedForRecord(launchErr, launchRec) {
		if target, ok := replacement(role, handle, binary, cwd, workstream); ok {
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
		return launchErr != nil || !rec.External
	}
	if rec.External {
		return false
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

// launchapiInspectOutcome classifies the result of one session-level
// launchapi.Inspect call (gh#737) into the cases this package's floor logic
// branches on.
type launchapiInspectOutcome int

const (
	// launchapiInspectNotApplicable means Inspect succeeded and reported
	// ReasonCode "binding_missing": no launchapi binding exists for this
	// session's Target, so the session was not launchapi-launched. Legacy
	// classification runs completely unchanged
	// (TestStatusRollupUnchangedForLegacyLaunches).
	launchapiInspectNotApplicable launchapiInspectOutcome = iota
	// launchapiInspectCallFailed means the Inspect call itself errored (I/O,
	// not a successful binding_missing result). Per cto's ruling: do NOT
	// clamp any member -- run today's classification unchanged and surface
	// a warning naming the error instead. Clamping every member of a
	// legacy session on a transient Inspect I/O error would be a worse
	// failure mode than just not consulting it.
	launchapiInspectCallFailed
	// launchapiInspectFloor means Inspect succeeded with State "unknown" or
	// ActionRequired true. Every member's verdict is capped below any live
	// sub-state, regardless of its own pane/presence signals -- unknown
	// never reads as clear.
	launchapiInspectFloor
	// launchapiInspectPresentSignal means Inspect succeeded with State
	// "present". This corroborates but never overrides per-seat
	// classification: a whole-session Inspect result cannot attribute
	// liveness to one specific seat (see the package's own aggregation,
	// traced against the pinned module source -- one BindingRecord per
	// session, no per-participant Inspect filter exists).
	launchapiInspectPresentSignal
	// launchapiInspectAbsentSignal means Inspect succeeded with State
	// "absent". Per-seat probes still run (no short-circuit); if a
	// member's own verdict says live, that is a discrepancy between two
	// live signals and gets capped at stale with the conflict named in
	// Detail, rather than silently trusting either source.
	launchapiInspectAbsentSignal
)

// launchapi's public LifecycleResultV1.State carries the same string values
// as the module's internal (unexported) InspectStatus enum -- "present",
// "absent", "unknown" -- but does not re-export typed constants for them.
// Traced directly against the pinned module source
// (internal/launch/backend.go's InspectPresent/InspectAbsent/InspectUnknown);
// docs/amq-0.75.0-adoption-verdict.md and this task's PR body record the
// verification.
const (
	launchapiInspectStatePresent = "present"
	launchapiInspectStateAbsent  = "absent"
)

// launchapiInspectSignal is the session-level Inspect result plus the
// evidence string callers surface in warnings/detail text.
type launchapiInspectSignal struct {
	Outcome  launchapiInspectOutcome
	Evidence string
}

// launchapiInspect is a package var so tests can stub the launchapi.Inspect
// call without a real amq binary or launchapi binding on disk.
var launchapiInspect = launchapi.Inspect

// launchapiSessionInspect calls launchapi.Inspect once for the given
// session Target and classifies the result into a launchapiInspectSignal.
// projectRoot must be the team's canonical project root (t.Project), NOT a
// per-member worktree cwd -- launchapi's own openPrepareTarget requires
// ProjectRoot to resolve to the exact canonical path Prepare/Apply used
// ("project_root must be canonical"), and per-member worktrees are
// different directories from the team's shared control root that
// Target.ProjectRoot/SessionRoot were set to at launch time
// (team_launch_launchapi.go's buildIntentInput uses t.Project for both).
// baseRoot is the session's resolved AMQ root (the same value every caller
// of classifyAgentLiveness already computes as `root`).
func launchapiSessionInspect(projectRoot, baseRoot, session string) launchapiInspectSignal {
	projectRoot = strings.TrimSpace(projectRoot)
	baseRoot = strings.TrimSpace(baseRoot)
	session = strings.TrimSpace(session)
	if projectRoot == "" || baseRoot == "" || session == "" {
		return launchapiInspectSignal{Outcome: launchapiInspectNotApplicable, Evidence: "incomplete session target"}
	}
	result, err := launchapiInspect(context.Background(), launchapi.InspectRequestV1{
		RequestVersion: launchapi.RequestVersionV1,
		Target: launchapi.TargetV1{
			ProjectRoot: projectRoot,
			BaseRoot:    baseRoot,
			SessionRoot: projectRoot,
			Session:     session,
		},
	})
	if err != nil {
		// base_root_unauthorized/base_root_relation_invalid mean "we cannot
		// even identify a launchapi target here" -- no .amqrc-style
		// authorization at this project root, or the target shape does not
		// resolve -- which is structurally the same "no evidence either
		// way" case as a successful binding_missing result, not a genuine
		// I/O failure. Many legacy test fixtures (and any project that has
		// never run real amq setup) never carry that authorization, so
		// treating this as launchapiInspectCallFailed would spuriously warn
		// on every legacy session. Only an error that is neither of these
		// two documented reason codes counts as a real call failure.
		msg := err.Error()
		if strings.Contains(msg, "base_root_unauthorized") || strings.Contains(msg, "base_root_relation_invalid") {
			return launchapiInspectSignal{Outcome: launchapiInspectNotApplicable, Evidence: msg}
		}
		return launchapiInspectSignal{Outcome: launchapiInspectCallFailed, Evidence: msg}
	}
	if result.ReasonCode == "binding_missing" {
		return launchapiInspectSignal{Outcome: launchapiInspectNotApplicable, Evidence: result.ReasonCode}
	}
	switch result.State {
	case launchapiInspectStatePresent:
		return launchapiInspectSignal{Outcome: launchapiInspectPresentSignal, Evidence: result.State}
	case launchapiInspectStateAbsent:
		return launchapiInspectSignal{Outcome: launchapiInspectAbsentSignal, Evidence: result.State}
	default:
		evidence := result.State
		if result.ReasonCode != "" {
			evidence = result.State + ": " + result.ReasonCode
		}
		return launchapiInspectSignal{Outcome: launchapiInspectFloor, Evidence: evidence}
	}
}

// applyLaunchapiInspectionFloor applies gh#737's session-level Inspect
// result to one member's already-computed agentLiveness verdict. Pure
// function: no I/O, so the floor/corroboration/conflict rules are directly
// unit-testable without a real launchapi binding.
func applyLaunchapiInspectionFloor(live agentLiveness, signal launchapiInspectSignal) agentLiveness {
	live.InspectSignal = signal
	switch signal.Outcome {
	case launchapiInspectNotApplicable, launchapiInspectPresentSignal:
		// Not launchapi-launched, or the whole session's owned panes are
		// intact: corroborates at most, never overrides per-seat evidence.
		return live
	case launchapiInspectCallFailed:
		// Do not clamp; today's classification stands. The caller surfaces
		// the evidence as a session-level warning separately.
		return live
	case launchapiInspectFloor:
		if live.Live() {
			live.Verdict = livenessStale
			live.Status = statusStateStale
			live.Detail = fmt.Sprintf("session Inspect reports %s; capping below live regardless of this seat's own signals (unknown never reads as clear)", signal.Evidence)
		}
		return live
	case launchapiInspectAbsentSignal:
		if live.Live() {
			live.Verdict = livenessStale
			live.Status = statusStateStale
			live.Detail = fmt.Sprintf("session Inspect reports absent while this seat's own signals report live (%s); treating as stale pending reconciliation", live.Detail)
		}
		return live
	default:
		return live
	}
}

// classifyAgentLivenessForRollup is classifyAgentLivenessWithReplacementResolver
// plus gh#737's session-level launchapi Inspect floor. Used only by the
// status/resume rollup call sites that share the "status and resume can
// never disagree" contract (classifyMemberStatusWithReplacementResolver,
// team_resume.go's roster-facing call sites); classifyAgentLiveness itself
// is untouched and used unchanged by every other caller (dispatch, session
// notifier, namespace migration planning, doctor's worktree collision
// check), which do not need session-wide Inspect corroboration for their
// narrower single-purpose checks.
//
// projectRoot must be the team's canonical project root (t.Project), not a
// per-member worktree cwd -- see launchapiSessionInspect's doc comment.
func classifyAgentLivenessForRollup(projectRoot, agentDir, root, expectedProfile, handle, role, binary, workstream, cwd string, probe duplicateLaunchProbe, replacement replacementPaneResolver) agentLiveness {
	live := classifyAgentLivenessWithReplacementResolver(agentDir, root, expectedProfile, handle, role, binary, workstream, cwd, probe, replacement)
	signal := launchapiSessionInspect(projectRoot, root, workstream)
	return applyLaunchapiInspectionFloor(live, signal)
}
