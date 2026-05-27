package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
)

type downStatus string

const (
	downStatusForceSent downStatus = "force-sent"
	downStatusNotLive   downStatus = "not-live"
	downStatusMaybeLive downStatus = "maybe-live"
	downStatusFailed    downStatus = "failed"
	// downStatusCleaned means the agent PID was already dead but stale
	// runtime artifacts (orphan wake process, wake.lock, active presence)
	// were reaped so the next `up` cannot collide with them.
	downStatusCleaned downStatus = "cleaned"
)

type downReport struct {
	Role     string
	Handle   string
	Binary   string
	AgentDir string
	Root     string
	PID      int
	Status   downStatus
	Detail   string
}

// processTerminator abstracts process-termination so tests can substitute a
// fake. Production uses signalTerminator (SIGTERM).
type processTerminator interface {
	Terminate(pid int) error
}

type signalTerminator struct{}

func (signalTerminator) Terminate(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(syscall.SIGTERM)
}

func runDown(args []string) error {
	fs := flag.NewFlagSet("down", flag.ContinueOnError)
	sessionName := fs.String("session", "", "AMQ workstream session name (default: team workstream)")
	role := fs.String("role", "", "narrow to a single configured role")
	all := fs.Bool("all", false, "target every configured member of the team")
	force := fs.Bool("force", false, "hard-terminate verified live agent PIDs")
	profileFlag := fs.String("profile", "", "team profile to target (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad down - stop configured team members

Usage:
  amq-squad down (--role R | --all) --force [--profile NAME] [--session NAME]

Exactly one selector is required: --role R or --all. --all targets the
configured members from this project's team.json in the resolved session
(default: the team's workstream).

Graceful termination is not yet available: the current AMQ surface has no
one-shot primitive to inject /exit into a running agent. For now down
requires --force; --force sends SIGTERM only to launch-record PIDs that
verify alive AND match the expected agent binary.

Mixed success/failure exits non-zero with a per-target report.

Examples:
  amq-squad down --role cto --force
  amq-squad down --all --force --session issue-96
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	if *role != "" && *all {
		return usageErrorf("--role and --all are mutually exclusive")
	}
	if *role == "" && !*all {
		return usageErrorf("down requires a target selector: pass --role <role> or --all")
	}
	if !*force {
		return fmt.Errorf("graceful down is unavailable: current AMQ has no one-shot /exit injection. Re-run with --force, or wait for the graceful-path decision.")
	}

	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.ExistsProfile(cwd, profile) {
		return fmt.Errorf("no team configured for profile %q. Run 'amq-squad team init%s' first.", profile, profileInitHint(profile))
	}
	return executeDown(downExecution{
		ProjectDir:       cwd,
		RequestedSession: *sessionName,
		ExplicitSession:  flagWasSet(fs, "session"),
		Role:             *role,
		All:              *all,
		Profile:          profile,
		Terminator:       signalTerminator{},
		Probe:            defaultDuplicateLaunchProbe,
		Out:              os.Stdout,
	})
}

type downExecution struct {
	ProjectDir       string
	RequestedSession string
	ExplicitSession  bool
	Role             string
	All              bool
	Profile          string
	Terminator       processTerminator
	Probe            duplicateLaunchProbe
	Out              io.Writer
}

func executeDown(d downExecution) error {
	t, err := team.ReadProfile(d.ProjectDir, d.Profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if len(t.Members) == 0 {
		return fmt.Errorf("team has no members")
	}
	workstream, err := resolveTeamWorkstreamName(t, d.RequestedSession, d.ExplicitSession)
	if err != nil {
		return err
	}
	targets, err := selectDownMembers(t, d.Role, d.All)
	if err != nil {
		return err
	}

	reports := make([]downReport, 0, len(targets))
	for _, m := range targets {
		reports = append(reports, terminateMember(t, m, workstream, d.Terminator, d.Probe))
	}
	return renderDownReports(d.Out, workstream, reports)
}

func selectDownMembers(t team.Team, role string, all bool) ([]team.Member, error) {
	members := orderedTeamMembers(t.Members)
	if all {
		return members, nil
	}
	role = strings.ToLower(strings.TrimSpace(role))
	for _, m := range members {
		if strings.EqualFold(m.Role, role) {
			return []team.Member{m}, nil
		}
	}
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Role)
	}
	return nil, fmt.Errorf("unknown role %q; team has: %s", role, strings.Join(names, ", "))
}

func terminateMember(t team.Team, m team.Member, workstream string, term processTerminator, probe duplicateLaunchProbe) downReport {
	report := downReport{Role: m.Role, Handle: m.Handle, Binary: m.Binary}
	cwd := m.EffectiveCWD(t.Project)
	env, err := resolveAMQEnvInDir(cwd, "", workstream, m.Handle)
	if err != nil {
		report.Status = downStatusNotLive
		report.Detail = "amq env unresolved: " + err.Error()
		return report
	}
	handle := m.Handle
	if env.Me != "" {
		handle = env.Me
	}
	report.Handle = handle
	report.Root = env.Root
	report.AgentDir = filepath.Join(env.Root, "agents", handle)
	rec, err := launch.Read(report.AgentDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.Status = downStatusNotLive
			report.Detail = "no launch record"
			return report
		}
		report.Status = downStatusFailed
		report.Detail = "read launch record: " + err.Error()
		return report
	}
	report.PID = rec.AgentPID
	if rec.AgentPID <= 0 {
		// No pid was captured at launch (e.g. codex seats never recorded one).
		// There is nothing to signal, so consult presence before implying the
		// member is gone: a fresh heartbeat means it may well still be running.
		if lastSeen, fresh := presenceFreshFor(report.AgentDir, handle, probe); fresh {
			report.Status = downStatusMaybeLive
			report.Detail = fmt.Sprintf("no pid captured at launch — may still be live (fresh presence, last seen %s); cannot signal", lastSeen.UTC().Format(time.RFC3339))
			return report
		}
		cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, term, probe)
		if cleaned.failed() {
			report.Status = downStatusFailed
			report.Detail = "no pid captured at launch; " + cleaned.summary()
			return report
		}
		if cleaned.any() {
			report.Status = downStatusCleaned
			report.Detail = "no pid captured at launch; " + cleaned.summary()
			return report
		}
		report.Status = downStatusNotLive
		report.Detail = "no pid captured at launch and presence is not fresh — treating as not live"
		return report
	}
	if !probe.PIDAlive(rec.AgentPID) {
		cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, term, probe)
		if cleaned.failed() {
			report.Status = downStatusFailed
			report.Detail = fmt.Sprintf("recorded pid %d is not alive; %s", rec.AgentPID, cleaned.summary())
			return report
		}
		if cleaned.any() {
			report.Status = downStatusCleaned
			report.Detail = fmt.Sprintf("recorded pid %d is not alive; %s", rec.AgentPID, cleaned.summary())
			return report
		}
		report.Status = downStatusNotLive
		report.Detail = fmt.Sprintf("recorded pid %d is not alive", rec.AgentPID)
		return report
	}
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = m.Binary
	}
	if binary == "" || !probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(binary)) {
		cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, term, probe)
		if cleaned.failed() {
			report.Status = downStatusFailed
			report.Detail = fmt.Sprintf("pid %d does not match expected binary %q (PID reuse); %s", rec.AgentPID, binary, cleaned.summary())
			return report
		}
		if cleaned.any() {
			report.Status = downStatusCleaned
			report.Detail = fmt.Sprintf("pid %d does not match expected binary %q (PID reuse); %s", rec.AgentPID, binary, cleaned.summary())
			return report
		}
		report.Status = downStatusNotLive
		report.Detail = fmt.Sprintf("pid %d does not match expected binary %q (PID reuse)", rec.AgentPID, binary)
		return report
	}
	if err := term.Terminate(rec.AgentPID); err != nil {
		report.Status = downStatusFailed
		report.Detail = fmt.Sprintf("terminate pid %d: %v", rec.AgentPID, err)
		return report
	}
	// The agent itself just received SIGTERM. Reap the wake sidecar and
	// flip presence offline up front so a racing `up` cannot collide on
	// artifacts whose owner is in the process of exiting. A reap-side
	// failure does not retract the SIGTERM we just sent to the agent;
	// surface it as a partial-progress detail line so the operator can see
	// the wake survived without rolling back the agent kill.
	cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, term, probe)
	report.Status = downStatusForceSent
	report.Detail = fmt.Sprintf("SIGTERM sent to pid %d", rec.AgentPID)
	if cleaned.any() || cleaned.failed() {
		report.Detail += "; " + cleaned.summary()
	}
	return report
}

// reapResult records which orphan artifacts were cleaned during teardown so
// the per-member report can surface them. WakeSignalFailed is set when a
// matching live wake was found but Terminate returned an error; in that
// case the lock and presence are preserved so the next preflight still sees
// the live writer.
type reapResult struct {
	WakeKilled       int
	WakeSignalFailed int
	WakeSignalError  string
	LockRemoved      bool
	PresenceFlip     bool
}

func (r reapResult) any() bool {
	return r.WakeKilled > 0 || r.LockRemoved || r.PresenceFlip
}

func (r reapResult) failed() bool {
	return r.WakeSignalFailed > 0
}

func (r reapResult) summary() string {
	parts := make([]string, 0, 3)
	if r.WakeKilled > 0 {
		parts = append(parts, fmt.Sprintf("SIGTERM sent to wake pid %d", r.WakeKilled))
	}
	if r.WakeSignalFailed > 0 {
		parts = append(parts, fmt.Sprintf("failed to signal wake pid %d (%s); lock and presence left intact", r.WakeSignalFailed, r.WakeSignalError))
	}
	if r.LockRemoved {
		parts = append(parts, "removed stale .wake.lock")
	}
	if r.PresenceFlip {
		parts = append(parts, "flipped presence to offline")
	}
	if len(parts) == 0 {
		return "no orphan artifacts found"
	}
	return strings.Join(parts, "; ")
}

// reapStaleArtifacts cleans up the runtime side-effects an agent leaves
// behind when its process dies but its wake sidecar and/or presence file
// survive. Returns a reapResult describing what was done so the caller can
// include it in user-visible reports. Errors during cleanup are best-effort
// and do not propagate: the goal is to unblock the next launch, not to
// guarantee atomicity.
func reapStaleArtifacts(agentDir, handle, root string, term processTerminator, probe duplicateLaunchProbe) reapResult {
	var result reapResult
	if agentDir == "" {
		return result
	}

	lockPath := wakeLockPath(agentDir)
	// canRemoveLock tracks whether we've confirmed the lock is safe to
	// remove: confirmed stale (dead PID / PID-reused / corrupt), or we
	// successfully signaled the live matching wake. If a matching wake is
	// live and we FAIL to signal it, leaving the lock in place keeps the
	// next preflight honest — operators must not be told the system is
	// clean when a foreign-uid wake is still running.
	canRemoveLock := false
	if data, err := os.ReadFile(lockPath); err == nil {
		var lock wakeLockFile
		switch jsonErr := json.Unmarshal(data, &lock); {
		case jsonErr != nil:
			// Corrupt lock: no PID to verify, safe to remove.
			canRemoveLock = true
		case lock.PID <= 0:
			canRemoveLock = true
		case !probe.PIDAlive(lock.PID):
			canRemoveLock = true
		default:
			expectedRoot := root
			if lock.Root != "" {
				expectedRoot = lock.Root
			}
			if !probe.ProcessMatch(lock.PID, wakeProcessMatcher(handle, expectedRoot)) {
				// PID-reuse by an unrelated process: lock is stale.
				canRemoveLock = true
			} else if termErr := term.Terminate(lock.PID); termErr == nil {
				result.WakeKilled = lock.PID
				canRemoveLock = true
			} else {
				// Live matching wake we could not signal. Surface the
				// failure and leave both lock and presence intact so
				// preflight keeps blocking the next `up`.
				result.WakeSignalFailed = lock.PID
				result.WakeSignalError = termErr.Error()
				return result
			}
		}
		if canRemoveLock {
			if rmErr := os.Remove(lockPath); rmErr == nil {
				result.LockRemoved = true
			}
		}
	}

	presencePath := filepath.Join(agentDir, "presence.json")
	if data, err := os.ReadFile(presencePath); err == nil {
		var pres presenceFile
		if jsonErr := json.Unmarshal(data, &pres); jsonErr == nil {
			if pres.Handle != "" && pres.Handle != handle {
				// Foreign handle wrote this file; leave it alone.
				return result
			}
			// Only flip ACTIVE+FRESH presence: a stale "active" file is not
			// blocking anyone (preflight ignores anything past the freshness
			// window), so touching it is pure noise. A fresh active file with
			// a dead agent/wake is the zombie-heartbeat case #38 / #44 — that
			// is what needs flipping so the next `up` is not blocked.
			if strings.EqualFold(pres.Status, "active") && !pres.LastSeen.IsZero() && probe.Now().Sub(pres.LastSeen) <= presenceFreshness {
				pres.Status = "offline"
				pres.LastSeen = probe.Now().UTC()
				if pres.Schema == 0 {
					pres.Schema = 1
				}
				if pres.Handle == "" {
					pres.Handle = handle
				}
				if newData, marshErr := json.Marshal(pres); marshErr == nil {
					if writeErr := os.WriteFile(presencePath, newData, 0o600); writeErr == nil {
						result.PresenceFlip = true
					}
				}
			}
		}
	}

	return result
}

// presenceFreshFor reports the agent's last heartbeat and whether it is recent
// enough to treat the member as possibly still running. It mirrors the
// freshness rule status and preflight use, so down agrees with them.
func presenceFreshFor(agentDir, handle string, probe duplicateLaunchProbe) (time.Time, bool) {
	pres, err := readPresenceForEntry(agentDir)
	if err != nil {
		return time.Time{}, false
	}
	// Honor the same handle rule status and preflight use: a presence file
	// written for a different handle is not evidence this member is live.
	if pres.Handle != "" && pres.Handle != handle {
		return time.Time{}, false
	}
	if !strings.EqualFold(pres.Status, "active") || pres.LastSeen.IsZero() {
		return time.Time{}, false
	}
	return pres.LastSeen, probe.Now().Sub(pres.LastSeen) <= presenceFreshness
}

func renderDownReports(out io.Writer, workstream string, reports []downReport) error {
	fmt.Fprintln(out, "# amq-squad down")
	fmt.Fprintf(out, "# workstream: %s\n", workstream)
	if root := firstDownRoot(reports); root != "" {
		fmt.Fprintf(out, "# AM_ROOT:    %s\n", root)
	}
	fmt.Fprintf(out, "# targets:    %d\n", len(reports))
	fmt.Fprintln(out)
	policy := outputPolicyCurrent()
	var sent, notLive, maybeLive, failed, cleaned int
	for _, r := range reports {
		fmt.Fprintf(out, "%-12s %-10s %s\n", r.Role, colorStatus(policy, string(r.Status)), r.Detail)
		switch r.Status {
		case downStatusForceSent:
			sent++
		case downStatusNotLive:
			notLive++
		case downStatusMaybeLive:
			maybeLive++
		case downStatusFailed:
			failed++
		case downStatusCleaned:
			cleaned++
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "# summary: %d force-sent, %d cleaned, %d not-live, %d maybe-live, %d failed\n", sent, cleaned, notLive, maybeLive, failed)
	if maybeLive > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "WARN: %d member(s) had no pid to signal but still report fresh presence — they may still be running.\n", maybeLive)
		fmt.Fprintln(out, "      down can only signal pids it recorded at launch. Stop the underlying tmux pane / terminal")
		fmt.Fprintln(out, "      manually, then re-run 'amq-squad status' to confirm (AM_ROOT above shows where presence lives).")
	}
	if failed > 0 {
		msg := fmt.Sprintf("down: %d of %d target(s) failed", failed, len(reports))
		if maybeLive > 0 {
			msg += fmt.Sprintf("; %d may still be live (no pid to signal)", maybeLive)
		}
		// Partial (exit 3) when there is either progress (a SIGTERM was sent,
		// an orphan was reaped) or an unconfirmed stop (maybe-live): neither
		// is a clean success nor a wholesale breakage. Only a pure all-failed
		// run stays a plain error.
		if sent > 0 || cleaned > 0 || maybeLive > 0 {
			return &PartialError{Message: msg}
		}
		return errors.New(msg)
	}
	// Members we could not confirm stopped must not read as a clean success:
	// surface them as partial so the exit code (3) signals "not fully down".
	if maybeLive > 0 {
		return &PartialError{Message: fmt.Sprintf("down: %d member(s) may still be live (no pid to signal)", maybeLive)}
	}
	return nil
}

func firstDownRoot(reports []downReport) string {
	for _, r := range reports {
		if r.Root != "" {
			return r.Root
		}
	}
	return ""
}
