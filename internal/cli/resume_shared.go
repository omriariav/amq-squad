package cli

import (
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// gh#758/t11 slice B commit 3: these helpers survived team_resume.go's
// deletion because other still-live code depends on them, unrelated to the
// classifier that file otherwise existed for:
//   - canonicalPath / compactNativeValue: generic utilities used by task.go,
//     team_lead.go, effort.go, model_defaults.go, resume_goal.go.
//   - findMemberRestoreRecord / projectLeadExternalRecordBoundaryViolation:
//     used by workstream.go and by resume_exec.go's resolveResumeLeadGate
//     (the external_lead_record_dead synthesized required action).
//   - parseResumeRoles / teamRoleList: the --role parsing/error-message
//     helpers plan.go, simple_start.go, and resume.go's exec path all share.

// parseResumeRoles normalizes a comma-separated --role value into a
// deduplicated, lowercased role list.
func parseResumeRoles(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		role := strings.ToLower(strings.TrimSpace(part))
		if role == "" || seen[role] {
			continue
		}
		seen[role] = true
		out = append(out, role)
	}
	return out
}

// teamRoleList returns the team's roles in canonical order, for error messages.
func teamRoleList(t team.Team) []string {
	out := make([]string, 0, len(t.Members))
	for _, m := range orderedTeamMembers(t.Members) {
		out = append(out, m.Role)
	}
	return out
}

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

func projectLeadExternalRecordBoundaryViolation(t team.Team, m team.Member, rec launch.Record, profile, session, root, handle string) bool {
	if !projectExecutionMode(effectiveTeamExecutionMode(t)) {
		return false
	}
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 {
		lead = t.Members[0].Role
	}
	if strings.TrimSpace(m.Role) != lead {
		return false
	}
	if !rec.External {
		return false
	}
	return !launchRecordAuthorizesProjectLead(rec, m.Role, handle, profile, session, root)
}
