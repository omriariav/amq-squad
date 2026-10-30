package claudeseat

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ModeUnverified is the launch refusal of a Claude seat whose effective
// permission mode on the pinned Claude Code is not the launch's mode, or
// cannot be read within ProbeTimeout (phase 5 re-pass of 4-2, P1-2).
const ModeUnverified = "claude_mode_unverified"

// ProbeTimeout bounds one mode probe; a probe that takes longer is
// claude_mode_unverified (the fix round's probe ruling).
var ProbeTimeout = 5 * time.Second

// Probe is what the pinned Claude Code reported for the seat's settings.
type Probe struct {
	Mode    string // the effective permissionMode of the stream-json init event
	Version string // claude_code_version of the same event
}

// ProbeKind keys one probe per launch: seats with the same mode, the same
// pinned claude (its sha256) and the same config dir as the probe copies it
// (settings.json and the plugin install, the seat's own dir path taken out)
// are probed once; a pin or a plugin that differs re-probes (the narrow
// pass's item 5).
func ProbeKind(dir, mode, pinSHA256 string) (string, error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", mode, pinSHA256)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		rel = filepath.ToSlash(rel)
		if rel != "." && rel != "settings.json" && rel != "plugins" && !strings.HasPrefix(rel, "plugins/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		raw = []byte(strings.ReplaceAll(string(raw), dir, ""))
		fmt.Fprintf(h, "%d:%s\x00%d:", len(rel), rel, len(raw))
		h.Write(raw)
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ProbeMode asks the pinned Claude Code (command builds its invocation:
// registry.VerifiedTool.Command) which permission mode it would run the
// seat in, isolated from the owner and the project (the fix round's probe
// ruling):
//
//   - HOME is a fresh scratch directory, never the owner's;
//   - CLAUDE_CONFIG_DIR is a full copy of the seat's config dir as written
//     (settings.json and the plugin install, the plugin paths rewritten to
//     the copy), with disableAllHooks set in the copy's settings only: the
//     probe measures the mode and runs no hook, the seat keeps its hooks;
//   - the working directory is an empty scratch directory, so no project
//     settings or hooks apply;
//   - no credential is in the environment.
//
// `-p --output-format stream-json --verbose --permission-mode MODE` reports
// the effective mode in its init event before any model request; the probe
// reads that event and kills the process group. A setting that forces
// another mode (on 2.1.310 CLAUDE_CODE_SUBPROCESS_ENV_SCRUB forces default)
// shows as another mode. No init event within ProbeTimeout, or any failure,
// is an error: the caller refuses the launch.
func ProbeMode(ctx context.Context, command func(args ...string) *exec.Cmd, dir, mode string) (Probe, error) {
	var p Probe
	scratch, err := os.MkdirTemp("", "amq-squad-mode-probe-")
	if err != nil {
		return p, err
	}
	defer os.RemoveAll(scratch)
	home, cfg, work := filepath.Join(scratch, "home"), filepath.Join(scratch, "config"), filepath.Join(scratch, "work")
	for _, d := range []string{home, work} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return p, err
		}
	}
	if err := copyConfig(dir, cfg); err != nil {
		return p, fmt.Errorf("copy the seat's config dir: %w", err)
	}
	cmd := command("-p", "--output-format", "stream-json", "--verbose", "--permission-mode", mode, "mode probe")
	cmd.Dir = work
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
	// The stream is read in a goroutine under the deadline (the narrow
	// pass's item 4): a descendant that left the group and holds the pipe
	// cannot keep the probe past ProbeTimeout; after the kill, Wait's
	// WaitDelay closes the pipe and the reader returns.
	type result struct {
		p   Probe
		err error
	}
	done := make(chan result, 1)
	go func() {
		pr, err := readInit(out)
		done <- result{pr, err}
	}()
	deadline := time.NewTimer(ProbeTimeout)
	defer deadline.Stop()
	var r result
	select {
	case r = <-done:
	case <-deadline.C:
		r.err = fmt.Errorf("the pinned Claude Code reported no init event within %s", ProbeTimeout)
	case <-ctx.Done():
		r.err = ctx.Err()
	}
	killProbe(cmd)
	_ = cmd.Wait()
	return r.p, r.err
}

// readInit reads the stream-json output up to the init event.
func readInit(out io.Reader) (Probe, error) {
	var p Probe
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
			return p, fmt.Errorf("the pinned Claude Code (%s) reported no permission mode", orUnknown(p.Version))
		}
		return p, nil
	}
	return p, fmt.Errorf("the pinned Claude Code exited without an init event")
}

// copyConfig copies a seat's config dir (settings.json and the plugins
// tree, the files the seat's Claude loads) into dst, rewrites the plugin
// paths that name the seat's dir to the copy, and sets disableAllHooks in
// the copy's settings.json only.
func copyConfig(src, dst string) error {
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		rel = filepath.ToSlash(rel)
		if rel != "." && rel != "settings.json" && rel != "plugins" && !strings.HasPrefix(rel, "plugins/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, "plugins/") && strings.HasSuffix(rel, ".json") {
			raw = []byte(strings.ReplaceAll(string(raw), src, dst))
		}
		if rel == "settings.json" {
			var settings map[string]any
			if err := json.Unmarshal(raw, &settings); err != nil {
				return fmt.Errorf("settings.json: %w", err)
			}
			settings["disableAllHooks"] = true // the probe's copy only
			if raw, err = json.MarshalIndent(settings, "", "  "); err != nil {
				return err
			}
		}
		return os.WriteFile(target, raw, info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	return os.Chmod(dst, 0o700)
}

func orUnknown(s string) string {
	if s == "" {
		return "version unknown"
	}
	return s
}
