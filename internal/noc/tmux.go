package noc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/state"
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
}

// TmuxTarget identifies a single pane for the jump action.
type TmuxTarget struct {
	Session string
	Window  string
	Pane    string
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

// DefaultPaneLister shells `tmux list-panes -a` with a tab-separated format and
// parses each row into a TmuxPane. It is strictly READ-ONLY. A missing tmux
// binary or no server is reported as an error so callers can degrade.
func DefaultPaneLister() ([]TmuxPane, error) {
	// pane_title is appended last so an empty title (older/non-amq panes) leaves a
	// trailing tab the parser tolerates; it carries the name-first resolution token.
	const format = "#{session_name}\t#{window_index}\t#{pane_index}\t#{pane_pid}\t#{pane_current_command}\t#{pane_current_path}\t#{pane_title}"
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", format).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w", err)
	}
	return parsePanes(string(out)), nil
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
		// pane_title is the optional 7th field; tolerate panes captured without
		// it (older tmux output, or rows that simply have no title).
		if len(fields) >= 7 {
			pane.Title = fields[6]
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
//	    agent's recorded AgentPID — strongest confirmation when the PID is still
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
	// title match. This disambiguates agents that share the same cwd AND engine —
	// the bug where cpo·codex and cto·codex in one repo both match the same panes
	// under the cwd+engine scoring below and resolve to whichever pane comes first.
	if want := expectedPaneToken(sessionName, a); want != "" {
		for _, p := range panes {
			if p.Title == want {
				return TmuxTarget{Session: p.Session, Window: p.Window, Pane: p.Pane}, true
			}
		}
	}

	// Fallback: cwd+engine+pid scoring for panes launched before titles existed
	// (or non-amq panes). Unchanged from the original resolver.
	wantCWD := cleanDir(projectDir)
	engine := strings.ToLower(strings.TrimSpace(a.Engine))

	bestScore := -1
	var best TmuxPane
	ok := false

	for _, p := range panes {
		if cleanDir(p.CWD) != wantCWD {
			continue
		}
		if !commandMatchesEngine(p.Command, engine) {
			continue
		}

		score := 0
		if a.AgentPID > 0 && pidTree != nil && subtreeContains(pidTree, p.PID, a.AgentPID) {
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
	return TmuxTarget{Session: best.Session, Window: best.Window, Pane: best.Pane}, true
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
	if abs, err := filepath.Abs(dir); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(dir)
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

// switchExec is the injectable subprocess seam for SwitchTo. Production points
// it at the real tmux binary; tests swap it for a recorder. It is package-level
// so tests can override it without changing the SwitchTo signature.
var switchExec execRunner = defaultExecRunner

// inTmux reports whether the current process is inside a tmux client. Overridable
// in tests via tmuxEnv.
var tmuxEnv = func() string { return os.Getenv("TMUX") }

// SwitchTo performs the explicit jump to a resolved target.
//
//   - Inside tmux ($TMUX set): `tmux switch-client -t <session>:<window>.<pane>`
//     moves the attached client to the target pane and returns nil.
//   - Inside an iTerm2 -CC control session (tmux running but not the current
//     client's server view) we still emit a `tmux select-window` so the -CC
//     window raises; this path returns a non-nil note (an *NotInTmuxError is
//     NOT used here — see below) only when the select fails.
//   - Not in tmux at all: no subprocess is run; a *NotInTmuxError carrying the
//     suggested command is returned so the UI can surface it.
func SwitchTo(t TmuxTarget) error {
	spec := targetSpec(t)
	if tmuxEnv() != "" {
		if err := switchExec("tmux", "switch-client", "-t", spec); err != nil {
			return fmt.Errorf("tmux switch-client -t %s: %w", spec, err)
		}
		return nil
	}
	// Not in a tmux client: best-effort select-window so an iTerm2 -CC attached
	// window raises the target, then report the suggested command so a UI can
	// guide the operator if select-window did nothing useful.
	_ = switchExec("tmux", "select-window", "-t", spec)
	return &NotInTmuxError{Target: t, Command: SuggestJump(t)}
}

// SuggestJump returns the human "run this to jump" command for a target.
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
