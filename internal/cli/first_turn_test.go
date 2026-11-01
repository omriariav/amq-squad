package cli

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// Brief 31-3 PR 2, section 5 (row 4's verb half): context --host carries
// the host facts the team lead's first turn states: the repository, its
// branch and tip from git, and the installed AMQ's version; without the
// flag the document has no host; a fact that cannot be read is empty, never
// guessed. git and amq run through a seam.
func TestContextHostCarriesTheHostFactsAndLeavesAMissingOneEmpty(t *testing.T) {
	f := newHookFixture(t)
	t.Setenv("AMQ_SQUAD_PROJECT", f.project)
	t.Setenv("AMQ_SQUAD_AMQ", "/opt/fake/amq")
	var ran []string
	prev := contextHostRun
	t.Cleanup(func() { contextHostRun = prev })
	gitOK := true
	branch := "v3-31-3b"
	contextHostRun = func(dir, name string, args ...string) (string, error) {
		ran = append(ran, dir+" "+name+" "+strings.Join(args, " "))
		switch {
		case name == "git" && !gitOK:
			return "", errors.New("not a git repository")
		case name == "git" && args[1] == "--abbrev-ref":
			return branch, nil
		case name == "git":
			return "95b12207f9a3", nil
		case name == "/opt/fake/amq":
			return "amq 0.116.2\nbuilt by ci", nil
		}
		return "", errors.New("unexpected")
	}
	host := func(args ...string) map[string]any {
		t.Helper()
		stdout, _, err := captureOutput(t, func() error { return Run(append([]string{"context"}, args...), "test") })
		if err != nil {
			t.Fatalf("context %v: %v", args, err)
		}
		var env struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal([]byte(stdout), &env); err != nil {
			t.Fatalf("context json: %v\n%s", err, stdout)
		}
		h, _ := env.Data["host"].(map[string]any)
		return h
	}
	if h := host("--json"); h != nil || len(ran) != 0 {
		t.Fatalf("context without --host: host %v, ran %q", h, ran)
	}
	h := host("--json", "--host")
	if h["repository"] != f.project || h["branch"] != "v3-31-3b" || h["tip"] != "95b12207f9a3" || h["amq_version"] != "amq 0.116.2" {
		t.Fatalf("host = %v", h)
	}
	gitOK = false
	if h := host("--json", "--host"); h["branch"] != "" || h["tip"] != "" || h["amq_version"] != "amq 0.116.2" {
		t.Fatalf("host without git = %v", h)
	}
	// Codex on #837, item 6: a credential in a branch name never leaves
	// context --host (the canary matches no redact.RealCredentialShapes)
	canary := "Zq9" + "xV7mP2wL4"
	gitOK = true
	branch = "feature/api_key=" + canary
	if h := host("--json", "--host"); h["branch"] != "feature/api_key=<redacted>" {
		t.Fatalf("a credential-named branch = %v", h["branch"])
	}
}
