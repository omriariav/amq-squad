package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/omriariav/amq-squad/v3/internal/drafter"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// v3_holding.go is the temporary holding file for phase 2a-1 extractions
// (plan 4.1 step 3). Every declaration here was moved verbatim from a file
// deleted in 2a-1 because a surviving non-test caller still needs it. 2a-2
// moves each one to its 3.0 home or deletes it; this file's target at the end
// of 2a is zero lines. Each block names its source file and line range on the
// v3 branch at the time of the cut.

// From internal/cli/goal.go:314-317 (C5 cut). Not listed in the manifest
// extract field; needed by global_noc.go, team_launch.go and the C3/C9/C18
// files that are deleted later in 2a-1.
const (
	goalOrchestratorRole          = "orchestrator"
	defaultGoalOrchestratorHandle = "orchestrator"
)

// From internal/cli/up.go:404-411 (C11 cut). Not in the manifest extract field;
// callers: dispatch.go, doctor.go, operator.go, status.go, team.go (kept) and
// runtime_actions.go, team_lead.go, team_member.go (cut later).
// profileInitCommand returns the creation command to suggest when reporting a
// missing team profile.
func profileInitCommand(profile string) string {
	if profile == "" || profile == team.DefaultProfile {
		return "amq-squad init"
	}
	return "amq-squad init --profile " + profile
}

// From internal/cli/resume_shared.go:37-44 (C11 cut). Callers plan.go and
// simple_start.go die in the C4 cut; drop this block with them.
// teamRoleList returns the team's roles in canonical order, for error messages.
func teamRoleList(t team.Team) []string {
	out := make([]string, 0, len(t.Members))
	for _, m := range orderedTeamMembers(t.Members) {
		out = append(out, m.Role)
	}
	return out
}

// From internal/cli/resume_shared.go:46-63 (C11 cut). Not in the manifest extract
// field; canonicalPath callers: task.go (kept), team_lead.go; compactNativeValue
// callers: effort.go, model_defaults.go (kept).
// canonicalPath keeps launch/resume identity comparisons on the shared pathnorm
// seam, including symlink and on-disk case normalization for existing paths.
func canonicalPath(p string) string {
	return canonicalFilesystemPath(p)
}

func compactNativeValue(arg string) string {
	if strings.HasPrefix(arg, "-c=") {
		return strings.TrimPrefix(arg, "-c=")
	}
	if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
		return strings.TrimPrefix(arg, "-c")
	}
	if _, value, ok := strings.Cut(arg, "="); ok {
		return value
	}
	return ""
}

// From internal/cli/resume_shared.go:65-107 (C11 cut). Not in the manifest extract
// field; caller: workstream.go (kept).
// findMemberRestoreRecord returns the most recent launch.Record for the
// given (member project, member cwd, workstream, role, handle) tuple under
// baseRoot. memberCWD anchors identity to the current team member's
// project; records whose CWD does not resolve to the same path are
// rejected so a sibling repo with the same role/handle/session cannot
// leak its restore command into this team's plan. Records with empty
// CWD (legacy AMQ-only inference) are accepted as fallback only when no
// CWD-matching record exists.
func findMemberRestoreRecord(baseRoot, projectDir, memberCWD, profile, workstream, role, handle string) (launch.Record, bool) {
	if baseRoot == "" {
		return launch.Record{}, false
	}
	entries, err := launch.ScanRestorableEntriesInRoot(projectDir, baseRoot)
	if err != nil {
		return launch.Record{}, false
	}
	wantCWD := canonicalPath(memberCWD)
	var bestExact, bestLegacy *launch.Entry
	for i := range entries {
		rec := entries[i].Record
		if !matchesRestoreFiltersForProfile(rec, role, handle, workstream, "", profile) {
			continue
		}
		recCWD := canonicalPath(rec.CWD)
		switch {
		case recCWD != "" && recCWD == wantCWD:
			if bestExact == nil || rec.StartedAt.After(bestExact.Record.StartedAt) {
				bestExact = &entries[i]
			}
		case recCWD == "":
			if bestLegacy == nil || rec.StartedAt.After(bestLegacy.Record.StartedAt) {
				bestLegacy = &entries[i]
			}
		}
	}
	if bestExact != nil {
		return bestExact.Record, true
	}
	if bestLegacy != nil {
		return bestLegacy.Record, true
	}
	return launch.Record{}, false
}

// From internal/cli/down.go:22 and :58-69 (C11 cut). Not in the manifest extract
// field; callers: session_reset_support.go (host, Terminator field) and
// pane_cleanup_rm.go (C1, cut later).
type downStatus string

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

// From internal/cli/down.go:90-134 (C11 cut). Same callers as above.
// newSignalTerminator returns a terminator that sends SIGTERM by default, or
// SIGKILL when force is set. `down` genuinely terminates the agent: SIGTERM
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

// From internal/cli/down.go:1613-1616 (C11 cut). Caller: pane_cleanup_rm.go (C1);
// drop with that cut.
type downAgentJSON struct {
	Outcome downStatus `json:"outcome"`
	Detail  string     `json:"detail"`
}

// From internal/cli/new.go (C4 cut); the manifest extract field names both. Callers:
// launch.go and team.go (kept).

// From new.go:488-524
func peelProjectFlag(args []string) (string, []string, error) {
	out := make([]string, 0, len(args))
	project := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			out = append(out, args[i:]...)
			break
		}
		if a == "--project" || a == "-p" {
			if i+1 >= len(args) {
				return "", nil, usageErrorf("--project requires a directory")
			}
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(args[i+1])
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			i++
			continue
		}
		if strings.HasPrefix(a, "--project=") || strings.HasPrefix(a, "-p=") {
			if project != "" {
				return "", nil, usageErrorf("--project may be passed only once")
			}
			project = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(a, "--project="), "-p="))
			if project == "" {
				return "", nil, usageErrorf("--project requires a directory")
			}
			continue
		}
		out = append(out, a)
	}
	return project, out, nil
}

// From new.go:596-620
func runInProject(project string, fn func() error) error {
	if strings.TrimSpace(project) == "" {
		return fn()
	}
	dir, err := expandPath(project)
	if err != nil {
		return fmt.Errorf("resolve --project: %w", err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--project %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--project %s is not a directory", dir)
	}
	old, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("chdir %s: %w", dir, err)
	}
	defer func() { _ = os.Chdir(old) }()
	return fn()
}

// From internal/cli/list.go (C4 cut). Not in the manifest extract field. Callers:
// restore.go (wakeHealthForEntry, host), liveness.go (readPresenceForEntry, kept),
// namespace_migration_plan.go (C7). wakeHealthForEntry still depends on wake_lock_compat.go
// (C1) and runtime_identity.go (C2); revisit at those cuts.

// From list.go:148-177
// wakeHealthForEntry returns a short label describing wake state. Empty when
// the record does not look active enough to investigate. Output values:
//   - "pid:<n>": wake.lock present and wake process alive
//   - "missing": agent looks active but no wake.lock found
//   - "stale":   wake.lock present but PID dead or unrelated
func wakeHealthForEntry(e launch.Entry, probe duplicateLaunchProbe) string {
	if !looksActive(e, probe) {
		return ""
	}
	lockPath := wakeLockPath(e.AgentDir)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return "missing"
	}
	lock, err := decodeWakeLockFile(data)
	if err != nil {
		return "stale"
	}
	if lock.PID <= 0 || !probe.PIDAlive(lock.PID) {
		return "stale"
	}
	expectedRoot := e.Record.Root
	if lock.Root != "" {
		expectedRoot = lock.Root
	}
	if !probe.ProcessMatch(lock.PID, wakeProcessMatcher(e.Record.Handle, expectedRoot)) {
		return "stale"
	}
	return fmt.Sprintf("pid:%d", lock.PID)
}

// From list.go:179-198
// looksActive reports whether a launch entry is fresh enough to bother
// inspecting wake state. A record is "active-looking" when its agent PID is
// alive and the binary command matches, or when presence is fresh.
func looksActive(e launch.Entry, probe duplicateLaunchProbe) bool {
	rec := e.Record
	if classifyLaunchPIDRuntimeIdentity(rec, rec.Binary, probe).PIDLive {
		return true
	}
	pres, err := readPresenceForEntry(e.AgentDir)
	if err != nil {
		return false
	}
	if !strings.EqualFold(pres.Status, "active") {
		return false
	}
	if pres.LastSeen.IsZero() {
		return false
	}
	return probe.Now().Sub(pres.LastSeen) <= presenceFreshness
}

// From list.go:200-210
func readPresenceForEntry(agentDir string) (presenceFile, error) {
	data, err := os.ReadFile(presencePath(agentDir))
	if err != nil {
		return presenceFile{}, err
	}
	var pres presenceFile
	if err := json.Unmarshal(data, &pres); err != nil {
		return presenceFile{}, err
	}
	return pres, nil
}

// From list.go:212-214
func presencePath(agentDir string) string {
	return filepath.Join(agentDir, "presence.json")
}

// From internal/cli/simple_start.go:60-73, 94-107 and 110-172 (C4 cut), REDUCED, not
// verbatim: the struct kept only the two drafter-seam fields that brief.go and
// simple_start_brief_draft.go (deferred) read; every other field's consumers
// (start, plan, wizard, resume) were deleted with simple_start.go, and the full
// struct named simple_start plan types that no longer exist.
type simpleStartDependencies struct {
	ResolveDrafter resolveCLIDrafterFunc
	RunDrafter     cliDrafterRunner
}

func defaultSimpleStartDependencies() simpleStartDependencies {
	return simpleStartDependencies{
		ResolveDrafter: resolveCLIDrafter,
		RunDrafter:     drafter.Run,
	}
}

func normalizeSimpleStartDependencies(deps simpleStartDependencies) simpleStartDependencies {
	defaults := defaultSimpleStartDependencies()
	if deps.ResolveDrafter == nil {
		deps.ResolveDrafter = defaults.ResolveDrafter
	}
	if deps.RunDrafter == nil {
		deps.RunDrafter = defaults.RunDrafter
	}
	return deps
}

// From internal/cli/simple_start.go (C4 cut). Caller: brief.go (kept).

// From simple_start.go:1291-1307
func writeSimpleStartDrafterAttempts(out io.Writer, draft *simpleStartBriefDraft) {
	if draft == nil {
		return
	}
	if len(draft.Attempts) == 0 {
		if command := strings.TrimSpace(draft.Evidence.CommandDisplay); command != "" {
			fmt.Fprintf(out, "  drafter command: %s\n", command)
		}
		return
	}
	for _, attempt := range draft.Attempts {
		fmt.Fprintf(out, "  drafter attempt (%s): %s\n", attempt.Backend, attempt.CommandDisplay)
		if failure := strings.TrimSpace(attempt.Failure); failure != "" {
			fmt.Fprintf(out, "  fall-through: %s\n", failure)
		}
	}
}

// From internal/cli/simple_start.go:30-58 (C4 cut). Dead path: simple_start.go was the
// only setter of teamLaunchOptions.AfterCheckpoint, but team_launch.go and
// team_launch_launchapi.go (kept) and team_launch_tmux.go (C1) still name the type at
// about a dozen sites. Parked so those files compile; drop with the C1 cut and the
// 2a-2 team_launch shrink.
type simpleStartCheckpoint string

const (
	simpleStartCheckpointNamespaceCreation simpleStartCheckpoint = "namespace_creation"
	simpleStartCheckpointPaneCreation      simpleStartCheckpoint = "pane_creation"
	simpleStartCheckpointChildDispatch     simpleStartCheckpoint = "child_dispatch"
	simpleStartCheckpointLaunchRecordWrite simpleStartCheckpoint = "launch_record_write"
)

type simpleStartCheckpointError struct {
	Checkpoint simpleStartCheckpoint
	Err        error
}

func (e *simpleStartCheckpointError) Error() string {
	return fmt.Sprintf("simple start checkpoint %s: %v", e.Checkpoint, e.Err)
}

func (e *simpleStartCheckpointError) Unwrap() error { return e.Err }

func callSimpleStartCheckpoint(after func(simpleStartCheckpoint) error, checkpoint simpleStartCheckpoint) error {
	if after == nil {
		return nil
	}
	if err := after(checkpoint); err != nil {
		return &simpleStartCheckpointError{Checkpoint: checkpoint, Err: err}
	}
	return nil
}
