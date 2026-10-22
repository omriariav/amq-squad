package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/omriariav/amq-squad/v3/internal/drafter"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/tmuxpane"
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

// From internal/cli/team_member_diff.go:11-17 (C18 cut). Callers: mutation_json.go (keep).
// memberFieldChange is one field-level before/after pair in a `team member
// update --dry-run` preview (#616).
type memberFieldChange struct {
	Field  string `json:"field"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// From internal/cli/team_member.go:78-85 (C18 cut). Callers: init.go, team.go (kept); team_member_control_continue.go (C1).
// wantsHelp reports whether args opens with -h/--help. A third-level
// subcommand (add/update/rm/list/control-continue) must check this BEFORE
// peeling a required positional or parsing flags, or --help fails closed as
// a validation error / falls through to Go's own default usage banner
// instead of this repo's help text (gh#762).
func wantsHelp(args []string) bool {
	return len(args) > 0 && (args[0] == "-h" || args[0] == "--help")
}

// From internal/cli/team_member.go:173-178 (C18 cut). Callers: task.go (kept).
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// From internal/cli/team_member.go:188-197 (C18 cut). Callers: task.go (kept); team_member_control_continue.go (C1).
// peelPositional splits a leading positional argument from the remaining flag
// args (Go's flag parser stops at the first non-flag, so a positional that
// precedes flags must be peeled first). ok is false when the first arg is
// missing or is itself a flag; the caller supplies the context-specific error.
func peelPositional(args []string) (val string, rest []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, false
	}
	return args[0], args[1:], true
}

// From internal/cli/team_member.go:920-926 (C18 cut). Callers: role_draft.go, team.go (kept).
func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// From internal/cli/team_member.go:928-940 (C18 cut). Callers: role_draft.go (kept).
// resolveExistingTeamProfile resolves the project dir + profile (reusing the
// shared resolver) and requires the profile to already exist — roster
// mutation needs a team to mutate.
func resolveExistingTeamProfile(projectFlag, profileFlag string, projectSet bool) (string, string, error) {
	projectDir, profile, err := resolveProjectProfile(projectFlag, profileFlag, projectSet)
	if err != nil {
		return "", "", err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return "", "", fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return projectDir, profile, nil
}

// From internal/cli/team_member.go:942-947 (C18 cut). Callers: team_operator.go (kept).
// withProfileLock serializes a read-modify-write of a team profile across
// concurrent amq-squad processes via an exclusive lock on a sidecar file, so
// a lead and a worker mutating the roster at once cannot lose an update.
func withProfileLock(projectDir, profile string, fn func() error) error {
	return team.WithProfileLock(projectDir, profile, fn)
}

// From internal/cli/team_member.go:984-1001 (C18 cut). Callers: amq_root_authority.go (kept).
// inheritedSession returns the workstream a new member should join: the
// session shared by existing members (so the roster stays one workstream),
// or empty when the team has no members or they disagree (resolved at launch).
func inheritedSession(t team.Team) string {
	session := ""
	for _, m := range t.Members {
		s := strings.TrimSpace(m.Session)
		if s == "" {
			continue
		}
		if session == "" {
			session = s
		} else if session != s {
			return ""
		}
	}
	return session
}

// From internal/cli/team_member.go:1024-1051 (C18 cut). Callers: team.go (kept).
// memberCWDOverride is the SINGLE resolver for a member's --cwd, shared by
// roster creation (team init), `team member add` and `team member update`.
//
// #538, second review F2: an earlier version of this claimed to be shared but was
// not -- team init kept its own copy -- and it resolved a relative value with
// filepath.Abs, i.e. against the SHELL working directory. So
// `--project /repo --cwd ../wt` run from /tmp recorded /tmp/wt here and
// /repo/../wt on the create path. That is precisely the two-writers/two-origins
// defect #539 and #540 were about, reintroduced in the fix for a different bug.
// Both problems have one fix: genuinely one function, anchored to the PROJECT.
//
// A relative path means "relative to the project", never to the caller's shell.
// A value naming the team-home itself records as no override, so the member stays
// clean rather than pinning the default explicitly.
func memberCWDOverride(projectDir, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	abs := absoluteFilesystemPathIn(projectDir, raw)
	if abs == "" {
		return "", fmt.Errorf("resolve --cwd %q", raw)
	}
	if sameFilesystemPath(abs, projectDir) {
		return "", nil
	}
	return abs, nil
}

// From internal/cli/team_lead.go:22-22 (C18 cut). Callers: global_noc.go, team_launch.go (kept); layout_finalize.go (C1).
var currentPaneIdentity = tmuxpane.CurrentPaneIdentity

// From internal/cli/team_lead.go:1082-1089 (C18 cut). Callers: team_launch.go (kept).
func memberByRole(t team.Team, role string) (team.Member, bool) {
	for _, m := range t.Members {
		if m.Role == role {
			return m, true
		}
	}
	return team.Member{}, false
}

// From internal/cli/team_rm.go:187-198 (C18 cut). Callers: status_board.go (C3).
func configuredTeamProfiles(projectDir string) ([]string, error) {
	var profiles []string
	if team.ExistsProfile(projectDir, team.DefaultProfile) {
		profiles = append(profiles, team.DefaultProfile)
	}
	named, err := team.ListProfiles(projectDir)
	if err != nil {
		return nil, fmt.Errorf("list profiles: %w", err)
	}
	profiles = append(profiles, named...)
	return profiles, nil
}

// From internal/cli/namespace_migration_plan.go:25-37 (C7 cut). Callers: namespaceMigrationPlan (below).
type namespaceMigrationArtifact struct {
	Name       string      `json:"name"`
	Source     string      `json:"source"`
	Target     string      `json:"target,omitempty"`
	Policy     string      `json:"policy"`
	Kind       string      `json:"kind"`
	SHA256     string      `json:"sha256"`
	Bytes      int64       `json:"bytes"`
	Mode       os.FileMode `json:"mode"`
	FileCount  int         `json:"file_count"`
	DirCount   int         `json:"dir_count"`
	Historical bool        `json:"historical,omitempty"`
}

// From internal/cli/namespace_migration_plan.go:39-49 (C7 cut). Callers: namespaceMigrationPlan (below).
type namespaceMigrationInputEvidence struct {
	SourceProfilePath   string `json:"source_profile_path"`
	SourceProfileSHA256 string `json:"source_profile_sha256"`
	TargetProfilePath   string `json:"target_profile_path"`
	TargetProfileSHA256 string `json:"target_profile_sha256"`
	AMQRCPath           string `json:"amqrc_path,omitempty"`
	AMQRCSHA256         string `json:"amqrc_sha256,omitempty"`
	Device              uint64 `json:"device"`
	AvailableBytes      uint64 `json:"available_bytes"`
	RequiredBytes       uint64 `json:"required_bytes"`
}

// From internal/cli/namespace_migration_plan.go:51-56 (C7 cut). Callers: namespaceMigrationPlan (below).
type namespaceMigrationLiveness struct {
	Endpoint string `json:"endpoint"`
	Handle   string `json:"handle,omitempty"`
	State    string `json:"state"`
	Detail   string `json:"detail"`
}

// From internal/cli/namespace_migration_plan.go:58-73 (C7 cut). Callers: namespace_migration_guard.go (host; namespaceMigrationJournal.Plan field).
type namespaceMigrationPlan struct {
	SchemaVersion int                             `json:"schema_version"`
	ID            string                          `json:"id"`
	ProjectDir    string                          `json:"project_dir"`
	Source        squadnamespace.Ref              `json:"source"`
	Target        squadnamespace.Ref              `json:"target"`
	Artifacts     []namespaceMigrationArtifact    `json:"artifacts"`
	History       []namespaceMigrationArtifact    `json:"history_references,omitempty"`
	Liveness      []namespaceMigrationLiveness    `json:"liveness"`
	Inputs        namespaceMigrationInputEvidence `json:"inputs"`
	Blockers      []string                        `json:"blockers,omitempty"`
	Warnings      []string                        `json:"warnings,omitempty"`
	Fingerprint   string                          `json:"fingerprint"`
	Recovery      string                          `json:"recovery_command"`
	DryRun        bool                            `json:"dry_run"`
}

// From internal/cli/namespace_migration_plan.go:632-644 (C7 cut). Callers: namespace_migration_guard.go (host).
func dedupeMigrationStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

// From internal/cli/namespace_migration_plan.go:219-233 (C7 cut). Callers: readNamespaceMigrationJournal (below).
func canonicalMigrationProject(project string) (string, error) {
	abs, err := filepath.Abs(strings.TrimSpace(project))
	if err != nil {
		return "", fmt.Errorf("resolve project: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve project symlinks: %w", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("project must resolve to a real directory")
	}
	return filepath.Clean(resolved), nil
}

// From internal/cli/namespace_migration_tx.go:376-400 (C7 cut). Callers: namespace_migration_guard.go (host; reads on-disk journals for the writer freeze).
func readNamespaceMigrationJournal(project, id string) (namespaceMigrationJournal, error) {
	if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
		return namespaceMigrationJournal{}, usageErrorf("namespace migration requires a valid --id")
	}
	canonicalProject, err := canonicalMigrationProject(project)
	if err != nil {
		return namespaceMigrationJournal{}, err
	}
	project = canonicalProject
	path := namespaceMigrationJournalPath(project, id)
	b, err := os.ReadFile(path)
	if err != nil {
		return namespaceMigrationJournal{}, fmt.Errorf("read migration journal %s: %w", path, err)
	}
	var journal namespaceMigrationJournal
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&journal); err != nil {
		return namespaceMigrationJournal{}, fmt.Errorf("parse migration journal %s: %w", path, err)
	}
	if journal.SchemaVersion != namespaceMigrationSchema || journal.ID != id || filepath.Clean(journal.ProjectDir) != filepath.Clean(project) {
		return namespaceMigrationJournal{}, fmt.Errorf("migration journal identity mismatch")
	}
	return journal, nil
}

// From internal/cli/namespace_migration_tx.go:591-615 (C7 cut). Callers: namespace_migration_guard.go (host).
func rejectMigrationSymlinkComponents(project, target string) error {
	project, target = filepath.Clean(project), filepath.Clean(target)
	if !pathContains(project, target) {
		return fmt.Errorf("migration path escapes project: %s", target)
	}
	rel, _ := filepath.Rel(project, target)
	current := project
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		if part == "" || part == "." {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink component refused: %s", current)
		}
	}
	return nil
}

// From internal/cli/team_member_control_continue.go:238-241 (C1 cut). Callers: runtime_json.go (C2, deferred: status JSON control-client view).
func listExactTmuxControlClients(session string, output func(name string, args ...string) (string, error)) ([]tmuxClient, error) {
	clients, _, err := readExactTmuxControlClients(session, output)
	return clients, err
}

// From internal/cli/team_member_control_continue.go:243-254 (C1 cut). Callers: listExactTmuxControlClients (above).
func readExactTmuxControlClients(session string, output func(name string, args ...string) (string, error)) ([]tmuxClient, string, error) {
	if output == nil || strings.TrimSpace(session) == "" {
		return nil, "", fmt.Errorf("tmux client discovery dependency or session is incomplete")
	}
	const format = "#{client_name}\t#{client_tty}\t#{client_control_mode}\t#{client_flags}\t#{client_session}"
	out, err := output("tmux", "list-clients", "-t", session, "-F", format)
	if err != nil {
		return nil, out, err
	}
	clients, err := parseExactTmuxControlClients(out)
	return clients, out, err
}

// From internal/cli/team_member_control_continue.go:256-290 (C1 cut). Callers: readExactTmuxControlClients (above).
func parseExactTmuxControlClients(out string) ([]tmuxClient, error) {
	var clients []tmuxClient
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			return nil, fmt.Errorf("unexpected tmux list-clients row with %d columns", len(parts))
		}
		name, tty := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		controlField, flags, session := strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3]), strings.TrimSpace(parts[4])
		if name != parts[0] || tty != parts[1] || controlField != parts[2] || flags != parts[3] || session != parts[4] {
			return nil, fmt.Errorf("tmux list-clients row contains non-canonical field whitespace")
		}
		if controlField != "0" && controlField != "1" {
			return nil, fmt.Errorf("invalid client_control_mode %q", controlField)
		}
		if len(tty) > 512 || strings.ContainsAny(tty, "\x00\r\n\t") || len(flags) > 4096 || strings.ContainsAny(flags, "\x00\r\n\t") {
			return nil, fmt.Errorf("tmux list-clients row contains invalid tty or flags")
		}
		if controlField != "1" {
			if strings.Contains(flags, "control-mode") {
				return nil, fmt.Errorf("contradictory control-mode client row")
			}
			continue
		}
		if _, err := validateTmuxControlClientName(name); err != nil || session == "" {
			return nil, fmt.Errorf("control-mode client row lacks exact client_name or client_session")
		}
		clients = append(clients, tmuxClient{Name: name, TTY: tty, ControlMode: true, Flags: flags, Session: session})
	}
	return clients, nil
}

// From internal/cli/team_member_control_continue.go:292-301 (C1 cut). Callers: runtime_json.go (C2, deferred).
// validateTmuxControlClientName also rejects a name usable as a `-t`
// selector only by accident: a leading "-" would be parsed by tmux as a flag
// rather than the target-client value (R5, #505 review D3).
func validateTmuxControlClientName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n\t") || strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("invalid tmux client name")
	}
	return value, nil
}

// From internal/cli/team_launch_iterm2.go:27-27 (C1 cut). Callers: focusITerm2Window (below).
var iterm2OutputCommand = outputCommand

// From internal/cli/team_launch_iterm2.go:168-181 (C1 cut). Callers: runtime_actions.go (C2, deferred; runFocus, unregistered since cut 11, drop with it).
func focusITerm2Window(windowID string) error {
	windowID = strings.TrimSpace(windowID)
	if windowID == "" {
		return fmt.Errorf("iTerm2 window id is unavailable")
	}
	out, err := iterm2OutputCommand("osascript", iterm2FocusArgv(windowID)...)
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "OK" {
		return fmt.Errorf("could not raise iTerm2 window %s; it may have been closed", windowID)
	}
	return nil
}

// From internal/cli/team_launch_iterm2.go:183-198 (C1 cut). Callers: focusITerm2Window (above).
func iterm2FocusArgv(windowID string) []string {
	script := `on run argv
set targetID to item 1 of argv
tell application "iTerm2"
	activate
	repeat with w in windows
		if (id of w as string) is targetID then
			tell w to select
			return "OK"
		end if
	end repeat
end tell
return "MISS"
end run`
	return []string{"-e", script, windowID}
}

// From internal/cli/layout_finalize.go:196-202 (C1 cut). Callers: status.go (kept; layout_finalization warning).
func layoutFinalizationWarningPath(project, profile, session string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	return filepath.Join(project, team.DirName, "layout-finalization", profile, session+".warning")
}

// From internal/cli/profiles.go:30-42 (C26 cut). Callers: context_resolution.go, team.go (kept).
// resolveProfileFlag normalizes a --profile value: empty or "default" maps
// to the implicit default profile; non-default names are validated against
// the slug rules. Returns the canonical profile name and any error.
func resolveProfileFlag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == team.DefaultProfile {
		return team.DefaultProfile, nil
	}
	if err := team.ValidateProfileName(name); err != nil {
		return "", fmt.Errorf("--profile: %w", err)
	}
	return name, nil
}
