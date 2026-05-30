package noc

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
)

// ProjectSnapshot is one discovered amq-squad project plus its computed
// state.Snapshot. Warning is non-empty when the project could not be collected
// (e.g. the build failed); in that case Snap is the zero snapshot. Collect
// NEVER drops a discovered project — a failed one is surfaced with its warning
// so the operator sees it instead of it silently vanishing.
type ProjectSnapshot struct {
	Project string         // basename of Dir, the human-facing project label
	Dir     string         // absolute project directory (parent of .agent-mail)
	Snap    state.Snapshot // per-project discovery + coordination snapshot
	Warning string         // non-empty when collection failed for this project
}

// MultiSnapshot is the full NOC view across every discovered project, plus a
// global triage rollup summing all per-project rollups and the observation
// time. It is the single read-only datum a command-center surface renders.
type MultiSnapshot struct {
	Roots      []string
	Projects   []ProjectSnapshot
	Rollup     state.TriageRollup // global headline across all projects
	ObservedAt time.Time
}

// Collect discovers every amq-squad project under roots (bounded by depth) and
// builds a per-project state.Snapshot via state.BuildWithThresholds, scanning
// each project's <projectDir>/.agent-mail container. The per-project triage
// rollups are summed into the global MultiSnapshot.Rollup.
//
// Collect is NEVER fatal: discovery failures and per-project build failures are
// recorded (a failed project becomes a ProjectSnapshot with a Warning) rather
// than aborting the whole collection. ObservedAt is taken from the probe clock
// so the result is deterministic under an injected probe.
//
// Projects are returned attention-first: projects with needs-you items sort
// before at-risk/blocked, which sort before merely-running, which sort before
// fully-stopped; ties break by project name. This ordering is what lets a
// command center put the project that needs the operator at the top.
func Collect(roots []string, depth int, probe state.Probe, th state.Thresholds) MultiSnapshot {
	probe = withProbeDefaults(probe)
	observedAt := probe.Now()

	ms := MultiSnapshot{
		Roots:      append([]string(nil), roots...),
		ObservedAt: observedAt,
	}

	dirs, err := Discover(roots, depth)
	if err != nil {
		// Discovery itself failed: record a single synthetic warning project so
		// the failure is visible, but still return a usable (empty) snapshot.
		ms.Projects = []ProjectSnapshot{{
			Project: "(discovery)",
			Warning: "discover: " + err.Error(),
		}}
		return ms
	}

	for _, dir := range dirs {
		baseRoot := filepath.Join(dir, AgentMailDirName)
		ps := ProjectSnapshot{
			Project: filepath.Base(dir),
			Dir:     dir,
		}
		snap, buildErr := state.BuildWithThresholds(dir, baseRoot, probe, th)
		if buildErr != nil {
			ps.Warning = buildErr.Error()
		} else {
			ps.Snap = snap
			ms.Rollup.Add(snap.Rollup)
		}
		ms.Projects = append(ms.Projects, ps)
	}

	sortProjectsAttentionFirst(ms.Projects)
	return ms
}

// attentionTier ranks a project by how urgently it wants the operator, lowest
// (most urgent) first. The tiers, in order:
//
//	0 needs-you : at least one needs-you triage item
//	1 at-risk/blocked : at-risk or blocked items, but nothing needs-you
//	2 running : at least one live/active agent, nothing outstanding
//	3 stopped : discovered agents but none live (and nothing outstanding)
//	4 empty : no agents at all (or a failed/warning project)
func attentionTier(ps ProjectSnapshot) int {
	r := ps.Snap.Rollup
	switch {
	case r.NeedsYou > 0:
		return 0
	case r.AtRisk > 0 || r.Blocked > 0:
		return 1
	case hasRunningAgent(ps.Snap):
		return 2
	case hasAnyAgent(ps.Snap):
		return 3
	default:
		return 4
	}
}

// hasRunningAgent reports whether any agent in the snapshot is currently live
// (alive or dead-mailbox-live — both indicate active mailbox traffic).
func hasRunningAgent(snap state.Snapshot) bool {
	for _, sess := range snap.Sessions {
		for _, ag := range sess.Agents {
			if ag.Liveness == state.LivenessAlive || ag.Liveness == state.LivenessDeadMailboxLive {
				return true
			}
		}
	}
	return false
}

// hasAnyAgent reports whether the snapshot discovered any agent at all.
func hasAnyAgent(snap state.Snapshot) bool {
	for _, sess := range snap.Sessions {
		if len(sess.Agents) > 0 {
			return true
		}
	}
	return false
}

// sortProjectsAttentionFirst orders projects by attention tier, then by name.
func sortProjectsAttentionFirst(projects []ProjectSnapshot) {
	sort.SliceStable(projects, func(i, j int) bool {
		ti, tj := attentionTier(projects[i]), attentionTier(projects[j])
		if ti != tj {
			return ti < tj
		}
		return projects[i].Project < projects[j].Project
	})
}

// withProbeDefaults fills any nil probe seam with the production probe, so a
// caller may pass the zero Probe (e.g. NOC entrypoints) and still get a real
// clock for ObservedAt. state.BuildWithThresholds also fills defaults, but we
// need a non-nil Now here for the snapshot timestamp.
func withProbeDefaults(p state.Probe) state.Probe {
	if p.PIDAlive == nil {
		p.PIDAlive = state.DefaultProbe.PIDAlive
	}
	if p.ProcessMatch == nil {
		p.ProcessMatch = state.DefaultProbe.ProcessMatch
	}
	if p.Now == nil {
		p.Now = state.DefaultProbe.Now
	}
	return p
}
