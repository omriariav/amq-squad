package cli

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestPrivateTmuxExactPausedStateContinuesOnlyExactVerifiedPane(t *testing.T) {
	if testing.Short() {
		t.Skip("real private tmux control-mode coverage")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("iTerm2 tmux -CC regression harness is Darwin-specific")
	}
	tmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux is unavailable")
	}
	script, err := exec.LookPath("script")
	if err != nil {
		t.Skip("script is unavailable for the private control-client PTY")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tmuxTmp := shortTestTempDir(t, "a505-control-tmux-")
	socket := fmt.Sprintf("a505-%d-%d", os.Getpid(), time.Now().UnixNano())
	var tmuxEnv []string
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "TMUX=") || strings.HasPrefix(entry, "TMUX_PANE=") || strings.HasPrefix(entry, "TMUX_TMPDIR=") {
			continue
		}
		tmuxEnv = append(tmuxEnv, entry)
	}
	tmuxEnv = append(tmuxEnv, "TMUX_TMPDIR="+tmuxTmp)
	tmuxOutput := func(name string, args ...string) (string, error) {
		if name != "tmux" {
			return "", fmt.Errorf("unexpected binary %q", name)
		}
		cmd := exec.CommandContext(ctx, tmux, append([]string{"-L", socket, "-f", "/dev/null"}, args...)...)
		cmd.Env = tmuxEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("private tmux %v: %w: %s", args, err, strings.TrimSpace(string(out)))
		}
		return string(out), nil
	}
	tmuxRun := func(name string, args ...string) error {
		_, err := tmuxOutput(name, args...)
		return err
	}
	privateSocketPath := ""
	cleanup := func() {
		if privateSocketPath == "" || !pathWithinTestRoot(tmuxTmp, privateSocketPath) {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cleanupCancel()
		cmd := exec.CommandContext(cleanupCtx, tmux, "-L", socket, "-f", "/dev/null", "kill-server")
		cmd.Env = tmuxEnv
		_ = cmd.Run()
	}
	defer cleanup()

	created, err := tmuxOutput("tmux", "new-session", "-d", "-P", "-F", "#{session_name}\t#{window_id}\t#{pane_id}\t#{socket_path}", "-s", "control-harness", "-n", "target", "-c", t.TempDir())
	startupMessage := strings.ToLower(created)
	if err != nil {
		startupMessage += " " + strings.ToLower(err.Error())
	}
	if strings.Contains(startupMessage, "operation not permitted") || strings.Contains(startupMessage, "permission denied") {
		t.Skipf("private tmux socket unavailable: %v %s", err, strings.TrimSpace(created))
	}
	if err != nil {
		t.Fatal(err)
	}
	createdFields := strings.Split(strings.TrimSpace(created), "\t")
	if len(createdFields) != 4 {
		t.Fatalf("private target identity=%q", created)
	}
	terminalSession, windowID, paneID := createdFields[0], createdFields[1], createdFields[2]
	privateSocketPath = filepath.Clean(createdFields[3])
	if !pathWithinTestRoot(tmuxTmp, privateSocketPath) {
		t.Fatalf("private tmux socket escaped TMUX_TMPDIR: root=%s socket=%s", tmuxTmp, privateSocketPath)
	}
	if _, err := os.Lstat(privateSocketPath); err != nil {
		t.Fatalf("private tmux socket is not inspectable: %v", err)
	}
	sibling, err := tmuxOutput("tmux", "new-window", "-d", "-P", "-F", "#{window_id}\t#{pane_id}", "-t", terminalSession+":", "-n", "sibling", "-c", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	siblingFields := strings.Split(strings.TrimSpace(sibling), "\t")
	if len(siblingFields) != 2 {
		t.Fatalf("private sibling identity=%q", sibling)
	}

	// tmux -CC is a terminal client and calls tcgetattr during startup. The
	// standard-library pipes below are not a TTY, so run only this disposable
	// client through script(1)'s PTY while every tmux command remains pinned to
	// the unique -L socket and /dev/null configuration.
	control := exec.CommandContext(ctx, script, "-q", "/dev/null", tmux, "-L", socket, "-f", "/dev/null", "-CC", "attach-session", "-f", "pause-after=1", "-t", terminalSession)
	control.Env = tmuxEnv
	stdin, err := control.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := control.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := control.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	var controlStderr synchronizedTestBuffer
	go func() { _, _ = io.Copy(&controlStderr, stderr) }()
	protocol := make(chan string, 16)
	go func() {
		defer close(protocol)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "%pause") || strings.HasPrefix(line, "%continue") {
				select {
				case protocol <- line:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	if err := control.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = stdin.Close()
		if control.Process != nil {
			_ = control.Process.Kill()
		}
		_ = control.Wait()
	}()

	var clients []tmuxClient
	var discoveryAttempts []string
	lastRawDiscovery := ""
	for {
		clients, lastRawDiscovery, err = readExactTmuxControlClients(terminalSession, tmuxOutput)
		if err != nil {
			discoveryAttempts = append(discoveryAttempts, "error="+err.Error())
		} else {
			discoveryAttempts = append(discoveryAttempts, fmt.Sprintf("rows=%d", len(clients)))
		}
		if len(discoveryAttempts) > 12 {
			discoveryAttempts = discoveryAttempts[len(discoveryAttempts)-12:]
		}
		if err == nil && len(clients) == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("control client did not attach: attempts=%v parsed=%+v err=%v last_raw=%q stderr=%q", discoveryAttempts, clients, err, lastRawDiscovery, controlStderr.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	client := clients[0].Name
	baselineTopology, err := tmuxOutput("tmux", "list-panes", "-a", "-F", "#{window_id}\t#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	baselineFocus, err := tmuxOutput("tmux", "display-message", "-p", "-t", paneID, "#{window_active}\t#{pane_active}")
	if err != nil {
		t.Fatal(err)
	}

	if err := tmuxRun("tmux", "refresh-client", "-t", client, "-A", paneID+":pause"); err != nil {
		t.Fatal(err)
	}
	waitProtocolLine(t, ctx, protocol, "%pause", paneID)
	if err := tmuxRun("tmux", "send-keys", "-t", paneID, "i=0; while [ $i -lt 512 ]; do printf 'a505-burst-%04d\\n' \"$i\"; i=$((i+1)); done", "C-m"); err != nil {
		t.Fatal(err)
	}

	project := t.TempDir()
	rec := launch.Record{Role: "qa", Handle: "qa", TeamProfile: team.DefaultProfile, Session: "workstream",
		Tmux: &launch.TmuxInfo{Target: "new-window", Session: terminalSession, WindowID: windowID, PaneID: paneID}}
	rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
	mr := memberRuntime{Member: team.Member{Role: "qa", Handle: "qa", Binary: "codex", Session: "workstream"}, Profile: team.DefaultProfile, Handle: "qa", HasRecord: true, Record: rec}
	canonical, err := liveidentity.CanonicalProject(project)
	if err != nil {
		t.Fatal(err)
	}
	verified := liveidentity.Result{SchemaVersion: liveidentity.SchemaVersion, Verified: &liveidentity.Verified{
		Key: liveidentity.Key{Project: canonical, Profile: team.DefaultProfile, Session: "workstream", Handle: "qa"}, Role: "qa", Terminal: liveIdentityTerminal(rec),
	}}
	deps := tmuxControlContinueDeps{Verify: func(_, _, _, _ string) (liveidentity.Result, error) { return verified, nil }, Output: tmuxOutput, Run: tmuxRun}
	if _, err := resolveExactTmuxControlContinue(project, team.DefaultProfile, "workstream", "qa", client+"-wrong", mr, deps); err == nil || !strings.Contains(err.Error(), "differs from unique resolved client") {
		t.Fatalf("wrong client error=%v", err)
	}
	wrongSession := mr
	wrongSession.Record.Tmux = &launch.TmuxInfo{Target: "new-window", Session: "wrong-session", WindowID: windowID, PaneID: paneID}
	wrongSession.Record.Terminal = launch.TerminalInfoFromTmux(wrongSession.Record.Tmux)
	if _, err := resolveExactTmuxControlContinue(project, team.DefaultProfile, "workstream", "qa", client, wrongSession, deps); err == nil || !strings.Contains(err.Error(), "authoritative live identity differs") {
		t.Fatalf("wrong session error=%v", err)
	}
	wrongPane := mr
	wrongPane.Record.Tmux = &launch.TmuxInfo{Target: "new-window", Session: terminalSession, WindowID: siblingFields[0], PaneID: siblingFields[1]}
	wrongPane.Record.Terminal = launch.TerminalInfoFromTmux(wrongPane.Record.Tmux)
	if _, err := resolveExactTmuxControlContinue(project, team.DefaultProfile, "workstream", "qa", client, wrongPane, deps); err == nil || !strings.Contains(err.Error(), "authoritative live identity differs") {
		t.Fatalf("wrong pane error=%v", err)
	}

	target, err := resolveExactTmuxControlContinue(project, team.DefaultProfile, "workstream", "qa", client, mr, deps)
	if err != nil {
		t.Fatal(err)
	}
	if err := continueExactTmuxControlClient(target, deps.Run); err != nil {
		t.Fatal(err)
	}
	waitProtocolLine(t, ctx, protocol, "%continue", paneID)
	afterTopology, err := tmuxOutput("tmux", "list-panes", "-a", "-F", "#{window_id}\t#{pane_id}")
	if err != nil {
		t.Fatal(err)
	}
	afterFocus, err := tmuxOutput("tmux", "display-message", "-p", "-t", paneID, "#{window_active}\t#{pane_active}")
	if err != nil {
		t.Fatal(err)
	}
	if afterTopology != baselineTopology || afterFocus != baselineFocus {
		t.Fatalf("continue mutated topology/focus: topology %q -> %q focus %q -> %q", baselineTopology, afterTopology, baselineFocus, afterFocus)
	}
	siblingAfter, err := tmuxOutput("tmux", "display-message", "-p", "-t", siblingFields[1], "#{window_id}\t#{pane_id}")
	if err != nil || strings.TrimSpace(siblingAfter) != strings.Join(siblingFields, "\t") {
		t.Fatalf("sibling changed: got=%q want=%q err=%v", siblingAfter, strings.Join(siblingFields, "\t"), err)
	}
}

func waitProtocolLine(t *testing.T, ctx context.Context, lines <-chan string, kind, paneID string) {
	t.Helper()
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("control protocol closed before %s for %s", kind, paneID)
			}
			if strings.HasPrefix(line, kind) && strings.Contains(line, paneID) {
				return
			}
		case <-ctx.Done():
			t.Fatalf("did not observe %s for %s: %v", kind, paneID, ctx.Err())
		}
	}
}

func pathWithinTestRoot(root, candidate string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

type synchronizedTestBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *synchronizedTestBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *synchronizedTestBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
