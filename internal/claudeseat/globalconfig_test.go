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
		"another entry trusted":        func(m map[string]any) { project(m, "/repo")["hasTrustDialogAccepted"] = true },
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
		"another entry trusted":         func(m map[string]any) { project(m, "/repo")["hasTrustDialogAccepted"] = true },
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

// A worktree path in decomposed Unicode is trusted under its NFC form, the
// key 2.1.314's trust walk compares (it normalizes the cwd to NFC); the
// decomposed form is not written, and Check passes on the dir.
func TestTrustKeysAreNFC(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	decomposed := filepath.Join(base, "café")
	composed := filepath.Join(base, "café")
	if err := os.MkdirAll(decomposed, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := Dir(base, "c1", "l")
	w, err := Write(dir, "dontAsk", decomposed, t0)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	editJSON(t, filepath.Join(dir, ".claude.json"), func(c map[string]any) {
		for k, v := range c {
			m[k] = v
		}
	})
	projects, _ := m["projects"].(map[string]any)
	if _, ok := projects[composed]; !ok || len(projects) != 1 {
		t.Fatalf("trust keys %v, want only the NFC %q", projects, composed)
	}
	if strings.Join(w.Seeded, "|") != `hasCompletedOnboarding|projects["`+composed+`"].hasTrustDialogAccepted` {
		t.Fatalf("seeded %q", w.Seeded)
	}
	digest, _ := Digest(dir)
	if code, detail := Check(dir, os.Getuid(), digest, "dontAsk"); code != "" {
		t.Fatalf("check: %s %s", code, detail)
	}
}

// gitWorktree lays out a main repository and a linked worktree of it the way
// git does (no git run): <repo>/.git/worktrees/wt1 with commondir and
// gitdir, and <wt>/.git naming that gitdir.
func gitWorktree(t *testing.T, base string) (repo, wt string) {
	t.Helper()
	repo, wt = filepath.Join(base, "repo"), filepath.Join(base, "wt")
	admin := filepath.Join(repo, ".git", "worktrees", "wt1")
	for _, d := range []string{admin, wt} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o600)
	os.WriteFile(filepath.Join(admin, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o600)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+admin+"\n"), 0o600)
	return repo, wt
}

// reviewer addition: an entry for the worktree's canonical repository root
// (the key 2.1.314 checks first, which would trust every sibling worktree)
// is refused by its own reason, claude_trust_root_refused, whatever it
// holds; another stray entry stays claude_config_unexpected. The launch
// records the root beside the worktree, and a main checkout has none.
func TestCanonicalRootEntryIsRefusedByName(t *testing.T) {
	for name, tc := range map[string]struct {
		entry map[string]any
		code  string
	}{
		"root trusted":       {map[string]any{"hasTrustDialogAccepted": true}, TrustRootRefused},
		"root defaults only": {map[string]any{"allowedTools": []any{}}, TrustRootRefused},
	} {
		t.Run(name, func(t *testing.T) {
			base, _ := filepath.EvalSymlinks(t.TempDir())
			repo, wt := gitWorktree(t, base)
			dir := Dir(base, "c1", "l")
			if _, err := Write(dir, "dontAsk", wt, t0); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(dir, WorktreeFile))
			if !strings.Contains(string(raw), `"canonical_root": [`) || !strings.Contains(string(raw), repo) {
				t.Fatalf("the launch did not record the root: %s", raw)
			}
			editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) {
				projects := m["projects"].(map[string]any)
				projects[repo] = tc.entry
			})
			digest, _ := Digest(dir)
			if code, detail := Check(dir, os.Getuid(), digest, "dontAsk"); code != tc.code || !strings.Contains(detail, repo) {
				t.Fatalf("want %s naming %s, got %q %s", tc.code, repo, code, detail)
			}
		})
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	_, wt := gitWorktree(t, base)
	dir := Dir(base, "c1", "l")
	if _, err := Write(dir, "dontAsk", wt, t0); err != nil {
		t.Fatal(err)
	}
	editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) {
		m["projects"].(map[string]any)["/elsewhere"] = map[string]any{"hasTrustDialogAccepted": true}
	})
	digest, _ := Digest(dir)
	if code, _ := Check(dir, os.Getuid(), digest, "dontAsk"); code != ConfigUnexpected {
		t.Fatalf("a stray entry: %q", code)
	}
	if got := canonicalRoot(filepath.Join(base, "repo")); got != "" {
		t.Fatalf("a main checkout has a canonical root %q", got)
	}
}

// Reviewer condition (b): every launch is born fresh. A launch's dir that
// Claude Code has used (a canonical-root entry, defaults only or trusted,
// and its own keys) is never written or reused again: a second Write to it
// fails, and Check on it refuses the root entry by name. The next launch of
// the same seat gets a new dir with only the seed, which passes.
func TestEveryLaunchIsBornFresh(t *testing.T) {
	for name, root := range map[string]map[string]any{
		"root entry, defaults only": {"allowedTools": []any{}, "hasTrustDialogAccepted": false, "lastSessionId": "s"},
		"root entry, trusted":       {"hasTrustDialogAccepted": true},
	} {
		t.Run(name, func(t *testing.T) {
			base, _ := filepath.EvalSymlinks(t.TempDir())
			repo, wt := gitWorktree(t, base)
			first := Dir(base, "c1", "launch-1")
			if _, err := Write(first, "dontAsk", wt, t0); err != nil {
				t.Fatal(err)
			}
			editJSON(t, filepath.Join(first, ".claude.json"), func(m map[string]any) {
				m["userID"], m["numStartups"] = "u", 1
				m["projects"].(map[string]any)[repo] = root
			})
			if _, err := Write(first, "dontAsk", wt, t0); err == nil {
				t.Fatal("a used launch dir was written again")
			}
			digest, _ := Digest(first)
			if code, _ := Check(first, os.Getuid(), digest, "dontAsk"); code != TrustRootRefused {
				t.Fatalf("the used dir: %q", code)
			}
			second := Dir(base, "c1", "launch-2")
			w, err := Write(second, "dontAsk", wt, t0)
			if err != nil {
				t.Fatal(err)
			}
			if code, detail := Check(second, os.Getuid(), w.Digest, "dontAsk"); code != "" {
				t.Fatalf("the next launch's dir: %s %s", code, detail)
			}
		})
	}
}
