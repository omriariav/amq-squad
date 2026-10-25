package claudeseat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// ModeUnverified is the launch refusal of a Claude seat whose effective
// permission mode on the installed Claude Code is not the launch's mode, or
// cannot be read (phase 5 re-pass of 4-2, P1-2).
const ModeUnverified = "claude_mode_unverified"

// ProbeTimeout bounds one mode probe.
const ProbeTimeout = 15 * time.Second

// Probe is what the installed Claude Code reported for the seat's settings.
type Probe struct {
	Mode    string // the effective permissionMode of the stream-json init event
	Version string // claude_code_version of the same event
}

// ProbeMode asks the installed Claude Code which permission mode it would run
// the seat in: `claude -p --output-format stream-json --verbose
// --permission-mode MODE` with a scratch config dir holding a copy of the
// seat's settings.json, in the seat's worktree (whose settings Claude merges),
// with no credential in the environment. Claude reports the effective mode in
// its init event before any model request; the probe reads that one event and
// kills the process group. A setting that forces another mode (on 2.1.310,
// CLAUDE_CODE_SUBPROCESS_ENV_SCRUB forces default) shows as a mode other than
// MODE. Every failure is an error: the caller refuses the launch.
func ProbeMode(ctx context.Context, claude, dir, worktree, home, mode string) (Probe, error) {
	var p Probe
	settings, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		return p, err
	}
	scratch, err := os.MkdirTemp("", "amq-squad-mode-probe-")
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(scratch)
	cfg := filepath.Join(scratch, "config")
	if err := os.Mkdir(cfg, 0o700); err != nil {
		return p, err
	}
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), settings, 0o600); err != nil {
		return p, err
	}
	cwd := scratch
	if st, err := os.Stat(worktree); err == nil && st.IsDir() {
		cwd = worktree
	}
	pctx, cancel := context.WithTimeout(ctx, ProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(pctx, claude, "-p", "--output-format", "stream-json", "--verbose", "--permission-mode", mode, "mode probe")
	cmd.Dir = cwd
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "CLAUDE_CONFIG_DIR=" + cfg,
		"DISABLE_AUTOUPDATER=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.WaitDelay = time.Second
	probeGroup(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return p, err
	}
	if err := cmd.Start(); err != nil {
		return p, err
	}
	defer func() {
		killProbe(cmd)
		_ = cmd.Wait()
	}()
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		var ev struct {
			Type           string `json:"type"`
			Subtype        string `json:"subtype"`
			PermissionMode string `json:"permissionMode"`
			Version        string `json:"claude_code_version"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "system" || ev.Subtype != "init" {
			continue
		}
		p.Mode, p.Version = ev.PermissionMode, ev.Version
		if p.Mode == "" {
			return p, fmt.Errorf("the installed Claude Code (%s) reported no permission mode", orUnknown(p.Version))
		}
		return p, nil
	}
	if pctx.Err() != nil {
		return p, fmt.Errorf("the installed Claude Code reported no init event within %s", ProbeTimeout)
	}
	return p, fmt.Errorf("the installed Claude Code exited without an init event")
}

func orUnknown(s string) string {
	if s == "" {
		return "version unknown"
	}
	return s
}
