//go:build unix

package claudeseat

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The fix round's probe ruling: the probe runs with a scratch HOME, a full
// copy of the seat's config dir (its plugin install, the paths rewritten to
// the copy, hooks disabled in the copy's settings only) and an empty
// working directory, so no project or owner setting or hook applies.
func TestProbeIsIsolated(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	wt := filepath.Join(base, "wt")
	os.MkdirAll(filepath.Join(wt, ".claude"), 0o700)
	os.WriteFile(filepath.Join(wt, ".claude", "settings.json"), []byte(`{"hooks":{"SessionStart":[]}}`), 0o600)
	dir := filepath.Join(base, "cfg", "launch-1")
	if _, err := Write(dir, "dontAsk", wt, time.Now()); err != nil {
		t.Fatal(err)
	}
	seen := filepath.Join(base, "seen")
	fake := filepath.Join(base, "fake-claude")
	script := "#!/bin/sh\n{ echo \"home=$HOME\"; echo \"cfg=$CLAUDE_CONFIG_DIR\"; echo \"pwd=$(pwd -P)\"; ls -A; echo files; " +
		"cat \"$CLAUDE_CONFIG_DIR/settings.json\"; echo; cat \"$CLAUDE_CONFIG_DIR/plugins/installed_plugins.json\"; } > " + seen + "\n" +
		"echo '{\"type\":\"system\",\"subtype\":\"init\",\"permissionMode\":\"dontAsk\",\"claude_code_version\":\"fake\"}'\n"
	os.WriteFile(fake, []byte(script), 0o700)
	// The fake's shell work can be slow on a loaded host; the 5 s bound is
	// TestSlowProbeIsModeUnverified's row.
	prev := ProbeTimeout
	ProbeTimeout = 60 * time.Second
	t.Cleanup(func() { ProbeTimeout = prev })
	p, err := ProbeMode(context.Background(), func(args ...string) *exec.Cmd { return exec.Command(fake, args...) }, dir, "dontAsk")
	if err != nil || p.Mode != "dontAsk" {
		t.Fatalf("probe: %+v %v", p, err)
	}
	raw, _ := os.ReadFile(seen)
	got := string(raw)
	owner, _ := os.UserHomeDir()
	var home, cfg, pwd, listing string
	for _, line := range strings.Split(got, "\n") {
		switch {
		case strings.HasPrefix(line, "home="):
			home = strings.TrimPrefix(line, "home=")
		case strings.HasPrefix(line, "cfg="):
			cfg = strings.TrimPrefix(line, "cfg=")
		case strings.HasPrefix(line, "pwd="):
			pwd = strings.TrimPrefix(line, "pwd=")
		}
	}
	if i := strings.Index(got, "\n"); i >= 0 {
		listing = got[strings.Index(got, "pwd="):strings.Index(got, "files")]
	}
	switch {
	case home == "" || home == owner || strings.HasPrefix(home, dir):
		t.Fatalf("the probe's HOME is %q (owner %q)", home, owner)
	case cfg == "" || cfg == dir:
		t.Fatalf("the probe ran in the seat's own config dir %q", cfg)
	case strings.Contains(pwd, wt) || strings.Count(listing, "\n") > 1:
		t.Fatalf("the probe's cwd is %q with entries %q, not an empty scratch dir", pwd, listing)
	case !strings.Contains(got, `"disableAllHooks": true`):
		t.Fatalf("the probe's settings do not disable hooks:\n%s", got)
	case strings.Contains(got, dir) || !strings.Contains(got, cfg+"/plugins"):
		t.Fatalf("the probe's plugin install names the seat's dir, not the copy:\n%s", got)
	}
	var seat map[string]any
	own, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if json.Unmarshal(own, &seat) != nil || seat["disableAllHooks"] != nil {
		t.Fatalf("the seat's own settings gained disableAllHooks:\n%s", own)
	}
}

// A probe that gives no init event within 5 s is an error (the caller
// refuses claude_mode_unverified), and its process group is killed.
func TestSlowProbeIsModeUnverified(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(base, "cfg", "launch-1")
	if _, err := Write(dir, "dontAsk", filepath.Join(base, "wt"), time.Now()); err != nil {
		t.Fatal(err)
	}
	slow := filepath.Join(base, "slow-claude")
	os.WriteFile(slow, []byte("#!/bin/sh\nsleep 30\n"), 0o700)
	start := time.Now()
	_, err := ProbeMode(context.Background(), func(args ...string) *exec.Cmd { return exec.Command(slow, args...) }, dir, "dontAsk")
	if err == nil || !strings.Contains(err.Error(), "within 5s") || time.Since(start) > 8*time.Second {
		t.Fatalf("a slow probe: %v after %s", err, time.Since(start).Round(time.Second))
	}
}
