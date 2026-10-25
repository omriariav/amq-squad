package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/agentcatalog"
	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/hostcheck"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/usagelimit"
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
	UsageScan     time.Duration // > 0: report the session files that carried a limit signal in this window
	Now           func() time.Time
	Probe         bool
	ProbeRuns     int
	ProbeCodex    func(ctx context.Context, env registry.Env, path, version, model string) registry.CodexVerdict
	ProbeLocal    func(ctx context.Context, h *doctorHost, det registry.Detection, rt registry.RehashResult, model string) (registry.ProbeRecord, error)
	TTFT          func(ctx context.Context, env registry.Env, model string) (registry.Latency, error)
	// Fix lets the pi runtime row repair (doctor --fix, brief 4-1).
	Fix bool
	// LoginService reads amq-remote's own login service state (brief 4-5);
	// nil skips that row (tests).
	LoginService buzz.LoginService
	// piRuntime is the embedded runtime the row verified (4-1); the R12
	// probe runs its PiArgv.
	piRuntime *piruntime.Runtime
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
		ProbeCodex:   registry.ProbeCodexModel,
		ProbeLocal:   defaultProbeLocal,
		LoginService: buzz.AMQRemoteService{},
		TTFT: func(ctx context.Context, env registry.Env, model string) (registry.Latency, error) {
			return registry.MeasureTTFT(ctx, env, model, 2048)
		},
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
	// The runtime is verified before anything reads it or any probe command
	// is built or run; the pi version shown comes from the verified manifest.
	rtCheck, rt, runtimeOK := h.runtimeCheck(det)
	var out []doctorCheck
	for _, t := range det.Tools {
		if t.Name == "pi" && runtimeOK {
			t.Version = rt.Manifest.Version
		}
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
	out = append(out, h.registryChecks(ctx, env, det, rt, runtimeOK)...)
	out = append(out, rtCheck)
	out = append(out, h.serviceChecks(ctx, det)...)
	out = append(out, h.codexChecks(det, projectDir)...)
	out = append(out, profileCheck(h.Home))
	for _, c := range hostcheck.OwnerConfigs(h.Home) {
		out = append(out, ownerConfigCheck(hostcheck.DiffOwnerConfig(c)))
	}
	out = append(out, grantsCheck(h.Home, projectDir))
	out = append(out, routingCheck(h.Home, projectDir))
	out = append(out, claudeAllowRulesCheck(projectDir))
	out = append(out, pushTargetChecks(h.Home, projectDir)...)
	out = append(out, claudeTokenCheck(h.Home, time.Now()))
	out = append(out, claudePinCheck(h.Home, env.LookPath)...)
	out = append(out, statusContractCheck(status.Golden, projectDir, time.Now()))
	if h.UsageScan > 0 {
		out = append(out, h.usageScanChecks()...)
	}
	return out
}

// usageScanChecks is the owner-run measurement of brief 2b-3 (lead ruling
// 1): which Claude and Codex session files carried a usage-limit, 429 or API
// error signal in the window. Read-only; it prints paths and counts, never
// message bodies beyond the provider's first 160 characters.
func (h *doctorHost) usageScanChecks() []doctorCheck {
	now := time.Now
	if h.Now != nil {
		now = h.Now
	}
	since := now().Add(-h.UsageScan)
	d := usagelimit.ForHome(h.Home, os.Getenv("CLAUDE_CONFIG_DIR"), h.CodexHome)
	events, _ := d.Scan(since)
	limits := 0
	for _, e := range events {
		if e.Kind == usagelimit.KindUsageLimit {
			limits++
		}
	}
	status := doctorOK
	if limits > 0 {
		status = doctorWarn
	}
	out := []doctorCheck{hostCheck("usage scan", status, "%d usage-limit event(s), %d signal(s) in all since %s, in %s and %s",
		limits, len(events), since.UTC().Format(time.RFC3339), d.ClaudeProjects, d.CodexSessions)}
	for _, f := range usagelimit.Summarize(events) {
		var parts []string
		for _, k := range []string{usagelimit.KindUsageLimit, usagelimit.KindHTTP429, usagelimit.KindRetry429, usagelimit.KindServerError, usagelimit.KindOtherError} {
			if n := f.Counts[k]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", k, n))
			}
		}
		out = append(out, hostCheck("usage/"+f.Provider, doctorOK, "%s: %s", f.File, strings.Join(parts, " ")))
	}
	return out
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
func (h *doctorHost) registryChecks(ctx context.Context, env registry.Env, det registry.Detection, rt registry.RehashResult, runtimeOK bool) []doctorCheck {
	path := registry.Path(h.Home)
	prev, readErr := registry.Read(path)
	var out []doctorCheck
	opts := registry.BuildOptions{Catalog: h.Catalog}
	if h.Probe {
		var probeChecks []doctorCheck
		opts, probeChecks = h.runProbes(ctx, env, det, rt, runtimeOK)
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

// runtimeCheck runs the full re-hash of the embedded pi runtime against the
// manifest digest this binary embeds (ruled: the full re-hash runs at every
// launch, and doctor runs the same check). ok reports a verified runtime.
//
// With the payload embedded (4-1), the row is piruntime's: the runtime this
// binary embeds is opened from HOME through descriptors and re-hashed against
// the embedded manifest digest, and the verified Runtime is kept for the R12
// probe, which runs its PiArgv (node, the self-check preload, the pi entry).
// With --fix a rejected runtime is removed (the next launch unpacks it).
func (h *doctorHost) runtimeCheck(det registry.Detection) (doctorCheck, registry.RehashResult, bool) {
	e, err := piLoad()
	if err != nil {
		return runtimeCheck(h.Home, det)
	}
	check, rt := doctorPiRuntimeRow(h.Home, e, h.Fix)
	if rt == nil {
		return check, registry.RehashResult{}, false
	}
	h.piRuntime = rt
	res := registry.RehashResult{Dir: rt.Dir, Manifest: registry.RuntimeManifest{Version: rt.Manifest.Version, Executable: rt.Manifest.Executable, Args: rt.Manifest.Args}}
	for _, f := range rt.Manifest.Files {
		res.Files++
		res.Bytes += f.Size
	}
	return check, res, true
}

func runtimeCheck(home string, det registry.Detection) (doctorCheck, registry.RehashResult, bool) {
	if det.PiRuntimeDir == "" {
		return hostCheck("pi runtime", doctorOK, "the built-in pi runtime is not installed"), registry.RehashResult{}, false
	}
	start := time.Now()
	rel, err := filepath.Rel(home, det.PiRuntimeDir)
	if err == nil && (rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
		err = fmt.Errorf("not under the home directory")
	}
	var res registry.RehashResult
	if err == nil {
		// Anchored at the home directory: no component below it is followed.
		res, err = registry.RehashRuntime(home, rel, registry.EmbeddedRuntimeManifestSHA256)
	}
	if err != nil {
		return hostCheck("pi runtime", doctorFail, "%s rejected (remove it explicitly to repair): %v", det.PiRuntimeDir, err), registry.RehashResult{}, false
	}
	return hostCheck("pi runtime", doctorOK, "%d files, %d MB re-hashed in %d ms", res.Files, res.Bytes>>20, time.Since(start).Milliseconds()), res, true
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
	// 4-5: amq-remote's own view (installed for this root, current binary, running)
	if h.LoginService != nil && det.Tool("amq-remote").Found {
		if w := buzz.ServiceWarning(ctx, h.LoginService, ""); w != "" {
			out = append(out, hostCheck("amq-remote login service state", doctorWarn, "%s", w))
		}
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

// grantsCheck reports the resolved standing grant through 3-1's loader: the
// owner file mode (2b-3 ruling 1: an untrusted file fails closed with
// owner_grants_insecure), the seat cap (amendment 22: default 12 per
// session) and any squad.yaml field that tried to widen it.
// claudeAllowRulesCheck warns when the repository's Claude settings files,
// which still merge into every Claude seat's config, carry permission allow
// rules: seat-exec refuses Claude seats there (claude_allow_rules_present),
// because under dontAsk a hook that fails open hands the call to them (2b-2
// milestone B). The owner's user-scope settings never reach a seat.
func claudeAllowRulesCheck(projectDir string) doctorCheck {
	if projectDir == "" {
		return hostCheck("claude seat allow rules", doctorOK, "no project")
	}
	if rules := claudeseat.WorktreeAllowRules(projectDir); len(rules) > 0 {
		return hostCheck("claude seat allow rules", doctorWarn, "%s carries permission allow rules; Claude seats there are refused (claude_allow_rules_present): %s", projectDir, strings.Join(rules, "; "))
	}
	return hostCheck("claude seat allow rules", doctorOK, "no allow rules in .claude/settings.json or settings.local.json")
}

// pushTargetChecks are doctor's rows of the push target (2b-2 milestone B
// D2): a local push_url the owner acknowledged has no server-side ruleset
// (a warning; the merge train refuses a real run against it), and the gh the
// harness runs is the registry's pin, re-hashed now; with no pin, doctor
// proposes one from `command -v gh`, but only the owner's registry write
// counts.
func pushTargetChecks(home, projectDir string) []doctorCheck {
	var out []doctorCheck
	g, err := grants.LoadFrom(hostcheck.GrantsPath(home), projectDir)
	if err != nil {
		return nil
	}
	https := false
	for _, m := range g.Merge {
		switch grants.PushKind(m.PushURL) {
		case grants.PushLocal:
			out = append(out, hostCheck("push target", doctorWarn, "push target has no server enforcement: %s pushes to the local %s (merge.local_push_ack); the merge train refuses a real run against it", m.Repository, m.PushURL))
		case grants.PushHTTPS:
			https = true
		}
	}
	reg, _ := registry.Read(registry.Path(home))
	pin, pinned := reg.PinFor("gh")
	switch {
	case pinned:
		if path, sum, code, detail := pin.Verify(); code != "" {
			out = append(out, hostCheck("gh pin", doctorFail, "%s: %s", code, detail))
		} else {
			out = append(out, hostCheck("gh pin", doctorOK, "%s sha256 %s", path, sum))
		}
	default:
		proposal := ""
		if p, err := exec.LookPath("gh"); err == nil {
			if real, err := filepath.EvalSymlinks(p); err == nil {
				if sum, err := registry.FileSHA256(real); err == nil {
					proposal = fmt.Sprintf("; to pin it, add to %s: pins: - name: gh, path: %s, sha256: %s", registry.Path(home), real, sum)
				}
			}
		}
		status := doctorWarn
		if https {
			status = doctorFail // an https push needs the pinned gh as its credential helper
		}
		out = append(out, hostCheck("gh pin", status, "%s: the registry pins no gh (PRs and https pushes use only a pinned gh, never PATH)%s", registry.PinMissing, proposal))
	}
	return out
}

// claudeTokenCheck reports the owner's Claude token file for seats: present,
// 0600 in an owner-only directory, younger than the rotation window (30 days,
// a proposal). It never prints the token.
func claudeTokenCheck(home string, now time.Time) doctorCheck {
	if ok, detail := claudeseat.TokenStatus(home, now); !ok {
		return hostCheck("claude seat token", doctorWarn, "%s", detail)
	} else {
		return hostCheck("claude seat token", doctorOK, "%s", detail)
	}
}

func grantsCheck(home, projectDir string) doctorCheck {
	path := hostcheck.GrantsPath(home)
	r, err := grants.LoadFrom(path, projectDir)
	mode := "absent"
	if st, statErr := os.Lstat(path); statErr == nil {
		mode = fmt.Sprintf("%04o", st.Mode().Perm())
	}
	switch {
	case err != nil:
		return doctorCheck{Name: "grants", Status: doctorFail, Kind: "grants_invalid", Detail: err.Error()}
	case r.Insecure:
		return doctorCheck{Name: "grants", Status: doctorFail, Kind: grants.ReasonInsecure,
			Detail: fmt.Sprintf("%s: mode %s; %s; fail closed: no new seat starts, typed gates stay needs_gate (fix: chmod 600 %s)", grants.ReasonInsecure, mode, r.InsecureWhy, path)}
	}
	c := hostCheck("grants", doctorOK, "%s (owner file %s, mode %s); mode %s; seat cap %d per session", r.Source, path, mode, r.Mode, r.MaxSeats)
	if len(r.Ignored) > 0 {
		c.Status = doctorWarn
		c.Detail += "; squad.yaml tried to widen: " + strings.Join(r.Ignored, ", ")
	}
	return c
}

// runProbes runs the slow probes of --probe: the Codex model id turn per
// catalog model, and, where a probe function is set, the R12 probe and the
// direct TTFT per local model.
func (h *doctorHost) runProbes(ctx context.Context, env registry.Env, det registry.Detection, rt registry.RehashResult, runtimeOK bool) (registry.BuildOptions, []doctorCheck) {
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
	// The R12 probe runs only the built-in pi runtime (R2), and only after
	// its full re-hash passed against the embedded manifest digest.
	if !runtimeOK || det.Tool("pi").Path != det.PiRuntimeDir {
		return opts, append(out, hostCheck("probe/pi", doctorFail, "R12 probe not run: it runs only the built-in pi runtime after its re-hash passes (see the pi runtime row)"))
	}
	for _, m := range det.Ollama {
		id := "ollama/" + m.Name
		if m.CapsKnown && !m.Tools {
			continue
		}
		if h.ProbeLocal != nil {
			start := time.Now()
			rec, err := h.ProbeLocal(ctx, h, det, rt, id)
			if err != nil {
				out = append(out, hostCheck("probe/"+id, doctorFail, "R12 probe failed to run: %v", err))
			} else {
				// A failed tally is a failure, not a passing row (review P2 (9)).
				opts.Probes[id] = rec
				status := doctorOK
				if rec.Runs == 0 || rec.Passes < rec.Runs || rec.FalseSuccesses > 0 {
					status = doctorFail
				}
				out = append(out, hostCheck("probe/"+id, status, "R12 %d of %d passes, %d false success(es), %d tool error(s) in %s",
					rec.Passes, rec.Runs, rec.FalseSuccesses, rec.ToolErrors, time.Since(start).Round(time.Second)))
			}
		}
		if h.TTFT != nil {
			if lat, err := h.TTFT(ctx, env, m.Name); err == nil {
				opts.Latency[id] = lat
			}
		}
	}
	return opts, out
}

// defaultProbeLocal runs the R12 probe ProbeRuns times through the installed
// pi with an isolated agent directory under ~/.amq-squad/probe.
func defaultProbeLocal(ctx context.Context, h *doctorHost, det registry.Detection, rt registry.RehashResult, model string) (registry.ProbeRecord, error) {
	// The command comes only from the manifest bytes the re-hash verified:
	// the embedded runtime's PiArgv (node, the self-check preload, the pi
	// entry) when this binary embeds pi (4-1), else the 2b-3 manifest read.
	var cmd []string
	if h.piRuntime != nil {
		cmd = h.piRuntime.PiArgv()
	} else {
		var err error
		if cmd, err = registry.PiCommand(rt); err != nil {
			return registry.ProbeRecord{}, err
		}
	}
	env := h.Env
	env.Home = h.Home
	piEnv, err := registry.PiProbeEnv(h.Home, env, det.Ollama)
	if err != nil {
		return registry.ProbeRecord{}, err
	}
	work, err := registry.SafeDir(h.Home, ".amq-squad", "probe", "runs")
	if err != nil {
		return registry.ProbeRecord{}, err
	}
	verify, err := registry.SafeDir(h.Home, ".amq-squad", "probe", "verify")
	if err != nil {
		return registry.ProbeRecord{}, err
	}
	cfg := registry.ProbeConfig{Pi: cmd, Env: piEnv, Model: model, WorkDir: work, VerifyRoot: verify}
	return registry.Tally(ctx, cfg, h.ProbeRuns, "pi "+rt.Manifest.Version)
}

// claudePinCheck warns when the registry's claude pin is behind the claude
// the owner's PATH resolves to (phase 5 release prep): the pin is a hard
// link that keeps the old bytes until the owner re-pins, by the digest-pin
// design, so after every Claude Code update the seats still run the old
// version. The installed binary is found through PATH and its symlink chain
// is followed and hashed read-only; it is never executed. The warning
// carries the re-pin command: the hard link and the registry line.
func claudePinCheck(home string, lookPath func(string) (string, error)) []doctorCheck {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	found, err := lookPath("claude")
	if err != nil {
		return nil // no installed claude to compare with
	}
	real, installed, err := resolveInstalledClaude(found, home)
	if err != nil {
		return []doctorCheck{hostCheck("claude pin", doctorWarn, "the installed claude %s is unresolvable, so the pin is not compared with it: %v", found, err)}
	}
	reg, _ := registry.Read(registry.Path(home))
	pin, ok := reg.PinFor("claude")
	if !ok || pin.Path == "" {
		return nil // the unpinned case is the launch's tool_unpinned, not a stale pin
	}
	if pin.SHA256 == installed {
		return []doctorCheck{hostCheck("claude pin", doctorOK, "the pinned claude matches the installed claude (sha256 %s)", installed)}
	}
	behind := fmt.Sprintf("the pinned claude is behind the installed claude (%s sha256 %s, installed %s sha256 %s)", pin.Path, pin.SHA256, real, installed)
	// A hard link needs one filesystem: name the limit instead of printing a
	// command that fails.
	if a, aok := fileDevice(real); aok {
		if b, bok := fileDevice(filepath.Dir(pin.Path)); bok && a != b {
			return []doctorCheck{hostCheck("claude pin", doctorWarn, "%s; the pin must be on the same filesystem as the installed binary (%s and %s are on different devices), so pin a hard link from a directory on the installed binary's filesystem, then set the claude pin's path and sha256 in %s to it and %s", behind, real, filepath.Dir(pin.Path), registry.Path(home), installed)}
		}
	}
	return []doctorCheck{hostCheck("claude pin", doctorWarn, "%s; to re-pin, run: ln -f %s %s, then set the claude pin's sha256 in %s to %s", behind, shellQuote(real), shellQuote(pin.Path), registry.Path(home), installed)}
}

// fileDevice is a file's device number; a var so a test can name two
// filesystems on a host that has one.
var fileDevice = statDevice

// claudeMaxHops bounds the installed claude's symlink chain: the ninth link
// found is refused, not followed.
const claudeMaxHops = 8

// claudeItem is one path component still to resolve; scope names the path
// (the original, or one link's target) it belongs to.
type claudeItem struct {
	name  string
	scope int
}

// resolveInstalledClaude resolves found to the regular file the kernel would
// open, one component at a time, and hashes that file's descriptor. It reads
// a path a same-UID writer controls, so nothing is trusted lexically: each
// directory is entered through the fsanchor walk (O_NOFOLLOW|O_DIRECTORY, no
// symlinked ancestor), each component is tested with readlinkat, and a link's
// target is spliced in front of the components that remain, never Clean'd
// across a link. A ".." that follows a symlinked component of its own path is
// unresolvable (it would leave the link's target, not the link's directory).
// A followed link and the final file must lie under the owner's HOME or
// /opt/homebrew; more than claudeMaxHops links is unresolvable. Nothing is
// executed.
func resolveInstalledClaude(found, home string) (real, digest string, err error) {
	roots := []string{"/opt/homebrew"}
	if home != "" {
		roots = append(roots, filepath.Clean(home))
	}
	within := func(p string) bool {
		for _, r := range roots {
			if p == r || strings.HasPrefix(p, r+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	if !filepath.IsAbs(found) {
		return "", "", fmt.Errorf("%s is not an absolute path", found)
	}
	split := func(p string, scope int) []claudeItem {
		var out []claudeItem
		for _, c := range strings.Split(p, "/") {
			if c != "" && c != "." {
				out = append(out, claudeItem{c, scope})
			}
		}
		return out
	}
	scope, hops := 0, 0
	tainted := map[int]bool{} // scopes whose own path already went through a link
	queue := split(found, scope)
	var parts []string // the physical directory reached so far, no link in it
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.name == ".." {
			if tainted[it.scope] {
				return "", "", fmt.Errorf("a .. after a symlinked component in %s (unresolvable, not followed)", found)
			}
			if len(parts) == 0 {
				return "", "", fmt.Errorf("a .. above the root in %s", found)
			}
			parts = parts[:len(parts)-1]
			continue
		}
		here := "/" + strings.Join(append(append([]string{}, parts...), it.name), "/")
		dir, err := fsanchor.OpenAnchored("/"+strings.Join(parts, "/"), nil, false)
		if err != nil {
			return "", "", err
		}
		target, lerr := dir.Readlink(it.name)
		switch {
		case lerr == nil:
			dir.Close()
			if !within(here) {
				return "", "", fmt.Errorf("%s is outside the owner's HOME and /opt/homebrew (not followed)", here)
			}
			if hops >= claudeMaxHops {
				return "", "", fmt.Errorf("more than %d symlink hops from %s", claudeMaxHops, found)
			}
			hops++
			tainted[it.scope] = true
			scope++
			if strings.HasPrefix(target, "/") {
				parts = nil
			}
			queue = append(split(target, scope), queue...)
		case errors.Is(lerr, syscall.EINVAL): // not a link
			if len(queue) > 0 { // a directory to enter
				sub, serr := dir.Sub(it.name)
				dir.Close()
				if serr != nil {
					return "", "", serr
				}
				sub.Close()
				parts = append(parts, it.name)
				continue
			}
			f, kind, _, _, oerr := dir.OpenEntry(it.name)
			dir.Close()
			if oerr != nil {
				return "", "", oerr
			}
			defer f.Close()
			if kind != fsanchor.KindRegular {
				return "", "", fmt.Errorf("%s is not a regular file", here)
			}
			if !within(here) {
				return "", "", fmt.Errorf("%s is outside the owner's HOME and /opt/homebrew (not followed)", here)
			}
			h := sha256.New()
			if _, err := io.Copy(h, f); err != nil {
				return "", "", err
			}
			return here, hex.EncodeToString(h.Sum(nil)), nil
		default:
			dir.Close()
			return "", "", lerr
		}
	}
	return "", "", fmt.Errorf("%s resolves to a directory, not a file", found)
}
