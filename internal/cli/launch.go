package cli

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// envTmuxTarget carries the tmux launch target (current-window / new-window /
// new-session) from a team-launch backend into each agent's launcher process,
// so the per-agent launch record can persist how its pane was created. It is
// set only on the live tmux launch paths; manual `agent up` launches leave it
// unset and record an empty target.
const envTmuxTarget = "AMQ_SQUAD_TMUX_TARGET"

// envTmuxLauncherPane carries the pane id that initiated a managed tmux launch.
// The child process runs in the agent pane, so it cannot recover this later
// from TMUX_PANE. Status uses it to detect same-pane lead collapse.
const envTmuxLauncherPane = "AMQ_SQUAD_TMUX_LAUNCHER_PANE"

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, " ")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type launchRecordWriteSnapshot struct {
	Previous *launch.Record
	Written  launch.Record
}

func resolveAMQEnvForLaunch(cwd, rootFlag, session, profile, handle string) (amqEnv, error) {
	if launchUsesExplicitRoot(rootFlag, session, profile) {
		env, err := resolveAMQEnvInDir(cwd, rootFlag, "", handle)
		if err != nil {
			return amqEnv{}, err
		}
		if strings.TrimSpace(env.SessionName) == "" {
			env.SessionName = strings.TrimSpace(session)
		}
		return env, nil
	}
	return resolveAMQEnvInDir(cwd, rootFlag, session, handle)
}

func launchUsesExplicitRoot(rootFlag, session, profile string) bool {
	return strings.TrimSpace(rootFlag) != "" &&
		strings.TrimSpace(session) != "" &&
		squadnamespace.NormalizeProfile(profile) != team.DefaultProfile
}

func normalizedAgentBinary(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

// launchProcessStartSkewEpsilon absorbs the wall-clock reconstruction error in
// Linux procfs start times (btime is only second-granularity) plus small clock
// adjustments. A process born more than two seconds after the launch record is
// a reused PID; at or inside the boundary it must still pass binary and TTY
// identity checks.
const launchProcessStartSkewEpsilon = 2 * time.Second

type launchRuntimeProbe struct {
	PIDAlive         func(int) bool
	ProcessMatch     func(int, func(string) bool) bool
	ProcessStartTime func(int) (time.Time, bool)
}

// launchRuntimeIdentity is the launch-record PID identity (moved from
// runtime_identity.go, C2 delete, in 2a-2). 3.0 (2a-2): the v2
// pane inputs (PaneLive, PaneTitleMatch, the pane-title and pane-tty
// corroboration) are cut with the tmux pane driver (manifest C10/C2).
type launchRuntimeIdentity struct {
	PIDLive     bool
	PIDAlive    bool
	BinaryMatch bool
}

// classifyLaunchRuntimeIdentity is the single launch-record runtime identity
// predicate: the recorded agent PID is alive, runs the recorded binary or
// launcher, was not born after the record (PID reuse), and holds the recorded
// tty. Context resolution, status and doctor all consume this result.
func classifyLaunchRuntimeIdentity(rec launch.Record, expectedBinary string, probe launchRuntimeProbe) launchRuntimeIdentity {
	var out launchRuntimeIdentity
	if rec.StoppedAt != nil && !rec.StoppedAt.IsZero() {
		return out
	}
	if rec.AgentPID <= 0 || probe.PIDAlive == nil || !probe.PIDAlive(rec.AgentPID) {
		return out
	}
	out.PIDAlive = true
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = strings.TrimSpace(expectedBinary)
	}
	launcher := strings.TrimSpace(rec.Launcher)
	if (binary == "" && launcher == "") || probe.ProcessMatch == nil || !probe.ProcessMatch(rec.AgentPID, launchRecordProcessMatcher(binary, launcher)) {
		return out
	}
	out.BinaryMatch = true
	if !rec.StartedAt.IsZero() && probe.ProcessStartTime != nil {
		if processStartedAt, ok := probe.ProcessStartTime(rec.AgentPID); ok &&
			processStartedAt.After(rec.StartedAt.Add(launchProcessStartSkewEpsilon)) {
			return out // reused PID
		}
	}
	out.PIDLive = true
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
		ProcessStartTime: probe.ProcessStartTime,
	}
}
