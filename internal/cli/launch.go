package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/catalog"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/role"
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

func writeLaunchRecordWithSnapshot(agentDir string, rec launch.Record) (launchRecordWriteSnapshot, error) {
	var snapshot launchRecordWriteSnapshot
	err := launch.WithRecordLock(agentDir, func() error {
		previous, err := launch.Read(agentDir)
		switch {
		case err == nil:
			snapshot.Previous = &previous
		case !os.IsNotExist(err):
			return fmt.Errorf("snapshot existing launch record: %w", err)
		}
		if err := launch.WriteUnderRecordLock(agentDir, rec); err != nil {
			return err
		}
		written, err := launch.Read(agentDir)
		if err != nil {
			return fmt.Errorf("read written launch record: %w", err)
		}
		snapshot.Written = written
		return nil
	})
	return snapshot, err
}

func rollbackLaunchRecordIfCurrent(agentDir string, snapshot launchRecordWriteSnapshot) (bool, error) {
	applied := false
	err := launch.WithRecordLock(agentDir, func() error {
		current, err := launch.Read(agentDir)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read current launch record: %w", err)
		}
		if !reflect.DeepEqual(current, snapshot.Written) {
			return nil
		}
		if snapshot.Previous != nil {
			if err := launch.WriteUnderRecordLock(agentDir, *snapshot.Previous); err != nil {
				return err
			}
			applied = true
			return nil
		}
		if err := os.Remove(launch.Path(agentDir)); err != nil && !os.IsNotExist(err) {
			return err
		}
		applied = true
		return nil
	})
	return applied, err
}

// ensureLauncherExecutable verifies a custom --launcher path exists and is an
// executable file before exec, so a missing or non-executable wrapper fails
// with a clear message instead of an opaque coop exec error.
func ensureLauncherExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("launcher %q not found", path)
		}
		return fmt.Errorf("launcher %q: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("launcher %q is a directory, not an executable", path)
	}
	if info.Mode()&0o111 == 0 {
		return fmt.Errorf("launcher %q is not executable (chmod +x it)", path)
	}
	return nil
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

func validateLaunchAMQVersion(version string) error {
	if err := validateSupportedAMQVersion(version); err != nil {
		return fmt.Errorf("agent up refused: %w", err)
	}
	return nil
}

func validateSupportedAMQVersion(version string) error {
	got := strings.TrimSpace(version)
	if got == "" {
		return fmt.Errorf("amq env returned no version (compatibility unknown)")
	}
	if _, ok := parseSemverParts(got); !ok {
		return fmt.Errorf("amq returned unparseable version %q", got)
	}
	if !semverMeetsStableFloor(got, doctorMinAMQVersion) {
		return fmt.Errorf(
			"amq %s is older than required %s; run `amq upgrade` to update",
			got,
			doctorMinAMQVersion,
		)
	}
	return nil
}

func launchUsesExplicitRoot(rootFlag, session, profile string) bool {
	return strings.TrimSpace(rootFlag) != "" &&
		strings.TrimSpace(session) != "" &&
		squadnamespace.NormalizeProfile(profile) != team.DefaultProfile
}

func normalizedAgentBinary(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

// seedRoleStub writes a role.md for the given agent directory. Precedence:
//  1. a custom role authored in a file and staged at
//     <teamHome>/.amq-squad/roles/<id>.md during team init (verbatim),
//  2. the built-in catalog entry for roleID,
//  3. a minimal fallback stub with label = roleID.
//
// It never overwrites existing user edits.
func seedRoleStub(agentDir, roleID, teamHome string) error {
	if catalog.Lookup(roleID) == nil && strings.TrimSpace(teamHome) != "" {
		docPath := team.CustomRolePath(teamHome, roleID)
		body, err := os.ReadFile(docPath)
		if err == nil {
			_, werr := role.EnsureContent(agentDir, string(body))
			return werr
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("read custom role doc %s: %w", docPath, err)
		}
	}
	stub := role.Stub{RoleID: roleID, Label: roleID}
	if r := catalog.Lookup(roleID); r != nil {
		stub.Label = r.Label
		stub.Description = r.Description
		stub.Skills = r.Skills
		stub.Peers = r.DefaultPeers
	}
	_, err := role.EnsureStub(agentDir, stub)
	return err
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
	ProcessTTY       func(int) (string, bool)
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
	recordedTTY := strings.TrimSpace(rec.AgentTTY)
	ttyMatches := recordedTTY == "" || recordedTTY == "unknown"
	if !ttyMatches {
		observedTTY, ok := "", false
		if probe.ProcessTTY != nil {
			observedTTY, ok = probe.ProcessTTY(rec.AgentPID)
		}
		ttyMatches = !ok || sameResolvedDir(recordedTTY, observedTTY)
	}
	out.PIDLive = ttyMatches
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
		ProcessTTY:       probe.ProcessTTY,
		ProcessStartTime: probe.ProcessStartTime,
	}
}
