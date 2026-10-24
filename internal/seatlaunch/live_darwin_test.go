//go:build darwin

package seatlaunch

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeProvider answers --version like the real CLI and otherwise records
// its argv and environment names (never values) and stays up.
const fakeProvider = `#!/bin/sh
case "$1" in --version|-V) echo "%s"; exit 0;; esac
# Anything that is not a seat start (AMQ's capability probes, such as
# --help) gets a short help text and exits at once.
if [ -z "$AMQ_SQUAD_SEAT" ]; then
  echo "Usage: fake [options]"; echo "Commands: resume [SESSION_ID]"; echo "  --session-id <uuid>  --resume [value]"; echo "  --model <m>  --effort <e>  --name <n>  --session-id <id>  --sandbox <s>  --ask-for-approval <a>  -c <k=v>"
  exit 0
fi
out="$AMQ_SQUAD_LIVE_OUT/$AM_ME"
{ echo "argv: $0 $*"; env | sort; } > "$out.env.tmp" && mv "$out.env.tmp" "$out.env"
sleep 30
`

// TestLiveTmuxLaunchWithInstalledAMQ is the tmux-backed launch on darwin
// with the installed amq (gate item of brief 2b-2). Fake claude and codex
// providers stand in for the real ones, so nothing touches ~/.claude or
// ~/.codex; HOME, XDG_STATE_HOME (AMQ's trust store) and TMUX_TMPDIR (a
// private tmux server) point into the test directory. It needs a local tmux
// and amq and starts real panes, so it runs only with AMQ_SQUAD_LIVE=1;
// CI and the default gate skip it.
func TestLiveTmuxLaunchWithInstalledAMQ(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_LIVE") != "1" {
		t.Skip("set AMQ_SQUAD_LIVE=1 to run the tmux-backed launch against the installed amq (needs tmux and amq on the owner's Mac; skipped in CI)")
	}
	amq, err1 := exec.LookPath("amq")
	tmux, err2 := exec.LookPath("tmux")
	if err1 != nil || err2 != nil {
		t.Skip("amq or tmux not on PATH")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home, bin, out := filepath.Join(base, "home"), filepath.Join(base, "bin"), filepath.Join(base, "out")
	project := filepath.Join(base, "proj")
	for _, d := range []string{home, bin, out, project, filepath.Join(base, "wt-cto"), filepath.Join(base, "wt-dev"), filepath.Join(base, "state")} {
		os.MkdirAll(d, 0o700)
	}
	// tmux's socket path has a short limit, so the private server lives
	// under /tmp, not under the long test directory.
	tmuxDir, err := os.MkdirTemp("/tmp", "2b2tmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmuxDir) })
	wrapper := filepath.Join(bin, "amq-squad")
	build := exec.Command("go", "build", "-o", wrapper, "github.com/omriariav/amq-squad/v3/cmd/amq-squad")
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build amq-squad: %v\n%s", err, b)
	}
	versions := map[string]string{"claude": "2.1.310 (Claude Code)", "codex": "codex-cli 0.163.0"}
	for name, v := range versions {
		os.WriteFile(filepath.Join(bin, name), []byte(strings.Replace(fakeProvider, "%s", v, 1)), 0o700)
	}
	env := []string{"HOME=" + home, "PATH=" + bin + ":" + filepath.Dir(amq) + ":" + filepath.Dir(tmux) + ":/usr/bin:/bin",
		"XDG_STATE_HOME=" + filepath.Join(base, "state"), "TMUX_TMPDIR=" + tmuxDir, "TERM=xterm-256color", "LANG=en_US.UTF-8",
		"AMQ_SQUAD_LIVE_OUT=" + out, "GEMINI_API_KEY=live-secret", "GH_TOKEN=live-secret", "AWS_SECRET_ACCESS_KEY=live-secret", "SSH_AUTH_SOCK=/tmp/none"}
	runIn := func(dir string, name string, args ...string) {
		cmd := exec.Command(name, args...)
		cmd.Env, cmd.Dir = env, dir
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, b)
		}
	}
	runIn(project, "git", "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(project, ".amqrc"), []byte(`{"root":".agent-mail"}`), 0o600)
	runIn(project, amq, "init", "--root", filepath.Join(project, ".agent-mail"), "--agents", "user,cto,dev")
	runIn(project, amq, "session", "create", "s1", "--root", filepath.Join(project, ".agent-mail"), "--me", "user", "--json")
	t.Cleanup(func() {
		cmd := exec.Command(tmux, "kill-server")
		cmd.Env = env
		cmd.Run()
	})
	rules := filepath.Join(project, "team-rules.md")
	os.WriteFile(rules, []byte("# team rules\n"), 0o600)
	q := Squad{
		ProjectRoot: project, Session: "s1", RulesPath: rules, Wrapper: wrapper, AMQPath: amq,
		Seats: []Seat{
			{Handle: "cto", Runtime: "claude", Model: "sonnet", Effort: "medium", Role: "# Role: cto\n\nLive launch probe seat.\n", Worktree: filepath.Join(base, "wt-cto"), Branch: "seat/cto"},
			{Handle: "dev", Runtime: "codex", Model: "gpt-6-astra", Effort: "medium", Role: "# Role: dev\n\nLive launch probe seat.\n", Worktree: filepath.Join(base, "wt-dev"), Branch: "seat/dev"},
		},
	}
	if only := os.Getenv("AMQ_SQUAD_LIVE_SEATS"); only != "" {
		var keep []Seat
		for _, seat := range q.Seats {
			if strings.Contains(","+only+",", ","+seat.Handle+",") {
				keep = append(keep, seat)
			}
		}
		q.Seats = keep
	}
	// The providers resolve in the fake bin only, never on the real PATH.
	fakeLook := func(name string) (string, error) { return filepath.Join(bin, name), nil }
	opts := Options{Env: append(env, "AM_ME=lead"), Home: home, Wait: 20 * time.Second, LookPath: fakeLook}
	res, err := Launch(context.Background(), q, opts)
	var report bytes.Buffer
	Report(&report, res)
	t.Logf("first pass:\n%s", report.String())
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage == "decisions_pending" {
		opts.Decisions = map[string]string{}
		for _, a := range res.Pending {
			if a.Kind != "trust_confirmation" {
				t.Fatalf("unexpected action %+v", a)
			}
			opts.Decisions[a.ActionID] = "trust_exact_subject"
		}
		started := time.Now()
		res, err = Launch(context.Background(), q, opts)
		report.Reset()
		Report(&report, res)
		t.Logf("apply pass (%s):\n%s", time.Since(started).Round(time.Millisecond), report.String())
		if err != nil {
			t.Fatal(err)
		}
	}
	if res.Stage != "applied" || res.Failed() {
		t.Fatalf("live launch did not bring both seats up: stage %s", res.Stage)
	}
	for _, seat := range q.Seats {
		h := seat.Handle
		deadline := time.Now().Add(10 * time.Second)
		var b []byte
		for time.Now().Before(deadline) {
			if b, _ = os.ReadFile(filepath.Join(out, h+".env")); len(b) > 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		text := string(b)
		for _, want := range []string{"AM_ME=" + h, "AM_SESSION=s1", "AM_ROOT=" + filepath.Join(project, ".agent-mail", "s1"), "AM_BASE_ROOT=" + filepath.Join(project, ".agent-mail"), "AMQ_SQUAD_BRANCH=seat/" + h} {
			if !strings.Contains(text, want+"\n") {
				t.Errorf("%s: seat env lacks %s\n%s", h, want, text)
			}
		}
		for _, secret := range []string{"GEMINI_API_KEY", "GH_TOKEN", "AWS_SECRET_ACCESS_KEY", "SSH_AUTH_SOCK"} {
			if strings.Contains(text, "\n"+secret+"=") {
				t.Errorf("%s: %s reached the seat", h, secret)
			}
		}
		t.Logf("%s argv: %s", h, strings.SplitN(text, "\n", 2)[0])
	}
	if _, err := os.Stat(filepath.Join(project, ".agent-mail", "s1", "agents", "lead")); !os.IsNotExist(err) {
		t.Errorf("R49: the launch registered a lead mailbox or presence: %v", err)
	}
}
