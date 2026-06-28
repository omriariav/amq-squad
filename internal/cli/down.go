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

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type downStatus string

const (
	// downStatusStopped means the live, binary-matched agent PID received the
	// stop signal (SIGTERM by default, SIGKILL under --force). The on-disk
	// state (launch record, mailbox, brief) is preserved, so the session is
	// recoverable via `amq-squad resume`.
	downStatusStopped   downStatus = "stopped"
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
	// PaneID is the agent's recorded tmux pane id, used for the optional
	// pane-close on teardown (--close-panes). Empty when no tmux record exists.
	PaneID string
	// CWD is the member's resolved working dir, used to identity-check the pane
	// before closing it (guards against pane-id reuse).
	CWD    string
	Status downStatus
	Detail string
}

// processTerminator abstracts process-termination so tests can substitute a
// fake. Production uses signalTerminator. SignalName reports the human label
// of the signal it sends ("SIGTERM"/"SIGKILL") so per-member reports stay
// honest about what was actually delivered.
type processTerminator interface {
	Terminate(pid int) error
	SignalName() string
}

type signalTerminator struct {
	sig syscall.Signal
}

// newSignalTerminator returns a terminator that sends SIGTERM by default, or
// SIGKILL when force is set. `stop` genuinely terminates the agent: SIGTERM
// asks it to exit, --force escalates to an unignorable SIGKILL for agents
// that swallow SIGTERM.
func newSignalTerminator(force bool) signalTerminator {
	if force {
		return signalTerminator{sig: syscall.SIGKILL}
	}
	return signalTerminator{sig: syscall.SIGTERM}
}

func (s signalTerminator) Terminate(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	sig := s.sig
	if sig == 0 {
		sig = syscall.SIGTERM
	}
	return proc.Signal(sig)
}

func (s signalTerminator) SignalName() string {
	if s.sig == syscall.SIGKILL {
		return "SIGKILL"
	}
	return "SIGTERM"
}

// signalNameOf returns the terminator's signal label, defaulting to SIGTERM
// for nil/zero terminators so report wording never reads empty.
func signalNameOf(term processTerminator) string {
	if term == nil {
		return "SIGTERM"
	}
	if name := term.SignalName(); name != "" {
		return name
	}
	return "SIGTERM"
}

// runStop is the primary teardown verb. With no flag it genuinely terminates
// the live, binary-matched agent PID with SIGTERM, reaps the wake sidecar, and
// flips presence offline. Because the agent is actually being stopped, flipping
// presence and reaping the sidecar are honest, not a status lie. The on-disk
// state (launch record, mailbox, brief) is PRESERVED, so the session is
// recoverable via `amq-squad resume`. --force escalates to SIGKILL for agents
// that ignore SIGTERM.
func runStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	sessionName := fs.String("session", "", "AMQ workstream session name (default: team workstream)")
	role := fs.String("role", "", "narrow to a single configured role")
	all := fs.Bool("all", false, "target every configured member of the team")
	force := fs.Bool("force", false, "escalate to SIGKILL for agents that ignore SIGTERM")
	closePanes := fs.Bool("close-panes", false, "also close each stopped agent's tmux pane (default: keep, so final output stays readable; resume re-creates panes)")
	projectFlag := fs.String("project", "", "project/team-home directory to target (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to target (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, stopUsage())
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	if *role != "" && *all {
		return usageErrorf("--role and --all are mutually exclusive")
	}
	if *role == "" && !*all {
		return usageErrorf("stop requires a target selector: pass --role <role> or --all")
	}

	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return executeDown(downExecution{
		Verb:             "stop",
		ProjectDir:       projectDir,
		RequestedSession: *sessionName,
		ExplicitSession:  flagWasSet(fs, "session"),
		Role:             *role,
		All:              *all,
		Profile:          profile,
		// default=SIGTERM; --force=SIGKILL escalation for agents that ignore
		// SIGTERM. The PID-liveness + binary-match guards still apply, so a
		// reused/foreign PID is never signaled regardless of --force.
		Terminator: newSignalTerminator(*force),
		Probe:      defaultDuplicateLaunchProbe,
		Out:        os.Stdout,
		ClosePanes: *closePanes,
	})
}

func stopUsage() string {
	var b strings.Builder
	b.WriteString("amq-squad stop - stop configured team members (the session stays resumable)\n\n")
	b.WriteString("Usage:\n  amq-squad stop (--role R | --all) [--project DIR] [--force] [--close-panes] [--profile NAME] [--session NAME]\n\n")
	b.WriteString(`Exactly one selector is required: --role R or --all. --all targets the
configured members from this project's team.json in the resolved session
(default: the team's workstream). --project targets another team-home without
changing directories.

stop GENUINELY TERMINATES each live, binary-matched agent: it sends SIGTERM to
the launch-record PID, reaps the wake sidecar, and flips presence offline. It
only signals PIDs that verify alive AND match the expected agent binary, so a
reused PID is never touched. --force escalates to SIGKILL for agents that
ignore SIGTERM.

The on-disk state (launch record, mailbox, brief) is PRESERVED, so the session
is recoverable: bring it back with 'amq-squad resume'.

Exit codes: a successful stop exits 0; a mixed run (some stopped, some failed
or unconfirmed) exits 3.

Examples:
  amq-squad stop --role cto
  amq-squad stop --project ~/Code/app --all --session issue-96
  amq-squad stop --all --session issue-96
  amq-squad stop --role cto --force   # SIGKILL an agent that ignores SIGTERM
`)
	return b.String()
}

type downExecution struct {
	Verb             string
	ProjectDir       string
	RequestedSession string
	ExplicitSession  bool
	Role             string
	All              bool
	Profile          string
	Terminator       processTerminator
	Probe            duplicateLaunchProbe
	Out              io.Writer
	// ClosePanes closes each downed agent's recorded tmux pane after teardown.
	// stop defaults this OFF (final output stays readable; --close-panes opts in).
	ClosePanes bool
}

func executeDown(d downExecution) error {
	verb := d.Verb
	if verb == "" {
		verb = "stop"
	}
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
	if err := ensureNoNamespaceConflict(verb, d.ProjectDir, d.Profile, workstream); err != nil {
		return err
	}
	targets, err := selectDownMembers(t, d.Role, d.All)
	if err != nil {
		return err
	}

	reports := make([]downReport, 0, len(targets))
	for _, m := range targets {
		reports = append(reports, terminateMember(t, d.Profile, m, workstream, d.Terminator, d.Probe))
	}
	if d.ClosePanes {
		closeDownedPanes(reports, workstream)
	}
	return renderDownReports(d.Out, verb, workstream, reports)
}

// closeDownedPanes closes the recorded tmux pane of every member that is
// confirmed DOWN (stopped / cleaned / not-live) and carries a recorded pane id.
// maybe-live and failed members are deliberately left alone — amq-squad never
// closes a pane it is not sure is dead. Best-effort: a kill-pane error (e.g. the
// pane is already gone) is swallowed and the teardown result is unaffected.
func closeDownedPanes(reports []downReport, workstream string) {
	for i := range reports {
		r := &reports[i]
		if strings.TrimSpace(r.PaneID) == "" {
			continue
		}
		switch r.Status {
		case downStatusStopped, downStatusCleaned, downStatusNotLive:
			closed, skip := closeRecordedPaneSafely(r.PaneID, workstream, r.Role, r.CWD)
			note := ""
			if closed {
				note = "closed tmux pane " + r.PaneID
			} else if skip != "" {
				note = "left tmux pane open: " + skip
			}
			if note != "" {
				if strings.TrimSpace(r.Detail) == "" {
					r.Detail = note
				} else {
					r.Detail += "; " + note
				}
			}
		}
	}
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

func terminateMember(t team.Team, profile string, m team.Member, workstream string, term processTerminator, probe duplicateLaunchProbe) downReport {
	report := downReport{Role: m.Role, Handle: m.Handle, Binary: m.Binary}
	cwd := m.EffectiveCWD(t.Project)
	env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, m.Handle)
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
	root := absoluteAMQRoot(cwd, env.Root)
	report.Root = root
	report.AgentDir = filepath.Join(root, "agents", handle)
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
	if rec.Tmux != nil {
		report.PaneID = rec.Tmux.PaneID
	}
	report.CWD = compareCWD(cwd, rec.CWD)
	report.PID = rec.AgentPID
	if rec.External {
		report.Status = downStatusMaybeLive
		if report.PaneID != "" {
			report.Detail = fmt.Sprintf("external/adopted pane %s is operator-owned; stop it manually if needed", report.PaneID)
		} else {
			report.Detail = "external/adopted session is operator-owned; stop it manually if needed"
		}
		return report
	}
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
	sigName := signalNameOf(term)
	if err := term.Terminate(rec.AgentPID); err != nil {
		report.Status = downStatusFailed
		report.Detail = fmt.Sprintf("terminate pid %d: %v", rec.AgentPID, err)
		return report
	}
	// The agent itself just received the stop signal. Reap the wake sidecar and
	// flip presence offline up front so a racing `up` cannot collide on
	// artifacts whose owner is in the process of exiting. Because the agent is
	// genuinely being stopped, flipping presence offline is honest, not a
	// status lie. A reap failure (live matching wake we could not signal) is
	// itself a partial-stop: the agent is dying but the wake is still running
	// and the on-disk lock + presence are intentionally preserved. Surface
	// that as downStatusFailed so renderDownReports counts it correctly;
	// without this, the per-member detail says "wake survived" but the summary
	// still reads as a clean success.
	cleaned := reapStaleArtifacts(report.AgentDir, handle, report.Root, term, probe)
	if cleaned.failed() {
		report.Status = downStatusFailed
		report.Detail = fmt.Sprintf("%s sent to pid %d; %s", sigName, rec.AgentPID, cleaned.summary())
		return report
	}
	report.Status = downStatusStopped
	report.Detail = fmt.Sprintf("%s sent to pid %d", sigName, rec.AgentPID)
	if cleaned.any() {
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
	WakeSignalName   string
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
		sig := r.WakeSignalName
		if sig == "" {
			sig = "SIGTERM"
		}
		parts = append(parts, fmt.Sprintf("%s sent to wake pid %d", sig, r.WakeKilled))
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
				result.WakeSignalName = signalNameOf(term)
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

func renderDownReports(out io.Writer, verb, workstream string, reports []downReport) error {
	fmt.Fprintf(out, "# amq-squad %s\n", verb)
	fmt.Fprintf(out, "# workstream: %s\n", workstream)
	if root := firstDownRoot(reports); root != "" {
		fmt.Fprintf(out, "# AM_ROOT:    %s\n", root)
	}
	fmt.Fprintf(out, "# targets:    %d\n", len(reports))
	fmt.Fprintln(out)
	policy := outputPolicyCurrent()
	var stopped, notLive, maybeLive, failed, cleaned int
	for _, r := range reports {
		fmt.Fprintf(out, "%-12s %-10s %s\n", r.Role, colorStatus(policy, string(r.Status)), r.Detail)
		switch r.Status {
		case downStatusStopped:
			stopped++
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
	fmt.Fprintf(out, "# summary: %d stopped, %d cleaned, %d not-live, %d maybe-live, %d failed\n", stopped, cleaned, notLive, maybeLive, failed)
	// State-aware resumable hint: a stop preserves on-disk state, so make it
	// explicit that the session can be brought back.
	if stopped > 0 {
		fmt.Fprintf(out, "# stopped %s; bring it back with 'amq-squad resume'\n", workstream)
	}
	if maybeLive > 0 {
		fmt.Fprintln(out)
		fmt.Fprintf(out, "WARN: %d member(s) had no pid to signal but still report fresh presence — they may still be running.\n", maybeLive)
		fmt.Fprintf(out, "      %s can only signal pids it recorded at launch. Stop the underlying tmux pane / terminal\n", verb)
		fmt.Fprintln(out, "      manually, then re-run 'amq-squad status' to confirm (AM_ROOT above shows where presence lives).")
	}
	if failed > 0 {
		msg := fmt.Sprintf("%s: %d of %d target(s) failed", verb, failed, len(reports))
		if maybeLive > 0 {
			msg += fmt.Sprintf("; %d may still be live (no pid to signal)", maybeLive)
		}
		// Partial (exit 3) when there is either progress (a stop signal was
		// sent, an orphan was reaped) or an unconfirmed stop (maybe-live):
		// neither is a clean success nor a wholesale breakage. Only a pure
		// all-failed run stays a plain error.
		if stopped > 0 || cleaned > 0 || maybeLive > 0 {
			return &PartialError{Message: msg}
		}
		return errors.New(msg)
	}
	// Members we could not confirm stopped must not read as a clean success:
	// surface them as partial so the exit code (3) signals "not fully down".
	if maybeLive > 0 {
		return &PartialError{Message: fmt.Sprintf("%s: %d member(s) may still be live (no pid to signal)", verb, maybeLive)}
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
