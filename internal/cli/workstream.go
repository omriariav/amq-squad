package cli

import (
	"flag"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// filterMembersBySession partitions members into active and skipped groups for a
// given target workstream.
func filterMembersBySession(members []team.Member, workstream string) (active, skipped []team.Member) {
	for _, m := range members {
		if m.Session == "" || m.Session == workstream {
			active = append(active, m)
		} else {
			skipped = append(skipped, m)
		}
	}
	return active, skipped
}

func defaultWorkstreamName(projectDir string) string {
	base := filepath.Base(projectDir)
	if base == "." || base == string(filepath.Separator) || base == "" {
		base = "project"
	}
	return sanitizeWorkstreamName(base)
}

func resolveWorkstreamName(projectDir, requested string, explicit bool) (string, error) {
	name := strings.TrimSpace(requested)
	if explicit && name == "" {
		return "", fmt.Errorf("session name cannot be empty")
	}
	if name == "" {
		return defaultWorkstreamName(projectDir), nil
	}
	if err := validateWorkstreamName(name); err != nil {
		return "", err
	}
	return name, nil
}

// resolveTeamWorkstreamName resolves the live workstream session for a team
// invocation.
func resolveTeamWorkstreamName(t team.Team, requested string, explicit bool) (string, error) {
	name := strings.TrimSpace(requested)
	if explicit {
		return resolveWorkstreamName(t.Project, name, true)
	}
	if name != "" {
		return resolveWorkstreamName(t.Project, name, false)
	}
	// Tier 1: member-session inference wins over the pin. No notice.
	if inferred := inferredSharedMemberSession(t); inferred != "" {
		return resolveWorkstreamName(t.Project, inferred, false)
	}
	// Tier 2: the deprecated pin shim. Still read, but emit a notice.
	if pinned := strings.TrimSpace(t.Workstream); pinned != "" {
		resolved, err := resolveWorkstreamName(t.Project, pinned, false)
		if err != nil {
			return "", err
		}
		quietNotice("notice: using session %q from the deprecated 'workstream' default pinned in team.json. "+
			"This pin is deprecated and will be removed in 2.1; pass --session %s explicitly (or re-init the team) instead.\n",
			resolved, resolved)
		return resolved, nil
	}
	// Tier 3: sanitized project basename.
	return resolveWorkstreamName(t.Project, defaultWorkstreamName(t.Project), false)
}

// inferredSharedMemberSession returns the workstream session shared by every
// member when it is not merely a copy of a role or handle, else "".
func inferredSharedMemberSession(t team.Team) string {
	unique := ""
	shared := true
	hasNonLegacyMember := false
	for _, m := range t.Members {
		session := strings.TrimSpace(m.Session)
		if session == "" {
			shared = false
			continue
		}
		if unique == "" {
			unique = session
		} else if session != unique {
			shared = false
		}
		handle := memberHandle(m)
		if session != m.Role && session != handle {
			hasNonLegacyMember = true
		}
	}
	if shared && unique != "" && hasNonLegacyMember {
		return unique
	}
	return ""
}

func validateWorkstreamName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid session name %q: AMQ session names allow lowercase a-z, 0-9, - and _ only; replace dots, spaces, or uppercase with -", name)
	}
	return nil
}

func sanitizeWorkstreamName(s string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(s) {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "project"
	}
	return out
}

func canonicalP2PThread(a, b string) string {
	handles := []string{a, b}
	sort.Strings(handles)
	return "p2p/" + handles[0] + "__" + handles[1]
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	seen := false
	alias := scopedFlagAlias(name)
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name || (alias != "" && f.Name == alias) {
			seen = true
		}
	})
	return seen
}

func scopedFlagAlias(name string) string {
	switch name {
	case "project":
		return "p"
	case "session":
		return "s"
	case "profile":
		return "P"
	default:
		return ""
	}
}

// registerScopedFlagAliases wires the conventional scoped short flags to the
// same backing variables as their long forms. Keep all -p/-s/-P registrations
// here so scoped verbs cannot drift.
func registerScopedFlagAliases(fs *flag.FlagSet, project, session, profile *string) {
	if project != nil {
		fs.StringVar(project, "p", *project, "alias for --project")
	}
	if session != nil {
		fs.StringVar(session, "s", *session, "alias for --session")
	}
	if profile != nil {
		fs.StringVar(profile, "P", *profile, "alias for --profile")
	}
}
