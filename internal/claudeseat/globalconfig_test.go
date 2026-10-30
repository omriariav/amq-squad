package claudeseat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fix round: the launch's .claude.json, rewritten the way a test needs it,
// with the digest re-recorded (a launch that wrote it so).
func seatDirWith(t *testing.T, edit func(m map[string]any)) (string, string) {
	t.Helper()
	dir := Dir(t.TempDir(), "c1", "l")
	if _, err := Write(dir, "dontAsk", "/wt/c1", t0); err != nil {
		t.Fatal(err)
	}
	editJSON(t, filepath.Join(dir, ".claude.json"), edit)
	digest, err := Digest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, digest
}

func project(m map[string]any, key string) map[string]any {
	projects, _ := m["projects"].(map[string]any)
	if projects == nil {
		projects = map[string]any{}
		m["projects"] = projects
	}
	e, _ := projects[key].(map[string]any)
	if e == nil {
		e = map[string]any{}
		projects[key] = e
	}
	return e
}

// Check refuses claude_config_unexpected for a projects entry other than
// the seat's trust keys (the canonical repository root, which 2.1.314
// checks first, would trust every sibling worktree) and for a permission
// input anywhere (allowedTools at the top level or in the worktree's entry,
// user or local MCP servers, a global env, a stored key). The detail names
// keys, never a value.
func TestCheckRefusesTrustOrPermissionsBeyondTheLaunch(t *testing.T) {
	const canary = "canary-not-a-secret-value"
	for name, edit := range map[string]func(m map[string]any){
		"canonical root trusted":       func(m map[string]any) { project(m, "/repo")["hasTrustDialogAccepted"] = true },
		"another entry, defaults only": func(m map[string]any) { project(m, "/repo")["allowedTools"] = []any{} },
		"allowedTools at the top":      func(m map[string]any) { m["allowedTools"] = []any{"Bash(*)"} },
		"allowedTools in the worktree": func(m map[string]any) { project(m, "/wt/c1")["allowedTools"] = []any{"Bash(*)"} },
		"local MCP server": func(m map[string]any) {
			project(m, "/wt/c1")["mcpServers"] = map[string]any{"x": map[string]any{"command": canary}}
		},
		"user MCP server": func(m map[string]any) { m["mcpServers"] = map[string]any{"x": map[string]any{"command": canary}} },
		"global env":      func(m map[string]any) { m["env"] = map[string]any{"X": canary} },
		"stored key":      func(m map[string]any) { m["primaryApiKey"] = canary },
		"permissions":     func(m map[string]any) { m["permissions"] = map[string]any{"allow": []any{"Bash(*)"}} },
	} {
		t.Run(name, func(t *testing.T) {
			dir, digest := seatDirWith(t, edit)
			code, detail := Check(dir, os.Getuid(), digest, "dontAsk")
			if code != ConfigUnexpected || strings.Contains(detail, canary) {
				t.Fatalf("want %s with no value in the detail, got %q %s", ConfigUnexpected, code, detail)
			}
		})
	}
}

// What Claude Code 2.1.314 keeps as bookkeeping (its pI and kI sets) passes
// Check and never moves the digest; nor does the entry it writes for the
// canonical repository root with its defaults, or its other state (userID).
func TestBookkeepingKeysPassAndKeepTheDigest(t *testing.T) {
	dir := Dir(t.TempDir(), "c1", "l")
	w, err := Write(dir, "dontAsk", "/wt/c1", t0)
	if err != nil {
		t.Fatal(err)
	}
	editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) {
		m["numStartups"], m["firstStartTime"], m["tipsHistory"] = 1, "2026-10-30T00:00:00Z", map[string]any{"x": 1}
		m["cachedGrowthBookFeatures"] = map[string]any{"f": true}
		e := project(m, "/wt/c1")
		e["lastSessionId"], e["lastCost"], e["hasCompletedProjectOnboarding"] = "s", 0.5, true
	})
	if code, detail := Check(dir, os.Getuid(), w.Digest, "dontAsk"); code != "" {
		t.Fatalf("bookkeeping refused: %s %s", code, detail)
	}
	editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) {
		m["userID"] = "u"
		root := project(m, "/repo")
		root["allowedTools"], root["mcpServers"], root["enabledMcpjsonServers"], root["disabledMcpjsonServers"] = []any{}, map[string]any{}, []any{}, []any{}
		root["mcpContextUris"], root["hasTrustDialogAccepted"], root["hasClaudeMdExternalIncludesApproved"] = []any{}, false, false
		root["lastSessionId"] = "s"
		m["customApiKeyResponses"] = map[string]any{"approved": []any{}, "rejected": []any{}}
		m["env"] = map[string]any{}
	})
	if d, _ := Digest(dir); d != w.Digest {
		t.Fatal("Claude Code's bookkeeping or default entries moved the digest")
	}
}

// The digest covers trust and permission inputs in any project entry and at
// the top level: each one added at run time moves it.
func TestDigestCoversTrustAndPermissionsAnywhere(t *testing.T) {
	for name, edit := range map[string]func(m map[string]any){
		"canonical root trusted":        func(m map[string]any) { project(m, "/repo")["hasTrustDialogAccepted"] = true },
		"allowedTools in another entry": func(m map[string]any) { project(m, "/repo")["allowedTools"] = []any{"Bash(*)"} },
		"allowedTools in the worktree":  func(m map[string]any) { project(m, "/wt/c1")["allowedTools"] = []any{"Bash(*)"} },
		"allowedTools at the top":       func(m map[string]any) { m["allowedTools"] = []any{"Bash(*)"} },
		"a local MCP server":            func(m map[string]any) { project(m, "/repo")["mcpServers"] = map[string]any{"x": map[string]any{}} },
		"a user MCP server":             func(m map[string]any) { m["mcpServers"] = map[string]any{"x": map[string]any{}} },
		"a global env entry":            func(m map[string]any) { m["env"] = map[string]any{"X": "1"} },
		"an approved API key":           func(m map[string]any) { m["customApiKeyResponses"] = map[string]any{"approved": []any{"k"}} },
		"bypass mode accepted":          func(m map[string]any) { m["bypassPermissionsModeAccepted"] = true },
		"external includes approved":    func(m map[string]any) { project(m, "/repo")["hasClaudeMdExternalIncludesApproved"] = true },
		"all project MCP servers":       func(m map[string]any) { project(m, "/repo")["enableAllProjectMcpServers"] = true },
		"an approved .mcp.json server":  func(m map[string]any) { project(m, "/repo")["enabledMcpjsonServers"] = []any{"x"} },
		"a stored key":                  func(m map[string]any) { m["primaryApiKey"] = "k" },
	} {
		t.Run(name, func(t *testing.T) {
			dir := Dir(t.TempDir(), "c1", "l")
			w, err := Write(dir, "dontAsk", "/wt/c1", t0)
			if err != nil {
				t.Fatal(err)
			}
			editJSON(t, filepath.Join(dir, ".claude.json"), edit)
			if d, _ := Digest(dir); d == w.Digest {
				t.Fatalf("%s kept the digest", name)
			}
		})
	}
}
