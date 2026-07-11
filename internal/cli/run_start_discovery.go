package cli

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

var (
	versionBranchPattern = regexp.MustCompile(`(?i)(?:^|[/_-])v([0-9]+)[._-]([0-9]+)[._-]([0-9]+)(?:$|[/_-])`)
	issueBranchPattern   = regexp.MustCompile(`(?i)^(?:feat|fix|chore|docs|refactor|test)/([0-9]+)(?:[-_/].*)?$`)
)

func inspectRunStartWizardProject(project string) (runwizard.ProjectContext, error) {
	root, err := nearestGitRoot(project)
	if err != nil {
		return runwizard.ProjectContext{}, err
	}
	ctx := runwizard.ProjectContext{
		Project:           root,
		PreferredBinaries: map[string]string{},
	}
	for _, entry := range catalog.All() {
		ctx.PreferredBinaries[entry.ID] = entry.PreferredBinary
	}
	gitDir := resolveGitDir(root)
	ctx.OriginSlug = gitOriginSlug(readGitOriginURL(gitDir))
	ctx.Branch = readGitBranch(gitDir)
	ctx.SessionSuggestion = suggestRunStartSession(ctx.Branch, root)
	ctx.NewProfileSuggestion = suggestedWizardProfile(ctx.SessionSuggestion)
	ctx.Profiles, err = runStartWizardProfiles(root)
	if err != nil {
		return runwizard.ProjectContext{}, err
	}
	return ctx, nil
}

func nearestGitRoot(start string) (string, error) {
	start = strings.TrimSpace(start)
	if start == "" {
		return "", fmt.Errorf("project directory cannot be empty")
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve project directory: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("project directory does not exist: %s", abs)
	}
	for dir := filepath.Clean(abs); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(abs), nil
		}
	}
}

func resolveGitDir(root string) string {
	dotGit := filepath.Join(root, ".git")
	info, err := os.Stat(dotGit)
	if err != nil || info.IsDir() {
		return dotGit
	}
	body, err := os.ReadFile(dotGit)
	if err != nil {
		return dotGit
	}
	line := strings.TrimSpace(string(body))
	if !strings.HasPrefix(line, "gitdir:") {
		return dotGit
	}
	dir := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return filepath.Clean(dir)
}

func readGitOriginURL(gitDir string) string {
	config := filepath.Join(gitDir, "config")
	if _, err := os.Stat(config); os.IsNotExist(err) {
		if common, readErr := os.ReadFile(filepath.Join(gitDir, "commondir")); readErr == nil {
			config = filepath.Join(gitDir, strings.TrimSpace(string(common)), "config")
		}
	}
	f, err := os.Open(config)
	if err != nil {
		return ""
	}
	defer f.Close()
	inOrigin := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			inOrigin = strings.EqualFold(line, `[remote "origin"]`)
			continue
		}
		if !inOrigin {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "url") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func gitOriginSlug(raw string) string {
	raw = strings.TrimSpace(strings.TrimSuffix(raw, ".git"))
	if raw == "" {
		return ""
	}
	if strings.Contains(raw, "://") {
		parts := strings.SplitN(raw, "://", 2)
		path := parts[1]
		if slash := strings.Index(path, "/"); slash >= 0 {
			return strings.Trim(path[slash+1:], "/")
		}
	}
	if at := strings.Index(raw, "@"); at >= 0 {
		if colon := strings.Index(raw[at:], ":"); colon >= 0 {
			return strings.Trim(raw[at+colon+1:], "/")
		}
	}
	return strings.Trim(raw, "/")
}

func readGitBranch(gitDir string) string {
	body, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(body))
	if !strings.HasPrefix(head, "ref:") {
		return ""
	}
	return strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(head, "ref:")), "refs/heads/")
}

func suggestRunStartSession(branch, project string) string {
	if match := versionBranchPattern.FindStringSubmatch(branch); len(match) == 4 {
		return "v" + match[1] + "-" + match[2] + "-" + match[3]
	}
	if match := issueBranchPattern.FindStringSubmatch(branch); len(match) == 2 {
		return "issue-" + match[1]
	}
	return defaultWorkstreamName(project)
}

func suggestedWizardProfile(session string) string {
	suggestion := sanitizeWorkstreamName("squad-" + strings.TrimSpace(session))
	if suggestion == "" {
		return "squad-project"
	}
	return suggestion
}

func runStartWizardProfiles(project string) ([]runwizard.ProfileSummary, error) {
	var names []string
	if team.Exists(project) {
		names = append(names, team.DefaultProfile)
	}
	named, err := team.ListProfiles(project)
	if err != nil {
		return nil, fmt.Errorf("list team profiles: %w", err)
	}
	names = append(names, named...)
	profiles := make([]runwizard.ProfileSummary, 0, len(names))
	for _, name := range names {
		t, err := team.ReadProfile(project, name)
		if err != nil {
			return nil, fmt.Errorf("read team profile %q: %w", name, err)
		}
		summary := runwizard.ProfileSummary{
			Name:                  name,
			MemberCount:           len(t.Members),
			PinnedSession:         runStartPinnedSession(t),
			Lead:                  t.Lead,
			LeadMode:              team.EffectiveLeadMode(t),
			OperatorMode:          team.EffectiveOperator(t).InteractionMode,
			OperatorNotifications: team.EffectiveOperatorNotifications(t.Operator).Enabled,
		}
		if view := team.EffectiveSelfOperator(t, runStartPinnedSession(t)); t.Operator != nil && t.Operator.InteractionMode == team.OperatorInteractionSelfOperator {
			summary.SelfOperatorLead = view.LeadRole
			summary.SelfOperatorAllow = strings.Join(view.AllowedGateKinds, ",")
			summary.SelfOperatorRevision = view.PolicyRevision
			summary.SelfOperatorPaused = view.Paused
		}
		for _, member := range t.Members {
			summary.Members = append(summary.Members, runwizard.MemberSummary{
				Role:   member.Role,
				Binary: member.Binary,
				Model:  member.Model,
				Effort: memberEffort(member),
			})
		}
		profiles = append(profiles, summary)
	}
	return profiles, nil
}

func runStartPinnedSession(t team.Team) string {
	if inferred := inferredSharedMemberSession(t); inferred != "" {
		return inferred
	}
	return strings.TrimSpace(t.Workstream)
}
