package cli

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// presenceFreshness is how recently presence.json must have been updated to
// count as a live signal.
const presenceFreshness = 90 * time.Second

// agentLaunchPreflight describes a target agent identity before launch (read by
// the kept-dead launchapi backend, 2b). AMQFloorViolation is set only for a
// dry-run whose AMQ version would make the live launch refuse.
type agentLaunchPreflight struct {
	Role, CWD, AgentDir, Handle, Workstream string
	Root, BaseRoot, RootSource, Binary      string
	AMQVersion, AMQFloorViolation           string
	Force, DryRun                           bool
}

// duplicateLaunchProbe abstracts liveness and process inspection for tests.
type duplicateLaunchProbe struct {
	PIDAlive         func(pid int) bool
	ProcessMatch     func(pid int, predicate func(args string) bool) bool
	ProcessTTY       func(pid int) (string, bool)
	ProcessStartTime func(pid int) (time.Time, bool)
	Now              func() time.Time
}

// defaultDuplicateLaunchProbe reads liveness from the fork-free procinfo
// package, shared with internal/state so every surface agrees (#87).
var defaultDuplicateLaunchProbe = duplicateLaunchProbe{
	PIDAlive:         procinfo.Alive,
	ProcessMatch:     procinfo.Match,
	ProcessTTY:       procinfo.TTY,
	ProcessStartTime: procinfo.StartTime,
	Now:              time.Now,
}

// presenceWriterIsKnownDead reports whether launch.json names an agent PID that
// is gone or reused by another binary. Without a record or a captured PID the
// writer cannot be proven dead. 3.0 (2a-2): the wake.lock writer no longer
// counts (manifest C10).
func presenceWriterIsKnownDead(agentDir, fallbackBinary string, probe duplicateLaunchProbe) bool {
	rec, err := launch.Read(agentDir)
	if err != nil || rec.AgentPID <= 0 {
		return false
	}
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = fallbackBinary
	}
	return !classifyLaunchRuntimeIdentity(rec, binary, launchRuntimeProbeFromDuplicate(probe)).PIDLive
}

type presenceFile struct {
	Schema   int       `json:"schema"`
	Handle   string    `json:"handle"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
}

// rootsMatch reports whether two AMQ roots are the same location. Two absolute
// roots compare canonically; a relative root matches an absolute one that ends
// with it; two relative roots must match literally.
func rootsMatch(actual, expected string) bool {
	a, b := filepath.Clean(actual), filepath.Clean(expected)
	if a == b {
		return true
	}
	if filepath.IsAbs(a) == filepath.IsAbs(b) {
		return filepath.IsAbs(a) && canonicalFilesystemPath(a) == canonicalFilesystemPath(b)
	}
	if filepath.IsAbs(a) {
		a, b = b, a
	}
	return strings.HasSuffix(b, string(filepath.Separator)+a)
}

// agentProcessMatcher recognizes the agent binary by the first argv token's
// base name (so "claude-mem" does not match "claude").
func agentProcessMatcher(binary string) func(args string) bool {
	binary = strings.TrimSpace(binary)
	return func(args string) bool {
		fields := strings.Fields(args)
		if len(fields) == 0 || binary == "" {
			return false
		}
		first := filepath.Base(fields[0])
		return first == filepath.Base(binary) || first == binary
	}
}
