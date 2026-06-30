package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRepoSlugFromRemoteURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/omriariav/amq-squad.git": "omriariav/amq-squad",
		"https://github.com/omriariav/amq-squad":     "omriariav/amq-squad",
		"git@github.com:omriariav/amq-squad.git":     "omriariav/amq-squad",
		"ssh://git@github.com/Owner/Repo.git":        "owner/repo",
	}
	for url, want := range cases {
		got, ok := repoSlugFromRemoteURL(url)
		if !ok || got != want {
			t.Errorf("repoSlugFromRemoteURL(%q) = %q,%v; want %q", url, got, ok, want)
		}
	}
	if _, ok := repoSlugFromRemoteURL(""); ok {
		t.Error("empty url must not yield a slug")
	}
}

func writeGitConfig(t *testing.T, dir, originURL string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "[remote \"origin\"]\n\turl = " + originURL + "\n"
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveTargetProjectRootCandidatesGitRemoteMatch(t *testing.T) {
	control := t.TempDir()
	// One immediate child is a checkout of the repo; another is a different repo;
	// a third has no git config (directory name match must NOT count).
	match := filepath.Join(control, "amq-squad")
	other := filepath.Join(control, "other")
	nameOnly := filepath.Join(control, "owner-repo-lookalike")
	for _, d := range []string{match, other, nameOnly} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeGitConfig(t, match, "git@github.com:omriariav/amq-squad.git")
	writeGitConfig(t, other, "https://github.com/someone/else.git")

	got := resolveTargetProjectRootCandidates(control, "omriariav/amq-squad")
	if len(got) != 1 || got[0] != match {
		t.Fatalf("candidates = %v, want exactly [%s] (git-remote match only)", got, match)
	}

	// Ambiguous: a second checkout of the same repo.
	dup := filepath.Join(control, "amq-squad-clone")
	if err := os.MkdirAll(dup, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitConfig(t, dup, "https://github.com/omriariav/amq-squad")
	if got := resolveTargetProjectRootCandidates(control, "omriariav/amq-squad"); len(got) != 2 {
		t.Fatalf("ambiguous case: candidates = %v, want 2", got)
	}

	// None.
	if got := resolveTargetProjectRootCandidates(control, "nobody/nothing"); len(got) != 0 {
		t.Fatalf("no-match case: candidates = %v, want 0", got)
	}
}

func TestClassifyDraftTargetProjectRoot(t *testing.T) {
	control := t.TempDir()
	match := filepath.Join(control, "amq-squad")
	if err := os.MkdirAll(match, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGitConfig(t, match, "git@github.com:omriariav/amq-squad.git")

	// Explicit value always wins.
	if tgt, src, _ := classifyDraftTargetProjectRoot(executionModeGlobalOrchestrator, control, "/explicit/path", "omriariav/amq-squad"); src != targetRootSourceProvided || tgt == "" {
		t.Fatalf("explicit: tgt=%q src=%q, want provided", tgt, src)
	}
	// global_orchestrator + single git-remote match → resolved_unconfirmed.
	if tgt, src, cands := classifyDraftTargetProjectRoot(executionModeGlobalOrchestrator, control, "", "omriariav/amq-squad"); src != targetRootSourceResolvedUnconfirmed || tgt != match || len(cands) != 1 {
		t.Fatalf("resolved: tgt=%q src=%q cands=%v, want resolved_unconfirmed %s", tgt, src, cands, match)
	}
	// global_orchestrator + no match → unresolved, empty target (never cwd).
	if tgt, src, _ := classifyDraftTargetProjectRoot(executionModeGlobalOrchestrator, control, "", "nobody/nothing"); src != targetRootSourceUnresolved || tgt != "" {
		t.Fatalf("unresolved: tgt=%q src=%q, want unresolved + empty", tgt, src)
	}
	// non-global mode → default (cwd).
	if _, src, _ := classifyDraftTargetProjectRoot(executionModeProjectLead, control, "", "omriariav/amq-squad"); src != targetRootSourceDefault {
		t.Fatalf("non-global: src=%q, want default", src)
	}
}
