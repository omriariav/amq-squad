package state

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/activity"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// BuildWithThresholds is Build with explicit triage thresholds.
func BuildWithThresholds(projectRoot, baseRoot string, probe Probe, th Thresholds) (Snapshot, error) {
	probe = withDefaults(probe)
	th = withThresholdDefaults(th)

	entries, err := launch.ScanRestorableEntriesInRoot(projectRoot, baseRoot)
	if err != nil {
		return Snapshot{}, err
	}

	// Group entries by canonical profile/session namespace, preserving a stable
	// legacy session root per namespace. Profiles may intentionally reuse a
	// human session label; the snapshot must not merge their launch records.
	type bucket struct {
		root        string
		session     string
		teamProfile string
		namespaceID string
		agents      []Agent
	}
	bySession := map[string]*bucket{}
	var order []string
	for _, e := range entries {
		name := e.Record.Session
		profile := namespace.NormalizeProfile(e.Record.TeamProfile)
		namespaceID := namespace.ID(profile, name)
		root := sessionRoot(projectRoot, baseRoot, e.Record)
		b, ok := bySession[namespaceID]
		if !ok {
			b = &bucket{root: root, session: name, teamProfile: profile, namespaceID: namespaceID}
			bySession[namespaceID] = b
			order = append(order, namespaceID)
		}
		b.agents = append(b.agents, classifyAgent(e, probe, th.Heartbeat))
	}

	sort.Strings(order)
	now := probe.Now()
	sessions := make([]Session, 0, len(order))
	for _, key := range order {
		b := bySession[key]
		sortAgents(b.agents)

		coord := coordinateSession(b.root, b.agents, now, th)
		sessions = append(sessions, Session{
			Name:         b.session,
			TeamProfile:  b.teamProfile,
			NamespaceID:  b.namespaceID,
			Root:         b.root,
			Agents:       b.agents,
			Coordination: coord,
		})
	}

	return Snapshot{BaseRoot: baseRoot, Sessions: sessions}, nil
}

// coordinateSession scans every agent's mailbox in a session, then collapses the
// observed messages into the derived coordination model (threads, edges,
// timeline, triage).
func coordinateSession(sessionRoot string, agents []Agent, now time.Time, th Thresholds) Coordination {
	var msgs []Message
	var warns []Warning
	scanned := map[string]bool{}
	for _, a := range agents {
		if a.AgentDir == "" {
			continue
		}
		scanned[filepath.Clean(a.AgentDir)] = true
		m, w := scanMailbox(a.AgentDir, a.Handle, func() time.Time { return now })
		msgs = append(msgs, m...)
		warns = append(warns, w...)
	}
	if op := strings.TrimSpace(th.OperatorHandle); op != "" && strings.TrimSpace(sessionRoot) != "" {
		dir := filepath.Join(sessionRoot, "agents", op)
		if !scanned[filepath.Clean(dir)] && dirExists(dir) {
			m, w := scanMailbox(dir, op, func() time.Time { return now })
			msgs = append(msgs, m...)
			warns = append(warns, w...)
		}
	}
	return buildCoordination(collapseInput{messages: msgs, agents: agents, warnings: warns}, now, th)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// sessionRoot derives the directory that anchors a session. It prefers the
// record's own Root (which the launcher captured); when absent it derives it
// from baseRoot + session name, matching the AMQ layouts the scanners walk.
func sessionRoot(projectRoot, baseRoot string, rec launch.Record) string {
	if rec.Root != "" {
		return absoluteLaunchRoot(projectRoot, rec.CWD, rec.Root)
	}
	if rec.Session == "" {
		return baseRoot
	}
	return filepath.Join(baseRoot, rec.Session)
}

func absoluteLaunchRoot(projectRoot, launchCWD, root string) string {
	root = strings.TrimSpace(root)
	if root == "" || filepath.IsAbs(root) {
		return root
	}
	base := strings.TrimSpace(launchCWD)
	if base == "" {
		base = strings.TrimSpace(projectRoot)
	}
	if base == "" {
		return root
	}
	return filepath.Clean(filepath.Join(base, root))
}

func sortAgents(agents []Agent) {
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Role != agents[j].Role {
			return agents[i].Role < agents[j].Role
		}
		if agents[i].Handle != agents[j].Handle {
			return agents[i].Handle < agents[j].Handle
		}
		return agents[i].Source < agents[j].Source
	})
}

// classifyAgent computes the run-state for a single discovered launch entry.
func classifyAgent(e launch.Entry, probe Probe, heartbeat time.Duration) Agent {
	rec := e.Record
	a := Agent{
		Handle:       rec.Handle,
		Engine:       rec.Binary,
		Role:         rec.Role,
		AgentPID:     rec.AgentPID,
		Conversation: rec.Conversation,
		AgentDir:     e.AgentDir,
		Source:       e.Source,
		TeamProfile:  rec.TeamProfile,
	}

	pres, presErr := readPresence(e.AgentDir)
	if presErr == nil {
		a.Presence = pres.Status
		a.LastSeen = pres.LastSeen
	}
	if act, ok, err := activity.Read(e.AgentDir, probe.Now(), heartbeat); err == nil && ok {
		a.Activity = &act
	} else if err != nil {
		act := activity.UnknownSnapshot(rec.Handle, "activity heartbeat unreadable")
		a.Activity = &act
	}

	// --- Agent PID liveness (the authoritative live signal). ---
	agentPIDLive := false
	if rec.AgentPID > 0 && probe.PIDAlive(rec.AgentPID) {
		if rec.Binary == "" || probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(rec.Binary)) {
			agentPIDLive = true
		}
	}

	// --- Presence freshness (a recently-touched mailbox heartbeat). ---
	presenceActiveFresh := false
	mailboxRecentlyTouched := false
	if presErr == nil && !pres.LastSeen.IsZero() {
		recent := probe.Now().Sub(pres.LastSeen) <= PresenceFreshness
		handleOK := pres.Handle == "" || pres.Handle == rec.Handle
		// A fresh write whose status is explicitly "offline" is the expected
		// terminal act of a clean stop (stop/down flip presence offline as
		// their last step), NOT evidence that something is still writing.
		// Counting it as a live-ish touch made the documented stop→rm
		// sequence deterministically refuse for PresenceFreshness (#109), so
		// an offline write classifies exactly like an expired one.
		terminal := strings.EqualFold(pres.Status, "offline")
		if recent && handleOK && !terminal {
			mailboxRecentlyTouched = true
			if strings.EqualFold(pres.Status, "active") {
				presenceActiveFresh = true
			}
		}
	}

	// --- Derive Liveness. ---
	hasAnyDiskSignal := rec.AgentPID > 0 || presErr == nil
	switch {
	case agentPIDLive:
		a.Liveness = LivenessAlive
	case presenceActiveFresh:
		// Presence says active and fresh, but the agent PID is NOT verified
		// alive. If we have a recorded agent PID and it is confirmed dead, this
		// is the explicit dead-process / live-mailbox case — surface it as its
		// own status, never as plain "alive" or "stale".
		if rec.AgentPID > 0 && !agentPIDLive {
			a.Liveness = LivenessDeadMailboxLive
		} else {
			a.Liveness = LivenessAlive
		}
	case mailboxRecentlyTouched && rec.AgentPID > 0:
		// Mailbox touched recently with a non-active, non-terminal status
		// (e.g. "idle"; "offline" never reaches here, see the terminal
		// carve-out above) yet the recorded agent PID is dead: something is
		// still writing while the agent is gone. Distinct dead-mailbox-live
		// signal.
		a.Liveness = LivenessDeadMailboxLive
	case !hasAnyDiskSignal:
		a.Liveness = LivenessMissing
	case rec.AgentPID > 0:
		// A recorded agent PID exists but nothing verifies a running agent: stale.
		a.Liveness = LivenessStale
	default:
		a.Liveness = LivenessDead
	}

	return a
}

// withDefaults fills any nil probe seam with the production implementation so
// callers may pass a partially-specified probe (and tests may override only the
// seams they care about).
func withDefaults(p Probe) Probe {
	if p.PIDAlive == nil {
		p.PIDAlive = procinfo.Alive
	}
	if p.ProcessMatch == nil {
		p.ProcessMatch = procinfo.Match
	}
	if p.Now == nil {
		p.Now = DefaultProbe.Now
	}
	return p
}
