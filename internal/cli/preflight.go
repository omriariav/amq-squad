package cli

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// presenceFreshness defines how recently a presence.json must have been
// updated for it to count as a live signal. Older presence is treated as
// stale and ignored.
const presenceFreshness = 90 * time.Second

// agentLaunchPreflight inspects existing artifacts for a target agent identity
// and reports whether a launch should be refused.
type agentLaunchPreflight struct {
	Role       string
	CWD        string
	AgentDir   string
	Handle     string
	Workstream string
	Root       string
	BaseRoot   string
	RootSource string
	Binary     string
	AMQVersion string
	// AMQFloorViolation is populated only for a dry-run whose resolved AMQ
	// version would make the equivalent live launch refuse. Dry-run renders
	// this diagnosis and continues the preview without mutating.
	AMQFloorViolation string
	Force             bool
	// DryRun keeps the preflight inspection read-only. Wake lock lifecycle is
	// always AMQ-owned; neither dry-run nor live preflight removes lock files.
	DryRun bool
}

// duplicateLaunchProbe abstracts liveness and process-inspection checks so
// tests can substitute deterministic implementations.
type duplicateLaunchProbe struct {
	PIDAlive         func(pid int) bool
	ProcessMatch     func(pid int, predicate func(args string) bool) bool
	ProcessTTY       func(pid int) (string, bool)
	ProcessStartTime func(pid int) (time.Time, bool)
	Now              func() time.Time
}

// defaultDuplicateLaunchProbe is the production probe. PID liveness and process
// matching come from the shared, fork-free internal/procinfo package so that
// every amq-squad surface (cli status/resume/doctor/preflight AND
// internal/state's board + NOC snapshots) reads liveness identically and cannot
// disagree about whether a PID is alive (#87).
var defaultDuplicateLaunchProbe = duplicateLaunchProbe{
	PIDAlive:         procinfo.Alive,
	ProcessMatch:     procinfo.Match,
	ProcessTTY:       procinfo.TTY,
	ProcessStartTime: procinfo.StartTime,
	Now:              time.Now,
}

// presenceWriterIsKnownDead reports whether launch.json points at an agent
// process that is gone (dead PID or PID reuse by an unrelated process). When
// the record is missing or carries no PID we cannot prove the writer is dead,
// so presence keeps counting as live. fallbackBinary is the agent binary to
// assume when the launch record itself carries none.
//
// 3.0 (2a-2): v2 also required the wake.lock writer to be dead before demoting
// presence; the wake input is cut (manifest C10), so the launch writer alone
// decides.
func presenceWriterIsKnownDead(agentDir, root, handle, fallbackBinary string, probe duplicateLaunchProbe) bool {
	launchDead, launchKnown := launchWriterDead(agentDir, fallbackBinary, probe)
	return launchKnown && launchDead
}

// launchWriterDead inspects launch.json. Returns (dead, known): "known" is
// true when the record existed and parsed and carries a captured AgentPID;
// "dead" is true when that PID is gone or the live PID does not match the
// expected agent binary. fallbackBinary is used only when the record itself
// recorded no binary.
func launchWriterDead(agentDir, fallbackBinary string, probe duplicateLaunchProbe) (dead, known bool) {
	rec, err := launch.Read(agentDir)
	if err != nil {
		return false, false
	}
	if rec.AgentPID <= 0 {
		// No pid was captured at launch (e.g. codex seats). We cannot prove
		// the writer is dead from this record alone.
		return false, false
	}
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = fallbackBinary
	}
	if !classifyLaunchRuntimeIdentity(rec, binary, launchRuntimeProbeFromDuplicate(probe)).PIDLive {
		return true, true
	}
	return false, true
}

type presenceFile struct {
	Schema   int       `json:"schema"`
	Handle   string    `json:"handle"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

// rootsMatch reports whether two AMQ root paths refer to the same location,
// tolerating one being relative and the other absolute. Both relative paths
// must match literally; otherwise the absolute side must end with the
// relative side's cleaned form.
func rootsMatch(actual, expected string) bool {
	a := filepath.Clean(actual)
	b := filepath.Clean(expected)
	if a == b {
		return true
	}
	if filepath.IsAbs(a) && filepath.IsAbs(b) {
		return canonicalRootForMatch(a) == canonicalRootForMatch(b)
	}
	return relativeRootMatchesAbsolute(a, b)
}

func canonicalRootForMatch(root string) string {
	return canonicalFilesystemPath(root)
}

func relativeRootMatchesAbsolute(a, b string) bool {
	if filepath.IsAbs(a) && filepath.IsAbs(b) {
		// Different absolute paths: not the same root.
		return false
	}
	rel, abs := a, b
	if filepath.IsAbs(rel) {
		rel, abs = abs, rel
	}
	if !filepath.IsAbs(abs) {
		// Neither side is absolute and they didn't match literally.
		return false
	}
	return abs == rel || strings.HasSuffix(abs, string(filepath.Separator)+rel)
}

// agentProcessMatcher returns a predicate that recognizes the agent binary.
func agentProcessMatcher(binary string) func(args string) bool {
	binary = strings.TrimSpace(binary)
	return func(args string) bool {
		if args == "" || binary == "" {
			return false
		}
		base := filepath.Base(binary)
		// Match either the absolute or basename form. Avoid false positives
		// like "claude-mem" by anchoring on a token boundary.
		fields := strings.Fields(args)
		if len(fields) == 0 {
			return false
		}
		first := filepath.Base(fields[0])
		return first == base || first == binary
	}
}
