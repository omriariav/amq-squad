package tmuxpane

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
)

// IsPermissionDenied reports whether a tmux command error means the process
// could not reach the tmux server because access was DENIED — the signature of
// a sandboxed agent (e.g. a Codex restricted sandbox) blocking the tmux socket,
// as opposed to a transient -CC pause or a genuinely-missing pane. tmux prints
// "error connecting to <socket> (Operation not permitted)" to stderr, which
// exec.Cmd.Output captures on the *exec.ExitError. A permission denial is NOT
// transient, so callers fail fast instead of retrying, and surface a clear
// "grant tmux access" message instead of "no live tmux pane found".
//
// Match only the PERMISSION phrasing, not a bare "error connecting to": a
// server-not-running failure also reads "error connecting to <socket> (No such
// file or directory)", and that must stay a normal/transient error (no
// misleading "you are sandboxed" advice, and it can still retry/degrade).
func IsPermissionDenied(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg += " " + strings.ToLower(string(ee.Stderr))
	}
	return strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "permission denied")
}

// tmuxReadAttempts bounds how many times a READ-ONLY tmux query is retried when
// it transiently fails. Under iTerm2 tmux -CC the control client pauses when an
// agent TUI floods output; while paused, in-session `tmux list-panes` /
// `display-message` queries can return exit 1 / empty even though the pane is
// alive. A pause clears in well under a second, so a few quick retries ride
// through it, while a genuinely-gone pane still fails within the small total
// budget ((attempts-1) * tmuxReadBackoff). READS ONLY: mutating tmux commands
// (send-keys, kill-pane) are never retried — a partial write must not repeat.
const tmuxReadAttempts = 3

// tmuxReadBackoff is the pause between read retries; tmuxReadSleep is the sleep
// seam. Both are vars so tests can zero the wait and run instantly.
var (
	tmuxReadBackoff = 90 * time.Millisecond
	tmuxReadSleep   = func(d time.Duration) { time.Sleep(d) }
)

// TmuxPane is one row from `tmux list-panes -a`: a pane plus its running
// command and current working directory. PID is the pane's foreground process
// (#{pane_pid}); the actual agent process is typically a descendant of it.
type TmuxPane struct {
	Session string
	Window  string
	Pane    string
	PID     int
	Command string
	CWD     string
	// Title is the pane's #{pane_title}. The launcher stamps a deterministic
	// token here (amq:<session>:<role>) so the jump can resolve name-first,
	// engine-agnostic and rotation-proof. Empty for panes launched before
	// titling existed (or non-amq panes); those fall back to cwd+engine scoring.
	Title string
	// DiscoveryToken is the non-focus-changing per-pane @amq_squad_title
	// option used by staged launches. When present it is authoritative and is
	// also projected through Title so existing name-first/exclusion logic keeps
	// one deterministic discovery surface.
	DiscoveryToken string
	// WindowName is the pane's #{window_name}. It is carried so the cross-session
	// iTerm2 -CC focus can fall back to matching the native window/tab by window
	// name when no pane-title token is present. Optional (older tmux output may
	// omit it; the parser tolerates its absence).
	WindowName string
	// PaneID is the pane's #{pane_id} (e.g. "%265") — the exact, stable tmux
	// control address for the pane. WindowID is #{window_id} (e.g. "@42").
	// Both are optional in parsing (older callers/output may omit them) but the
	// production lister always requests them.
	PaneID   string
	WindowID string
}

// TmuxTarget identifies a single pane for the jump action. Title carries the
// pane's deterministic token (amq:<session>:<role>) when known so the
// cross-session iTerm2 -CC focus can match the native window/tab by title
// without re-walking the pane list. It is best-effort: an empty Title falls
// back to the tmux window name for the osascript match.
type TmuxTarget struct {
	Session string
	Window  string
	Pane    string
	// Title is the pane title token (amq:<session>:<role>) used to match the
	// iTerm2 native window/tab on the cross-session focus path. Optional.
	Title string
	// WindowName is the tmux window's name (#{window_name}); the cross-session
	// osascript falls back to matching it when Title is empty. Optional.
	WindowName string
}

// PaneLister is the seam for enumerating tmux panes. The default implementation
// shells `tmux list-panes -a` READ-ONLY; tests inject a fake.
type PaneLister func() ([]TmuxPane, error)

// execRunner is the seam for the explicit jump action's subprocess. The default
// runs the real tmux binary; tests inject a recorder.
type execRunner func(name string, args ...string) error

// defaultExecRunner runs a command and discards its output, returning only the
// error. It is the production seam for SwitchTo.
func defaultExecRunner(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}

// osaRunner is the seam for the cross-session iTerm2 focus subprocess. Unlike
// execRunner it returns the command's stdout so the caller can read the
// AppleScript's OK/MISS sentinel. The crux of QA-8's fix: osascript exits 0
// whether it raised a tab OR fell off the end finding nothing, so the exit code
// alone cannot tell a real focus from a silent no-match. Production captures
// stdout; tests inject a recorder.
type osaRunner func(name string, args ...string) (string, error)

// defaultOsaRunner runs osascript and returns its trimmed stdout plus error. A
// non-nil error means osascript itself could not run (not macOS / not iTerm2 /
// scripting error); stdout carries the OK/MISS sentinel when it ran.
func defaultOsaRunner(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// PaneIdentity is the exact tmux identity of a single pane, resolved at launch
// time so follow-up control can target stable ids rather than re-inferring from
// names. PaneID/WindowID are tmux control addresses; WindowName is a label.
type PaneIdentity struct {
	Session    string
	WindowID   string
	WindowName string
	PaneID     string
}

// captureExec is the seam for the read-only `tmux display-message` query used to
// resolve a pane's identity. Production runs the real tmux binary; tests inject
// a recorder. It returns raw stdout so the caller parses the tab-separated row.
var captureExec = func(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return string(out), err
}

// tmuxPaneEnv reads $TMUX_PANE (the pane hosting the current process). Seam for
// tests. tmuxServerEnv reads $TMUX (presence of a tmux client).
var (
	tmuxPaneEnv   = func() string { return os.Getenv("TMUX_PANE") }
	tmuxServerEnv = func() string { return os.Getenv("TMUX") }
)

// CurrentPaneIdentity resolves the exact tmux identity of the pane hosting the
// current process. It returns (nil, nil) when not running inside tmux (so
// callers persist nothing rather than guessing). A non-nil error means tmux was
// present but the query failed; callers may treat that as best-effort and skip.
func CurrentPaneIdentity() (*PaneIdentity, error) {
	if strings.TrimSpace(tmuxServerEnv()) == "" {
		return nil, nil
	}
	pane := strings.TrimSpace(tmuxPaneEnv())
	if pane == "" {
		return nil, nil
	}
	return PaneIdentityFor(pane)
}

// PaneIdentityFor resolves the identity of an explicit pane id via
// `tmux display-message -p -t <pane> ...`. Targeting an explicit pane (not the
// active one) is deliberate: control must not depend on which client/window is
// currently focused.
func PaneIdentityFor(paneID string) (*PaneIdentity, error) {
	// Stable ids (pane_id, window_id, session_name) come first because they can
	// never contain a tab; window_name is a user-settable label that can, so it
	// is parsed last as the joined remainder. This guarantees a weird window
	// name can corrupt only the label, never the control addresses.
	const format = "#{pane_id}\t#{window_id}\t#{session_name}\t#{window_name}"
	out, err := captureExec("display-message", "-p", "-t", paneID, format)
	if err != nil {
		return nil, fmt.Errorf("tmux display-message -t %s: %w", paneID, err)
	}
	fields := strings.Split(strings.TrimRight(out, "\r\n"), "\t")
	if len(fields) < 4 {
		return nil, fmt.Errorf("unexpected tmux display-message output %q", out)
	}
	return &PaneIdentity{
		PaneID:     fields[0],
		WindowID:   fields[1],
		Session:    fields[2],
		WindowName: strings.Join(fields[3:], "\t"),
	}, nil
}

// paneListFormat is the tab-separated tmux format shared by the global
// `list-panes -a` scan and the single-pane `display-message` lookup, so both
// produce rows parsePanes understands.
//
// pane_id + window_id (exact control addresses, tab-free) are placed before the
// controlled staged discovery token and trailing human labels pane_title +
// window_name so a label containing a tab can
// never shift the ids. window_name remains the last field so the parser can
// absorb any embedded tabs into it; an empty pane_title (older/non-amq panes)
// leaves a trailing tab the parser tolerates.
const paneListFormat = "#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}\t#{pane_id}\t#{window_id}\tamqmeta:#{@amq_squad_title}\t#{pane_title}\t#{window_name}"

// DefaultPaneLister shells `tmux list-panes -a` with a tab-separated format and
// parses each row into a TmuxPane. It is strictly READ-ONLY. A missing tmux
// binary or no server is reported as an error so callers can degrade.
// listPanesExec is the seam for the global `tmux list-panes -a` scan. A var so
// tests can inject sequenced failures that mimic the -CC pause shape.
var listPanesExec = func() (string, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", paneListFormat).Output()
	return string(out), err
}

func DefaultPaneLister() ([]TmuxPane, error) {
	var lastErr error
	for attempt := 0; attempt < tmuxReadAttempts; attempt++ {
		out, err := listPanesExec()
		if err == nil {
			// An empty list with no error is a genuine "no panes", not a -CC
			// stutter, so it returns immediately. Only an error (exit 1, the
			// pause shape) is retried.
			return parsePanes(out), nil
		}
		lastErr = err
		// A permission denial (sandboxed agent) is not transient — don't burn
		// the retry budget on it; surface it immediately.
		if IsPermissionDenied(err) {
			break
		}
		if attempt+1 < tmuxReadAttempts {
			tmuxReadSleep(tmuxReadBackoff)
		}
	}
	return nil, fmt.Errorf("tmux list-panes: %w", lastErr)
}

// PaneInspectionState is the typed result of an exact-id pane lookup.  Only
// PaneInspectionGone is affirmative evidence that the requested id no longer
// names a pane.  In particular, denied, transient, empty, and malformed reads
// are not absence evidence.
type PaneInspectionState string

const (
	PaneInspectionFound       PaneInspectionState = "found"
	PaneInspectionGone        PaneInspectionState = "gone"
	PaneInspectionUnavailable PaneInspectionState = "unavailable"
	PaneInspectionMalformed   PaneInspectionState = "malformed"
)

// PaneInspection is the result of inspecting one exact tmux pane id.  Pane is
// populated only for Found.  Detail is diagnostic and must not be parsed as a
// policy signal; State is the complete machine-readable classification.
type PaneInspection struct {
	State  PaneInspectionState
	Pane   TmuxPane
	Detail string
}

// InspectPaneExactByID resolves a single pane directly by its tmux id via
// `tmux display-message -t <id>`, bypassing the global `list-panes -a` scan.
// This is the robust path under iTerm2 tmux -CC control mode, where the global
// scan can fail wholesale (exit 1) even though the exact recorded pane is still
// individually addressable. Strictly READ-ONLY. It reports Gone only from
// affirmative exact-id evidence: a successful fallback row naming a different
// pane, or a recognized tmux no-target error. Generic command failures remain
// Unavailable after the bounded read retry; empty/malformed rows are Malformed.
// It uses the same captureExec seam as PaneIdentityFor so tests never shell real
// tmux. display-message takes the format as a trailing positional argument (not
// -F), matching PaneIdentityFor.
func InspectPaneExactByID(paneID string) PaneInspection {
	id := strings.TrimSpace(paneID)
	if id == "" {
		return PaneInspection{State: PaneInspectionMalformed, Detail: "empty tmux pane id"}
	}
	if !isExactPaneID(id) {
		return PaneInspection{State: PaneInspectionMalformed, Detail: fmt.Sprintf("tmux pane id %q is not an exact %%<digits> id", id)}
	}
	// Retry through transient -CC pauses: a paused control client makes
	// `display-message -t <id>` return exit 1 / empty even though the pane is
	// live. Unrecognized failures exhaust the bounded budget as Unavailable.
	var lastErr error
	for attempt := 0; attempt < tmuxReadAttempts; attempt++ {
		out, err := captureExec("display-message", "-p", "-t", id, paneListFormat)
		if err == nil {
			// VERIFY the returned row is actually the requested pane. `tmux
			// display-message -t <id>` does NOT error when <id> is gone; it
			// silently resolves the target to a FALLBACK pane (the client's
			// current pane) and prints that one's fields. Returning it blindly
			// reports pane_alive:true for a pane that has been closed (the #156
			// false positive). Only accept the row when its pane_id matches the
			// id we asked for; a mismatch means the original pane is gone.
			panes := parsePanes(out)
			if len(panes) != 1 {
				return PaneInspection{State: PaneInspectionMalformed, Detail: fmt.Sprintf("unexpected tmux display-message output %q", out)}
			}
			if !isExactPaneID(panes[0].PaneID) {
				return PaneInspection{State: PaneInspectionMalformed, Detail: fmt.Sprintf("tmux returned malformed pane id %q for requested pane %q", panes[0].PaneID, id)}
			}
			if panes[0].PaneID != id {
				return PaneInspection{State: PaneInspectionGone, Detail: fmt.Sprintf("tmux returned fallback pane %q for requested pane %q", panes[0].PaneID, id)}
			}
			return PaneInspection{State: PaneInspectionFound, Pane: panes[0]}
		} else if IsPermissionDenied(err) {
			// Sandboxed: tmux access is denied, not transient — stop retrying.
			return PaneInspection{State: PaneInspectionUnavailable, Detail: err.Error()}
		} else if paneLookupDefinitelyGone(err) {
			return PaneInspection{State: PaneInspectionGone, Detail: err.Error()}
		}
		lastErr = err
		if attempt+1 < tmuxReadAttempts {
			tmuxReadSleep(tmuxReadBackoff)
		}
	}
	detail := fmt.Sprintf("tmux pane %s inspection unavailable after %d attempts", id, tmuxReadAttempts)
	if lastErr != nil {
		detail += ": " + lastErr.Error()
	}
	return PaneInspection{State: PaneInspectionUnavailable, Detail: detail}
}

// InspectPaneByID is the compatibility projection used by existing read-only
// status/focus paths. New destructive policy must consume InspectPaneExactByID
// so unavailable inspection can never be conflated with a gone pane.
func InspectPaneByID(paneID string) (TmuxPane, bool) {
	result := InspectPaneExactByID(paneID)
	return result.Pane, result.State == PaneInspectionFound
}

func isExactPaneID(id string) bool {
	if len(id) < 2 || id[0] != '%' {
		return false
	}
	for _, ch := range id[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// paneLookupDefinitelyGone recognizes only tmux's explicit no-target errors.
// Socket/server failures and generic exit errors are deliberately excluded.
func paneLookupDefinitelyGone(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		msg += " " + strings.ToLower(string(ee.Stderr))
	}
	for _, marker := range []string{"can't find pane", "no such pane", "unknown pane"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// parsePanes parses the tab-separated `tmux list-panes` output. Malformed rows
// (too few fields) are skipped rather than failing the whole parse.
func parsePanes(out string) []TmuxPane {
	var panes []TmuxPane
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			continue
		}
		pid, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
		pane := TmuxPane{
			Session: fields[0],
			Window:  fields[1],
			Pane:    fields[2],
			PID:     pid,
			Command: fields[4],
			CWD:     fields[5],
		}
		// Field order is: session, window_index, pane_index, pane_pid,
		// pane_current_command, pane_current_path, pane_id, window_id,
		// Current field order appends the controlled @amq_squad_title token,
		// pane_title, then window_name. Ten-field and shorter rows are the legacy
		// shape without the controlled token and remain compatible.
		if len(fields) >= 7 {
			pane.PaneID = strings.TrimSpace(fields[6])
		}
		if len(fields) >= 8 {
			pane.WindowID = strings.TrimSpace(fields[7])
		}
		if len(fields) >= 11 && strings.HasPrefix(fields[8], "amqmeta:") {
			pane.DiscoveryToken = strings.TrimSpace(strings.TrimPrefix(fields[8], "amqmeta:"))
			pane.Title = fields[9]
			if pane.DiscoveryToken != "" {
				pane.Title = pane.DiscoveryToken
			}
			pane.WindowName = strings.Join(fields[10:], "\t")
		} else {
			if len(fields) >= 9 {
				pane.Title = fields[8]
			}
			if len(fields) >= 10 {
				pane.WindowName = strings.Join(fields[9:], "\t")
			}
		}
		panes = append(panes, pane)
	}
	return panes
}

// ResolveTmuxTarget matches a running agent to the tmux pane hosting it.
//
// The core difficulty: resurrect/resume rotates PIDs, so the launch-record
// AgentPID is frequently STALE relative to the live pane (proven on this
// machine: launch.json recorded pid 70241 while the live pane_pid was 68476).
// We therefore do NOT require AgentPID to equal pane.PID. Instead we score
// candidate panes:
//
//	(a) REQUIRED: pane.CWD == projectDir AND the pane command matches the
//	    agent engine (codex/claude). This is the durable, rotation-proof signal.
//	(b) PREFERRED: the pane's process subtree (pidTree(pane.PID)) contains the
//	    agent's recorded AgentPID: strongest confirmation when the PID is still
//	    valid, but optional so a stale PID never disqualifies a pane.
//	(c) PREFERRED: the pane's tmux session name equals the amq session name.
//
// Among panes satisfying (a), the highest-scoring is returned. ok is false when
// no pane satisfies the required signal (a).
//
// pidTree may be nil; when nil the (b) bonus is simply not awarded.
//
// Agent does not carry the amq session name, so the (c) tmux-session-name bonus
// is applied only against the agent Handle (the common 1:1 session==handle
// convention). Callers that know the amq session name should use
// ResolveTmuxTargetForSession for the stronger session match.
func ResolveTmuxTarget(a state.Agent, projectDir string, panes []TmuxPane, pidTree func(pid int) []int) (TmuxTarget, bool) {
	return ResolveTmuxTargetForSession(a, "", projectDir, panes, pidTree)
}

// ResolveTmuxTargetForSession is ResolveTmuxTarget with an explicit amq session
// name so the (c) tmux-session-name bonus can be applied. ResolveTmuxTarget
// delegates to this with an empty session hint.
func ResolveTmuxTargetForSession(a state.Agent, sessionName, projectDir string, panes []TmuxPane, pidTree func(pid int) []int) (TmuxTarget, bool) {
	// Name-first pass (highest confidence, engine-agnostic, rotation-proof): if the
	// launcher stamped a deterministic token on a pane title, resolve by an exact
	// title match. This disambiguates agents that share the same cwd AND engine:
	// the bug where cpo·codex and cto·codex in one repo both match the same panes
	// under the cwd+engine scoring below and resolve to whichever pane comes first.
	want := expectedPaneToken(sessionName, a)
	if want != "" {
		for _, p := range panes {
			if p.Title == want {
				return targetFromPane(p), true
			}
		}
	}

	// Fallback: cwd+engine+pid scoring for panes launched before titles existed
	// (or non-amq panes).
	wantCWD := cleanDir(projectDir)
	engine := strings.ToLower(strings.TrimSpace(a.Engine))

	bestScore := -1
	var best TmuxPane
	ok := false

	for _, p := range panes {
		// A pane explicitly stamped for a DIFFERENT amq agent belongs to that
		// agent's role only. The exact-token match already ran above; in the
		// fallback, skip any other amq-tokened pane so a dead agent's request
		// never resolves onto a live sibling that merely shares cwd+engine.
		if want != "" && isAmqPaneToken(p.Title) && p.Title != want {
			continue
		}

		// A PID-lineage match is DEFINITIVE: the agent process literally lives in
		// this pane's process subtree, so this is the agent's pane regardless of
		// the pane's foreground command name (e.g. claude/codex rename their
		// process) or a changed cwd. It therefore BYPASSES the cwd+engine
		// heuristics, which only gate the non-pid fallback for agents launched
		// outside amq-squad's tmux backend (#95).
		pidMatch := a.AgentPID > 0 && pidTree != nil && subtreeContains(pidTree, p.PID, a.AgentPID)
		if !pidMatch {
			if cleanDir(p.CWD) != wantCWD {
				continue
			}
			if !commandMatchesEngine(p.Command, engine) {
				continue
			}
		}

		score := 0
		if pidMatch {
			score += 100
		}
		if sessionName != "" && p.Session == sessionName {
			score += 50
		}
		if a.Handle != "" && p.Session == a.Handle {
			score += 10
		}

		if score > bestScore {
			bestScore = score
			best = p
			ok = true
		}
	}

	if !ok {
		return TmuxTarget{}, false
	}
	return targetFromPane(best), true
}

// isAmqPaneToken reports whether a pane title is a launcher-stamped amq token
// ("amq:<session>:<role>"), i.e. the pane is explicitly owned by a known agent.
func isAmqPaneToken(title string) bool {
	return strings.HasPrefix(title, "amq:")
}

// targetFromPane builds a TmuxTarget from a resolved pane, carrying its title
// token + window name so the cross-session iTerm2 -CC focus can match the native
// window without re-walking the pane list.
func targetFromPane(p TmuxPane) TmuxTarget {
	return TmuxTarget{
		Session:    p.Session,
		Window:     p.Window,
		Pane:       p.Pane,
		Title:      p.Title,
		WindowName: p.WindowName,
	}
}

// expectedPaneToken derives the deterministic pane-title token the launcher
// stamps for an agent: "amq:<sessionName>:<role>". It MUST mirror the launcher's
// paneTitleToken (internal/cli/team_launch_tmux.go), which keys on the member
// role. We fall back to the agent Handle only when Role is empty, so the
// name-first pass still has a chance for older/role-less records. Returns ""
// (skip the name-first pass) when there is no session name or no role/handle.
func expectedPaneToken(sessionName string, a state.Agent) string {
	if strings.TrimSpace(sessionName) == "" {
		return ""
	}
	key := strings.TrimSpace(a.Role)
	if key == "" {
		key = strings.TrimSpace(a.Handle)
	}
	if key == "" {
		return ""
	}
	return "amq:" + sessionName + ":" + key
}

// commandMatchesEngine reports whether a pane command corresponds to the agent
// engine. tmux's pane_current_command is usually a bare basename ("codex",
// "claude", "node"), but be tolerant of paths. An empty engine matches nothing.
func commandMatchesEngine(command, engine string) bool {
	if engine == "" {
		return false
	}
	command = strings.ToLower(strings.TrimSpace(command))
	if command == "" {
		return false
	}
	base := command
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	return base == engine || strings.HasPrefix(base, engine)
}

// subtreeContains walks the process tree rooted at root (via pidTree) and
// reports whether want appears anywhere in it, including root itself. It is
// defensive against cycles and missing pidTree.
func subtreeContains(pidTree func(pid int) []int, root, want int) bool {
	if pidTree == nil || root <= 0 || want <= 0 {
		return false
	}
	if root == want {
		return true
	}
	seen := map[int]bool{}
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if pid == want {
			return true
		}
		for _, child := range pidTree(pid) {
			if child > 0 && !seen[child] {
				stack = append(stack, child)
			}
		}
	}
	return false
}

// cleanDir normalizes a directory path for comparison: absolute + cleaned, with
// any trailing separator removed. Empty stays empty.
func cleanDir(dir string) string {
	if dir == "" {
		return ""
	}
	// Absolutize first, then resolve symlinks, so a member cwd like macOS
	// /var/folders/... (a symlink to /private/var/folders/...) matches what tmux
	// reports for the pane via #{pane_current_path} (the resolved real path).
	// Without this, panes in a symlinked project dir never match and
	// focus/jump/send silently miss. Falls back to Abs+Clean when the path can't
	// be resolved (e.g. it does not exist, as in fake test fixtures).
	abs := dir
	if a, err := filepath.Abs(dir); err == nil {
		abs = a
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return filepath.Clean(abs)
}

// NotInTmuxError is returned by SwitchTo when invoked outside any tmux client
// ($TMUX unset). It carries the suggested command so a UI can show "run this".
type NotInTmuxError struct {
	Target  TmuxTarget
	Command string
}

func (e *NotInTmuxError) Error() string {
	return "not inside tmux; run: " + e.Command
}

// FocusMissError is returned by the cross-session focus path when osascript ran
// cleanly (iTerm2 is scriptable) but found NO native tab whose session name
// matched the token, i.e. the agent's tmux session is not attached to this
// iTerm2, or its tab title no longer carries the token. This is the QA-8 case
// the old code reported as a false "jumped": osascript exits 0 on a no-match, so
// without the sentinel the miss was invisible. Command carries the manual
// `tmux attach`/`switch-client` an operator can run to reach it.
type FocusMissError struct {
	Target  TmuxTarget
	Command string
}

func (e *FocusMissError) Error() string {
	return "could not raise the agent's iTerm2 tab (its tmux session may not be attached here); run: " + e.Command
}

// AttachCommand renders the manual command to reach a target session from
// outside tmux: attach the session in a fresh client.
func AttachCommand(t TmuxTarget) string {
	if t.Session == "" {
		return SuggestJump(t)
	}
	return "tmux attach -t " + t.Session
}

// switchExec is the injectable subprocess seam for the tmux side of SwitchTo.
// Production points it at the real tmux binary; tests swap it for a recorder. It
// is package-level so tests can override it without changing the SwitchTo
// signature.
var switchExec execRunner = defaultExecRunner

// closePaneExec is the injectable subprocess seam for ClosePane; tests swap it
// for a recorder so they never kill a real pane.
var closePaneExec execRunner = defaultExecRunner

// ClosePane closes a single tmux pane by its id (`tmux kill-pane -t <id>`). When
// the pane is the only one in its window tmux closes the window too, so this is
// the right primitive whether the agent was launched into a shared
// current-window split, its own new-window, or a new-session. Unlike the
// read-only resolvers it MUTATES tmux, so callers MUST gate it on the agent
// being down. A blank id is a no-op; an error (e.g. the pane is already gone) is
// returned for the caller to treat as best-effort — teardown never depends on it.
func ClosePane(paneID string) error {
	if strings.TrimSpace(paneID) == "" {
		return nil
	}
	return closePaneExec("tmux", "kill-pane", "-t", paneID)
}

// osascriptExec is the injectable subprocess seam for the iTerm2 native-window
// raise on the CROSS-SESSION focus path (macOS osascript, always present, no
// new dependency, no python). Production runs the real osascript and reads its
// OK/MISS stdout sentinel; tests swap it for a recorder so they assert the EXACT
// argv AND drive the sentinel without spawning anything. When osascript fails
// (or is absent: non-macOS / not iTerm2) SwitchTo degrades to a tmux
// select-window + a NotInTmuxError-style note; when it runs but reports MISS the
// path returns a FocusMissError so the operator sees an honest "couldn't raise
// the tab" instead of a false "jumped".
var osascriptExec osaRunner = defaultOsaRunner

// inTmux reports whether the current process is inside a tmux client. Overridable
// in tests via tmuxEnv.
var tmuxEnv = func() string { return os.Getenv("TMUX") }

// currentTmuxSession reports the tmux session name the operator's client is
// attached to (`tmux display-message -p "#{session_name}"`), used to choose the
// same-session vs cross-session focus branch. It is a package-level seam so a
// test can fix the "current session" without a live tmux. Returns "" when not
// resolvable (no server / not in tmux), which forces the cross-session branch
// (the safe default: never switch-client across sessions).
var currentTmuxSession = func() string {
	out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// SwitchTo moves the OPERATOR'S terminal focus to a resolved target pane. It is
// read-only w.r.t. squad state. Its only effect is terminal focus. The whole
// product runs iTerm2 in tmux CONTROL MODE (-CC), where each tmux WINDOW is its
// own native iTerm2 window/tab, so the focus strategy is split to avoid the
// window-explosion bug:
//
//   - SAME tmux session (target session == the client's current session): focus
//     the window IN-SESSION via `tmux select-window` (+ `tmux select-pane`),
//     NEVER `switch-client`. Same-session select-window under -CC raises the
//     right native tab with NO window explosion.
//   - DIFFERENT tmux session: do NOT use switch-client (that moves the client to
//     the other session and makes -CC spawn a native window per tmux window,
//     the "jumping separates all tabs into their own windows" bug). Instead raise
//     the iTerm2 native window/tab for the pane via osascript, matched by the
//     pane title token (amq:<session>:<role>) or the tmux window name. If
//     osascript fails (not macOS / not iTerm2 / no match) fall back to a
//     best-effort `tmux select-window` and return a *NotInTmuxError-style note
//     carrying the suggested manual command.
//   - Not in tmux at all ($TMUX unset): no switch is run; a best-effort
//     select-window is attempted and a *NotInTmuxError carrying the suggested
//     command is returned so the UI can surface it.
func SwitchTo(t TmuxTarget) error {
	return switchToWithSession(t, currentTmuxSession)
}

// switchToWithSession is SwitchTo with the current-session resolver injected, so
// the same-session vs cross-session branch is testable without a live tmux.
func switchToWithSession(t TmuxTarget, curSession func() string) error {
	spec := targetSpec(t)
	if tmuxEnv() == "" {
		// Not in a tmux client at all: best-effort select-window so an iTerm2 -CC
		// attached window raises, then report the suggested command.
		_ = switchExec("tmux", "select-window", "-t", spec)
		return &NotInTmuxError{Target: t, Command: AttachCommand(t)}
	}

	cur := ""
	if curSession != nil {
		cur = curSession()
	}
	if cur != "" && cur == t.Session {
		// SAME session: select the window (and pane) in place. No switch-client,
		// so iTerm2 -CC does NOT explode the layout.
		if err := switchExec("tmux", "select-window", "-t", spec); err != nil {
			return fmt.Errorf("tmux select-window -t %s: %w", spec, err)
		}
		// Best-effort pane focus within the window (ignore failure: a 1-pane
		// window has nothing to select and tmux errors harmlessly).
		_ = switchExec("tmux", "select-pane", "-t", spec)
		return nil
	}

	// DIFFERENT session (or current session unknown): raise the iTerm2 native
	// window via osascript. NEVER switch-client across sessions. The script
	// prints "OK" when it selected a tab and "MISS" when it scanned every window
	// and matched nothing; the exit code is 0 for BOTH, so we must read stdout.
	out, err := osascriptExec("osascript", iTermActivateArgs(t)...)
	if err == nil && out == focusOK {
		return nil
	}
	if err == nil {
		// osascript ran cleanly but found no matching tab (out == "MISS" or, defensively,
		// any non-OK output). The target session is not attached to this iTerm2: no
		// select-window here can raise a tab that doesn't exist, so do NOT pretend.
		return &FocusMissError{Target: t, Command: SuggestJump(t)}
	}
	// osascript itself failed (not macOS / not iTerm2): degrade to a best-effort
	// tmux select-window and surface the manual command.
	_ = switchExec("tmux", "select-window", "-t", spec)
	return &NotInTmuxError{Target: t, Command: SuggestJump(t)}
}

// focusOK / focusMiss are the stdout sentinels the cross-session AppleScript
// prints so the Go caller can tell a real tab raise from a silent no-match
// (osascript exits 0 for both). They are package constants so the production
// script builder and the tests agree on the exact strings.
const (
	focusOK   = "OK"
	focusMiss = "MISS"
)

// iTermFocusToken is the string the cross-session osascript matches an iTerm2
// tab/window by: the pane title token (amq:<session>:<role>) when present, else
// the tmux window name, else the "session:window" spec. Exported-shaped as a
// helper so the argv is unit-testable.
func iTermFocusToken(t TmuxTarget) string {
	if s := strings.TrimSpace(t.Title); s != "" {
		return s
	}
	if s := strings.TrimSpace(t.WindowName); s != "" {
		return s
	}
	return targetSpec(t)
}

// iTermActivateArgs builds the osascript argv that activates iTerm2 and raises
// the window whose current session's name (the tab title, which iTerm2 -CC sets
// from the tmux pane title) contains the focus token. It is split out so tests
// assert the EXACT argv without spawning osascript. The AppleScript is passed via
// repeated -e lines (no shell, no interpolation surprises): the token is a
// separate -e-quoted literal so a title with spaces stays intact.
func iTermActivateArgs(t TmuxTarget) []string {
	token := iTermFocusToken(t)
	script := `on run argv
set tok to item 1 of argv
tell application "iTerm2"
	activate
	repeat with w in windows
		repeat with tb in tabs of w
			repeat with sess in sessions of tb
				if (name of sess) contains tok then
					select tb
					tell w to select
					return "` + focusOK + `"
				end if
			end repeat
		end repeat
	end repeat
end tell
return "` + focusMiss + `"
end run`
	return []string{"-e", script, token}
}

// SuggestJump returns the human "run this to jump" command for a target. It
// still renders the tmux switch-client form because that is the universally
// recognized manual command an operator can paste, even though SwitchTo itself
// no longer runs switch-client (it uses select-window / osascript to avoid the
// iTerm2 -CC window explosion).
func SuggestJump(t TmuxTarget) string {
	return "tmux switch-client -t " + targetSpec(t)
}

// targetSpec renders a TmuxTarget as tmux's "session:window.pane" addressing.
// Missing window/pane components are omitted gracefully.
func targetSpec(t TmuxTarget) string {
	spec := t.Session
	if t.Window != "" {
		spec += ":" + t.Window
		if t.Pane != "" {
			spec += "." + t.Pane
		}
	}
	return spec
}
