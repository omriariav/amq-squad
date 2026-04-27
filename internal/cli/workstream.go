package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/internal/team"
)

type bootstrapWorkstream struct {
	Name        string
	Handles     string
	LastTouched string
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

func resolveTeamWorkstreamName(t team.Team, requested string, explicit bool) (string, error) {
	name := strings.TrimSpace(requested)
	if explicit {
		return resolveWorkstreamName(t.Project, name, true)
	}
	if name != "" {
		return resolveWorkstreamName(t.Project, name, false)
	}
	if strings.TrimSpace(t.Workstream) != "" {
		return resolveWorkstreamName(t.Project, t.Workstream, false)
	}
	return resolveWorkstreamName(t.Project, defaultTeamWorkstreamName(t), false)
}

func defaultTeamWorkstreamName(t team.Team) string {
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
	return defaultWorkstreamName(t.Project)
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
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			seen = true
		}
	})
	return seen
}

func teamWorkstreamExists(t team.Team, workstream string) (bool, string, error) {
	seen := map[string]bool{}
	for _, m := range t.Members {
		cwd := m.EffectiveCWD(t.Project)
		handle := memberHandle(m)
		key := cwd + "\x00" + handle
		if seen[key] {
			continue
		}
		seen[key] = true
		root, err := resolveAMQRootInDir(cwd, "", workstream, handle)
		if err != nil {
			return false, "", err
		}
		if _, err := os.Stat(root); err == nil {
			return true, root, nil
		} else if !os.IsNotExist(err) {
			return false, "", err
		}
	}
	return false, "", nil
}

func siblingWorkstreamSummaries(currentRoot, currentSession string) []bootstrapWorkstream {
	if currentRoot == "" || currentSession == "" {
		return nil
	}
	base := filepath.Dir(currentRoot)
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []bootstrapWorkstream
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == currentSession {
			continue
		}
		sessionRoot := filepath.Join(base, e.Name())
		handles := workstreamHandles(sessionRoot)
		if len(handles) == 0 {
			continue
		}
		out = append(out, bootstrapWorkstream{
			Name:        e.Name(),
			Handles:     strings.Join(handles, ", "),
			LastTouched: formatOptionalTime(latestWorkstreamModTime(sessionRoot)),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func workstreamHandles(sessionRoot string) []string {
	agentEntries, err := os.ReadDir(filepath.Join(sessionRoot, "agents"))
	if err != nil {
		return nil
	}
	var handles []string
	for _, e := range agentEntries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(sessionRoot, "agents", e.Name(), "inbox")); err == nil {
			handles = append(handles, e.Name())
		}
	}
	sort.Strings(handles)
	return handles
}

func latestWorkstreamModTime(root string) time.Time {
	var latest time.Time
	observe := func(path string) {
		info, err := os.Stat(path)
		if err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	observe(root)
	agentsRoot := filepath.Join(root, "agents")
	observe(agentsRoot)
	agentEntries, err := os.ReadDir(agentsRoot)
	if err != nil {
		return latest
	}
	for _, e := range agentEntries {
		if !e.IsDir() {
			continue
		}
		agentRoot := filepath.Join(agentsRoot, e.Name())
		observe(agentRoot)
		for _, name := range []string{"inbox", "outbox", "acks", "receipts", "dlq"} {
			observe(filepath.Join(agentRoot, name))
		}
	}
	return latest
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04")
}
