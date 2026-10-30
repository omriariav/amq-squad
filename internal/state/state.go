// Package state is the read-only mailbox and thread model under the status
// contract (internal/status), the operator view and the gate flows: message
// parsing, thread collapse and triage, operator gate replay, and per-agent
// liveness. It never execs amq: sessions and agent dirs are found by scanning
// the base root the caller resolved. Liveness is computed from the launch
// record PID and presence freshness, never read from presence.status.
package state

import (
	"time"

	"github.com/omriariav/amq-squad/v3/internal/activity"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// PresenceFreshness defines how recently a presence.json must have been updated
// for it to count as a live signal. Mirrors internal/cli.presenceFreshness so
// this package agrees with status / preflight / list about what "fresh" means.
const PresenceFreshness = 90 * time.Second

// Liveness is the computed run-state of a single agent. It is derived from the
// launch record PID, the agent process command line, and presence — NOT read
// verbatim from presence.status.
type Liveness string

const (
	// LivenessAlive: launch.json agent_pid is alive (signal-0) AND its process
	// args match the agent binary; OR presence.status=="active" and fresh with
	// no contradicting dead PID. The agent is running.
	LivenessAlive Liveness = "alive"

	// LivenessStale: a presence or launch signal exists on disk but none of
	// them verify as a running agent for this handle, and there is no evidence
	// the mailbox is being actively touched. The disk record is leftover.
	LivenessStale Liveness = "stale"

	// LivenessDead: the agent PID is dead (or never recorded) and presence is
	// neither fresh-active nor recently touched. The agent is gone.
	LivenessDead Liveness = "dead"

	// LivenessMissing: no launch record and no presence file. The
	// agent dir has no live signals at all.
	LivenessMissing Liveness = "missing"

	// LivenessDeadMailboxLive is the explicit, distinct dead-process / live-
	// mailbox case: the recorded agent PID is dead (or unverifiable) yet the
	// mailbox/presence was touched within PresenceFreshness.
	LivenessDeadMailboxLive Liveness = "dead-mailbox-live"
)

// Agent is a single discovered agent plus its computed run-state.
type Agent struct {
	Handle       string
	Engine       string // the launch binary: "claude", "codex", ...
	Role         string
	AgentPID     int
	Liveness     Liveness
	LastSeen     time.Time
	Presence     string // raw presence.status as found on disk (informational)
	Activity     *activity.Snapshot
	Conversation string
	AgentDir     string
	Source       string // launch source label, e.g. "launch.json" or "amq history"
	TeamProfile  string // launch team profile; empty means the default profile
}

// Session groups the agents discovered under one AMQ session root, plus the
// derived read-only coordination model (threads/edges/timeline/triage) the
// Mission Control console renders.
type Session struct {
	Name         string // workstream/session name; "" for the rootless layout
	TeamProfile  string // launch team profile; empty means the default profile
	NamespaceID  string // canonical profile/session identity for clients
	Root         string // the session root directory (base root or base root/<name>)
	Agents       []Agent
	Coordination Coordination // derived threads and triage for this session
}

// Snapshot is the full read-only view of all discovered sessions and agents,
// plus a snapshot-wide triage rollup aggregating every session.
type Snapshot struct {
	BaseRoot string
	Sessions []Session
}

// Probe abstracts PID liveness and process-args inspection so tests can supply
// deterministic fakes. It mirrors internal/cli.duplicateLaunchProbe so the two
// packages stay semantically aligned.
type Probe struct {
	// PIDAlive reports whether pid is a live process (signal-0 probe).
	PIDAlive func(pid int) bool
	// ProcessMatch reports whether the live pid's command line satisfies
	// predicate (typically a ps -o args= read).
	ProcessMatch func(pid int, predicate func(args string) bool) bool
	// ProcessStartTime is the launch-record PID-reuse signal; state rollups do
	// not interpret it but carry it for callers adapting the shared probe.
	ProcessStartTime func(pid int) (time.Time, bool)
	// Now returns the current time. Injected so liveness freshness is
	// deterministic in tests and so the package never depends on a real
	// wall-clock that the sandbox may forbid.
	Now func() time.Time
}

// DefaultProbe is the production probe. PID liveness and process matching come
// from the shared, fork-free internal/procinfo package so the status board and
// NOC snapshots read liveness identically to the cli status/resume/doctor
// surfaces and cannot disagree about whether a PID is alive (#87).
var DefaultProbe = Probe{
	PIDAlive:         procinfo.Alive,
	ProcessMatch:     procinfo.Match,
	ProcessStartTime: procinfo.StartTime,
	Now:              time.Now,
}
