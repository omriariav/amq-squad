package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// memberRuntime is a resolved team member plus its on-disk launch record, used
// by the runtime control verbs (focus/send) to target the exact tmux pane the
// agent was launched into.
type memberRuntime struct {
	Member    team.Member
	Profile   string
	Handle    string
	CWD       string
	AgentDir  string
	HasRecord bool
	Record    launch.Record
	// ProfileMismatch means the session+handle mailbox currently contains a
	// launch record for another profile. Runtime control must fail closed rather
	// than falling back to title/cwd matching, whose identity is only session+role.
	ProfileMismatch bool
}

// resolveMemberRuntime finds the team member with the given role for a session
// and loads its launch record. role is required.
func resolveMemberRuntime(projectDir, profile, session string, explicitSession bool, role string) (memberRuntime, string, error) {
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return memberRuntime{}, "", fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, session, explicitSession)
	if err != nil {
		return memberRuntime{}, "", err
	}
	role = strings.ToLower(strings.TrimSpace(role))
	var m team.Member
	found := false
	for _, mm := range orderedTeamMembers(t.Members) {
		if strings.ToLower(mm.Role) == role {
			m = mm
			found = true
			break
		}
	}
	if !found {
		return memberRuntime{}, workstream, fmt.Errorf("no team member with role %q in this team", role)
	}
	cwd := m.EffectiveCWD(t.Project)
	env, err := resolveAMQEnvForTeamProfile(cwd, profile, workstream, m.Handle)
	if err != nil {
		return memberRuntime{}, workstream, fmt.Errorf("resolve amq env for %s: %w", role, err)
	}
	handle := m.Handle
	if env.Me != "" {
		handle = env.Me
	}
	agentDir := filepath.Join(absoluteAMQRoot(cwd, env.Root), "agents", handle)
	mr := memberRuntime{Member: m, Profile: profile, Handle: handle, CWD: cwd, AgentDir: agentDir}
	if rec, rerr := launch.Read(agentDir); rerr == nil {
		if !squadnamespace.ProfilesEqual(profile, rec.TeamProfile) {
			mr.ProfileMismatch = true
			return mr, workstream, nil
		}
		mr.Record = rec
		mr.HasRecord = true
		mr.CWD = compareCWD(cwd, rec.CWD)
	}
	return mr, workstream, nil
}

// compareCWD returns the directory resolveControlTarget compares a recorded pane
// against. It prefers the member's pinned/effective cwd, but falls back to the
// launch record's cwd when the member pins none. That fallback matters for the
// dynamic-team default: a `team member add` roster entry records no cwd, and an
// orchestrated team often has no project pin, so EffectiveCWD returns "" — and
// sameResolvedDir treats "" as no-match, which would make the cwd guard reject
// EVERY live pane (send/focus fail with "no live tmux pane" while status/resume
// pane_alive, which skip the cwd check, report the same pane alive — the exact
// split a 2.0 dogfood hit). The record carries the agent's real launch cwd, so
// it is the authoritative compare dir when the member itself pins none.
func compareCWD(effectiveCWD, recordCWD string) string {
	if strings.TrimSpace(effectiveCWD) != "" {
		return effectiveCWD
	}
	return strings.TrimSpace(recordCWD)
}

// recordedPaneID returns the exact tmux pane id persisted for the member, if any.
func (mr memberRuntime) recordedPaneID() string {
	if mr.HasRecord && mr.Record.Tmux != nil {
		return strings.TrimSpace(mr.Record.Tmux.PaneID)
	}
	return ""
}

// resolveControlTarget picks the tmux pane to act on for a member: the exact
// recorded pane id when it is still live AND its working directory still
// matches the member (guarding against pane-id reuse after a tmux server
// restart), otherwise the neutral resolver (title-first, then engine+cwd).
// Returns the pane id plus a focus target.
func resolveControlTarget(mr memberRuntime, workstream string, panes []tmuxpane.TmuxPane) (paneID string, target tmuxpane.TmuxTarget, ok bool) {
	if mr.ProfileMismatch {
		return "", tmuxpane.TmuxTarget{}, false
	}
	if !mr.HasRecord && squadnamespace.NormalizeProfile(mr.Profile) != team.DefaultProfile {
		return "", tmuxpane.TmuxTarget{}, false
	}
	// When we know the agent's exact recorded pane, it is the authoritative
	// identity: use it only if that pane is still live and in the member's cwd.
	// If the recorded pane is gone, the agent's pane is gone — do NOT fall back
	// to the fuzzy cwd+engine resolver, which for `send` could deliver to the
	// wrong agent (a same-cwd/engine peer whose pane is still alive). Report
	// not-found so the verb errors clearly instead of guessing.
	if id := mr.recordedPaneID(); id != "" {
		p, found := tmuxpane.FindPaneByID(id, panes)
		if !found {
			// The global `list-panes -a` scan missed this pane — it was empty,
			// or it failed wholesale under iTerm2 tmux -CC even though the
			// recorded id is still individually addressable. The recorded id IS
			// the authoritative address, so inspect that one pane directly.
			p, found = statusPaneInspector(id)
		}
		if found &&
			sameResolvedDir(p.CWD, mr.CWD) &&
			!paneTitledForDifferentAgent(p.Title, workstream, mr.Member.Role) {
			return id, tmuxpane.TargetFromPane(p), true
		}
		return "", tmuxpane.TmuxTarget{}, false
	}
	// No recorded pane id (a pre-1.5 record, or an agent launched outside
	// amq-squad's tmux backend): best-effort resolve by title-first, then
	// cwd+engine, anchored by PID lineage so an externally-launched pane resolves
	// to the right agent even when peers share cwd+engine (#95). The recorded
	// agent pid (present even when no tmux block was captured) anchors the match.
	ag := state.Agent{Handle: mr.Handle, Role: mr.Member.Role, Engine: mr.Member.Binary}
	if mr.HasRecord {
		// Trust PID lineage only for a VERIFIED live agent pid: focus/send read
		// the record without a liveness verdict, so a stale/reused pid must not
		// anchor a cwd/engine-bypassing match (#95 review).
		ag.AgentPID = verifiedAgentPID(mr.Record.AgentPID, mr.Member.Binary)
	}
	if tgt, found := tmuxpane.ResolveTmuxTargetForSession(ag, workstream, mr.CWD, panes, childrenPidTree()); found {
		// tgt.Pane is the pane INDEX; tmux would resolve a bare index relative
		// to the current client/window, not the agent's pane. Resolve to the
		// exact pane_id, falling back to a fully-qualified session:window.pane
		// spec (also unambiguous) when the id is unavailable.
		paneTarget := paneIDForTarget(tgt, panes)
		if paneTarget == "" {
			paneTarget = tgt.Session + ":" + tgt.Window + "." + tgt.Pane
		}
		return paneTarget, tgt, true
	}
	return "", tmuxpane.TmuxTarget{}, false
}

// sameResolvedDir reports whether two paths refer to the same directory after
// resolving symlinks, so a member cwd under a symlinked path (e.g. macOS
// /var -> /private/var TMPDIR) matches tmux's resolved #{pane_current_path}.
// Falls back to a plain absolute comparison when a side cannot be resolved.
func sameResolvedDir(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return resolveDir(a) == resolveDir(b)
}

func resolveDir(dir string) string {
	abs := dir
	if a, err := filepath.Abs(dir); err == nil {
		abs = a
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// closeRecordedPaneSafely closes paneID ONLY when the live pane it resolves to is
// provably the SAME pane the launch record describes — the destructive twin of
// #156's read-path guard, applied to kill-pane so teardown never closes a pane
// whose id was REUSED by a different agent/session after a tmux server restart.
// It inspects the pane by id (InspectPaneByID already verifies the returned
// pane_id == paneID), then requires positive identity: the pane's title is this
// agent's amq token, or — for an untitled/clobbered pane — its cwd matches the
// recorded cwd. A pane that is already gone is a silent no-op. session+role
// derive the expected amq title token; recordedCWD is the agent's launch cwd (""
// when unknown). Returns whether it closed, plus a non-empty skip reason when it
// deliberately left the pane open, so the caller can warn instead of killing the
// wrong pane.
func closeRecordedPaneSafely(paneID, session, role, recordedCWD string) (closed bool, skip string) {
	id := strings.TrimSpace(paneID)
	if id == "" {
		return false, ""
	}
	p, ok := statusPaneInspector(id)
	if !ok {
		return false, "" // pane already gone — nothing to close
	}
	switch {
	case p.Title == paneTitleToken(session, role):
		// exact amq token -> definitely this agent's pane.
	case paneTitledForDifferentAgent(p.Title, session, role):
		return false, fmt.Sprintf("pane %s now belongs to a different agent (title %q); left open (likely pane-id reuse)", id, p.Title)
	default:
		// No amq title token to trust: only safe to close if the cwd still matches.
		if strings.TrimSpace(recordedCWD) == "" || !sameResolvedDir(p.CWD, recordedCWD) {
			return false, fmt.Sprintf("pane %s identity unconfirmed (cwd %q vs recorded %q); left open", id, p.CWD, recordedCWD)
		}
	}
	if err := paneCloser(id); err != nil {
		return false, ""
	}
	return true, ""
}

// paneTitledForDifferentAgent reports whether a pane carries an amq title token
// (amq:<workstream>:<role>) for a role OTHER than the expected one — i.e. the
// recorded pane id was reused by a sibling agent (e.g. after a tmux server
// restart) in the same repo. Such a pane must not be trusted for the recorded
// agent even when its cwd matches, or `send` could deliver to the wrong agent.
// An untitled or clobbered (non-amq) title is not second-guessed: the recorded
// pane id + cwd match stand.
func paneTitledForDifferentAgent(title, workstream, role string) bool {
	title = strings.TrimSpace(title)
	if !strings.HasPrefix(title, "amq:") {
		return false
	}
	return title != paneTitleToken(workstream, role)
}

// paneIDForTarget returns the #{pane_id} of the live pane the resolver selected,
// matched by session+window+pane index. Empty when no live pane matches (e.g.
// older tmux output without ids).
func paneIDForTarget(tgt tmuxpane.TmuxTarget, panes []tmuxpane.TmuxPane) string {
	for _, p := range panes {
		if p.Session == tgt.Session && p.Window == tgt.Window && p.Pane == tgt.Pane {
			return p.PaneID
		}
	}
	return ""
}

// --- focus / open ---

var paneBusyForSend = tmuxpane.PaneBusy
var sendPromptToPane = tmuxpane.SendPromptToPane

func runFocus(args []string) error {
	fs := flag.NewFlagSet("focus", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "workstream session of the team to focus")
	roleFlag := fs.String("role", "", "focus a specific agent's pane by role (omit to focus the session)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad focus - bring a team session or agent pane into view (tmux)

Usage:
  amq-squad focus [--project DIR] [--profile NAME] --session S [--role ROLE]

Focuses the tmux pane an agent was launched into, using the exact pane id from
its launch record (falling back to the neutral title/cwd resolver). Without
--role, focuses the session's first resolvable pane. 'open' is an alias.

Examples:
  amq-squad focus --session issue-96 --role cto
  amq-squad focus --session issue-96
  amq-squad open --project ~/Code/app --session issue-96
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	return focusTarget(projectDir, profile, *sessionFlag, flagWasSet(fs, "session"), flagWasSet(fs, "profile"), *roleFlag)
}

// focusTarget resolves and switches to the pane for a role (or the session's
// first resolvable pane when role is empty).
// errTmuxAccessDenied is returned when amq-squad's internal tmux query is denied
// access to the tmux server (a sandboxed agent), instead of the misleading "no
// live tmux pane found". It names the cause and the fix so a sandboxed lead is
// not left debugging a phantom dead pane.
func errTmuxAccessDenied() error {
	return fmt.Errorf("tmux control unavailable: connecting to the tmux server was denied (operation not permitted). " +
		"The agent is likely sandboxed — grant it tmux socket access (Codex: run `/permissions full access`, or scope-approve " +
		"`amq-squad status`/`focus`/`send`/`resume`; or launch the lead unsandboxed), then retry")
}

func focusTarget(projectDir, profile, session string, explicitSession bool, explicitProfile bool, role string) error {
	if strings.TrimSpace(role) != "" {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		if err := ensureTargetIsNotOperator(t, "focus", role); err != nil {
			return err
		}
	}
	panes, err := statusPaneLister()
	if err != nil {
		if tmuxpane.IsPermissionDenied(err) {
			// The tmux socket itself is unreachable because access was denied
			// (a sandboxed agent). That is NOT "pane gone" — surface the real
			// cause and how to fix it, instead of the misleading "no live pane".
			return errTmuxAccessDenied()
		}
		// The global `tmux list-panes -a` scan can fail wholesale under iTerm2
		// tmux -CC control mode even when a recorded pane is still directly
		// addressable. Degrade to no scan results and let resolveControlTarget
		// address the recorded pane id directly rather than aborting the op.
		panes = nil
	}
	roles := []string{role}
	if strings.TrimSpace(role) == "" {
		// Session focus: try each member until one resolves to a live pane.
		t, terr := team.ReadProfile(projectDir, profile)
		if terr != nil {
			return fmt.Errorf("read team: %w", terr)
		}
		roles = roles[:0]
		for _, m := range orderedTeamMembers(t.Members) {
			roles = append(roles, m.Role)
		}
	}
	for _, r := range roles {
		mr, workstream, rerr := resolveMemberRuntime(projectDir, profile, session, explicitSession, r)
		if rerr != nil {
			if strings.TrimSpace(role) != "" {
				return rerr
			}
			continue
		}
		if err := ensureNoNamespaceConflict("focus", projectDir, profile, workstream, explicitProfile); err != nil {
			return err
		}
		if _, target, ok := resolveControlTarget(mr, workstream, panes); ok {
			if err := tmuxpane.SwitchTo(target); err != nil {
				return err
			}
			return nil
		}
	}
	if strings.TrimSpace(role) != "" {
		return fmt.Errorf("no live tmux pane found for role %q; check 'amq-squad status --session %s --json'", role, shellQuote(session))
	}
	return fmt.Errorf("no live tmux pane found for this session; check 'amq-squad status --session %s --json'", shellQuote(session))
}

// --- send ---

func runSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "workstream session of the team")
	roleFlag := fs.String("role", "", "role of the agent to send the prompt to")
	bodyFile := fs.String("body-file", "", "read the prompt from this file ('-' for stdin)")
	body := fs.String("body", "", "prompt text (alternative to --body-file)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	forceFlag := fs.Bool("force", false, "deliver even if the agent appears busy (mid-turn)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad send - deliver a prompt to an agent's tmux pane and submit it

Usage:
  amq-squad send [--project DIR] [--profile NAME] --session S --role ROLE
                 (--body TEXT | --body-file FILE | --body-file -) [--force]

Stages the prompt in a tmux paste buffer (via stdin, never a shell string) and
pastes it into the agent's exact pane, then submits a single Enter. Multi-line
prompts and text with quotes or shell metacharacters are delivered verbatim.
Errors clearly if the target pane is gone.

By default it REFUSES to deliver into a pane whose agent looks busy (mid-turn),
since a prompt pushed over a working agent lands in a tool-result buffer and is
lost; pass --force to deliver anyway.

Examples:
  amq-squad send --session issue-96 --role cto --body "please review PR #64"
  amq-squad send --session issue-96 --role qa --body-file ./prompt.md
  cat prompt.md | amq-squad send --session issue-96 --role cto --body-file -
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*roleFlag) == "" {
		return usageErrorf("send requires --role")
	}
	prompt, err := readPromptBody(*body, *bodyFile, flagWasSet(fs, "body"), flagWasSet(fs, "body-file"), os.Stdin, stdinIsInteractive())
	if err != nil {
		return err
	}
	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	if err := ensureTargetIsNotOperator(t, "send", *roleFlag); err != nil {
		return err
	}
	mr, workstream, err := resolveMemberRuntime(projectDir, profile, *sessionFlag, flagWasSet(fs, "session"), *roleFlag)
	if err != nil {
		return err
	}
	if err := ensureNoNamespaceConflict("send", projectDir, profile, workstream, flagWasSet(fs, "profile")); err != nil {
		return err
	}
	panes, err := statusPaneLister()
	if err != nil {
		if tmuxpane.IsPermissionDenied(err) {
			// The tmux socket itself is unreachable because access was denied
			// (a sandboxed agent). That is NOT "pane gone" — surface the real
			// cause and how to fix it, instead of the misleading "no live pane".
			return errTmuxAccessDenied()
		}
		// The global `tmux list-panes -a` scan can fail wholesale under iTerm2
		// tmux -CC control mode even when a recorded pane is still directly
		// addressable. Degrade to no scan results and let resolveControlTarget
		// address the recorded pane id directly rather than aborting the op.
		panes = nil
	}
	paneID, _, ok := resolveControlTarget(mr, workstream, panes)
	if !ok || strings.TrimSpace(paneID) == "" {
		return fmt.Errorf("no live tmux pane found for role %q; the agent may not be running", *roleFlag)
	}
	// Don't talk over a working agent: a prompt pushed into a pane whose agent is
	// mid-turn lands in a tool-result buffer and is silently lost. Refuse unless
	// --force. A capture error is not treated as busy (never block on a failed
	// check) — only a positive busy signal refuses.
	if !*forceFlag {
		if busy, berr := paneBusyForSend(paneID); berr == nil && busy {
			return fmt.Errorf("agent %q at pane %s appears busy (mid-turn); retry when idle, or pass --force to deliver anyway", *roleFlag, paneID)
		}
	}
	if err := sendPromptToPane(paneID, prompt); err != nil {
		return err
	}
	quietNotice("Delivered prompt to %s pane %s.\n", *roleFlag, paneID)
	return nil
}

// readPromptBody resolves the prompt text from --body, --body-file (a path or
// "-" for stdin), or bare stdin when neither flag is set and stdin is piped.
// interactiveStdin reports whether stdin is a terminal; the bare-stdin path
// then returns a usage error instead of blocking forever waiting for EOF.
func readPromptBody(body, bodyFile string, bodySet, fileSet bool, stdin io.Reader, interactiveStdin bool) (string, error) {
	if bodySet && fileSet {
		return "", usageErrorf("use either --body or --body-file, not both")
	}
	if bodySet {
		if strings.TrimSpace(body) == "" {
			return "", usageErrorf("--body cannot be empty")
		}
		return body, nil
	}
	if fileSet {
		if bodyFile == "-" {
			return readAllPrompt(stdin)
		}
		b, err := os.ReadFile(bodyFile)
		if err != nil {
			return "", fmt.Errorf("read --body-file: %w", err)
		}
		if strings.TrimSpace(string(b)) == "" {
			return "", fmt.Errorf("--body-file %s is empty", bodyFile)
		}
		return string(b), nil
	}
	if interactiveStdin {
		return "", usageErrorf("no prompt provided; pass --body, --body-file FILE, or pipe text on stdin")
	}
	return readAllPrompt(stdin)
}

// stdinIsInteractive reports whether os.Stdin is a terminal (char device)
// rather than a pipe or file, so `send` can refuse to block on TTY input.
func stdinIsInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func readAllPrompt(stdin io.Reader) (string, error) {
	b, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("read prompt from stdin: %w", err)
	}
	if strings.TrimSpace(string(b)) == "" {
		return "", usageErrorf("no prompt provided; pass --body, --body-file, or pipe text on stdin")
	}
	return string(b), nil
}

// resolveProjectProfile resolves the --project and --profile flags shared by the
// runtime control verbs.
func resolveProjectProfile(projectFlag, profileFlag string, projectSet bool) (string, string, error) {
	profile, err := resolveProfileFlag(profileFlag)
	if err != nil {
		return "", "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, projectFlag, projectSet)
	if err != nil {
		return "", "", err
	}
	return projectDir, profile, nil
}
