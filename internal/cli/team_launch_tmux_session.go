package cli

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func init() {
	registerTeamLaunchBackend(tmuxSessionTeamLaunchBackend{})
}

// tmuxSessionBinary is the user CLI this backend drives. It is a package var
// so tests can assert binary resolution without depending on the real wrapper
// being installed, and so the LIVE path that spawns iTerm2 -CC stays out of CI.
const tmuxSessionBinary = "tmux-session"

// tmuxSessionLookPath resolves the tmux-session wrapper on PATH. Indirected
// through a var so Validate can be unit-tested both ways (present/absent)
// without touching the real PATH or spawning anything.
var tmuxSessionLookPath = exec.LookPath

// tmuxSessionTeamLaunchBackend is the opt-in window-per-agent backend selected
// via `--terminal tmux-session`. Unlike the default tmux backend (which splits
// panes inside one window), it drives the user's `tmux-session` CLI so EACH
// agent lands in its own named iTerm2 window. The LIVE path goes through
// iTerm2 -CC and cannot be verified headlessly; only the emitted command shape
// is unit-tested. The default tmux backend is unchanged and stays the default.
type tmuxSessionTeamLaunchBackend struct{}

// tmuxSessionLaunchPlan is the resolved, backend-agnostic launch description
// the emitter and exec path both consume. Workstream is the AMQ session that
// also names the tmux-session session (one session, one window per agent).
type tmuxSessionLaunchPlan struct {
	Workstream string
	Panes      []teamLaunchPane
	StartDelay time.Duration
}

func (tmuxSessionTeamLaunchBackend) Name() string {
	return "tmux-session"
}

// Validate fails fast with an actionable message when the tmux-session wrapper
// is not on PATH, so the operator is told exactly how to recover (install it or
// fall back to the default tmux backend) before any pane work begins.
func (tmuxSessionTeamLaunchBackend) Validate(opts teamLaunchOptions) error {
	if opts.Stagger < 0 {
		return fmt.Errorf("--stagger cannot be negative")
	}
	if _, err := tmuxSessionLookPath(tmuxSessionBinary); err != nil {
		return fmt.Errorf("%s not found on PATH — install it or use --terminal tmux", tmuxSessionBinary)
	}
	return nil
}

func (b tmuxSessionTeamLaunchBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	printTmuxSessionLaunchPlan(b.buildPlan(t, opts))
	return nil
}

func (b tmuxSessionTeamLaunchBackend) Launch(t team.Team, opts teamLaunchOptions) error {
	return runTmuxSessionLaunchPlan(b.buildPlan(t, opts))
}

func (tmuxSessionTeamLaunchBackend) buildPlan(t team.Team, opts teamLaunchOptions) tmuxSessionLaunchPlan {
	return tmuxSessionLaunchPlan{
		Workstream: opts.Workstream,
		Panes:      buildTeamLaunchPanes(t, opts),
		StartDelay: opts.Stagger,
	}
}

// tmuxSessionCreateArgv is the PURE per-agent argv for adding (and attaching) a
// named window in the workstream session. Keeping it a standalone function
// means emission is unit-testable without spawning iTerm2: it is the single
// source of truth used by both the dry-run printer and the live exec path.
//
//	tmux-session --session <workstream> --create <role> <cwd>
func tmuxSessionCreateArgv(workstream, role, cwd string) []string {
	return []string{"--session", workstream, "--create", role, cwd}
}

// tmuxSessionRenameArgv is the PURE per-agent argv that stamps the window with
// the deterministic name-first jump token (amq:<session>:<role>) by renaming
// the just-created <role> window. Reusing paneTitleToken keeps tmux pane
// resolver's expectedPaneToken in lockstep across both backends.
//
//	tmux-session --session <workstream> --rename <role> amq:<session>:<role>
func tmuxSessionRenameArgv(workstream, role string) []string {
	return []string{"--session", workstream, "--rename", role, paneTitleToken(workstream, role)}
}

// tmuxSessionResumeArgv is the PURE final argv that resumes/attaches the whole
// session for focus once every agent window exists.
//
//	tmux-session --session <workstream> --resume
func tmuxSessionResumeArgv(workstream string) []string {
	return []string{"--session", workstream, "--resume"}
}

func printTmuxSessionLaunchPlan(plan tmuxSessionLaunchPlan) {
	fmt.Println("# amq-squad team launch - tmux-session (window per agent)")
	if plan.Workstream != "" {
		fmt.Printf("# workstream: %s\n", plan.Workstream)
	}
	fmt.Printf("# windows: %d\n\n", len(plan.Panes))
	for _, line := range tmuxSessionDryRunLines(plan) {
		fmt.Println(line)
	}
}

// tmuxSessionDryRunLines renders the faithful, copy-pasteable preview for the
// window-per-agent plan. For each agent it emits, in order: a --create for the
// agent's own named window, a tmux send-keys that types the agent command into
// that window, and a --rename that stamps the amq:<session>:<role> jump token.
// A trailing --resume focuses the session. The emitted lines mirror exactly
// what runTmuxSessionLaunchPlan executes.
func tmuxSessionDryRunLines(plan tmuxSessionLaunchPlan) []string {
	if len(plan.Panes) == 0 {
		return nil
	}
	lines := make([]string, 0, len(plan.Panes)*3+1)
	for i, pane := range plan.Panes {
		lines = append(lines,
			shellCommand(tmuxSessionBinary, tmuxSessionCreateArgv(plan.Workstream, pane.Role, pane.CWD)...),
			tmuxSessionPaneCommandDryRunLine(plan.Workstream, pane.Role, pane.Command),
			shellCommand(tmuxSessionBinary, tmuxSessionRenameArgv(plan.Workstream, pane.Role)...),
		)
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			lines = append(lines, sleepDryRunLine(plan.StartDelay))
		}
	}
	lines = append(lines, shellCommand(tmuxSessionBinary, tmuxSessionResumeArgv(plan.Workstream)...))
	return lines
}

// tmuxSessionPaneCommandDryRunLine targets the agent's own named window by its
// "<session>:<role>" tmux target (the window-per-agent equivalent of the pane-id
// targeting the default backend uses) and makes the agent command that window's
// ROOT PROCESS.
//
// #571: the previous name said SendKeys and the previous comment said "types the
// agent command". Both were still accurate about the mechanism this backend used
// and both became false in the same commit that stopped typing. A name that
// survives the mechanism it names is how the next reader searching for typing
// paths misses this one.
func tmuxSessionPaneCommandDryRunLine(workstream, role, command string) string {
	return tmuxPaneCommandDryRunLine(workstream+":"+role, command)
}

// tmuxSessionRunCommand is the seam for this backend's calls to the tmux-session wrapper CLI.
//
// Added in #577 round 3 because the execution path had NO test coverage: every test stopped at
// the dry-run or at a refusal that returned before creation. Both round-1 and round-2 safety
// findings in this file were in code no test could reach, which is not a coincidence.
var tmuxSessionRunCommand = runCommand

func runTmuxSessionLaunchPlan(plan tmuxSessionLaunchPlan) error {
	if len(plan.Panes) == 0 {
		return fmt.Errorf("tmux-session plan has no panes")
	}
	for i, pane := range plan.Panes {
		// Snapshot the session's panes BEFORE creating, so the pane this launcher causes can
		// be identified by difference rather than by name. An unreadable snapshot is a refusal:
		// delivery replaces a pane's process, so an unverifiable target may be an operator's.
		before, snapshotOK := tmuxSessionWindowIDs(plan.Workstream)
		if !snapshotOK {
			return fmt.Errorf("refusing to launch %s: session %s exists but its panes cannot be enumerated, so a freshly created pane cannot be told apart from one already in use. Delivery replaces a pane's process and would risk an operator's window",
				pane.Role, plan.Workstream)
		}
		if err := tmuxSessionRunCommand(tmuxSessionBinary, tmuxSessionCreateArgv(plan.Workstream, pane.Role, pane.CWD)...); err != nil {
			return err
		}
		// "new-window" is the correct visibility provenance here: this backend's
		// contract is one named, attached iTerm2 window per agent (tmuxSessionResumeArgv
		// attaches the session for focus), so status maps it to adoption_mode=managed_window
		// (operator-visible), NOT a detached session. Do not remap to managed_session.
		// #577 finding 3, and this is the most dangerous defect #571 introduced.
		// respawn-pane -k DESTROYS whatever occupies the target, and a NAME target
		// ("<session>:<role>") is not proof of what that is. An operator window that
		// happens to be named for the role, or the leftover window of a prior partial
		// launch, would have its live shell or TUI killed. "Delivery cannot lose the
		// command" and "delivery cannot destroy the operator's work" are different
		// properties; the original fix only reasoned about the first.
		//
		// So delivery binds to the EXACT pane identity creation produced, and a window
		// that already exists is refused instead of respawned. Resolving the pane id
		// after creation also means the -k applies to a pane this launcher just made.
		paneID, err := tmuxSessionCreatedPaneID(plan.Workstream, pane.Role, before)
		if err != nil {
			return fmt.Errorf("bind %s to a verified fresh pane: %w", pane.Role, err)
		}
		if err := deliverPaneCommand(paneID, withTmuxTargetEnv("new-window", pane.Command)); err != nil {
			return fmt.Errorf("deliver command for %s: %w", pane.Role, err)
		}
		// Verified counting applies to THIS backend too. #571 landed the pane-pid check
		// on the two default-backend sites and left this one delivering unverified, so
		// "pane creation can no longer count as success" held for two of three paths.
		// This is the operator-visible iTerm2 path, where an unstarted agent is a window
		// sitting at a shell prompt -- the exact failure that read as a successful launch.
		//
		// Placed BEFORE the rename deliberately: tmuxSessionRenameArgv retires the
		// "<session>:<role>" window name in favour of the amq: token, so this target only
		// resolves until that call.
		if _, err := verifyPaneProcessLaunched(paneID); err != nil {
			return fmt.Errorf("worker %s not launched: %w", pane.Role, err)
		}
		if err := tmuxSessionRunCommand(tmuxSessionBinary, tmuxSessionRenameArgv(plan.Workstream, pane.Role)...); err != nil {
			return err
		}
		if i < len(plan.Panes)-1 && plan.StartDelay > 0 {
			time.Sleep(plan.StartDelay)
		}
	}
	if err := tmuxSessionRunCommand(tmuxSessionBinary, tmuxSessionResumeArgv(plan.Workstream)...); err != nil {
		return err
	}
	quietNotice("Opened %d agent window(s) in tmux-session %s.\n", len(plan.Panes), shellQuote(plan.Workstream))
	verbosePolicyEcho()
	return nil
}

// tmuxSessionWindowIDs maps pane id -> location for every pane in the session, and reports
// whether that answer is TRUSTWORTHY.
//
// #577 round 3 finding 1: an earlier version enumerated the session before the first --create,
// so on a FRESH workstream list-panes exited nonzero, the answer was judged untrustworthy, and
// the backend refused -- making its first launch structurally impossible. A fail-closed check
// was added on a path where the failing condition IS the normal initial state.
//
// The rule is that absence must be PROVEN, never inferred from an error. See the body for how,
// and note that the round-4 review rejected the first attempt at this comment's own claim: a
// failed probe is not proof of anything.
func tmuxSessionWindowIDs(workstream string) (map[string]tmuxSessionPaneLocation, bool) {
	// #577 round 4: the previous version treated a FAILED has-session probe as proven vacancy.
	// It proves nothing: has-session exits nonzero for session-absent, for server-unreachable,
	// and for any probe malfunction, and the exit code cannot tell them apart. The failure that
	// mattered: a session holding one operator pane, has-session fails transiently, the
	// before-map is trusted EMPTY, --create selects the existing role window, and the operator's
	// pane then looks novel-in-a-novel-window and gets destroyed. A failed probe became a
	// licence to delete.
	//
	// Vacancy is now proven by a SUCCESSFUL observation: list-sessions succeeds and the
	// workstream is absent from its output. The single well-defined "no server running" error is
	// also proof -- no server means no panes -- and is matched specifically rather than by
	// treating all errors alike. ANY other failure is a refusal, exactly as for a session that
	// exists and cannot be enumerated.
	sessions, err := tmuxOutputCommand("tmux", "list-sessions", "-F", "#{session_name}")
	if err != nil {
		if tmuxNoServerRunning(err) {
			return map[string]tmuxSessionPaneLocation{}, true
		}
		return nil, false
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(sessions), "\n") {
		if strings.TrimSpace(line) == workstream {
			found = true
		}
	}
	if !found {
		// PROVEN vacancy: the server answered and this workstream is not among its sessions.
		return map[string]tmuxSessionPaneLocation{}, true
	}
	// #577 round 6 F1, and this is name-as-identity ONE LAYER UP from the pane and window
	// fixes: tmux resolves a -t target by exact name, then PREFIX, then GLOB, unless the target
	// starts with '='. So a bare "-t issue-96" can resolve to "issue-96-old" -- a DIFFERENT
	// session -- if this one vanishes between the list-sessions proof and this read. Those rows'
	// %/@ ids then pass every shape check, because the format never said which session they came
	// from, and the attribution conjunction can go on to destroy an operator's pane.
	//
	// Two belts with distinct roles, per the ruling:
	//   PRIMARY: '=' is the exact-target INSTRUCTION to tmux -- it prevents prefix/glob
	//            resolution from selecting another session in the first place.
	//   DEFENSE-IN-DEPTH: the echoed #{session_name}, compared per row, is EVIDENCE about what
	//            tmux actually returned. An instruction is not evidence, which is this
	//            milestone's whole theme; the comparison also survives a tmux behaviour change
	//            and costs one field per row.
	// Do not delete the comparison as dead code because '=' makes it unreachable: an
	// unreachable branch that ever fires means the assumption behind it was wrong, and the cost
	// of being wrong here is an operator's pane.
	out, err := tmuxOutputCommand("tmux", "list-panes", "-s", "-t", "="+workstream, "-F", "#{session_name} #{pane_id} #{window_id} #{window_name}")
	if err != nil {
		// The session EXISTS and could not be enumerated: the genuine cannot-prove case.
		return nil, false
	}
	// #577 round 5 F2: a SUCCESSFUL read was trusted even when empty or malformed. A tmux
	// session cannot exist with zero panes -- and this session was just PROVEN to exist -- so an
	// empty or all-malformed listing is broken evidence, not vacancy. Short rows were silently
	// skipped by a `continue`, which turned unparseable output into a smaller, confident answer.
	//
	// Ids are shape-validated through the existing exactTmuxPaneID/exactTmuxWindowID helpers
	// rather than a sixth local copy of the same regexp.
	panes := map[string]tmuxSessionPaneLocation{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 3 {
			// A row we cannot parse is not a row we can ignore: the pane it describes may be
			// an operator's, and delivery replaces a pane's process.
			return nil, false
		}
		if fields[0] != workstream {
			// The row belongs to ANOTHER session. With '=' this should be unreachable, which is
			// exactly why it is checked: an unreachable branch that fires means the assumption
			// behind it was wrong, and the cost of being wrong here is an operator's pane.
			return nil, false
		}
		paneID, paneErr := exactTmuxPaneID(fields[1])
		windowID, windowErr := exactTmuxWindowID(fields[2])
		if paneErr != nil || windowErr != nil {
			return nil, false
		}
		loc := tmuxSessionPaneLocation{WindowID: windowID}
		if len(fields) >= 4 {
			loc.WindowName = strings.Join(fields[3:], " ")
		}
		panes[paneID] = loc
	}
	if len(panes) == 0 {
		// The session exists, so it has at least one pane. Zero means the evidence is broken.
		return nil, false
	}
	return panes, true
}

// tmuxSessionPaneLocation is where a pane sits: its window id and that window's name. Both are
// needed because #577 round 4 requires a created pane to be attributable to THIS create, and
// the wrapper's contract is one window per role NAMED BY ROLE.
type tmuxSessionPaneLocation struct {
	WindowID   string
	WindowName string
}

// tmuxNoServerRunning matches the ONE tmux error that is affirmative evidence of no panes.
//
// #577 round 5 F1: the previous version ALSO matched "error connecting to", and this repository
// already documents what that phrase means. See IsPermissionDenied in
// internal/tmuxpane/tmux.go: tmux prints "error connecting to <socket> (Operation not
// permitted)" for a PERMISSION DENIAL -- the signature of a sandboxed agent -- and a denial is
// NOT transient. So the broad half treated "the tmux socket is blocked" and "a connection
// failed" as PROOF OF VACANCY.
//
// The consequence was not theoretical: a sandboxed or transiently-unreachable server holding a
// POPULATED session produced a falsely empty before-map, and when connectivity returned for
// --create the attribution conjunction evaluated against that empty snapshot and could destroy
// an operator's pane. The comment claimed "the ONE tmux error" while the code matched two, and
// the round-4 commit repeated that claim -- a warning already written three files away.
//
// Only the no-server phrasing counts. Anything else, permission included, is a refusal.
func tmuxNoServerRunning(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no server running")
}

// tmuxSessionCreatedPaneID identifies the pane this launcher's --create produced, requiring
// both novelty AND provenance.
//
// #577 round 3 finding 2, the sixth shape-not-proof instance: set difference proves COUNT, not
// CAUSALITY. The interleaving that defeats it -- the role window already exists so --create
// selects it and creates NOTHING, an operator concurrently creates one unrelated pane, the diff
// is exactly {operator pane}, it is accepted, and respawn-pane -k destroys their live pane.
//
// So novelty is necessary and insufficient. The created pane must ALSO sit in a window that did
// not exist before this create: that is what --create does, and an operator's pane in a
// pre-existing window can no longer satisfy it. "The only new thing" is replaced by "the new
// thing in a window I caused to exist".
func tmuxSessionCreatedPaneID(workstream, role string, before map[string]tmuxSessionPaneLocation) (string, error) {
	after, ok := tmuxSessionWindowIDs(workstream)
	if !ok {
		return "", fmt.Errorf("cannot enumerate panes in session %s after creating the %s window: refusing to deliver, because delivery replaces a pane's process and an unverified target may be an operator's", workstream, role)
	}
	beforeWindows := map[string]bool{}
	for _, loc := range before {
		beforeWindows[loc.WindowID] = true
	}
	var candidates []string
	for pane, loc := range after {
		if _, existed := before[pane]; existed {
			continue
		}
		// The window must be new. Necessary, and #577 round 4 proved it insufficient: an
		// operator's concurrent pane can arrive in a new window too.
		if beforeWindows[loc.WindowID] {
			continue
		}
		// ATTRIBUTION to THIS create: the wrapper's contract is one window per role, named by
		// role, and the rename to the amq: token happens only AFTER delivery -- so at this
		// instant our window is still named exactly <role>. Name alone is not identity, but
		// name AND novelty narrows the residual to "an operator deliberately created a window
		// named exactly this role during the launch instant", which is documentable, unlike the
		// accidental collision that novelty alone admitted.
		if loc.WindowName != role {
			continue
		}
		candidates = append(candidates, pane)
	}
	sort.Strings(candidates)
	switch len(candidates) {
	case 1:
		return candidates[0], nil
	case 0:
		return "", fmt.Errorf("creating the %s window in session %s produced no new pane in a new window named %q: either the window already existed and --create selected it, or creation produced nothing. This launcher cannot attribute any pane to its own create, and delivery would replace a pane it did not create", role, workstream, role)
	default:
		return "", fmt.Errorf("creating the %s window in session %s produced %d panes in new windows (%s); exactly one is required to attribute the pane to this create", role, workstream, len(candidates), strings.Join(candidates, " "))
	}
}
