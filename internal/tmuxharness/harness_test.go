package tmuxharness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestServerEnvStripsAmbientTmuxAndHarnessIdentity(t *testing.T) {
	got := serverEnv([]string{
		"PATH=/bin", "HOME=/home/test", "PWD=/live",
		"TMUX=/live/socket,1,0", "TMUX_PANE=%99", "TMUX_TMPDIR=/live",
		"AMQ_SQUAD_TMUX_HARNESS_SOCKET=hostile",
	}, "/private/harness", "/usr/bin/tmux", "isolated", 42)
	joined := strings.Join(got, "\n")
	for _, forbidden := range []string{"TMUX=/live", "TMUX_PANE=", "TMUX_TMPDIR=/live", "SOCKET=hostile", "PWD=/live"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("ambient identity %q leaked into:\n%s", forbidden, joined)
		}
	}
	for _, want := range []string{
		"PATH=/bin", "HOME=/home/test", "TMUX_TMPDIR=/private/harness",
		"AMQ_SQUAD_TMUX_HARNESS_TMUX=/usr/bin/tmux",
		"AMQ_SQUAD_TMUX_HARNESS_SOCKET=isolated",
		"AMQ_SQUAD_TMUX_HARNESS_CONTROLLER_PID=42",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("server env missing %q:\n%s", want, joined)
		}
	}
}

func TestCommandEnvUsesCapturedExactLauncherIdentity(t *testing.T) {
	h := &Harness{
		CWD: "/repo", SocketPath: "/tmp/private/socket", ServerPID: 123,
		SessionID: "$2", LauncherWindowID: "@4", LauncherPaneID: "%8",
		env: []string{"PATH=/private/bin:/bin", "TMUX_TMPDIR=/private"},
	}
	joined := strings.Join(h.CommandEnv(), "\n")
	for _, want := range []string{
		"TMUX=/tmp/private/socket,123,0", "TMUX_PANE=%8", "PWD=/repo",
		"AMQ_SQUAD_TMUX_HARNESS_SESSION_ID=$2", "AMQ_SQUAD_TMUX_HARNESS_WINDOW_ID=@4",
		"AMQ_SQUAD_TMUX_HARNESS_PANE_ID=%8", "AMQ_SQUAD_TMUX_HARNESS_SOCKET_PATH=/tmp/private/socket",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("command env missing %q:\n%s", want, joined)
		}
	}
}

func TestExactIDValidationRejectsNamesAndMalformedIDs(t *testing.T) {
	if err := validateIDs("$1", "@2", "%3"); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][3]string{{"live", "@2", "%3"}, {"$1", "main", "%3"}, {"$1", "@2", "cto"}, {"$x", "@2", "%3"}} {
		if err := validateIDs(ids[0], ids[1], ids[2]); err == nil {
			t.Fatalf("accepted non-exact ids %q", ids)
		}
	}
}

func TestResolveTmuxPathPrefersInheritedHarnessBinary(t *testing.T) {
	dir := t.TempDir()
	realTmux := filepath.Join(dir, "real-tmux")
	if err := os.WriteFile(realTmux, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	wrapperDir := filepath.Join(dir, "wrapper")
	if err := os.Mkdir(wrapperDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wrapperDir, "tmux"), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"PATH=" + wrapperDir,
		harnessEnvPrefix + "TMUX=" + realTmux,
		harnessEnvPrefix + "SOCKET=outer-socket",
	}
	got, err := resolveTmuxPath("", env)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(realTmux)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("resolved tmux = %q, want inherited real binary %q", got, want)
	}

	for _, incomplete := range [][]string{
		{harnessEnvPrefix + "TMUX=" + realTmux},
		{harnessEnvPrefix + "SOCKET=outer-socket"},
		{harnessEnvPrefix + "TMUX=relative/tmux", harnessEnvPrefix + "SOCKET=outer-socket"},
	} {
		if _, err := resolveTmuxPath("", incomplete); err == nil {
			t.Fatalf("accepted incomplete or non-absolute inherited identity: %#v", incomplete)
		}
	}
}

func TestRoutedWrapperFailsClosedAndPreservesFormatArgument(t *testing.T) {
	dir := t.TempDir()
	called := filepath.Join(dir, "called")
	fake := filepath.Join(dir, "fake tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$CALLED\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(dir, "tmux")
	if err := os.WriteFile(wrapper, []byte(tmuxWrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(wrapper, "display-message", "-p", "#{pane_id}")
	cmd.Env = []string{
		"CALLED=" + called,
		"AMQ_SQUAD_TMUX_HARNESS_TMUX=" + fake,
		"AMQ_SQUAD_TMUX_HARNESS_SOCKET=isolated",
		"TMUX_TMPDIR=" + dir,
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrapper: %v: %s", err, out)
	}
	body, err := os.ReadFile(called)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(body)), "-f /dev/null -L isolated display-message -p #{pane_id}"; got != want {
		t.Fatalf("wrapper argv = %q, want %q", got, want)
	}

	if err := os.Remove(called); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command(wrapper, "list-panes")
	cmd.Env = []string{"AMQ_SQUAD_TMUX_HARNESS_TMUX=" + fake, "TMUX_TMPDIR=" + dir, "CALLED=" + called}
	if err := cmd.Run(); err == nil {
		t.Fatal("wrapper without socket identity must fail closed")
	}
	if _, err := os.Stat(called); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fail-closed wrapper invoked fake tmux: %v", err)
	}
}

func TestRemoveOwnedRootRejectsAnythingOutsideDirectTempChild(t *testing.T) {
	if err := removeOwnedRoot("/"); err == nil {
		t.Fatal("refused root should return an error")
	}
	base, err := harnessTempBase()
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, tempPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeOwnedRoot(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owned root remains: %v", err)
	}
}

func TestCleanupRetainsPrivateRootWhenKillFails(t *testing.T) {
	base, err := harnessTempBase()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, tempPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketPath := filepath.Join(root, "socket")
	if err := os.WriteFile(socketPath, []byte("sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &Harness{Root: root, SocketPath: socketPath}
	wantErr := errors.New("forced kill failure")
	killCalled := false
	err = cleanupHarnessWith(
		h,
		func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("startup cleanup probe has no deadline")
			}
			return nil
		},
		func(ctx context.Context) error {
			killCalled = true
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("startup cleanup kill has no deadline")
			}
			return wantErr
		},
	)
	if !killCalled || !errors.Is(err, wantErr) {
		t.Fatalf("cleanup error = %v, kill called = %v", err, killCalled)
	}
	if !strings.Contains(err.Error(), "retained private root "+root) {
		t.Fatalf("cleanup error does not identify retained root: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("private root was removed after failed kill: %v", err)
	}
}

func TestStartRetainsPrivateRootWhenStartupKillFails(t *testing.T) {
	fakeTmux := filepath.Join(t.TempDir(), "tmux")
	if err := os.WriteFile(fakeTmux, []byte(`#!/bin/sh
set -eu
socket_name=$4
command=$5
socket_dir="$TMUX_TMPDIR/tmux-$(id -u)"
socket="$socket_dir/$socket_name"
case "$command" in
  new-session)
    mkdir -p "$socket_dir"
    : > "$socket"
    printf '$0\t@0\t%%0\t4242\t%s\n' "$socket"
    ;;
  new-window)
    echo 'forced launcher failure' >&2
    exit 77
    ;;
  display-message)
    printf '%s\t$0\t4242\n' "$socket"
    ;;
  kill-server)
    echo 'forced kill failure' >&2
    exit 88
    ;;
  *)
    echo "unexpected command: $command" >&2
    exit 99
    ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := Start(context.Background(), Options{
		CWD:         t.TempDir(),
		TmuxPath:    fakeTmux,
		Environment: []string{"PATH=/usr/bin:/bin"},
	})
	if err == nil || !strings.Contains(err.Error(), "forced launcher failure") || !strings.Contains(err.Error(), "forced kill failure") {
		t.Fatalf("Start error = %v, want launcher and bounded cleanup failures", err)
	}
	const marker = "retained private root "
	idx := strings.LastIndex(err.Error(), marker)
	if idx < 0 {
		t.Fatalf("Start error does not identify retained root: %v", err)
	}
	fields := strings.Fields(err.Error()[idx+len(marker):])
	if len(fields) == 0 {
		t.Fatalf("Start error has empty retained root: %v", err)
	}
	root := strings.TrimSuffix(fields[0], ":")
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	for _, path := range []string{root, filepath.Join(root, "bin", "tmux"), filepath.Join(root, "keeper")} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("startup cleanup removed retained artifact %s: %v", path, statErr)
		}
	}
}

func TestCloseRefusesReplacementServerOnSameSocket(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux requires a Unix platform")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	h, err := Start(ctx, Options{CWD: t.TempDir()})
	if err != nil {
		skipUnavailableTmuxSocket(t, err)
		t.Fatal(err)
	}
	root := h.Root
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		_ = runTmux(cleanupCtx, h.TmuxPath, h.env, "", "kill-server")
		_ = os.RemoveAll(root)
	})

	if err := runTmux(ctx, h.TmuxPath, h.env, "", "kill-server"); err != nil {
		t.Fatalf("kill original harness server: %v", err)
	}
	if err := runTmux(ctx, h.TmuxPath, h.env, "", "new-session", "-d", "-s", "replacement"); err != nil {
		t.Fatalf("start replacement server: %v", err)
	}
	replacement, err := outputTmux(ctx, h.TmuxPath, h.env, "", "display-message", "-p", "-t", "replacement", "#{socket_path}\t#{session_id}\t#{pid}")
	if err != nil {
		t.Fatalf("read replacement identity: %v", err)
	}
	parts := strings.Split(strings.TrimSpace(replacement), "\t")
	if len(parts) != 3 || parts[0] != h.SocketPath || parts[1] != h.SessionID || parts[2] == "" {
		t.Fatalf("replacement did not reuse path/session identity needed by regression: %q", replacement)
	}
	if parts[2] == strconv.Itoa(h.ServerPID) {
		t.Fatalf("replacement unexpectedly reused original server pid %d", h.ServerPID)
	}

	err = h.Close()
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("Close replacement error = %v, want fail-closed identity mismatch", err)
	}
	if _, probeErr := outputTmux(ctx, h.TmuxPath, h.env, "", "has-session", "-t", "replacement"); probeErr != nil {
		t.Fatalf("replacement server was killed by failed-close path: %v", probeErr)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Fatalf("private root was removed after replacement mismatch: %v", statErr)
	}
}

func TestNestedHarnessBypassesOuterRoutingWrapper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux requires a Unix platform")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cwd := t.TempDir()
	outer, err := Start(ctx, Options{CWD: cwd})
	if err != nil {
		skipUnavailableTmuxSocket(t, err)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = outer.Close() })

	outerEnv := outer.CommandEnv()
	for _, key := range []string{
		"PATH", "TMUX", "TMUX_PANE", "TMUX_TMPDIR",
		harnessEnvPrefix + "TMUX", harnessEnvPrefix + "SOCKET", harnessEnvPrefix + "CONTROLLER_PID",
	} {
		t.Setenv(key, envValue(outerEnv, key))
	}
	if routed, err := exec.LookPath("tmux"); err != nil || routed != filepath.Join(outer.Root, "bin", "tmux") {
		t.Fatalf("test did not install outer routing wrapper: path=%q err=%v", routed, err)
	}

	nestedCtx, nestedCancel := context.WithTimeout(ctx, 5*time.Second)
	defer nestedCancel()
	nested, err := Start(nestedCtx, Options{CWD: cwd})
	if err != nil {
		t.Fatalf("start nested harness through outer environment: %v", err)
	}
	if nested.TmuxPath != outer.TmuxPath {
		t.Fatalf("nested tmux binary = %q, want canonical outer binary %q", nested.TmuxPath, outer.TmuxPath)
	}
	if nested.SocketPath == outer.SocketPath || nested.Root == outer.Root {
		t.Fatalf("nested harness reused outer private identity: nested=%+v outer=%+v", nested, outer)
	}
	if err := nested.Close(); err != nil {
		t.Fatalf("close nested harness: %v", err)
	}
	if err := outer.Probe(ctx); err != nil {
		t.Fatalf("nested lifecycle touched outer server: %v", err)
	}
}

func TestHarnessIsolatesNestedClientsAndTeardown(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux requires a Unix platform")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cwd := t.TempDir()
	home := t.TempDir()
	configSideEffect := filepath.Join(home, "tmux-config-ran")
	if err := os.WriteFile(filepath.Join(home, ".tmux.conf"), []byte("run-shell 'touch "+configSideEffect+"'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	isolatedEnv := replaceEnv(os.Environ(), "HOME", home)
	sentinel, err := Start(ctx, Options{CWD: cwd, Environment: isolatedEnv})
	if err != nil {
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") || os.Getenv("CODEX_SANDBOX") != "" {
			t.Skipf("tmux socket access unavailable: %v", err)
		}
		t.Fatalf("start sentinel: %v", err)
	}
	t.Cleanup(func() { _ = sentinel.Close() })
	if _, err := os.Stat(configSideEffect); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("tmux loaded user config despite -f /dev/null: %v", err)
	}

	contaminated := append(isolatedEnv,
		"TMUX="+sentinel.SocketPath+",1,0",
		"TMUX_PANE="+sentinel.LauncherPaneID,
		"TMUX_TMPDIR="+sentinel.Root,
	)
	h, err := Start(ctx, Options{CWD: cwd, Environment: contaminated})
	if err != nil {
		t.Fatalf("start isolated harness: %v", err)
	}
	t.Cleanup(func() { _ = h.Close() })
	if h.SocketPath == sentinel.SocketPath || h.SessionID == sentinel.SessionID && h.Root == sentinel.Root {
		t.Fatalf("harness reused sentinel identity: harness=%+v sentinel=%+v", h, sentinel)
	}

	directPath := filepath.Join(cwd, "direct.txt")
	nestedPath := filepath.Join(cwd, "nested.txt")
	nestedScript := "tmux display-message -p -t " + testShellQuote(h.LauncherPaneID) + " '##{session_id} ##{window_id} ##{pane_id}' > " + testShellQuote(nestedPath)
	script := "tmux display-message -p -t \"$TMUX_PANE\" '#{session_id} #{window_id} #{pane_id}' > " + testShellQuote(directPath) +
		"; tmux run-shell -b " + testShellQuote(nestedScript)
	if err := h.Run(ctx, []string{"/bin/sh", "-c", script}, Streams{}); err != nil {
		t.Fatalf("run nested clients: %v", err)
	}
	want := h.SessionID + " " + h.LauncherWindowID + " " + h.LauncherPaneID
	if got := waitFile(t, directPath); got != want {
		t.Fatalf("direct nested identity = %q, want %q", got, want)
	}
	if got := waitFile(t, nestedPath); got != want {
		t.Fatalf("run-shell nested identity = %q, want %q", got, want)
	}

	rename := "tmux rename-window -t \"$TMUX_PANE\" changed; tmux select-pane -t \"$TMUX_PANE\" -T changed"
	if err := h.Run(ctx, []string{"/bin/sh", "-c", rename}, Streams{}); err != nil {
		t.Fatalf("rename launcher: %v", err)
	}
	if err := h.Probe(ctx); err != nil {
		t.Fatalf("renames changed exact control identity: %v", err)
	}
	if err := h.Run(ctx, []string{"/bin/sh", "-c", "tmux kill-pane -t \"$TMUX_PANE\""}, Streams{}); err != nil {
		t.Fatalf("kill launcher: %v", err)
	}
	if err := h.Probe(ctx); err != nil {
		t.Fatalf("keeper did not preserve server after launcher close: %v", err)
	}

	root := h.Root
	if err := h.Close(); err != nil {
		t.Fatalf("close harness: %v", err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("harness root remains after close: %v", err)
	}
	if err := sentinel.Probe(ctx); err != nil {
		t.Fatalf("harness teardown touched sentinel server: %v", err)
	}
	if err := h.Close(); err != nil {
		t.Fatalf("second close must be idempotent: %v", err)
	}
}

func waitFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if body, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(body)) != "" {
			return strings.TrimSpace(string(body))
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
	return ""
}

func testShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func skipUnavailableTmuxSocket(t *testing.T, err error) {
	t.Helper()
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "operation not permitted") || strings.Contains(msg, "permission denied") || os.Getenv("CODEX_SANDBOX") != "" {
		t.Skipf("tmux socket access unavailable: %v", err)
	}
}
