package cli

import (
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
		report.Status = downStatusNotLive
		report.Detail = "no pid captured at launch and presence is not fresh — treating as not live"
		return report
	}
	if !probe.PIDAlive(rec.AgentPID) {
		report.Status = downStatusNotLive
		report.Detail = fmt.Sprintf("recorded pid %d is not alive", rec.AgentPID)
		return report
	}
	binary := strings.TrimSpace(rec.Binary)
	if binary == "" {
		binary = m.Binary
	}
	if binary == "" || !probe.ProcessMatch(rec.AgentPID, agentProcessMatcher(binary)) {
		report.Status = downStatusNotLive
		report.Detail = fmt.Sprintf("pid %d does not match expected binary %q (PID reuse)", rec.AgentPID, binary)
		return report
	}
	if err := term.Terminate(rec.AgentPID); err != nil {
		report.Status = downStatusFailed
		report.Detail = fmt.Sprintf("terminate pid %d: %v", rec.AgentPID, err)
		return report
	}
	report.Status = downStatusForceSent
	report.Detail = fmt.Sprintf("SIGTERM sent to pid %d", rec.AgentPID)
	return report
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
	var sent, notLive, maybeLive, failed int
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
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "# summary: %d force-sent, %d not-live, %d maybe-live, %d failed\n", sent, notLive, maybeLive, failed)
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
		// Partial (exit 3) when there is either progress (a SIGTERM was sent) or
		// an unconfirmed stop (maybe-live): neither is a clean success nor a
		// wholesale breakage. Only a pure all-failed run stays a plain error.
		if sent > 0 || maybeLive > 0 {
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
