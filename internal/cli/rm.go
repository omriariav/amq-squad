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

	Out io.Writer
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
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	return executeRm(rmExecution{
		ProjectDir: projectDir,
		Session:    session,
		Mode:       mode,
		Yes:        *yes,
		Force:      *force || *stopAgents, // --stop-agents is a stronger "tear it down" intent
		ClosePanes: !*keepPanes,
		StopAgents: *stopAgents,
		Terminator: newSignalTerminator(false),
		Probe:      state.DefaultProbe,
		Confirm:    os.Stdin,
		Out:        os.Stdout,
		Profile:    profile,
	})
}

func rmUsage(fs *flag.FlagSet, mode rmMode) func() {
	return func() {
		if mode == rmModeArchive {
			fmt.Fprint(os.Stderr, `amq-squad archive - move a finished session aside (non-destructive)

Usage:
  amq-squad archive <session> [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes]
  amq-squad archive --session NAME [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes]

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
  amq-squad rm <session> [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes]
  amq-squad rm --session NAME [--project DIR] [--profile NAME] [--yes|-y] [--force] [--stop-agents] [--keep-panes]

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
	// SAFETY 2 (highest-risk property): the target MUST resolve to a direct
	// child of the base root. A validated session name already forbids
	// separators, but we re-derive and compare independently so a future change
	// to validation can never silently re-open an escape. Deleting session X
	// must be provably incapable of touching session Y or anything outside
	// <baseRoot>/<session>/.
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

	// SAFETY 5: nothing to remove is a clean error, never a panic.
	if !target.RootExists && !target.BriefHas {
		return false, fmt.Errorf("%s: session %q has no AMQ root or brief under %s; nothing to remove", verb, session, baseRoot)
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

	// PREVIEW: list exactly what will be removed/moved, every time.
	renderRmPreview(out, e.Mode, target)

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
	// --stop-agents: full teardown of a live squad. Gracefully stop the live
	// agents now (SIGTERM); their panes are closed below with the non-live ones.
	if e.StopAgents && len(liveAgents) > 0 {
		stopLiveSessionAgents(out, liveAgents, e.Terminator)
	}

	// Collect the panes to close BEFORE the root is moved/removed (the launch
	// records live under it). Live agents are excluded by default so rm --force
	// never kills a still-running agent's pane; --stop-agents closes their
	// (now-stopped) panes too. Each pane is identity-checked at close time so a
	// reused pane id never closes the wrong pane.
	var panesToClose []recordedPane
	if e.ClosePanes && target.RootExists {
		exclude := liveSet
		if e.StopAgents {
			exclude = nil
		}
		panesToClose = collectSessionPaneIDs(target.Root, exclude)
	}

	if e.Mode == rmModeArchive {
		if err := archiveSession(out, target); err != nil {
			return false, err
		}
	} else if err := deleteSession(out, target); err != nil {
		return false, err
	}
	closeSessionPanes(out, session, panesToClose)

	// Without --stop-agents, this verb removed/moved the session state but
	// deliberately left live agents running (it does not stop agents). That used
	// to be SILENT; name the now-unmanaged panes and how to finish the teardown.
	if len(liveAgents) > 0 && !e.StopAgents {
		notifyLiveAgentsLeftRunning(out, verb, liveAgents)
	}
	return false, nil
}

// collectSessionPaneIDs reads the recorded tmux pane id of every agent mailbox
// under <root>/agents, skipping any handle in excludeLive. It is called BEFORE
// the session root is moved/removed, since the launch records live under it.
// recordedPane is a pane to close, carried with the identity fields the safe
// close needs to confirm it was not reused by a different agent.
type recordedPane struct {
	PaneID string
	Role   string
	CWD    string
}

func collectSessionPaneIDs(root string, excludeLive map[string]bool) []recordedPane {
	entries, err := os.ReadDir(filepath.Join(root, "agents"))
	if err != nil {
		return nil
	}
	var panes []recordedPane
	for _, ent := range entries {
		if !ent.IsDir() || excludeLive[ent.Name()] {
			continue
		}
		rec, err := launch.Read(filepath.Join(root, "agents", ent.Name()))
		if err != nil || rec.Tmux == nil {
			continue
		}
		if rec.External {
			continue
		}
		if id := strings.TrimSpace(rec.Tmux.PaneID); id != "" {
			panes = append(panes, recordedPane{PaneID: id, Role: rec.Role, CWD: rec.CWD})
		}
	}
	return panes
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
		sa := sessionAgent{Handle: handle, PID: rec.AgentPID, External: rec.External}
		if rec.Tmux != nil {
			sa.PaneID = strings.TrimSpace(rec.Tmux.PaneID)
		}
		out = append(out, sa)
	}
	return out
}

// stopLiveSessionAgents gracefully terminates each live agent's recorded process
// (SIGTERM via the terminator) — the first half of --stop-agents' teardown; the
// caller then closes the panes, which guarantees anything still running is gone.
// Best-effort: a terminate error (already exited / gone) is swallowed.
func stopLiveSessionAgents(out io.Writer, agents []sessionAgent, term processTerminator) {
	if term == nil {
		term = newSignalTerminator(false)
	}
	for _, a := range agents {
		if a.External {
			continue
		}
		if a.PID <= 0 {
			continue
		}
		if err := term.Terminate(a.PID); err == nil {
			fmt.Fprintf(out, "stopped agent %s (pid %d, %s)\n", a.Handle, a.PID, term.SignalName())
		}
	}
}

// notifyLiveAgentsLeftRunning warns that a teardown removed/moved the session
// state but deliberately left live agents running (rm/archive never stop
// agents). It names the now-unmanaged panes and the two ways to finish: a direct
// kill-pane, or re-running with --stop-agents. Removing the SILENCE is the fix;
// the kill-semantics are intentionally unchanged.
func notifyLiveAgentsLeftRunning(out io.Writer, verb string, agents []sessionAgent) {
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
		cmds := make([]string, 0, len(panes))
		for _, p := range panes {
			cmds = append(cmds, "tmux kill-pane -t "+p)
		}
		fmt.Fprintf(out, "  their panes are now unmanaged; close them with:  %s\n", strings.Join(cmds, " ; "))
	}
	if len(external) > 0 {
		fmt.Fprintf(out, "  external pane(s) are operator-owned and were left open: %s\n", strings.Join(external, ", "))
	}
	fmt.Fprintf(out, "  or re-run with --stop-agents to stop them and close their panes as part of teardown.\n")
}

// closeSessionPanes best-effort closes each recorded pane (kill-pane) and notes
// it. A kill error (e.g. the pane is already gone) is swallowed; teardown has
// already succeeded on disk and must not be reported as failed.
func closeSessionPanes(out io.Writer, session string, panes []recordedPane) {
	for _, p := range panes {
		closed, skip := closeRecordedPaneSafely(p.PaneID, session, p.Role, p.CWD)
		if closed {
			fmt.Fprintf(out, "closed tmux pane %s\n", p.PaneID)
		} else if skip != "" {
			fmt.Fprintf(out, "left tmux pane open: %s\n", skip)
		}
	}
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
		return
	}
	if t.RootExists {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Root)
	}
	if t.BriefHas {
		fmt.Fprintf(out, "  %s  %s\n", action, t.Brief)
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
	fmt.Fprintf(out, "archive: session %s moved to %s.\n", t.Session, dest)
	return nil
}
