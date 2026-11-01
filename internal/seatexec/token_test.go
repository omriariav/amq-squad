//go:build unix

package seatexec

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
)

// A Claude seat's CLAUDE_CODE_OAUTH_TOKEN comes from the owner's token file
// only: a parent-supplied value is replaced (a runtime cannot declare the
// name: v3's ValidateNeeds refuses it as a host agent-session variable), and
// a Codex seat never gets one.
func TestClaudeTokenComesOnlyFromTheOwnerFile(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Env: f.env(claudeseat.TokenVar + "=parent-token")}); err != nil {
		t.Fatal(err)
	}
	if got := Getenv(f.execEnv, claudeseat.TokenVar); got != canaryToken {
		t.Fatalf("seat token = %q, want the owner file's", got)
	}
	g := newFixture(t, "codex")
	g.stage()
	if err := g.run(Options{Env: g.env(claudeseat.TokenVar + "=parent-token")}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range g.execEnv {
		if strings.HasPrefix(kv, claudeseat.TokenVar+"=") {
			t.Fatalf("a Codex seat got %s", kv)
		}
	}
}

// A missing or insecure token file refuses the Claude seat
// (claude_token_insecure); there is no fallback.
func TestClaudeSeatRefusesWithoutASecureTokenFile(t *testing.T) {
	cases := map[string]func(f *fixture){
		"missing":            func(f *fixture) { os.Remove(claudeseat.TokenPath(f.home)) },
		"readable by others": func(f *fixture) { os.Chmod(claudeseat.TokenPath(f.home), 0o644) },
		"a symlink": func(f *fixture) {
			p := claudeseat.TokenPath(f.home)
			write(t, p+".real", canaryToken, 0o600)
			os.Remove(p)
			os.Symlink(p+".real", p)
		},
		"a group-writable directory": func(f *fixture) { os.Chmod(filepath.Dir(claudeseat.TokenPath(f.home)), 0o770) },
		"two lines":                  func(f *fixture) { write(t, claudeseat.TokenPath(f.home), canaryToken+"\nsecond-"+canaryToken, 0o600) },
	}
	for name, breakIt := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "claude")
			f.stage()
			breakIt(f)
			err := f.run(Options{})
			if refusalCode(err) != claudeseat.TokenInsecure || f.execPath != "" {
				t.Fatalf("want %s, got %v", claudeseat.TokenInsecure, err)
			}
			if strings.Contains(err.Error(), canaryToken) {
				t.Fatalf("the refusal prints the token: %v", err)
			}
		})
	}
}

// Reviewer condition 2: the token value never appears in the launch record,
// the ledger, the evidence or any output: a canary runs through a full
// fake-provider seat launch, and every file written under the fixture (the
// session root, the seat's published record and snapshots, the worktree) and every output is searched for it. It is only in
// the provider's environment.
func TestClaudeTokenCanaryAppearsNowhereButTheProviderEnv(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if Getenv(f.execEnv, claudeseat.TokenVar) != canaryToken {
		t.Fatal("the provider did not get the token")
	}
	for _, s := range append(append([]string{}, f.execArgv...), f.drainArgs...) {
		if strings.Contains(s, canaryToken) {
			t.Fatalf("an argv carries the token: %q", s)
		}
	}
	tokenFile := claudeseat.TokenPath(f.home)
	checked := 0
	filepath.WalkDir(f.base, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == tokenFile {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err == nil {
			checked++
			if strings.Contains(string(raw), canaryToken) {
				t.Errorf("%s carries the token", p)
			}
		}
		return nil
	})
	if checked < 5 {
		t.Fatalf("only %d files checked", checked)
	}
}
