package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/agentcatalog"
	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
	"github.com/omriariav/amq-squad/v3/internal/registry"
)

// doctorHost is the host probe of plan 16.2: tool detection, versions, login
// state, the registry, local model probes and latency, sleep and the login
// service, the runtime re-hash, Codex trust and notify, the shell profile,
// the owner config diff and the grants file mode. Every path derives from
// Home, so tests run it on a temp HOME.
type doctorHost struct {
	Home      string
	GOOS      string
	CodexHome string
	Env       registry.Env
	Detect    func(ctx context.Context, env registry.Env) registry.Detection
	Pmset     func(ctx context.Context) (string, error)
	Catalog   agentcatalog.Catalog

	WriteRegistry bool
	Probe         bool
	ProbeRuns     int
	ProbeCodex    func(ctx context.Context, env registry.Env, path, version, model string) registry.CodexVerdict
	ProbeLocal    func(ctx context.Context, model string, runs int) (registry.ProbeRecord, error)
	TTFT          func(ctx context.Context, model string) (registry.Latency, error)
}

func defaultDoctorHost() *doctorHost {
	home, _ := os.UserHomeDir()
	return &doctorHost{
		Home: home, GOOS: runtime.GOOS, CodexHome: os.Getenv("CODEX_HOME"),
		Env:    registry.Env{Home: home},
		Detect: registry.Detect,
		Pmset: func(ctx context.Context) (string, error) {
			out, err := exec.CommandContext(ctx, "pmset", "-g").Output()
			return string(out), err
		},
		ProbeCodex: registry.ProbeCodexModel,
	}
}

func hostCheck(name string, status doctorStatus, format string, args ...any) doctorCheck {
	return doctorCheck{Name: name, Status: status, Detail: fmt.Sprintf(format, args...)}
}

func (h *doctorHost) checks(projectDir string) []doctorCheck {
	if h.Home == "" {
		return []doctorCheck{hostCheck("host", doctorWarn, "no home directory; host checks skipped")}
	}
	ctx := context.Background()
	env := h.Env
	env.Home = h.Home
	det := h.Detect(ctx, env)
	var out []doctorCheck
	for _, t := range det.Tools {
		out = append(out, toolCheck(t))
	}
	out = append(out, hostCheck("host", doctorOK, "RAM %d GB, GPU %s", det.Host.RAMGB, det.Host.GPU))
	if det.Tool("ollama").Found {
		if det.OllamaErr != "" {
			out = append(out, hostCheck("ollama models", doctorWarn, "%s", det.OllamaErr))
		} else {
			out = append(out, hostCheck("ollama models", doctorOK, "%d model(s)", len(det.Ollama)))
		}
	}
	out = append(out, h.registryChecks(ctx, env, det)...)
	out = append(out, runtimeCheck(det))
	out = append(out, h.serviceChecks(ctx, det)...)
	out = append(out, h.codexChecks(det, projectDir)...)
	out = append(out, profileCheck(h.Home))
	for _, c := range hostcheck.OwnerConfigs(h.Home) {
		out = append(out, ownerConfigCheck(hostcheck.DiffOwnerConfig(c)))
	}
	return append(out, grantsCheck(h.Home))
}

// toolCheck is one status line per tool. Only amq is required.
func toolCheck(t registry.Tool) doctorCheck {
	name := "tool/" + t.Name
	if !t.Found {
		if t.Name == "amq" {
			return hostCheck(name, doctorFail, "amq is not on PATH (required)")
		}
		return hostCheck(name, doctorOK, "not on PATH (optional)")
	}
	status := doctorOK
	parts := []string{fmt.Sprintf("%s at %s", orUnknown(t.Version), t.Path)}
	if t.VerifiedVersion != "" {
		parts = append(parts, fmt.Sprintf("launch API verified %s (%s)", t.VerifiedVersion, t.VersionCheck))
		// An older provider than the launch API verified, or an amq newer than
		// the pinned launch library (gh#766: never verified against it), warns.
		if t.VersionCheck == "older" || t.Name == "amq" && t.VersionCheck == "newer" {
			status = doctorWarn
		}
	}
	switch t.Login {
	case "logged_in":
		parts = append(parts, "logged in ("+t.LoginSource+")")
	case "logged_out":
		parts = append(parts, "not logged in")
		status = doctorWarn
	case "unknown":
		parts = append(parts, "login state unknown")
	}
	return doctorCheck{Name: name, Status: status, Detail: strings.Join(parts, "; ")}
}

func orUnknown(s string) string {
	if s == "" {
		return "version unknown"
	}
	return s
}

// registryChecks builds the registry from this detection (plus the probes
// under --probe), reports it, and writes it under --write-registry.
func (h *doctorHost) registryChecks(ctx context.Context, env registry.Env, det registry.Detection) []doctorCheck {
	path := registry.Path(h.Home)
	prev, readErr := registry.Read(path)
	var out []doctorCheck
	opts := registry.BuildOptions{Catalog: h.Catalog}
	if h.Probe {
		var probeChecks []doctorCheck
		opts, probeChecks = h.runProbes(ctx, env, det)
		out = append(out, probeChecks...)
	}
	reg := registry.Build(det, prev, opts)
	verified := len(reg.Proposable())
	summary := fmt.Sprintf("%d entries, %d verified (the lead proposes seats from these only)", len(reg.Entries), verified)
	switch {
	case h.WriteRegistry:
		if err := registry.Write(path, reg); err != nil {
			out = append(out, hostCheck("registry", doctorFail, "write %s: %v", path, err))
		} else {
			out = append(out, hostCheck("registry", doctorOK, "%s; written to %s", summary, path))
		}
	case errors.Is(readErr, fs.ErrNotExist):
		out = append(out, hostCheck("registry", doctorWarn, "%s; %s absent (run 'amq-squad doctor --write-registry')", summary, path))
	case readErr != nil:
		out = append(out, hostCheck("registry", doctorFail, "%v", readErr))
	case registry.Format(prev) != registry.Format(reg):
		out = append(out, hostCheck("registry", doctorWarn, "%s; %s is out of date (run 'amq-squad doctor --write-registry')", summary, path))
	default:
		out = append(out, hostCheck("registry", doctorOK, "%s; %s is current", summary, path))
	}
	return append(out, entryStateChecks(reg)...)
}

// entryStateChecks names the models that are not usable, one line per runtime.
func entryStateChecks(reg registry.Registry) []doctorCheck {
	byRuntime := map[string]map[string]string{}
	for _, e := range reg.Entries {
		if e.Verified || e.State == registry.StateMissingTool || e.State == registry.StateLoggedOut {
			continue
		}
		if byRuntime[e.Runtime] == nil {
			byRuntime[e.Runtime] = map[string]string{}
		}
		byRuntime[e.Runtime][e.Model] = e.State + ": " + e.Reason
	}
	var out []doctorCheck
	for _, rt := range []string{"claude", "codex", "pi"} {
		models := byRuntime[rt]
		if len(models) == 0 {
			continue
		}
		var parts []string
		for m, why := range models {
			parts = append(parts, m+" "+why)
		}
		sort.Strings(parts)
		out = append(out, hostCheck("registry/"+rt, doctorWarn, "unverified: %s", strings.Join(parts, "; ")))
	}
	return out
}

// runtimeCheck runs the full re-hash of the embedded pi runtime (ruled: the
// full re-hash runs at every launch, and doctor runs the same check).
func runtimeCheck(det registry.Detection) doctorCheck {
	if det.PiRuntimeDir == "" {
		return hostCheck("pi runtime", doctorOK, "the built-in pi runtime is not installed")
	}
	start := time.Now()
	res, err := registry.RehashRuntime(det.PiRuntimeDir, "")
	if err != nil {
		return hostCheck("pi runtime", doctorFail, "%s rejected (remove it explicitly to repair): %v", det.PiRuntimeDir, err)
	}
	return hostCheck("pi runtime", doctorOK, "%d files, %d MB re-hashed in %d ms", res.Files, res.Bytes>>20, time.Since(start).Milliseconds())
}

// serviceChecks are the plan 8 warnings: sleep permitted, and no login
// service for amq-remote. macOS only (the 3.0 host).
func (h *doctorHost) serviceChecks(ctx context.Context, det registry.Detection) []doctorCheck {
	if h.GOOS != "darwin" {
		return nil
	}
	var out []doctorCheck
	if h.Pmset != nil {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		text, err := h.Pmset(cctx)
		cancel()
		permitted, detail, ok := hostcheck.SleepPermitted(text)
		switch {
		case err != nil || !ok:
			out = append(out, hostCheck("sleep", doctorWarn, "could not read pmset -g"))
		case permitted:
			out = append(out, hostCheck("sleep", doctorWarn, "%s", detail))
		default:
			out = append(out, hostCheck("sleep", doctorOK, "%s", detail))
		}
	}
	switch services := hostcheck.LoginServices(h.Home); {
	case !det.Tool("amq-remote").Found:
		out = append(out, hostCheck("amq-remote login service", doctorOK, "amq-remote is not installed"))
	case len(services) == 0:
		out = append(out, hostCheck("amq-remote login service", doctorWarn, "no launchd agent for amq-remote under ~/Library/LaunchAgents; remote access stops at logout or reboot"))
	default:
		out = append(out, hostCheck("amq-remote login service", doctorOK, "%s", strings.Join(services, ", ")))
	}
	return out
}

// codexChecks list project roots codex-cli does not trust (amendment 1) and
// check the candidate notify fan-out line for presence only (16.8).
func (h *doctorHost) codexChecks(det registry.Detection, projectDir string) []doctorCheck {
	if !det.Tool("codex").Found {
		return nil
	}
	path := hostcheck.CodexConfigPath(h.Home, h.CodexHome)
	ct, err := hostcheck.ReadCodexTrust(path)
	if err != nil {
		return []doctorCheck{hostCheck("codex trust", doctorWarn, "read %s: %v", path, err)}
	}
	var out []doctorCheck
	roots := []string{canonicalFilesystemPath(projectDir)}
	if untrusted := ct.UntrustedRoots(roots); len(untrusted) > 0 {
		out = append(out, hostCheck("codex trust", doctorWarn, "codex-cli does not trust %s; a Codex seat stops on its folder-trust prompt. Run 'amq-squad trust %s' for the owner-run steps", strings.Join(untrusted, ", "), untrusted[0]))
	} else {
		out = append(out, hostCheck("codex trust", doctorOK, "%s is trusted in %s", strings.Join(roots, ", "), path))
	}
	if ct.HasNotifyFanout() {
		out = append(out, hostCheck("codex notify fan-out", doctorOK, "present in %s", path))
	} else {
		out = append(out, hostCheck("codex notify fan-out", doctorOK, "absent (candidate, plan 16.8; 'amq-squad trust' prints the line)"))
	}
	return out
}

func profileCheck(home string) doctorCheck {
	exports := hostcheck.ShellProfileExports(home)
	if len(exports) == 0 {
		return hostCheck("shell profile", doctorOK, "no exported *_API_KEY, *_TOKEN or *_SECRET names")
	}
	var parts []string
	for _, e := range exports {
		parts = append(parts, fmt.Sprintf("%s (%s:%d)", e.Name, e.File, e.Line))
	}
	return hostCheck("shell profile", doctorWarn, "exported key names reach provider tool shells, past the seat-exec scrub: %s", strings.Join(parts, ", "))
}

func ownerConfigCheck(d hostcheck.ConfigDiff) doctorCheck {
	name := "owner config/" + d.Config.Name
	switch d.State {
	case "same":
		return hostCheck(name, doctorOK, "%s matches the preflight copy", d.Config.Live)
	case "no_preflight":
		return hostCheck(name, doctorOK, "no preflight copy yet (seat-exec writes %s at launch)", d.Config.Preflight)
	case "no_live":
		return hostCheck(name, doctorWarn, "%s is gone since the preflight copy", d.Config.Live)
	case "changed":
		return hostCheck(name, doctorWarn, "%s changed since the preflight copy: %s", d.Config.Live, strings.Join(d.Lines, " | "))
	}
	return hostCheck(name, doctorWarn, "%s or its preflight copy is unreadable", d.Config.Live)
}

var maxSeatsRE = regexp.MustCompile(`(?m)^max_seats:\s*(\d+)\s*(#.*)?$`)

// grantsCheck reports the owner grants file mode (2b-3 ruling 1: an
// untrusted file fails closed with owner_grants_insecure) and the seat cap
// (amendment 22: default 12 per session).
func grantsCheck(home string) doctorCheck {
	path := hostcheck.GrantsPath(home)
	mode, present, reason := hostcheck.GrantsMode(path)
	if !present {
		return hostCheck("grants", doctorOK, "%s absent: built-in defaults, seat cap 12 per session", path)
	}
	if reason != "" {
		return doctorCheck{Name: "grants", Status: doctorFail, Kind: hostcheck.ReasonGrantsInsecure,
			Detail: fmt.Sprintf("%s: mode %s; %s; fail closed: no new seat starts, typed gates stay needs_gate (fix: chmod 600 %s)", hostcheck.ReasonGrantsInsecure, mode, reason, path)}
	}
	seats := "12 (default)"
	if raw, err := os.ReadFile(path); err == nil {
		if m := maxSeatsRE.FindSubmatch(raw); m != nil {
			seats = string(m[1])
		}
	}
	return hostCheck("grants", doctorOK, "%s mode %s; seat cap %s per session", filepath.Clean(path), mode, seats)
}

// runProbes runs the slow probes of --probe: the Codex model id turn per
// catalog model, and, where a probe function is set, the R12 probe and the
// direct TTFT per local model.
func (h *doctorHost) runProbes(ctx context.Context, env registry.Env, det registry.Detection) (registry.BuildOptions, []doctorCheck) {
	opts := registry.BuildOptions{Catalog: h.Catalog, CodexProbes: map[string]registry.CodexVerdict{},
		Probes: map[string]registry.ProbeRecord{}, Latency: map[string]registry.Latency{}}
	var out []doctorCheck
	if codex := det.Tool("codex"); codex.Found && h.ProbeCodex != nil && codex.Login != "logged_out" {
		for _, m := range h.Catalog.Effective().Entries("codex", agentcatalog.Models) {
			v := h.ProbeCodex(ctx, env, codex.Path, codex.Version, m.Value)
			opts.CodexProbes[m.Value] = v
			status, detail := doctorOK, "usable"
			if !v.Usable {
				status, detail = doctorWarn, v.Reason
			}
			out = append(out, hostCheck("probe/codex/"+m.Value, status, "%s", detail))
		}
	}
	if !det.Tool("pi").Found {
		return opts, out
	}
	for _, m := range det.Ollama {
		id := "ollama/" + m.Name
		if m.CapsKnown && !m.Tools {
			continue
		}
		if h.ProbeLocal != nil {
			start := time.Now()
			rec, err := h.ProbeLocal(ctx, id, h.ProbeRuns)
			if err != nil {
				out = append(out, hostCheck("probe/"+id, doctorWarn, "R12 probe did not run: %v", err))
			} else {
				opts.Probes[id] = rec
				out = append(out, hostCheck("probe/"+id, doctorOK, "R12 %d of %d passes, %d false success(es), %d tool error(s) in %s",
					rec.Passes, rec.Runs, rec.FalseSuccesses, rec.ToolErrors, time.Since(start).Round(time.Second)))
			}
		}
		if h.TTFT != nil {
			if lat, err := h.TTFT(ctx, m.Name); err == nil {
				opts.Latency[id] = lat
			}
		}
	}
	return opts, out
}
