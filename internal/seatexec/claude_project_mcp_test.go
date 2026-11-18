package seatexec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// sbxGit runs git in dir with no global or system config.
func sbxGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// 3.3.1 item 5, D5 (c): a Claude seat is never exec'd while its worktree or
// its main checkout enables project MCP servers or plugins: MCP servers run
// outside the guard, and AMQ does not admit --strict-mcp-config. The
// refusal names the fix.
func TestSbxAClaudeSeatIsNotExecdWithAnEnabledProjectMCPConfig(t *testing.T) {
	type plant struct{ rel, body string }
	cases := []struct {
		name   string
		inMain bool
		files  []plant
	}{
		{"worktree .mcp.json with a server", false, []plant{{".mcp.json", `{"mcpServers":{"fs":{"command":"npx","args":["fs-server","/"]}}}`}}},
		{"worktree settings enableAllProjectMcpServers", false, []plant{{".claude/settings.json", `{"enableAllProjectMcpServers":true}`}}},
		{"worktree local settings enabledMcpjsonServers", false, []plant{{".claude/settings.local.json", `{"enabledMcpjsonServers":["fs"]}`}}},
		{"worktree settings enable a plugin", false, []plant{{".claude/settings.json", `{"enabledPlugins":{"fs-tools@acme":true}}`}}},
		{"worktree settings register a marketplace", false, []plant{{".claude/settings.local.json", `{"extraKnownMarketplaces":{"acme":{"source":{"source":"directory","path":"./m"}}}}`}}},
		{"worktree skills-directory plugin with mcpServers", false, []plant{{".claude/skills/fs/.claude-plugin/plugin.json", `{"name":"fs","mcpServers":{"fs":{"command":"npx"}}}`}}},
		{"worktree skills-directory plugin with a .mcp.json", false, []plant{{".claude/skills/fs/.claude-plugin/plugin.json", `{"name":"fs"}`}, {".claude/skills/fs/.mcp.json", `{"mcpServers":{"fs":{"command":"npx"}}}`}}},
		{"worktree .mcp.json unreadable", false, []plant{{".mcp.json", `{not json`}}},
		{"main checkout .mcp.json with a server", true, []plant{{".mcp.json", `{"mcpServers":{"fs":{"command":"npx"}}}`}}},
		{"main checkout local settings enableAllProjectMcpServers", true, []plant{{".claude/settings.local.json", `{"enableAllProjectMcpServers":true}`}}},
		{"main checkout project settings enable a plugin", true, []plant{{".claude/settings.json", `{"enabledPlugins":{"fs-tools@acme":true}}`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, "claude")
			base := f.wt
			if tc.inMain {
				if _, err := exec.LookPath("git"); err != nil {
					t.Skip("git is not on PATH")
				}
				main := filepath.Join(filepath.Dir(f.wt), "main")
				if err := os.MkdirAll(main, 0o700); err != nil {
					t.Fatal(err)
				}
				sbxGit(t, main, "init", "-q", "-b", "main")
				sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
				wt := filepath.Join(main, ".worktrees", "cto")
				sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
				f.wt, f.rec.Worktree, base = wt, wt, main
			}
			for _, p := range tc.files {
				full := filepath.Join(base, filepath.FromSlash(p.rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				write(t, full, p.body, 0o600)
			}
			f.stage()
			err := f.run(Options{})
			if refusalCode(err) == "" || f.execPath != "" {
				t.Fatalf("exec %q, err %v", f.execPath, err)
			}
			for _, w := range []string{"project MCP servers", "--strict-mcp-config", "Codex seat"} {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("the refusal does not name the fix (%q): %v", w, err)
				}
			}
		})
	}
	// nothing enabled: an empty server map, unrelated settings, a plugin
	// set false, a skills-directory plugin without MCP
	f := newFixture(t, "claude")
	for rel, body := range map[string]string{
		".mcp.json":                   `{"mcpServers":{}}`,
		".claude/settings.json":       `{"model":"x","enabledPlugins":{"fs-tools@acme":false},"enableAllProjectMcpServers":false,"enabledMcpjsonServers":[],"extraKnownMarketplaces":{}}`,
		".claude/skills/fmt/SKILL.md": "---\nname: fmt\ndescription: format files\n---\nRun the formatter.\n",
		".claude/skills/README.md":    "# skills\n",
		".claude/agents/plain.md":     "---\nname: plain\ndescription: no servers\n---\nThis agent mentions mcpServers: only in its body.\n",
	} {
		full := filepath.Join(f.wt, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, full, body, 0o600)
	}
	f.stage()
	if err := f.run(Options{}); err != nil || f.execPath == "" {
		t.Errorf("a worktree enabling nothing: exec %q, err %v", f.execPath, err)
	}
}

// Codex pass 1 on 3.3.1 item 5 (D5 (c)): the check reads the right files
// whatever the layout (glob characters in the checkout path, a bare
// repository's worktree, a relative gitdir pointer) and never hangs or
// reads without bound on a file the seat planted (a FIFO, /dev/zero), and a
// file it cannot read counts as enabling.
func TestSbxTheProjectMCPCheckHoldsForEveryLayout(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	linked := func(t *testing.T, f *fixture, mainName string) (main, wt string) {
		main = filepath.Join(filepath.Dir(f.wt), mainName)
		if err := os.MkdirAll(main, 0o700); err != nil {
			t.Fatal(err)
		}
		sbxGit(t, main, "init", "-q", "-b", "main")
		sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
		wt = filepath.Join(main, ".worktrees", "cto")
		sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
		f.wt, f.rec.Worktree = wt, wt
		return main, wt
	}
	// refusedNaming: refused, and the refusal names what (the reason that
	// drew it, not another layer's)
	var refusedNaming func(t *testing.T, f *fixture, what string)
	refused := func(t *testing.T, f *fixture) {
		t.Helper()
		refusedNaming(t, f, "")
	}
	refusedNaming = func(t *testing.T, f *fixture, what string) {
		t.Helper()
		f.stage()
		done := make(chan error, 1)
		go func() { done <- f.run(Options{}) }()
		select {
		case err := <-done:
			if refusalCode(err) != "claude_project_mcp" || f.execPath != "" {
				t.Fatalf("exec %q, err %v", f.execPath, err)
			}
			if what != "" && !strings.Contains(err.Error(), what) {
				t.Fatalf("the refusal does not name %s: %v", what, err)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("seat-exec did not return within 20 s")
		}
	}
	t.Run("glob characters in the main checkout path", func(t *testing.T) {
		f := newFixture(t, "claude")
		main, _ := linked(t, f, "proj[1]")
		mf := filepath.Join(main, ".claude", "skills", "fs", ".claude-plugin", "plugin.json")
		if err := os.MkdirAll(filepath.Dir(mf), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, mf, `{"name":"fs","mcpServers":{"fs":{"command":"npx"}}}`, 0o600)
		refused(t, f)
	})
	t.Run("a bare repository's worktree", func(t *testing.T) {
		f := newFixture(t, "claude")
		bare := filepath.Join(filepath.Dir(f.wt), "repo.git")
		sbxGit(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
		wt := filepath.Join(filepath.Dir(f.wt), "bare-wt")
		sbxGit(t, bare, "worktree", "add", "-q", "--orphan", "-b", "seat/cto", wt)
		f.wt, f.rec.Worktree = wt, wt
		if err := os.MkdirAll(filepath.Join(bare, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(bare, ".claude", "settings.local.json"), `{"enableAllProjectMcpServers":true}`, 0o600)
		refusedNaming(t, f, filepath.Join(bare, ".claude", "settings.local.json")+" sets enableAllProjectMcpServers")
	})
	t.Run("a relative gitdir pointer", func(t *testing.T) {
		f := newFixture(t, "claude")
		main, wt := linked(t, f, "main")
		gitdir := filepath.Join(main, ".git", "worktrees", "cto")
		rel, err := filepath.Rel(wt, gitdir)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(wt, ".git"), "gitdir: "+rel+"\n", 0o600)
		// a file only the main checkout's discovery finds (an ancestor's
		// .mcp.json would be found by the ancestor scan as well)
		if err := os.MkdirAll(filepath.Join(main, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(main, ".claude", "settings.local.json"), `{"enableAllProjectMcpServers":true}`, 0o600)
		refusedNaming(t, f, filepath.Join(main, ".claude", "settings.local.json")+" sets enableAllProjectMcpServers")
	})
	t.Run("a FIFO", func(t *testing.T) {
		f := newFixture(t, "claude")
		if err := syscall.Mkfifo(filepath.Join(f.wt, ".mcp.json"), 0o600); err != nil {
			t.Skip(err)
		}
		refused(t, f)
	})
	t.Run("a link to /dev/zero", func(t *testing.T) {
		f := newFixture(t, "claude")
		if err := os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("/dev/zero", filepath.Join(f.wt, ".claude", "settings.json")); err != nil {
			t.Fatal(err)
		}
		refused(t, f)
	})
	// Codex pass 2: what Claude Code reads, read as it reads it
	for name, plant := range map[string]struct{ rel, body string }{
		"a project .mcp.json as a flat map":             {".mcp.json", `{"fs":{"command":"/tmp/server"}}`},
		"a case-variant key masking mcpServers":         {".claude/skills/fs/.claude-plugin/plugin.json", `{"name":"fs","mcpServers":{"fs":{"command":"/tmp/server"}},"MCPSERVERS":null}`},
		"a project .mcp.json masking mcpServers":        {".mcp.json", `{"mcpServers":{"fs":{"command":"/tmp/server"}},"MCPServers":{}}`},
		"enableAllProjectMcpServers as a truthy string": {".claude/settings.json", `{"enableAllProjectMcpServers":"yes"}`},
		"a plugin enabled by a truthy non-boolean":      {".claude/settings.json", `{"enabledPlugins":{"fs@acme":1}}`},
		"settings masking enableAllProjectMcpServers":   {".claude/settings.local.json", `{"enableAllProjectMcpServers":true,"EnableAllProjectMcpServers":false}`},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			full := filepath.Join(f.wt, filepath.FromSlash(plant.rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
				t.Fatal(err)
			}
			write(t, full, plant.body, 0o600)
			refused(t, f)
		})
	}
	// Codex pass 4: a config path that is a symlink (here dangling, or to a
	// file outside the checkout) points the settings where the guard cannot
	// hold them; and an unreadable plugin manifest counts as enabling
	for name, link := range map[string]string{
		"a symlinked .claude directory (dangling)": ".claude",
		"a symlinked local settings file":          ".claude/settings.local.json",
		"a symlinked .mcp.json":                    ".mcp.json",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			p := filepath.Join(f.wt, filepath.FromSlash(link))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(filepath.Dir(f.wt), "elsewhere", "future"), p); err != nil {
				t.Fatal(err)
			}
			refused(t, f)
		})
	}
	// Codex pass 5: a settings.json that links to an ordinary file outside
	// the checkout, in the worktree and in the main checkout; and a
	// .mcp.json whose mcpServers is null beside a server (Claude Code's
	// loader then takes the whole object as the map)
	for _, inMain := range []bool{false, true} {
		t.Run(fmt.Sprintf("a settings.json linked to an ordinary file (main %v)", inMain), func(t *testing.T) {
			f := newFixture(t, "claude")
			base := f.wt
			if inMain {
				base, _ = linked(t, f, "main")
			}
			outside := filepath.Join(filepath.Dir(f.root), "outside.json")
			write(t, outside, "{}", 0o600)
			if err := os.MkdirAll(filepath.Join(base, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(base, ".claude", "settings.json")); err != nil {
				t.Fatal(err)
			}
			refused(t, f)
		})
	}
	t.Run("mcpServers null beside a server", func(t *testing.T) {
		f := newFixture(t, "claude")
		p := filepath.Join(f.wt, ".mcp.json")
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, p, `{"mcpServers":null,"fs":{"command":"/tmp/server"}}`, 0o600)
		refused(t, f)
	})
	// Codex pass 6: a project agent whose frontmatter declares mcpServers
	// (top level, in a subfolder, quoted, in the main checkout), and a
	// config file with a hard link elsewhere
	for name, plant := range map[string]struct {
		rel    string
		inMain bool
		body   string
	}{
		"an agent with inline mcpServers": {".claude/agents/fs.md", false, "---\nname: fs\ndescription: files\nmcpServers:\n  - fs:\n      command: /tmp/server\n---\nHelp.\n"},
		"an agent in a subfolder":         {".claude/agents/review/fs.md", false, "---\nname: fs\nmcpServers: [{fs: {command: /tmp/server}}]\n---\n"},
		"an agent with a quoted key":      {".claude/agents/fs.md", false, "---\nname: fs\n\"mcpServers\" :\n  - fs: {command: /tmp/server}\n---\n"},
		"an agent in the main checkout":   {".claude/agents/fs.md", true, "---\nname: fs\nmcpServers:\n  - fs: {command: /tmp/server}\n---\n"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			base := f.wt
			if plant.inMain {
				base, _ = linked(t, f, "main")
			}
			full := filepath.Join(base, filepath.FromSlash(plant.rel))
			if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
				t.Fatal(err)
			}
			write(t, full, plant.body, 0o600)
			refused(t, f)
		})
	}
	t.Run("a hard-linked settings file", func(t *testing.T) {
		f := newFixture(t, "claude")
		if err := os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		alias := filepath.Join(filepath.Dir(f.root), "alias.json")
		write(t, alias, "{}", 0o600)
		if err := os.Link(alias, filepath.Join(f.wt, ".claude", "settings.local.json")); err != nil {
			t.Fatal(err)
		}
		refused(t, f)
	})
	// Codex pass 7: git metadata with another name still leads to the main
	// checkout; a pointer that leads nowhere refuses; a symlinked agents or
	// skills directory, or a link inside it, refuses; an agent's
	// frontmatter that could spell mcpServers (a flow mapping, an escape)
	// refuses; every hard-linked config file refuses
	t.Run("a hard-linked .git pointer", func(t *testing.T) {
		f := newFixture(t, "claude")
		main, wt := linked(t, f, "main")
		if err := os.Link(filepath.Join(wt, ".git"), filepath.Join(filepath.Dir(main), "git-alias")); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(main, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(main, ".claude", "settings.local.json"), `{"enableAllProjectMcpServers":true}`, 0o600)
		refused(t, f)
	})
	t.Run("a --separate-git-dir checkout enabling nothing", func(t *testing.T) {
		f := newFixture(t, "claude")
		wt := filepath.Join(filepath.Dir(f.wt), "sep-wt")
		sbxGit(t, filepath.Dir(f.wt), "init", "-q", "-b", "main", "--separate-git-dir", filepath.Join(filepath.Dir(f.wt), "sep.git"), wt)
		f.wt, f.rec.Worktree = wt, wt
		f.stage()
		if err := f.run(Options{}); err != nil || f.execPath == "" {
			t.Fatalf("a valid separate-git-dir checkout: exec %q, err %v", f.execPath, err)
		}
	})
	t.Run("a symlinked skills directory", func(t *testing.T) {
		f := newFixture(t, "claude")
		elsewhere := filepath.Join(filepath.Dir(f.root), "skills-elsewhere")
		if err := os.MkdirAll(elsewhere, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(f.wt, ".claude", "skills")); err != nil {
			t.Fatal(err)
		}
		refused(t, f)
	})
	// Codex pass 9: an ancestor's .mcp.json, which Claude Code merges; a
	// truthy value of another JSON type; a hard-linked commondir in a valid
	// worktree that enables nothing
	t.Run("an ancestor's .mcp.json", func(t *testing.T) {
		f := newFixture(t, "claude")
		main, wt := linked(t, f, "main")
		p := filepath.Join(filepath.Dir(wt), ".mcp.json")
		write(t, p, `{"mcpServers":{"fs":{"command":"/tmp/server"}}}`, 0o600)
		_ = main
		refusedNaming(t, f, p+" declares")
	})
	// Codex pass 10: an ancestor two levels up, and a symlinked ancestor
	t.Run("a .mcp.json two levels above the worktree", func(t *testing.T) {
		f := newFixture(t, "claude")
		_, wt := linked(t, f, "main")
		p := filepath.Join(filepath.Dir(filepath.Dir(wt)), "..", ".mcp.json")
		p = filepath.Clean(p)
		write(t, p, `{"mcpServers":{"fs":{"command":"/tmp/server"}}}`, 0o600)
		refusedNaming(t, f, p+" declares")
	})
	t.Run("a symlinked ancestor .mcp.json", func(t *testing.T) {
		f := newFixture(t, "claude")
		_, wt := linked(t, f, "main")
		target := filepath.Join(filepath.Dir(f.root), "current-config.json")
		write(t, target, "{}", 0o600)
		p := filepath.Join(filepath.Dir(wt), ".mcp.json")
		if err := os.Symlink(target, p); err != nil {
			t.Fatal(err)
		}
		refusedNaming(t, f, p+" is a symlink")
	})
	for name, body := range map[string]string{
		"enabledMcpjsonServers as a string":  `{"enabledMcpjsonServers":"fs"}`,
		"extraKnownMarketplaces as a string": `{"extraKnownMarketplaces":"acme"}`,
		"enableAllProjectMcpServers as 1":    `{"enableAllProjectMcpServers":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			if err := os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(f.wt, ".claude", "settings.json"), body, 0o600)
			refused(t, f)
		})
	}
	t.Run("a hard-linked commondir in a valid worktree", func(t *testing.T) {
		f := newFixture(t, "claude")
		main, _ := linked(t, f, "main")
		if err := os.Link(filepath.Join(main, ".git", "worktrees", "cto", "commondir"), filepath.Join(filepath.Dir(main), "commondir-alias")); err != nil {
			t.Fatal(err)
		}
		f.stage()
		if err := f.run(Options{}); err != nil || f.execPath == "" {
			t.Fatalf("a valid worktree with a hard-linked commondir: exec %q, err %v", f.execPath, err)
		}
	})
	// Codex pass 11: a skills-directory plugin, whatever it carries (here
	// an agent with inline servers and a bare manifest), and a worktree
	// recorded through a symlink, whose real ancestors Claude Code walks
	t.Run("a skills-directory plugin with an agent", func(t *testing.T) {
		f := newFixture(t, "claude")
		dir := filepath.Join(f.wt, ".claude", "skills", "demo")
		if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "agents"), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(dir, ".claude-plugin", "plugin.json"), `{"name":"demo"}`, 0o600)
		write(t, filepath.Join(dir, "agents", "fs.md"), "---\nname: fs\nmcpServers: [{fs: {command: /tmp/server}}]\n---\n", 0o600)
		refusedNaming(t, f, dir+" is a plugin")
	})
	t.Run("a worktree recorded through a symlink", func(t *testing.T) {
		f := newFixture(t, "claude")
		top := filepath.Dir(f.wt)
		real := filepath.Join(top, "data", "project", "repo")
		if err := os.MkdirAll(real, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(top, "data", "project"), filepath.Join(top, "short")); err != nil {
			t.Fatal(err)
		}
		f.wt = filepath.Join(top, "short", "repo")
		f.rec.Worktree = f.wt
		p := filepath.Join(top, "data", "project", ".mcp.json")
		write(t, p, `{"mcpServers":{"fs":{"command":"/tmp/server"}}}`, 0o600)
		refusedNaming(t, f, p+" declares")
	})
	// Codex pass 12: a commondir naming the common directory through an
	// alias is a valid layout; a seat beside (not inside) the main
	// checkout still has the main checkout's .mcp.json checked
	t.Run("a commondir through an alias", func(t *testing.T) {
		f := newFixture(t, "claude")
		main, _ := linked(t, f, "main")
		alias := filepath.Join(filepath.Dir(main), "main-alias")
		if err := os.Symlink(main, alias); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(main, ".git", "worktrees", "cto", "commondir"), filepath.Join(alias, ".git")+"\n", 0o600)
		f.stage()
		if err := f.run(Options{}); err != nil || f.execPath == "" {
			t.Fatalf("a valid worktree whose commondir names an alias: exec %q, err %v", f.execPath, err)
		}
	})
	t.Run("a seat beside the main checkout", func(t *testing.T) {
		f := newFixture(t, "claude")
		main := filepath.Join(filepath.Dir(f.wt), "main")
		if err := os.MkdirAll(main, 0o700); err != nil {
			t.Fatal(err)
		}
		sbxGit(t, main, "init", "-q", "-b", "main")
		sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
		wt := filepath.Join(filepath.Dir(f.wt), "beside", "cto")
		sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
		f.wt, f.rec.Worktree = wt, wt
		write(t, filepath.Join(main, ".mcp.json"), `{"mcpServers":{"fs":{"command":"/tmp/server"}}}`, 0o600)
		refusedNaming(t, f, filepath.Join(main, ".mcp.json")+" declares")
	})
	t.Run("a .git pointer that leads nowhere", func(t *testing.T) {
		f := newFixture(t, "claude")
		write(t, filepath.Join(f.wt, ".git"), "gitdir: "+filepath.Join(filepath.Dir(f.wt), "nowhere")+"\n", 0o600)
		refused(t, f)
	})
	for name, plant := range map[string]func(t *testing.T, wt, elsewhere string){
		"a symlinked agents directory": func(t *testing.T, wt, elsewhere string) {
			if err := os.Symlink(elsewhere, filepath.Join(wt, ".claude", "agents")); err != nil {
				t.Fatal(err)
			}
		},
		"a symlink inside the agents directory": func(t *testing.T, wt, elsewhere string) {
			if err := os.MkdirAll(filepath.Join(wt, ".claude", "agents"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, filepath.Join(wt, ".claude", "agents", "more")); err != nil {
				t.Fatal(err)
			}
		},
		"a symlinked skills plugin": func(t *testing.T, wt, elsewhere string) {
			if err := os.MkdirAll(filepath.Join(wt, ".claude", "skills"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, filepath.Join(wt, ".claude", "skills", "fs")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			elsewhere := filepath.Join(filepath.Dir(f.root), "elsewhere-dir")
			if err := os.MkdirAll(elsewhere, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(f.wt, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			plant(t, f.wt, elsewhere)
			refused(t, f)
		})
	}
	for name, body := range map[string]string{
		"an agent's flow-mapping frontmatter": "---\n{name: fs, description: files, mcpServers: [{fs: {command: /tmp/server}}]}\n---\n",
		"an agent's escaped key":              "---\nname: fs\n\"\\x6dcpServers\": [{fs: {command: /tmp/server}}]\n---\n",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			p := filepath.Join(f.wt, ".claude", "agents", "fs.md")
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			write(t, p, body, 0o600)
			refused(t, f)
		})
	}
	for _, rel := range []string{".claude/settings.json", ".mcp.json"} {
		t.Run("a hard-linked "+rel, func(t *testing.T) {
			f := newFixture(t, "claude")
			p := filepath.Join(f.wt, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			alias := filepath.Join(filepath.Dir(f.root), "alias-"+filepath.Base(rel))
			write(t, alias, "{}", 0o600)
			if err := os.Link(alias, p); err != nil {
				t.Fatal(err)
			}
			refused(t, f)
		})
	}
	t.Run("the exec record pins the main checkout", func(t *testing.T) {
		f := newFixture(t, "claude")
		main, _ := linked(t, f, "main")
		f.stage()
		if err := f.run(Options{}); err != nil || f.execPath == "" {
			t.Fatalf("exec %q, err %v", f.execPath, err)
		}
		rec, err := launch.ReadExec(f.root, "cto")
		if err != nil || rec.MainCheckout != main {
			t.Errorf("exec record main_checkout %q (%v), want %s", rec.MainCheckout, err, main)
		}
	})
	t.Run("an unreadable plugin manifest", func(t *testing.T) {
		f := newFixture(t, "claude")
		mf := filepath.Join(f.wt, ".claude", "skills", "fs", ".claude-plugin", "plugin.json")
		if err := os.MkdirAll(filepath.Dir(mf), 0o700); err != nil {
			t.Fatal(err)
		}
		write(t, mf, `{"name":`, 0o600)
		refused(t, f)
	})
	t.Run("an oversized regular file", func(t *testing.T) {
		f := newFixture(t, "claude")
		big := `{"mcpServers":{}, "pad":"` + strings.Repeat("x", 1<<20) + `"}`
		write(t, filepath.Join(f.wt, ".mcp.json"), big, 0o600)
		refused(t, f)
	})
	t.Run("a file it cannot read", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads a 000 file")
		}
		f := newFixture(t, "claude")
		write(t, filepath.Join(f.wt, ".mcp.json"), `{"mcpServers":{}}`, 0o600)
		if err := os.Chmod(filepath.Join(f.wt, ".mcp.json"), 0); err != nil {
			t.Fatal(err)
		}
		refused(t, f)
	})
}

// 3.3.1 item 5, the not-refused side (condition 2): a clean repository with
// ordinary .claude content (a settings file with only a permissions allow
// list, a skill without a plugin manifest, a project agent whose
// frontmatter neither mentions mcp nor holds a backslash) is not refused:
// the stub provider is exec'd, in the worktree and in the main checkout.
func TestSbxAClaudeSeatWithOrdinaryClaudeContentIsExecd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	ordinary := map[string]string{
		".claude/settings.json":           `{"permissions":{"allow":["Bash(go test:*)","Read"]}}`,
		".claude/skills/release/SKILL.md": "---\nname: release\ndescription: Prepare a release note\n---\nWrite the release note from the merged PRs.\n",
		// a standalone skill's .mcp.json is inert without a plugin manifest
		// (round 2, fix 4)
		".claude/skills/release/.mcp.json": `{"mcpServers":{"sample":{"command":"/tmp/server"}}}`,
		// the body may say anything: only the frontmatter is read
		".claude/agents/reviewer.md": "---\nname: reviewer\ndescription: Reviews a diff for correctness\ntools: Read, Grep\n---\nReview the diff you are given; explain any mcpServers change and paths like C:\\repo.\n",
	}
	plant := func(t *testing.T, base string) {
		t.Helper()
		for rel, body := range ordinary {
			p := filepath.Join(base, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				t.Fatal(err)
			}
			write(t, p, body, 0o600)
		}
	}
	for _, where := range []string{"worktree", "main checkout", "both", "a plain checkout as the seat's tree"} {
		t.Run(where, func(t *testing.T) {
			f := newFixture(t, "claude")
			main := filepath.Join(filepath.Dir(f.wt), "main")
			if err := os.MkdirAll(main, 0o700); err != nil {
				t.Fatal(err)
			}
			sbxGit(t, main, "init", "-q", "-b", "main")
			sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
			wt := filepath.Join(main, ".worktrees", "cto")
			if where == "a plain checkout as the seat's tree" {
				wt = main // a .git directory, no linked worktree
			} else {
				sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
			}
			f.wt, f.rec.Worktree = wt, wt
			if where != "main checkout" {
				plant(t, wt)
			}
			if where == "main checkout" || where == "both" {
				plant(t, main)
			}
			f.stage()
			if err := f.run(Options{}); err != nil || f.execPath == "" {
				t.Fatalf("ordinary .claude content in the %s: exec %q, err %v", where, f.execPath, err)
			}
		})
	}
}

// Round 2 (Codex r2 pass 1): the project MCP check is a Claude seat's only;
// a Codex seat whose worktree enables project MCP servers is exec'd (a pi
// seat is not exec'd by seat-exec; the guard row covers pi).
func TestSbxR2TheProjectMCPCheckIsForClaudeSeatsOnly(t *testing.T) {
	for _, runtime := range []string{"codex"} {
		t.Run(runtime, func(t *testing.T) {
			f := newFixture(t, runtime)
			for rel, body := range map[string]string{
				".mcp.json":             `{"mcpServers":{"fs":{"command":"/tmp/server"}}}`,
				".claude/settings.json": `{"enableAllProjectMcpServers":true}`,
			} {
				full := filepath.Join(f.wt, filepath.FromSlash(rel))
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				write(t, full, body, 0o600)
			}
			f.stage()
			if err := f.run(Options{}); err != nil || f.execPath == "" {
				t.Fatalf("a %s seat: exec %q, err %v", runtime, f.execPath, err)
			}
		})
	}
}

// Codex r2 pass 2: an alias anywhere in the skills tree survives a clean
// launch as a route to a plugin the guard cannot see: a symlinked plugin
// directory (its target empty at launch) or a skill file with a second
// name is refused; so is a hard-linked agent.
func TestSbxR2TheLaunchRefusesAliasesInsideSkillsAndAgents(t *testing.T) {
	for _, tc := range []struct {
		name, what string
		plant      func(t *testing.T, wt, outside string)
	}{
		{"a symlinked .claude-plugin in a skill", ".claude-plugin is a symlink", func(t *testing.T, wt, outside string) {
			write(t, filepath.Join(wt, ".claude", "skills", "demo", "SKILL.md"), "---\nname: demo\n---\n", 0o600)
			if err := os.Symlink(outside, filepath.Join(wt, ".claude", "skills", "demo", ".claude-plugin")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlink deep in a skill", "deep is a symlink", func(t *testing.T, wt, outside string) {
			if err := os.MkdirAll(filepath.Join(wt, ".claude", "skills", "demo", "a"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(wt, ".claude", "skills", "demo", "a", "deep")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a hard-linked SKILL.md", "has another name (a hard link)", func(t *testing.T, wt, outside string) {
			p := filepath.Join(wt, ".claude", "skills", "demo", "SKILL.md")
			write(t, p, "---\nname: demo\n---\n", 0o600)
			if err := os.Link(p, filepath.Join(outside, "alias.md")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a hard-linked agent", "fs.md: unreadable", func(t *testing.T, wt, outside string) {
			p := filepath.Join(wt, ".claude", "agents", "fs.md")
			write(t, p, "---\nname: fs\n---\n", 0o600)
			if err := os.Link(p, filepath.Join(outside, "alias.md")); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		for _, inMain := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s (main checkout %v)", tc.name, inMain), func(t *testing.T) {
				f := newFixture(t, "claude")
				outside := t.TempDir()
				base := f.wt
				if inMain {
					if _, err := exec.LookPath("git"); err != nil {
						t.Skip("git is not on PATH")
					}
					main := filepath.Join(filepath.Dir(f.wt), "main")
					if err := os.MkdirAll(main, 0o700); err != nil {
						t.Fatal(err)
					}
					sbxGit(t, main, "init", "-q", "-b", "main")
					sbxGit(t, main, "commit", "-q", "--allow-empty", "-m", "base")
					wt := filepath.Join(main, ".worktrees", "cto")
					sbxGit(t, main, "worktree", "add", "-q", "-b", "seat/cto", wt)
					f.wt, f.rec.Worktree, base = wt, wt, main
				}
				if err := os.MkdirAll(filepath.Join(base, ".claude", "skills", "demo"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(base, ".claude", "agents"), 0o700); err != nil {
					t.Fatal(err)
				}
				tc.plant(t, base, outside)
				f.stage()
				err := f.run(Options{})
				if refusalCode(err) != "claude_project_mcp" || f.execPath != "" {
					t.Fatalf("exec %q, err %v", f.execPath, err)
				}
				if !strings.Contains(err.Error(), tc.what) {
					t.Fatalf("the refusal does not name %q: %v", tc.what, err)
				}
			})
		}
	}
}
