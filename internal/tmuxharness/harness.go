// Package tmuxharness creates short-lived tmux servers for process-level
// smoke tests without touching the caller's tmux server.
package tmuxharness

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	tempPrefix       = "amq-th-"
	harnessEnvPrefix = "AMQ_SQUAD_TMUX_HARNESS_"
)

var (
	exactSessionID = regexp.MustCompile(`^\$[0-9]+$`)
	exactWindowID  = regexp.MustCompile(`^@[0-9]+$`)
	exactPaneID    = regexp.MustCompile(`^%[0-9]+$`)
)

// Options controls creation of one disposable tmux server.
type Options struct {
	CWD           string
	Shell         string
	Environment   []string
	TmuxPath      string
	ControllerPID int
}

// Streams are connected to an exec or attach process.
type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Harness is the exact identity and lifecycle owner of one isolated server.
// All IDs are captured synchronously from tmux; names are never used for
// follow-up control after creation.
type Harness struct {
	Root             string
	CWD              string
	TmuxPath         string
	SocketName       string
	SocketPath       string
	SessionID        string
	KeeperWindowID   string
	KeeperPaneID     string
	LauncherWindowID string
	LauncherPaneID   string
	ServerPID        int

	env       []string
	closeOnce sync.Once
	closeErr  error
}

// Start creates a private named tmux server, a watchdog/keeper pane and a
// launcher pane. It never consults the ambient TMUX server.
func Start(ctx context.Context, opts Options) (h *Harness, err error) {
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("tmux harness is not supported on windows")
	}
	baseEnv := opts.Environment
	if baseEnv == nil {
		baseEnv = os.Environ()
	}
	tmuxPath, err := resolveTmuxPath(opts.TmuxPath, baseEnv)
	if err != nil {
		return nil, err
	}

	cwd := strings.TrimSpace(opts.CWD)
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve harness cwd: %w", err)
		}
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolve harness cwd: %w", err)
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("tmux harness cwd is not a directory: %s", cwd)
	}

	tempBase, err := harnessTempBase()
	if err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(tempBase, tempPrefix)
	if err != nil {
		return nil, fmt.Errorf("allocate tmux harness root: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return nil, fmt.Errorf("secure tmux harness root: %w", err)
	}
	var (
		startupHarness     *Harness
		expectedSocketPath string
	)
	defer func() {
		if err == nil {
			return
		}
		var cleanupErr error
		switch {
		case startupHarness != nil:
			cleanupErr = startupHarness.cleanup()
		case expectedSocketPath != "":
			if _, statErr := os.Lstat(expectedSocketPath); errors.Is(statErr, os.ErrNotExist) {
				cleanupErr = removeOwnedRoot(root)
			} else {
				cleanupErr = fmt.Errorf("refusing unverified tmux harness startup cleanup; retained private root %s", root)
			}
		default:
			cleanupErr = removeOwnedRoot(root)
		}
		if cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()

	suffix := make([]byte, 8)
	if _, err = rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("allocate tmux harness identity: %w", err)
	}
	socketName := "amq-th-" + hex.EncodeToString(suffix)
	sessionName := socketName
	expectedSocketPath = filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()), socketName)
	controllerPID := opts.ControllerPID
	if controllerPID <= 0 {
		controllerPID = os.Getpid()
	}

	binDir := filepath.Join(root, "bin")
	if err = os.Mkdir(binDir, 0o700); err != nil {
		return nil, fmt.Errorf("create tmux harness bin directory: %w", err)
	}
	wrapperPath := filepath.Join(binDir, "tmux")
	if err = os.WriteFile(wrapperPath, []byte(tmuxWrapper), 0o700); err != nil {
		return nil, fmt.Errorf("write routed tmux wrapper: %w", err)
	}
	keeperPath := filepath.Join(root, "keeper")
	if err = os.WriteFile(keeperPath, []byte(keeperScript), 0o700); err != nil {
		return nil, fmt.Errorf("write tmux harness keeper: %w", err)
	}

	env := serverEnv(baseEnv, root, tmuxPath, socketName, controllerPID)
	env = replaceEnv(env, "PATH", binDir+string(os.PathListSeparator)+envValue(baseEnv, "PATH"))

	const createFormat = "#{session_id}\t#{window_id}\t#{pane_id}\t#{pid}\t#{socket_path}"
	out, err := outputTmux(ctx, tmuxPath, env, "", "new-session", "-d", "-P", "-F", createFormat,
		"-s", sessionName, "-n", "__harness_keeper", "-c", cwd, keeperPath)
	if err != nil {
		return nil, fmt.Errorf("create isolated tmux server: %w", err)
	}
	fields := strings.Split(strings.TrimSpace(out), "\t")
	if len(fields) != 5 {
		if _, diagnoseErr := outputTmux(ctx, tmuxPath, env, "", "list-sessions"); diagnoseErr != nil {
			return nil, fmt.Errorf("unexpected tmux harness identity %q: %w", out, diagnoseErr)
		}
		return nil, fmt.Errorf("unexpected tmux harness identity %q", out)
	}
	serverPID, parseErr := strconv.Atoi(fields[3])
	if parseErr != nil || serverPID <= 0 {
		return nil, fmt.Errorf("invalid tmux harness server pid %q", fields[3])
	}
	if err = validateIDs(fields[0], fields[1], fields[2]); err != nil {
		return nil, err
	}
	socketPath := filepath.Clean(fields[4])
	if !pathWithin(root, socketPath) {
		return nil, fmt.Errorf("tmux harness socket escaped private root: %s", socketPath)
	}
	startupHarness = &Harness{
		Root: root, CWD: cwd, TmuxPath: tmuxPath, SocketName: socketName, SocketPath: socketPath,
		SessionID: fields[0], KeeperWindowID: fields[1], KeeperPaneID: fields[2], ServerPID: serverPID,
		env: env,
	}

	shell := strings.TrimSpace(opts.Shell)
	if shell == "" {
		shell = "/bin/sh"
	}
	const launcherFormat = "#{window_id}\t#{pane_id}"
	launcher, err := outputTmux(ctx, tmuxPath, env, "", "new-window", "-d", "-P", "-F", launcherFormat,
		"-t", fields[0], "-n", "launcher", "-c", cwd, shell)
	if err != nil {
		return nil, fmt.Errorf("create tmux harness launcher: %w", err)
	}
	launcherFields := strings.Split(strings.TrimSpace(launcher), "\t")
	if len(launcherFields) != 2 || !exactWindowID.MatchString(launcherFields[0]) || !exactPaneID.MatchString(launcherFields[1]) {
		return nil, fmt.Errorf("unexpected tmux launcher identity %q", launcher)
	}

	const verifyFormat = "#{session_id}\t#{window_id}\t#{pane_id}\t#{pid}\t#{socket_path}"
	verified, err := outputTmux(ctx, tmuxPath, env, "", "display-message", "-p", "-t", launcherFields[1], verifyFormat)
	if err != nil {
		return nil, fmt.Errorf("verify tmux harness launcher: %w", err)
	}
	want := strings.Join([]string{fields[0], launcherFields[0], launcherFields[1], fields[3], socketPath}, "\t")
	if strings.TrimSpace(verified) != want {
		return nil, fmt.Errorf("tmux harness identity mismatch: got %q, want %q", strings.TrimSpace(verified), want)
	}

	h = startupHarness
	h.LauncherWindowID = launcherFields[0]
	h.LauncherPaneID = launcherFields[1]
	return h, nil
}

// resolveTmuxPath returns the canonical executable used for every direct and
// routed client in a harness. A nested harness must bypass the outer harness's
// PATH wrapper; that wrapper records the real binary in its private identity.
func resolveTmuxPath(explicit string, env []string) (string, error) {
	candidate := strings.TrimSpace(explicit)
	if candidate == "" {
		recorded := strings.TrimSpace(envValue(env, harnessEnvPrefix+"TMUX"))
		socket := strings.TrimSpace(envValue(env, harnessEnvPrefix+"SOCKET"))
		if recorded != "" || socket != "" {
			if recorded == "" || socket == "" {
				return "", fmt.Errorf("incomplete inherited tmux harness identity")
			}
			if !filepath.IsAbs(recorded) {
				return "", fmt.Errorf("inherited tmux harness binary is not absolute: %q", recorded)
			}
			candidate = recorded
		} else {
			var err error
			candidate, err = exec.LookPath("tmux")
			if err != nil {
				return "", fmt.Errorf("tmux is required for tmux-harness: %w", err)
			}
		}
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve tmux path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve tmux executable %q: %w", absolute, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect tmux executable %q: %w", resolved, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("tmux executable is not an executable file: %s", resolved)
	}
	return resolved, nil
}

// CommandEnv returns the isolated tmux identity used by Run. The returned
// slice is a copy and may be modified by the caller.
func (h *Harness) CommandEnv() []string {
	env := append([]string(nil), h.env...)
	env = replaceEnv(env, "TMUX", fmt.Sprintf("%s,%d,0", h.SocketPath, h.ServerPID))
	env = replaceEnv(env, "TMUX_PANE", h.LauncherPaneID)
	env = replaceEnv(env, "PWD", h.CWD)
	env = replaceEnv(env, harnessEnvPrefix+"SESSION_ID", h.SessionID)
	env = replaceEnv(env, harnessEnvPrefix+"WINDOW_ID", h.LauncherWindowID)
	env = replaceEnv(env, harnessEnvPrefix+"PANE_ID", h.LauncherPaneID)
	env = replaceEnv(env, harnessEnvPrefix+"SOCKET_PATH", h.SocketPath)
	return env
}

// Run executes argv from the harness cwd with explicit routing to the
// disposable launcher identity. It does not require the command itself to run
// inside a pty; use Attach for interactive smoke testing.
func (h *Harness) Run(ctx context.Context, argv []string, streams Streams) error {
	if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
		return fmt.Errorf("tmux harness command is required")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = h.CWD
	cmd.Env = h.CommandEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	return cmd.Run()
}

// Attach selects the exact launcher IDs and attaches an interactive client to
// this server. Ambient TMUX is stripped and -L is always explicit.
func (h *Harness) Attach(ctx context.Context, streams Streams) error {
	if err := runTmux(ctx, h.TmuxPath, h.env, "", "select-window", "-t", h.LauncherWindowID); err != nil {
		return err
	}
	if err := runTmux(ctx, h.TmuxPath, h.env, "", "select-pane", "-t", h.LauncherPaneID); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, h.TmuxPath, "-f", "/dev/null", "-L", h.SocketName, "attach-session", "-t", h.SessionID)
	cmd.Env = h.env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = streams.Stdin, streams.Stdout, streams.Stderr
	return cmd.Run()
}

// Probe verifies that the recorded socket still resolves the recorded exact
// session ID and server process. The PID check prevents a server recreated on
// the same private socket from being mistaken for the harness we started.
func (h *Harness) Probe(ctx context.Context) error {
	out, err := outputTmux(ctx, h.TmuxPath, h.env, "", "display-message", "-p", "-t", h.SessionID, "#{socket_path}\t#{session_id}\t#{pid}")
	if err != nil {
		return err
	}
	want := h.SocketPath + "\t" + h.SessionID + "\t" + strconv.Itoa(h.ServerPID)
	if strings.TrimSpace(out) != want {
		return fmt.Errorf("tmux harness teardown identity mismatch: got %q, want %q", strings.TrimSpace(out), want)
	}
	return nil
}

// Close tears down only this harness's named server. It fails closed if the
// exact socket/session identity cannot be verified.
func (h *Harness) Close() error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		h.closeErr = h.cleanup()
	})
	return h.closeErr
}

func (h *Harness) cleanup() error {
	return cleanupHarnessWith(
		h,
		h.Probe,
		func(ctx context.Context) error {
			return runTmux(ctx, h.TmuxPath, h.env, "", "kill-server")
		},
	)
}

func cleanupHarnessWith(h *Harness, probe, kill func(context.Context) error) error {
	if _, statErr := os.Stat(h.SocketPath); errors.Is(statErr, os.ErrNotExist) {
		return removeOwnedRoot(h.Root)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := probe(ctx); err != nil {
		return fmt.Errorf("refusing unverified tmux harness teardown; retained private root %s: %w", h.Root, err)
	}
	if err := kill(ctx); err != nil {
		return fmt.Errorf("kill isolated tmux server; retained private root %s: %w", h.Root, err)
	}
	return removeOwnedRoot(h.Root)
}

func validateIDs(sessionID, windowID, paneID string) error {
	if !exactSessionID.MatchString(sessionID) || !exactWindowID.MatchString(windowID) || !exactPaneID.MatchString(paneID) {
		return fmt.Errorf("tmux did not return exact ids: session=%q window=%q pane=%q", sessionID, windowID, paneID)
	}
	return nil
}

func serverEnv(input []string, root, tmuxPath, socketName string, controllerPID int) []string {
	out := make([]string, 0, len(input)+8)
	for _, entry := range input {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "PWD" || strings.HasPrefix(key, "TMUX") || strings.HasPrefix(key, harnessEnvPrefix) {
			continue
		}
		out = append(out, entry)
	}
	out = replaceEnv(out, "TMUX_TMPDIR", root)
	out = replaceEnv(out, harnessEnvPrefix+"TMUX", tmuxPath)
	out = replaceEnv(out, harnessEnvPrefix+"SOCKET", socketName)
	out = replaceEnv(out, harnessEnvPrefix+"CONTROLLER_PID", strconv.Itoa(controllerPID))
	return out
}

func replaceEnv(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

func outputTmux(ctx context.Context, tmuxPath string, env []string, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, tmuxPath, append([]string{"-f", "/dev/null", "-L", envValue(env, harnessEnvPrefix+"SOCKET")}, args...)...)
	cmd.Env = env
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return "", fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, detail)
		}
		return "", fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func runTmux(ctx context.Context, tmuxPath string, env []string, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, tmuxPath, append([]string{"-f", "/dev/null", "-L", envValue(env, harnessEnvPrefix+"SOCKET")}, args...)...)
	cmd.Env = env
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, detail)
		}
		return fmt.Errorf("tmux %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func removeOwnedRoot(root string) error {
	clean := filepath.Clean(root)
	base, err := harnessTempBase()
	if err != nil {
		return err
	}
	if filepath.Dir(clean) != base || !strings.HasPrefix(filepath.Base(clean), tempPrefix) {
		return fmt.Errorf("refusing to remove non-harness root %q", root)
	}
	return os.RemoveAll(clean)
}

func harnessTempBase() (string, error) {
	for _, candidate := range []string{"/tmp", os.TempDir()} {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		if info, statErr := os.Stat(resolved); statErr == nil && info.IsDir() {
			return filepath.Clean(resolved), nil
		}
	}
	return "", fmt.Errorf("resolve a short temporary root for tmux harness sockets")
}

const tmuxWrapper = `#!/bin/sh
set -eu
: "${AMQ_SQUAD_TMUX_HARNESS_TMUX:?missing harness tmux binary}"
: "${AMQ_SQUAD_TMUX_HARNESS_SOCKET:?missing harness socket}"
: "${TMUX_TMPDIR:?missing harness tmux root}"
exec "$AMQ_SQUAD_TMUX_HARNESS_TMUX" -f /dev/null -L "$AMQ_SQUAD_TMUX_HARNESS_SOCKET" "$@"
`

const keeperScript = `#!/bin/sh
set -u
while kill -0 "${AMQ_SQUAD_TMUX_HARNESS_CONTROLLER_PID:?}" 2>/dev/null; do
  sleep 1
done
tmux kill-server >/dev/null 2>&1 || :
`
