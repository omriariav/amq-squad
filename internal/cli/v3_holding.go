package cli

import (
	"fmt"
	"os"
	"strings"
	"syscall"

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
