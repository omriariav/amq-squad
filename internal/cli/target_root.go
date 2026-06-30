package cli

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// target_project_root source classification (#290), so the operator can tell an
// explicit value from a proposed-but-unconfirmed resolver match. "resolved" is
// deliberately NOT "confirmed": a single git-remote match may be proposed in a
// preview, but a global_orchestrator run still requires an explicit/confirmed
// path before it edits files.
const (
	targetRootSourceProvided            = "provided"             // operator passed --target-project-root
	targetRootSourceResolvedUnconfirmed = "resolved_unconfirmed" // exactly one git-remote match; needs confirmation
	targetRootSourceUnresolved          = "unresolved"           // none or ambiguous; operator must pass the path
	targetRootSourceDefault             = "default"              // non-global mode; control == target
)

// repoSlugFromGitConfig reads <dir>/.git/config and returns the trailing
// "owner/repo" slug of a configured remote URL (origin preferred), lowercased.
// It is a read-only parse (no git subprocess, no network). ok is false when dir
// is not a git checkout or no remote URL is present.
func repoSlugFromGitConfig(dir string) (string, bool) {
	f, err := os.Open(filepath.Join(dir, ".git", "config"))
	if err != nil {
		return "", false
	}
	defer f.Close()

	var originURL, anyURL string
	section := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(line)
			continue
		}
		if !strings.HasPrefix(strings.ToLower(line), "url") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		url := strings.TrimSpace(line[eq+1:])
		if url == "" {
			continue
		}
		if anyURL == "" {
			anyURL = url
		}
		if strings.Contains(section, `remote "origin"`) {
			originURL = url
		}
	}
	url := originURL
	if url == "" {
		url = anyURL
	}
	slug, ok := repoSlugFromRemoteURL(url)
	return slug, ok
}

// repoSlugFromRemoteURL extracts the trailing "owner/repo" from a git remote URL
// in https, ssh (git@host:owner/repo), or scp-like form, lowercased and with a
// trailing .git stripped.
func repoSlugFromRemoteURL(url string) (string, bool) {
	url = strings.TrimSpace(url)
	if url == "" {
		return "", false
	}
	url = strings.TrimSuffix(url, ".git")
	// Normalize ssh "git@host:owner/repo" to a slash path so the trailing two
	// components are owner/repo regardless of scheme.
	url = strings.ReplaceAll(url, ":", "/")
	parts := strings.Split(strings.Trim(url, "/"), "/")
	if len(parts) < 2 {
		return "", false
	}
	owner := parts[len(parts)-2]
	repo := parts[len(parts)-1]
	if owner == "" || repo == "" {
		return "", false
	}
	return strings.ToLower(owner + "/" + repo), true
}

// classifyDraftTargetProjectRoot decides the preview's target_project_root and
// how it was determined (#290). An explicit value always wins (provided). For
// global_orchestrator with no explicit value it proposes exactly one git-remote
// match as resolved_unconfirmed (a proposal, NOT a confirmation) and otherwise
// reports unresolved with no path — it never silently defaults to cwd. Other
// modes (the lead runs inside the project) keep the cwd default.
func classifyDraftTargetProjectRoot(mode, controlRoot, explicitTarget, ownerRepo string) (target, source string, candidates []string) {
	if strings.TrimSpace(explicitTarget) != "" {
		return cleanRootOrDefault(explicitTarget, ""), targetRootSourceProvided, nil
	}
	if mode != executionModeGlobalOrchestrator {
		return cleanRootOrDefault("", cwdOrEmpty()), targetRootSourceDefault, nil
	}
	cands := resolveTargetProjectRootCandidates(controlRoot, ownerRepo)
	if len(cands) == 1 {
		return cands[0], targetRootSourceResolvedUnconfirmed, cands
	}
	return "", targetRootSourceUnresolved, cands
}

// goalTargetProjectRootLine renders the preview's target_project_root with its
// #290 source, so an unresolved global_orchestrator target is shown honestly
// instead of masquerading as the cwd default the execution contract falls back
// to.
func goalTargetProjectRootLine(data goalDraftData) string {
	switch data.TargetProjectRootSource {
	case targetRootSourceUnresolved:
		return "(unresolved — pass --target-project-root before start)"
	case targetRootSourceResolvedUnconfirmed:
		return data.TargetProjectRoot + " (resolved single git-remote match, UNCONFIRMED — confirm or pass --target-project-root)"
	case targetRootSourceProvided:
		return data.TargetProjectRoot + " (provided)"
	default:
		return data.Execution.TargetProjectRoot
	}
}

// resolveTargetProjectRootCandidates returns local checkouts of ownerRepo found
// by matching the git remote origin slug, scanning controlRoot itself plus its
// immediate child directories ONLY (no recursive crawl, per #290). Directory
// names are never treated as a match. Results are deduplicated and sorted by the
// caller's iteration; the caller treats exactly one match as confident.
func resolveTargetProjectRootCandidates(controlRoot, ownerRepo string) []string {
	controlRoot = strings.TrimSpace(controlRoot)
	ownerRepo = strings.ToLower(strings.TrimSpace(ownerRepo))
	if controlRoot == "" || ownerRepo == "" {
		return nil
	}
	var out []string
	check := func(dir string) {
		if slug, ok := repoSlugFromGitConfig(dir); ok && slug == ownerRepo {
			out = append(out, dir)
		}
	}
	check(controlRoot)
	entries, err := os.ReadDir(controlRoot)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			check(filepath.Join(controlRoot, e.Name()))
		}
	}
	return out
}
