package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// archiveDirName is the base-root subdirectory that `archive` MOVES sessions
// into. It is dot-prefixed so the status board (which skips dot-dirs) and the
// AMQ session scanners never treat archived sessions as live workstreams.
const archiveDirName = ".archive"

// rmMode is the one bit that separates the only two destructive verbs:
//   - rmModeDelete (rm):     permanently remove the session root + brief.
//   - rmModeArchive (archive): MOVE the session root (and brief) aside into
//     <baseRoot>/.archive/<session>/ instead of deleting.
type rmMode int

const (
	rmModeDelete rmMode = iota
	rmModeArchive
)

func (m rmMode) verb() string {
	if m == rmModeArchive {
		return "archive"
	}
	return "rm"
}

// rmExecution carries everything the destructive path needs, with every
// dangerous seam injectable so tests drive it deterministically: the base-root
// resolver, the liveness probe, the confirmation reader, and the writer.
//
// SAFETY CONTRACT (the whole point of this verb):
//   - The session name is validated through validateWorkstreamName so a
//     traversal or absolute path can never escape the base root.
//   - The target is filepath.Join(BaseRoot, session) AND is re-checked to be a
//     direct child of BaseRoot before a single byte is touched.
//   - A live agent in the session refuses the operation unless Force.
//   - Without Yes, the operator must confirm an explicit preview; the default
//     answer is NO, and declining makes ZERO filesystem changes.
type rmExecution struct {
	ProjectDir string
	Session    string
	Mode       rmMode
	Yes        bool
	Force      bool
	// ClosePanes closes the recorded tmux pane of each torn-down agent. rm/archive
	// default this ON (the session is going away); --keep-panes opts out. Panes of
	// agents still considered live are never closed (rm --force leaves them running)
	// UNLESS StopAgents is set.
	ClosePanes bool

	// StopAgents opts into a full teardown of a LIVE squad: gracefully stop the
	// live agents (SIGTERM via Terminator) and close their panes too, then remove
	// the session. It implies Force. Without it, rm --force removes session state
	// but leaves live agents running (and now says so).
	StopAgents bool
	// Terminator delivers the stop signal to live agents under StopAgents.
	// Defaults to a SIGTERM terminator; tests inject a recorder.
	Terminator processTerminator

	// BaseRoot, when set, is used verbatim and ResolveBaseRoot is NOT called.
	// Tests seed this; production leaves it empty and resolves once.
	BaseRoot        string
	ResolveBaseRoot func(projectDir string) (string, error)
	Profile         string

	// Probe drives liveness detection through internal/state. Tests inject a
	// deterministic probe; production uses state.DefaultProbe.
	Probe state.Probe

	// Confirm is the confirmation reader. Defaults to os.Stdin. Tests supply a
	// strings.Reader so y/n is deterministic without real stdin.
	Confirm io.Reader

	Out              io.Writer
	JSON             bool
	PaneDeps         PaneCleanupDependencies
	ManifestStore    paneCleanupManifestStore
	OperationID      string
	SnapshotPaneWork func(root string, tm team.Team, projectDir, profile, session, baseRoot string, requested bool) ([]rmPaneWork, error)
}

func runRm(args []string, mode rmMode) error {
	verb := mode.verb()
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	yes := fs.Bool("yes", false, "skip the confirmation prompt (for automation)")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	force := fs.Bool("force", false, "proceed even when the session has live agents (does NOT stop them; use --stop-agents for that)")
	stopAgents := fs.Bool("stop-agents", false, "stop the session's live agents (SIGTERM) and close their panes as part of teardown (implies --force)")
	keepPanes := fs.Bool("keep-panes", false, "do NOT close the torn-down agents' tmux panes (default: close them, since the session is being removed)")
	projectFlag := fs.String("project", "", "project/team-home directory to target (default: cwd)")
	sessionFlag := fs.String("session", "", "AMQ workstream session name to remove/archive")
	profileFlag := fs.String("profile", team.DefaultProfile, "team profile namespace to target (default: default profile)")
	jsonOut := fs.Bool("json", false, "emit machine-readable teardown results")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	fs.Usage = rmUsage(fs, mode)
	args = allowInterspersedFlags(fs, args)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session := strings.TrimSpace(*sessionFlag)
	if fs.NArg() == 0 && session == "" {
		return usageErrorf("%s requires a session name: %s <session>", verb, verb)
	}
	if fs.NArg() == 1 && session != "" {
		return usageErrorf("pass the session name either positionally or via --session, not both")
	}
	if fs.NArg() > 1 {
		return usageErrorf("%s takes exactly one session; got %d", verb, fs.NArg())
	}
	if session == "" {
		session = fs.Arg(0)
	}
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag, SessionFlag: session,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"), SessionExplicit: true,
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	return executeRm(rmExecution{
		ProjectDir: ctx.ProjectDir,
		Session:    ctx.Session,
		Mode:       mode,
		Yes:        *yes,
		Force:      *force || *stopAgents, // --stop-agents is a stronger "tear it down" intent
		ClosePanes: !*keepPanes,
		StopAgents: *stopAgents,
		Terminator: newSignalTerminator(false),
		Probe:      state.DefaultProbe,
		Confirm:    os.Stdin,
		Out:        os.Stdout,
		Profile:    ctx.Profile,
		JSON:       *jsonOut,
	})
}

func rmUsage(fs *flag.FlagSet, mode rmMode) func() {
	return func() {
		if mode == rmModeArchive {
			fmt.Fprint(os.Stderr, `amq-squad archive - move a finished session aside (non-destructive)

Usage:
  amq-squad archive <session> [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes] [--json]
  amq-squad archive --session NAME [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes] [--json]

Moves the session's AMQ root dir to <baseRoot>/.archive/<session>/ and moves
its brief alongside it as .archive/<session>/<session>.md. Nothing is deleted.
The session leaves the board but its mailboxes and brief are recoverable.
--project targets another team-home without changing directories.
--profile targets that profile's namespaced AMQ root and brief; default targets
the legacy/default profile root.

By default archive PREVIEWS exactly what will move and prompts for confirmation
(default: No). Declining makes zero filesystem changes. Pass --yes/-y to skip
the prompt for automation.

A session with any LIVE agent is refused unless --force. --force moves the
session aside but does NOT stop the agents (it leaves them running and names the
now-unmanaged panes). Pass --stop-agents (implies --force) to stop the live
agents and close their panes as part of the archive.
--keep-panes keeps pane cleanup not_requested; it does not suppress --stop-agents.
--json requires --yes and emits one machine-readable lifecycle result.

Examples:
  amq-squad archive issue-96
  amq-squad archive issue-96 --project ~/Code/app --yes
  amq-squad archive issue-96 --yes
  amq-squad archive issue-96 --force --yes
  amq-squad archive issue-96 --stop-agents --yes
`)
			return
		}
		fmt.Fprint(os.Stderr, `amq-squad rm - permanently remove a finished session

Usage:
  amq-squad rm <session> [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes] [--json]
  amq-squad rm --session NAME [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes] [--json]

Deletes the resolved session AMQ root and brief for the selected profile/session
namespace. This session-destructive verb is confined to that namespace: it never
touches a sibling session or anything outside that resolved root and brief.
--project targets another team-home without changing directories.
--profile targets that profile's namespaced AMQ root and brief; default targets
the legacy/default profile root.

By default rm PREVIEWS exactly what will be removed (the resolved paths + agent
count) and prompts for confirmation (default: No). Declining makes zero
filesystem changes. Pass --yes/-y to skip the prompt for automation. To keep the
data recoverable, use 'amq-squad archive <session>' instead.

A session with any LIVE agent is refused unless --force. --force removes the
session state but does NOT stop the agents: it leaves them running (and prints
which panes are now unmanaged). For a one-command full teardown, pass
--stop-agents (implies --force): it stops the live agents (SIGTERM) and closes
their panes before removing. The graceful two-step still works too:
'amq-squad stop --all [--session <session>] --force --close-panes' then rm.
--keep-panes keeps pane cleanup not_requested; it does not suppress --stop-agents.
--json requires --yes and emits one machine-readable lifecycle result.

Examples:
  amq-squad rm issue-96
  amq-squad rm issue-96 --project ~/Code/app --yes
  amq-squad rm issue-96 --yes
  amq-squad rm issue-96 --force --yes
  amq-squad rm issue-96 --stop-agents --yes   # stop live agents + close panes, then remove
`)
	}
}

// allowInterspersedFlags moves flags before positional arguments so small
// imperative commands like `amq-squad rm issue-96 --yes` work the way operators
// naturally type them while still using the stdlib flag parser for validation.
func allowInterspersedFlags(fs *flag.FlagSet, args []string) []string {
	var flags []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positional = append(positional, arg)
			continue
		}
		flags = append(flags, arg)
		name := flagName(arg)
		if name == "" || strings.Contains(arg, "=") {
			continue
		}
		f := fs.Lookup(name)
		if f == nil || isBoolFlag(f) {
			continue
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positional...)
}

func flagName(arg string) string {
	arg = strings.TrimLeft(arg, "-")
	if arg == "" {
		return ""
	}
	if name, _, ok := strings.Cut(arg, "="); ok {
		return name
	}
	return arg
}

type boolFlag interface {
	IsBoolFlag() bool
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(boolFlag)
	return ok && bf.IsBoolFlag()
}

// rmTarget is the fully resolved, safety-checked footprint of one session.
type rmTarget struct {
	Session    string
	BaseRoot   string
	Root       string // <baseRoot>/<session>
	RootExists bool
	Brief      string // brief path; "" when none could be resolved
	BriefHas   bool
	Agents     int // count of agent mailboxes under <root>/agents
	// Prepared and Generations are the accepted-preparation state for this
	// namespace (#598). Teardown used to leave both behind while printing
	// "session removed", so the next launch loaded a stale accepted preview,
	// compared it against a brief that no longer existed, and drifted
	// permanently. That is what turned a failed launch into a bricked
	// namespace, so removing them is part of removing the session.
	Prepared       string // .amq-squad/prepared/<profile>/<session>.json
	PreparedHas    bool
	Generations    string // .amq-squad/prepared/<profile>/<session>.generations
	GenerationsHas bool
}

// hasPreparedState reports whether any accepted-preparation artifact survives
// for this namespace. Teardown must treat these as removable state in their own
// right: after a failed launch the AMQ root and brief can already be gone while
// the prepared manifest remains, and that orphan alone is enough to brick every
// subsequent launch.
func (t rmTarget) hasPreparedState() bool {
	return t.PreparedHas || t.GenerationsHas
}

func executeRm(e rmExecution) error {
	_, err := executeRmReportDeclined(e)
	return err
}

// executeRmReportDeclined is executeRm's body, additionally reporting whether
// the operator declined the confirmation gate (which, like executeRm, makes
// ZERO filesystem changes and returns a nil error). `up --reset` reuses this
// so it can cancel the whole launch on a decline rather than proceeding to
// launch into the session the operator just refused to clear.
func executeRmReportDeclined(e rmExecution) (bool, error) {
	verb := e.Mode.verb()
	out := e.Out
	if out == nil {
		out = os.Stdout
	}
	// --stop-agents is a stronger "tear it down" intent than --force, so it
	// implies Force. Normalize here (not just at the flag layer) so a direct
	// executeRm caller can't set StopAgents without Force and trip the
	// live-session refusal below.
	if e.StopAgents {
		e.Force = true
	}
	if e.JSON && !e.Yes {
		return false, usageErrorf("--json requires --yes so stdout remains one machine-readable result")
	}

	// SAFETY 1: validate the session name BEFORE it is ever joined into a path,
	// so a traversal ("../foo"), an absolute path, or a name with separators is
	// rejected outright and can never escape the base root.
	session := strings.TrimSpace(e.Session)
	if err := validateWorkstreamName(session); err != nil {
		return false, err
	}

	resolve := e.ResolveBaseRoot
	if resolve == nil {
		resolve = scanBaseRootForProject
	}
	profile := strings.TrimSpace(e.Profile)
	if profile == "" {
		profile = team.DefaultProfile
	}
	if profile != team.DefaultProfile {
		if err := team.ValidateProfileName(profile); err != nil {
			return false, err
		}
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(e.ProjectDir, profile, session), "")
	if err != nil {
		return false, err
	}
	admission, err := acquireNamespaceWriterAdmission(e.ProjectDir, profile, session)
	if err != nil {
		return false, err
	}
	defer admission.close()
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(e.ProjectDir, profile, session), "")
	if err != nil {
		return false, err
	}
	if err := validateReResolvedEndpointIdentity(verb, initialIdentity, currentIdentity); err != nil {
		return false, err
	}
	if err := ensureNoNamespaceMigration(verb, e.ProjectDir, profile, session); err != nil {
		return false, err
	}
	baseRoot := e.BaseRoot
	if baseRoot == "" {
		var err error
		baseRoot, err = resolve(e.ProjectDir)
		if err != nil {
			return false, fmt.Errorf("resolve AMQ base root: %w", err)
		}
		if profile != team.DefaultProfile {
			baseRoot = filepath.Join(baseRoot, profile)
		}
	}
	if strings.TrimSpace(baseRoot) == "" {
		return false, fmt.Errorf("resolved AMQ base root is empty; nothing to %s", verb)
	}
	baseRoot = filepath.Clean(baseRoot)

	root := filepath.Join(baseRoot, session)
	// SAFETY 2: the target must be a direct LEXICAL child of the base root.
	//
	// Read what this actually provides, because the previous comment here
	// claimed more than the code delivers and that overclaim is itself the
	// defect class this release is about. Dir(Join(baseRoot, session)) IS
	// baseRoot whenever session contains no separator, which
	// validateWorkstreamName already guarantees, so both clauses below are
	// vacuous. They are a cheap restatement of validation, NOT an independent
	// check, and they would not catch a future change that let a separator
	// through in a form Join collapses.
	//
	// They also prove nothing about SYMLINKS. If baseRoot or an intermediate
	// component resolves outside the project, the RemoveAll and Rename below
	// follow it, and the blast radius here is the whole AMQ root: every agent
	// mailbox for the session. That hole is tracked in #606 and is NOT fixed
	// here; see resolvePreparedRunTeardown in this file for the resolved-path
	// containment it needs, applied to the prepared tree.
	//
	// Comment corrected only. Behavior is unchanged.
	if filepath.Dir(root) != baseRoot || filepath.Base(root) != session {
		return false, fmt.Errorf("refusing to %s: resolved path %q is not a direct child of base root %q", verb, root, baseRoot)
	}

	target := rmTarget{
		Session:  session,
		BaseRoot: baseRoot,
		Root:     root,
		Brief:    briefPathForProfile(e.ProjectDir, profile, session),
	}
	if fi, err := os.Stat(root); err == nil && fi.IsDir() {
		target.RootExists = true
		target.Agents = countAgentMailboxes(root)
	} else if err == nil && !fi.IsDir() {
		return false, fmt.Errorf("refusing to %s: %q exists but is not a directory", verb, root)
	}
	if target.Brief != "" {
		if _, err := os.Stat(target.Brief); err == nil {
			target.BriefHas = true
		}
	}
	if err := resolvePreparedRunTeardown(&target, verb, e.ProjectDir, profile, session); err != nil {
		return false, err
	}

	// SAFETY 5: nothing to remove is a clean error, never a panic.
	//
	// Prepared state counts (#598). An orphaned prepared manifest with no root
	// and no brief is exactly the bricked state operators land in, and the old
	// check refused it as "nothing to remove" -- a refusal that pointed at
	// nothing, from the one verb able to clear it.
	if !target.RootExists && !target.BriefHas && !target.hasPreparedState() {
		return false, fmt.Errorf("%s: session %q has no AMQ root, brief, or prepared launch state under %s; nothing to remove", verb, session, baseRoot)
	}

	// SAFETY 3: refuse a running session unless --force. Reuse the repo's
	// liveness (internal/state) so this agrees with status/down about "live".
	liveSet := map[string]bool{}
	if target.RootExists {
		live, mailboxWindow, err := liveAgentsInSession(e.ProjectDir, baseRoot, session, e.Probe)
		if err != nil {
			return false, fmt.Errorf("check liveness for session %q: %w", session, err)
		}
		for _, h := range live {
			liveSet[h] = true
		}
		if len(live) > 0 && !e.Force {
			msg := fmt.Sprintf("session %q has live agents (%s); stop it first with 'amq-squad stop --all --session %s --force', or pass --force to %s anyway",
				session, strings.Join(live, ", "), session, verb)
			if mailboxWindow > 0 {
				// Some refusing agents are only "live" via a fresh presence
				// write, not a verified process. Tell the operator the window
				// so waiting is a known option, not folklore.
				display := mailboxWindow.Round(time.Second)
				if display < time.Second {
					display = time.Second // never render a confusing "~0s"
				}
				msg += fmt.Sprintf(" (some presence files were written within the %s freshness window; it clears in ~%s)",
					state.PresenceFreshness, display)
			}
			return false, errors.New(msg)
		}
	}

	// PREVIEW: list exactly what will be removed/moved for interactive use.
	if !e.JSON {
		renderRmPreview(out, e.Mode, target)
	}

	// SAFETY 2 (confirm gate): default is NO. Declining makes ZERO changes.
	if !e.Yes {
		if !confirmRm(out, e.Confirm, session) {
			fmt.Fprintf(out, "%s: aborted; no changes made.\n", verb)
			return true, nil
		}
	}

	// Resolve the LIVE agents' records (pid + pane) BEFORE the root is removed —
	// the launch records live under it. Needed to stop them (--stop-agents) and to
	// name their now-unmanaged panes in the notice otherwise.
	var liveAgents []sessionAgent
	if target.RootExists && len(liveSet) > 0 {
		liveAgents = liveSessionAgents(target.Root, liveSet)
	}

	var watcherTeam team.Team
	watcherTeam, watcherTeamErr := team.ReadProfile(e.ProjectDir, profile)
	watcherStatus := notificationWatcherStatus{Health: "disabled"}
	watcherWasActive := false
	watcherManaged := watcherTeamErr == nil && team.EffectiveOperatorNotifications(watcherTeam.Operator).Enabled
	if watcherTeamErr == nil {
		watcherStatus = inspectNotificationWatcher(watcherTeam, profile, session, notificationWatcherNow())
		watcherWasActive = watcherStatus.record.Expected && watcherStatus.record.OwnerToken != "" && notificationWatcherNow().Before(watcherStatus.record.LeaseExpiresAt)
	}
	manifestTeam := watcherTeam
	if watcherTeamErr != nil {
		manifestTeam = team.Team{Project: e.ProjectDir}
	}
	var paneWork []rmPaneWork
	if target.RootExists {
		snapshot := e.SnapshotPaneWork
		if snapshot == nil {
			snapshot = snapshotRmPaneWork
		}
		paneWork, err = snapshot(target.Root, manifestTeam, e.ProjectDir, profile, session, baseRoot, e.ClosePanes)
		if err != nil {
			return false, err
		}
	}
	operationID := strings.TrimSpace(e.OperationID)
	if operationID == "" {
		operationID, err = newPaneCleanupOperationID()
		if err != nil {
			return false, err
		}
	}
	manifestStore := e.ManifestStore
	if manifestStore == nil {
		manifestStore = filesystemPaneCleanupManifestStore{}
	}
	createdAt := time.Now().UTC()
	preparedManifest := paneCleanupManifest{Schema: paneCleanupManifestSchema, OperationID: operationID, Operation: verb,
		Phase: paneCleanupManifestPrepared, Project: e.ProjectDir, Profile: profile, Session: session, CreatedAt: createdAt,
		Entries: plannedRmManifestEntries(paneWork)}
	manifestHandle, err := manifestStore.Prepare(e.ProjectDir, preparedManifest)
	if err != nil {
		return false, paneManifestPrepareError(err)
	}
	if !e.JSON {
		fmt.Fprintf(out, "pane cleanup prepared manifest: %s\n", manifestHandle.Prepared)
	}
	if watcherManaged || watcherWasActive {
		if err := stopNotificationWatcher(e.ProjectDir, profile, session); err != nil {
			return false, fmt.Errorf("refusing to %s before notification watcher is stopped: %w", verb, err)
		}
	}
	// Only after watcher fencing succeeds may --stop-agents signal members. A
	// remote or ambiguous watcher therefore refuses the whole destructive
	// lifecycle before either agent or namespace mutation.
	attestAndStopRmAgents(paneWork, liveSet, e.StopAgents, e.Terminator, e.Probe, e.PaneDeps)
	restartWatcherAfterFailure := func(mutationErr error) error {
		if !watcherWasActive || watcherTeamErr != nil {
			return mutationErr
		}
		if info, statErr := os.Stat(target.Root); statErr != nil || !info.IsDir() {
			return fmt.Errorf("%w; notification watcher was stopped and namespace is no longer restartable", mutationErr)
		}
		if restartErr := reconcileNotificationWatcherStarted(watcherTeam, profile, session, baseRoot); restartErr != nil {
			return fmt.Errorf("%w; notification watcher rollback failed: %v", mutationErr, restartErr)
		}
		return mutationErr
	}

	mutationOut := out
	if e.JSON {
		mutationOut = io.Discard
	}
	mutationStatus := "succeeded"
	var mutationErr error
	if e.Mode == rmModeArchive {
		mutationErr = archiveSession(mutationOut, target)
	} else {
		mutationErr = deleteSession(mutationOut, target)
	}
	if mutationErr == nil {
		closePreparedRmPanes(paneWork, e.PaneDeps)
	} else {
		mutationStatus = "failed: " + mutationErr.Error()
		preservePreparedRmPanes(paneWork, "namespace mutation failed; prepared pane was deliberately preserved")
	}
	finalManifest := manifestHandle.PreparedManifest
	finalManifest.Phase = paneCleanupManifestFinalized
	finalManifest.PreparedSHA256 = manifestHandle.PreparedSHA256
	finalManifest.NamespaceMutation = mutationStatus
	finalManifest.FinalizedAt = time.Now().UTC()
	finalManifest.Entries = finalRmManifestEntries(paneWork)
	finalizeErr := manifestStore.Finalize(manifestHandle, finalManifest)
	finalizationStatus := "succeeded"
	finalManifestPath := manifestHandle.Final
	finalCandidate := ""
	if finalizeErr != nil {
		finalizationStatus = "failed: " + finalizeErr.Error()
		finalManifestPath = ""
		finalCandidate = manifestHandle.Final
	}
	emitResult := func() error {
		if e.JSON {
			return writeJSONEnvelope(out, verb, rmCleanupEnvelopeData{
				Project: manifestHandle.Project, Profile: profile, Session: session, Root: target.Root, Operation: verb,
				PreparedManifest: manifestHandle.Prepared, FinalManifest: finalManifestPath, FinalCandidate: finalCandidate,
				NamespaceMutation: mutationStatus, Finalization: finalizationStatus,
				Reports: rmCleanupJSONReports(paneWork), Summary: summarizeRmPaneWork(paneWork),
			})
		}
		if finalizeErr == nil {
			fmt.Fprintf(out, "pane cleanup final manifest: %s\n", manifestHandle.Final)
		} else {
			fmt.Fprintf(out, "pane cleanup finalization: %s\n", finalizationStatus)
			fmt.Fprintf(out, "prepared evidence retained: %s\n", manifestHandle.Prepared)
			fmt.Fprintf(out, "final manifest candidate (durability uncertain): %s\n", manifestHandle.Final)
		}
		renderRmPaneResults(out, paneWork)
		return nil
	}
	if emitErr := emitResult(); emitErr != nil {
		if mutationErr != nil || finalizeErr != nil {
			return false, &PartialError{Message: fmt.Sprintf("%s result reporting failed after lifecycle mutation", verb), Cause: errors.Join(mutationErr, finalizeErr, emitErr)}
		}
		return false, emitErr
	}
	if mutationErr != nil {
		mutationErr = restartWatcherAfterFailure(mutationErr)
		if finalizeErr != nil {
			return false, &PartialError{Message: fmt.Sprintf("%s failed and pane cleanup finalization was uncertain; prepared evidence retained at %s", verb, manifestHandle.Prepared), Cause: errors.Join(mutationErr, finalizeErr)}
		}
		if stoppedRmAgentCount(paneWork) > 0 {
			return false, &PartialError{Message: fmt.Sprintf("%s namespace mutation failed after %d agent(s) were signaled; prepared evidence retained at %s", verb, stoppedRmAgentCount(paneWork), manifestHandle.Prepared), Cause: mutationErr}
		}
		return false, mutationErr
	}
	if finalizeErr != nil {
		return false, paneManifestFinalizePartial(manifestHandle, finalizeErr)
	}

	// Without --stop-agents, this verb removed/moved the session state but
	// deliberately left live agents running (it does not stop agents). That used
	// to be SILENT; name the now-unmanaged panes and how to finish the teardown.
	if len(liveAgents) > 0 && !e.StopAgents {
		if !e.JSON {
			notifyLiveAgentsLeftRunning(out, verb, liveAgents, manifestHandle.Prepared)
		}
	}
	if partial := rmPanePartial(paneWork); partial > 0 {
		return false, &PartialError{Message: fmt.Sprintf("%s: namespace mutation succeeded but %d requested pane cleanup(s) were preserved or failed", verb, partial)}
	}
	return false, nil
}

// sessionAgent is a live agent's recorded identity (handle + agent pid + pane),
// read from its launch record under the session root. Used by --stop-agents to
// terminate it and by the "left running" notice to name its unmanaged pane.
type sessionAgent struct {
	Handle   string
	PID      int
	PaneID   string
	External bool
}

// liveSessionAgents reads the recorded agent pid and pane id for each handle in
// liveSet from its mailbox under <root>/agents. Handles with a missing/unreadable
// record are skipped. Must be called BEFORE the root is removed.
func liveSessionAgents(root string, liveSet map[string]bool) []sessionAgent {
	var out []sessionAgent
	for handle := range liveSet {
		rec, err := launch.Read(filepath.Join(root, "agents", handle))
		if err != nil {
			continue
		}
		sa := sessionAgent{Handle: handle, PID: rec.AgentPID, External: recordIsExternal(rec)}
		if rec.Tmux != nil {
			sa.PaneID = strings.TrimSpace(rec.Tmux.PaneID)
		}
		out = append(out, sa)
	}
	return out
}

// notifyLiveAgentsLeftRunning warns that a teardown removed/moved the session
// state but deliberately left live agents running. Exact pane IDs are recovery
// evidence, not standalone authority to mutate a pane after namespace removal.
func notifyLiveAgentsLeftRunning(out io.Writer, verb string, agents []sessionAgent, preparedManifest string) {
	handles := make([]string, 0, len(agents))
	var panes []string
	var external []string
	for _, a := range agents {
		handles = append(handles, a.Handle)
		if a.External {
			external = append(external, a.Handle)
			continue
		}
		if a.PaneID != "" {
			panes = append(panes, a.PaneID)
		}
	}
	fmt.Fprintf(out, "\nNote: %d live agent(s) left RUNNING (%s --force removes session state but does not stop agents): %s\n",
		len(agents), verb, strings.Join(handles, ", "))
	if len(panes) > 0 {
		fmt.Fprintf(out, "  unmanaged recorded pane id(s): %s\n", strings.Join(panes, ", "))
		fmt.Fprintf(out, "  inspect retained identity evidence at %s, re-attest the exact pane id, then close only that exact id\n", preparedManifest)
	}
	if len(external) > 0 {
		fmt.Fprintf(out, "  external pane(s) are operator-owned and were left open: %s\n", strings.Join(external, ", "))
	}
	fmt.Fprintln(out, "  no title-based or pane-pruning fallback is safe for this recovery")
}

// liveAgentsInSession returns the handles of agents the repo's liveness
// classifier considers operational (alive, wake-live, or dead-mailbox-live) in
// the named session. An empty slice means the session is safe to tear down.
//
// The second return is the longest remaining presence-freshness window among
// the dead-mailbox-live agents (zero when none): how long until their fresh
// presence writes expire and they stop counting as live. The refusal message
// uses it so the operator knows waiting is an option (#109).
func liveAgentsInSession(projectDir, baseRoot, session string, probe state.Probe) ([]string, time.Duration, error) {
	snap, err := state.Build(projectDir, baseRoot, probe)
	if err != nil {
		return nil, 0, err
	}
	var live []string
	var mailboxWindow time.Duration
	for _, sess := range snap.Sessions {
		if sess.Name != session {
			continue
		}
		for _, a := range sess.Agents {
			switch a.Liveness {
			case state.LivenessAlive, state.LivenessWakeLive:
				live = append(live, a.Handle)
			case state.LivenessDeadMailboxLive:
				live = append(live, a.Handle)
				if !a.LastSeen.IsZero() {
					if rem := state.PresenceFreshness - probe.Now().Sub(a.LastSeen); rem > mailboxWindow {
						mailboxWindow = rem
					}
				}
			}
		}
	}
	return live, mailboxWindow, nil
}

// countAgentMailboxes counts the agent subdirectories under <root>/agents so
// the preview can report how many agents a session footprint covers, even when
// no launch record exists (e.g. a session that only has mailboxes + a brief).
func countAgentMailboxes(root string) int {
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		return 0
	}
	n := 0
	for _, ent := range entries {
		if ent.IsDir() {
			n++
		}
	}
	return n
}

// resolvePreparedRunTeardown fills in the prepared-manifest paths for a
// teardown target and proves each one is confined to this exact
// profile/session before teardown is allowed to touch it.
//
// The containment check mirrors SAFETY 2 on the AMQ root rather than trusting
// name validation alone: removing session X must be provably incapable of
// touching session Y's accepted preparation, and the prepared tree is shared
// by every session in the profile, so a single bad join here would be a
// cross-session data-loss bug rather than a local one.
func resolvePreparedRunTeardown(target *rmTarget, verb, project, profile, session string) error {
	manifest := preparedRunPath(project, profile, session)
	generations := preparedRunGenerationsPath(project, profile, session)
	preparedDir := filepath.Dir(manifest)

	// Lexical shape first. These are a cheap gate, NOT the containment proof.
	//
	// The previous version of this function stopped here and compared
	// filepath.Dir(path) against filepath.Dir(manifest), which is the same
	// value by construction, so the check was tautological and proved nothing.
	// A reviewer built the case that breaks it: make
	// .amq-squad/prepared/<profile> a symlink to an external directory and the
	// lexical paths still look project-local while the real files are outside.
	// deleteSession would then RemoveAll state that does not belong to this
	// project. Containment is therefore proven on RESOLVED paths below.
	if filepath.Base(manifest) != session+".json" {
		return fmt.Errorf("refusing to %s: prepared manifest %q does not belong to session %q", verb, manifest, session)
	}
	if filepath.Base(generations) != session+".generations" {
		return fmt.Errorf("refusing to %s: prepared generation state %q does not belong to session %q", verb, generations, session)
	}

	// Resolved containment. Every failure direction refuses, matching this
	// verb's existing conservative posture: a teardown that cannot PROVE what
	// it is about to delete must not delete it.
	resolvedProject, err := filepath.EvalSymlinks(project)
	if err != nil {
		return fmt.Errorf("refusing to %s: cannot resolve project directory %q: %w", verb, project, err)
	}
	resolvedPrepared, err := filepath.EvalSymlinks(preparedDir)
	switch {
	case os.IsNotExist(err):
		// No prepared tree at all. Nothing to contain and nothing to remove;
		// the stat probes below will simply find neither artifact.
		target.Prepared = manifest
		target.Generations = generations
		return nil
	case err != nil:
		return fmt.Errorf("refusing to %s: cannot resolve prepared directory %q: %w", verb, preparedDir, err)
	}
	// The prepared directory must resolve to its CANONICAL location, not merely
	// to somewhere inside the project.
	//
	// "Inside the project" was the second wrong question here. A reviewer
	// redirected .amq-squad/prepared/<profile> at the project ROOT and at an
	// unrelated in-project directory; both stayed within the project and both
	// made same-named victim.json / victim.generations belonging to something
	// else removable. Containment by ancestry cannot express the actual
	// invariant, which is identity: this directory IS the prepared namespace
	// for this profile, or teardown does not touch it.
	//
	// Equality against the canonical path rejects every redirect in one rule
	// rather than enumerating bad destinations, and it means a symlinked
	// prepared directory is refused even when the target looks harmless. That
	// is deliberate: the operator can move real state into place, and a
	// destructive verb should not be the thing that follows an indirection it
	// cannot justify.
	canonicalPrepared := filepath.Join(resolvedProject, team.DirName, "prepared", squadnamespace.NormalizeProfile(profile))
	if resolvedPrepared != filepath.Clean(canonicalPrepared) {
		return fmt.Errorf("refusing to %s: prepared directory %q resolves to %q, which is not the canonical prepared namespace %q; refusing to remove state that is not this namespace's prepared state", verb, preparedDir, resolvedPrepared, filepath.Clean(canonicalPrepared))
	}
	for label, path := range map[string]string{
		"prepared manifest":         manifest,
		"prepared generation state": generations,
	} {
		resolved, resolveErr := filepath.EvalSymlinks(path)
		if os.IsNotExist(resolveErr) {
			continue // absent artifacts are handled by the stat probes below
		}
		if resolveErr != nil {
			return fmt.Errorf("refusing to %s: cannot resolve %s %q: %w", verb, label, path, resolveErr)
		}
		if !pathWithinResolvedRoot(resolvedPrepared, resolved) {
			return fmt.Errorf("refusing to %s: %s %q resolves to %q, which escapes the prepared directory %q", verb, label, path, resolved, resolvedPrepared)
		}
	}
	target.Prepared = manifest
	target.Generations = generations
	if fi, err := os.Stat(manifest); err == nil {
		if fi.IsDir() {
			return fmt.Errorf("refusing to %s: %q exists but is a directory", verb, manifest)
		}
		target.PreparedHas = true
	}
	if fi, err := os.Stat(generations); err == nil {
		if !fi.IsDir() {
			return fmt.Errorf("refusing to %s: %q exists but is not a directory", verb, generations)
		}
		target.GenerationsHas = true
	}
	return nil
}

func renderRmPreview(out io.Writer, mode rmMode, t rmTarget) {
	if mode == rmModeArchive {
		fmt.Fprintf(out, "# amq-squad archive — preview\n")
	} else {
		fmt.Fprintf(out, "# amq-squad rm — preview\n")
	}
	fmt.Fprintf(out, "# session:  %s\n", t.Session)
	fmt.Fprintf(out, "# agents:   %d\n", t.Agents)
	fmt.Fprintln(out)
	action := "DELETE"
	if mode == rmModeArchive {
		action = "MOVE"
		dest := filepath.Join(t.BaseRoot, archiveDirName, t.Session)
		if t.RootExists {
			fmt.Fprintf(out, "  %s  %s\n", action, t.Root)
			fmt.Fprintf(out, "      -> %s\n", dest)
		}
		if t.BriefHas {
			fmt.Fprintf(out, "  %s  %s\n", action, t.Brief)
			fmt.Fprintf(out, "      -> %s\n", filepath.Join(dest, t.Session+".md"))
		}
		if t.PreparedHas {
			fmt.Fprintf(out, "  %s  %s\n", action, t.Prepared)
			fmt.Fprintf(out, "      -> %s\n", filepath.Join(dest, filepath.Base(t.Prepared)))
		}
		if t.GenerationsHas {
			fmt.Fprintf(out, "  %s  %s\n", action, t.Generations)
			fmt.Fprintf(out, "      -> %s\n", filepath.Join(dest, filepath.Base(t.Generations)))
		}
		return
	}
	if t.RootExists {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Root)
	}
	if t.BriefHas {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Brief)
	}
	if t.PreparedHas {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Prepared)
	}
	if t.GenerationsHas {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Generations)
	}
}

// confirmRm prompts and reads a single y/N answer. The default is NO: any
// answer that is not an explicit yes (y / yes, case-insensitive) declines, and
// EOF / empty input declines too. This is intentionally strict — an rm that
// proceeds on a stray keypress is a defect.
func confirmRm(out io.Writer, r io.Reader, session string) bool {
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprintf(out, "Remove session %s? [y/N] ", session)
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	return answer == "y" || answer == "yes"
}

func deleteSession(out io.Writer, t rmTarget) error {
	if t.RootExists {
		if err := os.RemoveAll(t.Root); err != nil {
			return fmt.Errorf("remove session root %q: %w", t.Root, err)
		}
		fmt.Fprintf(out, "removed %s\n", t.Root)
	}
	if t.BriefHas {
		if err := os.Remove(t.Brief); err != nil {
			return fmt.Errorf("remove brief %q: %w", t.Brief, err)
		}
		fmt.Fprintf(out, "removed %s\n", t.Brief)
	}
	// #598: the accepted preparation is part of the session. Leaving it behind
	// while printing "session removed" is what made a failed launch
	// unrecoverable instead of merely annoying.
	if t.PreparedHas {
		if err := os.Remove(t.Prepared); err != nil {
			return fmt.Errorf("remove prepared manifest %q: %w", t.Prepared, err)
		}
		fmt.Fprintf(out, "removed %s\n", t.Prepared)
	}
	if t.GenerationsHas {
		if err := os.RemoveAll(t.Generations); err != nil {
			return fmt.Errorf("remove prepared generation state %q: %w", t.Generations, err)
		}
		fmt.Fprintf(out, "removed %s\n", t.Generations)
	}
	fmt.Fprintf(out, "rm: session %s removed.\n", t.Session)
	return nil
}

func archiveSession(out io.Writer, t rmTarget) error {
	dest := filepath.Join(t.BaseRoot, archiveDirName, t.Session)
	if err := os.MkdirAll(filepath.Join(t.BaseRoot, archiveDirName), 0o755); err != nil {
		return fmt.Errorf("create archive dir: %w", err)
	}
	// Refuse to clobber an existing archive entry: silently overwriting a prior
	// archive of the same session would be its own data-loss defect.
	if _, err := os.Stat(dest); err == nil {
		return fmt.Errorf("archive: %q already exists; remove it first or pick a different session name", dest)
	}
	if t.RootExists {
		if err := os.Rename(t.Root, dest); err != nil {
			return fmt.Errorf("archive session root %q: %w", t.Root, err)
		}
		fmt.Fprintf(out, "moved %s -> %s\n", t.Root, dest)
	}
	if t.BriefHas {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("create archive dir: %w", err)
		}
		briefDest := filepath.Join(dest, t.Session+".md")
		if err := os.Rename(t.Brief, briefDest); err != nil {
			return fmt.Errorf("archive brief %q: %w", t.Brief, err)
		}
		fmt.Fprintf(out, "moved %s -> %s\n", t.Brief, briefDest)
	}
	// #598: archive moves the same prepared state rm deletes. Leaving it in
	// place would archive a session while keeping the accepted preview that
	// bricks the next launch under the same name -- the identical defect, just
	// reached by the other verb.
	for _, item := range []struct {
		has   bool
		src   string
		label string
	}{
		{t.PreparedHas, t.Prepared, "prepared manifest"},
		{t.GenerationsHas, t.Generations, "prepared generation state"},
	} {
		if !item.has {
			continue
		}
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return fmt.Errorf("create archive dir: %w", err)
		}
		itemDest := filepath.Join(dest, filepath.Base(item.src))
		if err := os.Rename(item.src, itemDest); err != nil {
			return fmt.Errorf("archive %s %q: %w", item.label, item.src, err)
		}
		fmt.Fprintf(out, "moved %s -> %s\n", item.src, itemDest)
	}
	fmt.Fprintf(out, "archive: session %s moved to %s.\n", t.Session, dest)
	return nil
}

// pathWithinResolvedRoot reports whether an ALREADY-RESOLVED path is the
// resolved root or lies beneath it.
//
// Both arguments must have been through filepath.EvalSymlinks. Passing lexical
// paths here would reintroduce exactly the defect this exists to prevent, which
// is why the parameter names say resolved.
//
// The separator suffix matters: a plain strings.HasPrefix would accept
// "/tmp/project-evil" as being inside "/tmp/project".
func pathWithinResolvedRoot(resolvedRoot, resolvedPath string) bool {
	resolvedRoot = filepath.Clean(resolvedRoot)
	resolvedPath = filepath.Clean(resolvedPath)
	if resolvedPath == resolvedRoot {
		return true
	}
	return strings.HasPrefix(resolvedPath, resolvedRoot+string(os.PathSeparator))
}
