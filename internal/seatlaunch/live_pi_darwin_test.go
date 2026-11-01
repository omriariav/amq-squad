//go:build darwin

package seatlaunch

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
)

// TestLivePiSeatThroughTheAdapter is brief 5-amq-A acceptance 2, live: one
// pi seat through `amq launch` (the amq CLI named by AMQ_SQUAD_LIVE_AMQ, a
// v0.116.x build) with the embedded runtime (AMQ_SQUAD_LIVE_PI_BIN, an
// amq-squad built with the payload), a scratch HOME (the runtime unpacks
// there), a private tmux server, and the local Ollama model
// (AMQ_SQUAD_LIVE_PI_MODEL, default ollama/qwen2.5:32b). It checks the exec
// record carries the adapter's argv with --session-id, that pi persisted
// its session under that id after the first turn, and that the seat
// answers one prompt; it writes the timings to AMQ_SQUAD_LIVE_OUT. Needs
// AMQ_SQUAD_LIVE_PI=1; CI and the default gate skip it.
func TestLivePiSeatThroughTheAdapter(t *testing.T) {
	if os.Getenv("AMQ_SQUAD_LIVE_PI") != "1" {
		t.Skip("set AMQ_SQUAD_LIVE_PI=1, AMQ_SQUAD_LIVE_PI_BIN and AMQ_SQUAD_LIVE_AMQ to run the live pi seat")
	}
	wrapper, amqDir := os.Getenv("AMQ_SQUAD_LIVE_PI_BIN"), os.Getenv("AMQ_SQUAD_LIVE_AMQ")
	model := os.Getenv("AMQ_SQUAD_LIVE_PI_MODEL")
	if model == "" {
		model = "ollama/qwen2.5:32b"
	}
	amq := filepath.Join(amqDir, "amq")
	tmux, err := exec.LookPath("tmux")
	if err != nil || !filepath.IsAbs(wrapper) || !filepath.IsAbs(amq) {
		t.Skip("tmux, AMQ_SQUAD_LIVE_PI_BIN or AMQ_SQUAD_LIVE_AMQ missing")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	home, project, wt := filepath.Join(base, "home"), filepath.Join(base, "proj"), filepath.Join(base, "wt-scout")
	for _, d := range []string{home, project, wt, filepath.Join(base, "state")} {
		os.MkdirAll(d, 0o700)
	}
	tmuxDir, err := os.MkdirTemp("/tmp", "5amqtmux")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmuxDir) })
	// The test process's own tmux lookups (the pane shell for the proof's
	// origin check) must reach the private server too.
	t.Setenv("TMUX_TMPDIR", tmuxDir)
	env := []string{"HOME=" + home, "PATH=" + amqDir + ":" + filepath.Dir(tmux) + ":/usr/bin:/bin",
		"XDG_STATE_HOME=" + filepath.Join(base, "state"), "TMUX_TMPDIR=" + tmuxDir, "TERM=xterm-256color", "LANG=en_US.UTF-8", "AMQ_SQUAD_PI_TIMING=1"}
	runIn := func(dir string, name string, args ...string) []byte {
		cmd := exec.Command(name, args...)
		cmd.Env, cmd.Dir = env, dir
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, b)
		}
		return b
	}
	t.Cleanup(func() {
		cmd := exec.Command(tmux, "kill-server")
		cmd.Env = env
		cmd.Run()
	})
	timings := map[string]any{"model": model}
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

	runIn(project, "git", "init", "-q", "-b", "main")
	runIn(wt, "git", "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(project, ".amqrc"), []byte(`{"root":".agent-mail"}`), 0o600)
	runIn(project, amq, "init", "--root", filepath.Join(project, ".agent-mail"), "--agents", "user,scout")
	runIn(project, amq, "session", "create", "s1", "--root", filepath.Join(project, ".agent-mail"), "--me", "user", "--json")
	// The cold install (unpack + full re-hash) outside the launch numbers.
	start := time.Now()
	runIn(base, wrapper, "pi", "path")
	timings["cold_install_ms"] = ms(time.Since(start))

	q := Squad{ProjectRoot: project, Session: "s1", Wrapper: wrapper, AMQPath: amq,
		Seats: []Seat{{Handle: "scout", Runtime: "pi", Model: model, Role: "# Role: scout\n\nYou are the scout seat of a live launch probe. Answer the operator's question briefly.\n", Worktree: wt}}}
	opts := Options{Env: env, Home: home, Wait: 90 * time.Second,
		PiShim: func(home, wrapper string) (string, error) { return piruntime.EnsureShim(home, wrapper) }}
	start = time.Now()
	res, err := Launch(context.Background(), q, opts)
	timings["prepare_ms"] = ms(time.Since(start))
	t.Logf("first pass:\n%s", reportOf(res))
	if err != nil {
		t.Fatal(err)
	}
	if res.Stage == "decisions_pending" {
		opts.Decisions = map[string]string{}
		for _, a := range res.Pending {
			opts.Decisions[a.ActionID] = "trust_exact_subject"
		}
		start = time.Now()
		res, err = Launch(context.Background(), q, opts)
		timings["launch_to_up_ms"] = ms(time.Since(start))
		t.Logf("apply pass:\n%s", reportOf(res))
		if err != nil {
			t.Fatal(err)
		}
	}
	up := time.Now()
	if res.Stage != "applied" || res.Failed() {
		t.Fatalf("the pi seat did not come up: stage %s", res.Stage)
	}
	root := filepath.Join(project, ".agent-mail", "s1")
	rec, err := launch.Read(filepath.Join(root, "agents", "scout"))
	if err != nil || rec.Exec == nil || rec.Exec.Outcome != "exec" {
		t.Fatalf("exec record %+v %v", rec.Exec, err)
	}
	argv := rec.Exec.ProviderArgv
	uuid := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-8[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	if len(argv) != 3 || argv[0] != piruntime.ShimPath(home) || argv[1] != "--session-id" || !uuid.MatchString(argv[2]) {
		t.Fatalf("provider argv %q, want <shim> --session-id <uuid v8 from the launch nonce>", argv)
	}
	// The seat (launcher and its node child) must be gone before the temp
	// dir is removed: it writes into its TMPDIR until it exits.
	t.Cleanup(func() {
		kill := exec.Command(tmux, "kill-server")
		kill.Env = env
		kill.Run()
		for i := 0; i < 100 && syscall.Kill(rec.Exec.PID, 0) == nil; i++ {
			time.Sleep(50 * time.Millisecond)
		}
		_ = syscall.Kill(rec.Exec.PID, syscall.SIGKILL)
		time.Sleep(200 * time.Millisecond)
	})
	timings["provider_argv"] = argv
	timings["launcher_argv"] = rec.Exec.LauncherArgv

	pane := strings.TrimSpace(string(runIn(base, tmux, "list-panes", "-a", "-F", "#{pane_id} #{@amq_pane_agent}")))
	var paneID string
	for _, line := range strings.Split(pane, "\n") {
		if id, h, _ := strings.Cut(line, " "); h == "scout" {
			paneID = id
		}
	}
	if paneID == "" {
		t.Fatalf("no scout pane in %q", pane)
	}
	capture := func() string { return string(runIn(base, tmux, "capture-pane", "-p", "-t", paneID)) }
	// pi is ready when its editor and footer are drawn (the footer names the
	// model).
	_, modelID, _ := strings.Cut(model, "/")
	waitFor := func(what string, limit time.Duration, ok func(string) bool) (string, time.Duration) {
		start := time.Now()
		for time.Since(start) < limit {
			if s := capture(); ok(s) {
				return s, time.Since(start)
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s: not seen in %s; pane:\n%s", what, limit, capture())
		return "", 0
	}
	_, ready := waitFor("pi ready", 60*time.Second, func(s string) bool { return strings.Contains(s, modelID) })
	_ = ready
	timings["up_to_pi_ready_ms"] = ms(time.Since(up))
	// The launcher's own line (AMQ_SQUAD_PI_TIMING=1), if the pane history
	// still holds it: the full re-hash and the rest of the launch.
	history := string(runIn(base, tmux, "capture-pane", "-p", "-S", "-", "-t", paneID))
	if m := regexp.MustCompile(`rehash_ms=([0-9.]+) rest_ms=([0-9.]+) launch_check_ms=([0-9.]+)`).FindStringSubmatch(history); m != nil {
		timings["launcher_rehash_ms"], timings["launcher_rest_ms"], timings["launcher_check_ms"] = m[1], m[2], m[3]
	}
	runIn(base, tmux, "send-keys", "-t", paneID, "-l", "Reply with exactly the word PINEAPPLE and nothing else.")
	sent := time.Now()
	runIn(base, tmux, "send-keys", "-t", paneID, "Enter")
	answer := regexp.MustCompile(`(?m)^\s*PINEAPPLE\.?\s*$`) // the reply on its own line, not the prompt's sentence
	screen, _ := waitFor("the answer", 180*time.Second, answer.MatchString)
	timings["prompt_to_answer_ms"] = ms(time.Since(sent))
	t.Logf("pane after the answer:\n%s", screen)
	// pi persists the session after its first turn, under the adapter's id.
	agentDir := piruntime.AgentDirFor(home, "scout")
	var file string
	for i := 0; i < 100 && file == ""; i++ {
		file, _ = piruntime.SessionFile(agentDir, wt, argv[2])
		time.Sleep(100 * time.Millisecond)
	}
	if file == "" {
		t.Fatalf("no pi session file for %s under %s", argv[2], piruntime.SessionDir(agentDir, wt))
	}
	timings["session_file"] = strings.TrimPrefix(file, base)
	// Fix round: no compile cache appears in the seat's TMPDIR after a turn
	// (NODE_DISABLE_COMPILE_CACHE=1 in the forced set; with the cache on,
	// pi's node writes node-compile-cache/ there). The node process's own
	// environment cannot be read back on macOS: pi sets process.title, which
	// overwrites the argv and environment area ps -E reads; the exec'd
	// environment is TestPiRunAsSeatExecStartsIt's row.
	seatTmp := filepath.Join(root, "agents", "scout", "extensions", "io.github.omriariav.amq-squad", "tmp")
	if ents, err := os.ReadDir(seatTmp); err != nil {
		t.Errorf("seat TMPDIR %s: %v", seatTmp, err)
	} else {
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), "node-compile-cache") {
				t.Errorf("a compile cache %s in the seat's TMPDIR", e.Name())
			}
		}
	}
	timings["no_compile_cache"] = true
	if out := os.Getenv("AMQ_SQUAD_LIVE_OUT"); out != "" {
		b, _ := json.MarshalIndent(timings, "", "  ")
		os.WriteFile(out, append(b, '\n'), 0o600)
	}
	t.Logf("timings: %v", timings)
}
