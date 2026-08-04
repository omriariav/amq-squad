package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// orchestrateTmuxRun executes a tmux command. It is a package var so tests can
// stub it and assert the launch was invoked with the expected arguments,
// matching the injectable-runner pattern used elsewhere in this package
// (externalLeadWakeCommand, runAMQCommand).
var orchestrateTmuxRun = func(args ...string) error { return exec.Command("tmux", args...).Run() }
var orchestrateTmuxOutput = func(args ...string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	return string(out), err
}

func insideTmux() bool { return strings.TrimSpace(os.Getenv("TMUX")) != "" }

func validOrchestrateAgent(agent string) error {
	switch agent {
	case "claude", "codex":
		return nil
	default:
		return usageErrorf("--agent must be claude or codex, got %q", agent)
	}
}

// -----------------------------------------------------------------------------
// global: multi-run global / NOC orchestrator
// -----------------------------------------------------------------------------

func runGlobal(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad global - stand up a global / NOC orchestrator

Usage:
  amq-squad global start [--root DIR] [--agent claude|codex] [--name WINDOW]
      [--monitor-interval D --monitor-timeout D --monitor-max-ticks N] [--go]
  amq-squad global status [--root DIR] [--json]

A global orchestrator is a control-plane conversation that supervises MANY runs
across repos from a neutral root. global start injects the standing NOC contract
natively, records a stamped launch generation, and configures a bounded polling
backstop. Runs started from that exact verified NOC pane default to wake
registration; --no-register-orchestrator on run start is the explicit opt-out.

global status is a read-only projection over that registry and each registered
run's canonical lead, gates, notification watcher, and bounded backstop state.
It never repairs, drains, answers, adopts, or scans arbitrary repositories.

Preview by default (prints the deterministic bootstrap and launch plan); pass
--go to create the stamped tmux window, persist its generation, and launch.
`)
		if len(args) == 0 {
			return usageErrorf("global requires a subcommand (start or status)")
		}
		return nil
	}
	switch args[0] {
	case "start":
		return runGlobalStart(args[1:])
	case "status":
		return runGlobalStatus(args[1:])
	default:
		return unknownSubcommandError("global", args[0], "start", "status")
	}
}

func defaultGlobalNOCControlRoot() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Code")
	}
	return ""
}

func runGlobalStart(args []string) error {
	fs := flag.NewFlagSet("global start", flag.ContinueOnError)
	root := fs.String("root", defaultGlobalNOCControlRoot(), "neutral root directory the supervisor runs from")
	agent := fs.String("agent", "claude", "agent binary to launch: claude or codex")
	name := fs.String("name", "global-orch", "tmux window name")
	model := fs.String("model", "", "model to pass to the agent (e.g. claude-opus-4-8, gpt-5.6-terra)")
	codexArgs := fs.String("codex-args", "", "extra args when --agent codex (e.g. reasoning effort); space-split")
	claudeArgs := fs.String("claude-args", "", "extra args when --agent claude; space-split")
	monitorInterval := fs.Duration("monitor-interval", defaultMonitorInterval, "bounded stall-backstop poll interval")
	monitorTimeout := fs.Duration("monitor-timeout", defaultMonitorTimeout, "bounded duration of one stall-backstop sweep")
	monitorMaxTicks := fs.Int("monitor-max-ticks", defaultMonitorMaxTicks, "bounded maximum ticks in one stall-backstop sweep")
	goFlag := fs.Bool("go", false, "actually open the window and launch the agent (default: preview only)")
	fs.Usage = func() { _ = runGlobal([]string{"-h"}) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if err := validOrchestrateAgent(*agent); err != nil {
		return err
	}
	if *monitorInterval < time.Second || *monitorTimeout < time.Second || *monitorMaxTicks <= 0 {
		return usageErrorf("global start stall backstop requires --monitor-interval and --monitor-timeout >= 1s, and positive --monitor-max-ticks")
	}
	// Build the agent argv: binary, then model, then the matching per-binary
	// passthrough. Global start remains a single-agent surface, so effort rides
	// directly in --codex-args / --claude-args; the role-mapped --effort flag is
	// only meaningful for a multi-role project run.
	agentArgv := []string{*agent}
	if strings.TrimSpace(*model) != "" {
		agentArgv = append(agentArgv, "--model", strings.TrimSpace(*model))
	}
	extra := *claudeArgs
	if *agent == "codex" {
		extra = *codexArgs
	}
	if fields := strings.Fields(extra); len(fields) > 0 {
		agentArgv = append(agentArgv, fields...)
	}
	if strings.TrimSpace(*root) == "" {
		return usageErrorf("global start requires --root (could not infer a home directory)")
	}
	if info, err := os.Stat(*root); err != nil || !info.IsDir() {
		return usageErrorf("root directory does not exist: %s", *root)
	}
	controlRoot, err := canonicalGlobalNOCControlRoot(*root)
	if err != nil {
		return usageErrorf("%v", err)
	}
	now := globalNOCNow().UTC()
	launchID := globalNOCLaunchID(controlRoot, now)
	backstop := globalNOCBackstop{
		IntervalSeconds: int(monitorInterval.Seconds()),
		TimeoutSeconds:  int(monitorTimeout.Seconds()),
		MaxTicks:        *monitorMaxTicks,
	}
	bootstrap := buildGlobalNOCBootstrap(controlRoot, launchID, globalNOCRegistryPath(controlRoot), backstop)
	bootstrapDigest := globalNOCBootstrapDigest(bootstrap)
	agentArgv = appendGeneratedBootstrapPrompt(agentArgv, bootstrap)

	fmt.Printf("global orchestrator (wake-registered NOC with bounded polling fallback)\n")
	fmt.Printf("  root:   %s\n", controlRoot)
	fmt.Printf("  agent:  %s\n", *agent)
	fmt.Printf("  window: %s\n", *name)
	fmt.Printf("  launch-id: %s\n", launchID)
	fmt.Printf("  registry: %s\n", globalNOCRegistryPath(controlRoot))
	fmt.Printf("  bootstrap: %s\n", bootstrapDigest)
	fmt.Printf("  stall-backstop: interval=%s timeout=%s max-ticks=%d\n", *monitorInterval, *monitorTimeout, *monitorMaxTicks)
	fmt.Printf("  registration: verified runs default to --register-orchestrator; explicit opt-out is --no-register-orchestrator\n")
	previewArgv := append([]string(nil), agentArgv...)
	previewArgv[len(previewArgv)-1] = "<native-noc-bootstrap>"
	fmt.Printf("  launch: tmux new-window -c %s -n %s %s\n", controlRoot, *name, strings.Join(previewArgv, " "))

	if !*goFlag {
		fmt.Print(`
PREVIEW only -- nothing launched. Re-run with --go to open the window.
`)
		if !insideTmux() {
			fmt.Println("degradation: poll_required (preview is outside tmux; live launch requires a stamped tmux pane)")
		}
		fmt.Println()
		fmt.Print(bootstrap)
		return nil
	}

	if !insideTmux() {
		return usageErrorf("not inside tmux; global start --go must run from a tmux session (visible spawns require it)")
	}
	if _, err := exec.LookPath(*agent); err != nil {
		return usageErrorf("%s not found on PATH", *agent)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return usageErrorf("tmux not found on PATH")
	}
	paneOutput, err := orchestrateTmuxOutput("new-window", "-P", "-F", "#{pane_id}\t#{pane_pid}", "-c", controlRoot, "-n", *name)
	if err != nil {
		return fmt.Errorf("tmux new-window failed: %w", err)
	}
	paneFields := strings.Split(strings.TrimSpace(paneOutput), "\t")
	if len(paneFields) != 2 {
		return fmt.Errorf("tmux new-window returned incomplete pane/process identity %q", strings.TrimSpace(paneOutput))
	}
	paneID := strings.TrimSpace(paneFields[0])
	if _, err := exactTmuxPaneID(paneID); err != nil {
		return fmt.Errorf("tmux new-window returned invalid pane identity: %w", err)
	}
	panePID, err := strconv.Atoi(strings.TrimSpace(paneFields[1]))
	if err != nil || panePID <= 0 {
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return fmt.Errorf("tmux new-window returned invalid pane PID %q", strings.TrimSpace(paneFields[1]))
	}
	identity, err := globalNOCPaneIdentityFor(paneID)
	if err != nil {
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return fmt.Errorf("capture NOC tmux identity: %w", err)
	}
	if err := stampCapturedLaunchPane(paneID, launchID, globalNOCRole); err != nil {
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return fmt.Errorf("stamp NOC pane %s: %w", paneID, err)
	}
	preparedAt := globalNOCNow().UTC()
	if defaultDuplicateLaunchProbe.ProcessStartTime != nil {
		if processStartedAt, ok := defaultDuplicateLaunchProbe.ProcessStartTime(panePID); ok {
			preparedAt = processStartedAt.UTC()
		}
	}
	prepared, err := beginGlobalNOCLaunch(controlRoot, launchID, *agent, strings.TrimSpace(*model), panePID, identity, bootstrapDigest, backstop, preparedAt)
	if err != nil {
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return fmt.Errorf("persist prepared NOC launch generation: %w", err)
	}
	// exec preserves tmux's pane PID while replacing the bootstrap shell with
	// the agent process. The registry can therefore bind a positive PID before
	// dispatch and activate only after the canonical PID classifier observes
	// the expected binary.
	//
	// #577 finding 1: this is a FOURTH delivery path and it still types the command, so it
	// carried the whole #454 NOC bootstrap -- at least 1,783 bytes before quoting -- through
	// the pane tty, deterministically past the 1,024-byte MAX_CANON boundary. global start
	// --go could silently lose the command and then die in an identity timeout: the exact
	// #571 defect on the path #454 shipped.
	//
	// The other three sites switched to respawn-pane, and that would be WRONG here.
	// respawn-pane replaces the pane's process and therefore its PID, while this path
	// deliberately relies on exec KEEPING the pane PID that beginGlobalNOCLaunch has already
	// persisted above; waitForGlobalNOCPIDIdentity then watches that same PID become the
	// agent binary. Copying the earlier fix would have broken the #454 durable generation
	// contract to fix a length bug.
	//
	// So the payload moves OFF the tty instead of the mechanism changing: the bootstrap is
	// written beside the launch generation and the typed line substitutes it back in. The
	// line stays a fixed ~150 bytes regardless of bootstrap size, exec still preserves the
	// PID, and the agent receives byte-identical text. Double quotes are required -- bare
	// $(cat ...) would word-split the prompt into hundreds of arguments.
	promptPath, err := writeGlobalNOCBootstrapPayload(controlRoot, launchID, bootstrap)
	if err != nil {
		_ = transitionGlobalNOCLaunch(controlRoot, launchID, globalNOCLaunchFailed, "bootstrap payload write failed: "+err.Error(), globalNOCNow().UTC())
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return fmt.Errorf("write NOC bootstrap payload: %w", err)
	}
	command := nocDispatchCommand(agentArgv, promptPath, nocPromptDigest(bootstrap))
	if err := checkNOCDispatchLineBound(command, promptPath); err != nil {
		_ = transitionGlobalNOCLaunch(controlRoot, launchID, globalNOCLaunchFailed, "dispatch line over safe tty bound: "+err.Error(), globalNOCNow().UTC())
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return err
	}
	if err := orchestrateTmuxRun("send-keys", "-t", paneID, command, "C-m"); err != nil { // #571-exempt-typed-delivery: noc-bootstrap-dispatch
		_ = transitionGlobalNOCLaunch(controlRoot, launchID, globalNOCLaunchFailed, "agent command dispatch failed: "+err.Error(), globalNOCNow().UTC())
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return fmt.Errorf("launch NOC agent command: %w", err)
	}
	if err := waitForGlobalNOCPIDIdentity(prepared.Record); err != nil {
		_ = transitionGlobalNOCLaunch(controlRoot, launchID, globalNOCLaunchFailed, "agent runtime identity verification failed: "+err.Error(), globalNOCNow().UTC())
		_ = orchestrateTmuxRun("kill-window", "-t", paneID)
		return err
	}
	if err := transitionGlobalNOCLaunch(controlRoot, launchID, globalNOCLaunchActive, "native NOC bootstrap dispatched to stamped pane", globalNOCNow().UTC()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: NOC agent launched but active registry publication failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "warning: implicit wake registration is disabled; use bounded polling until the registry is repaired")
		return fmt.Errorf("publish active NOC launch generation: %w", err)
	}
	quietNotice("launched %s in stamped NOC pane %s (window %q) at %s\n", *agent, paneID, *name, controlRoot)
	fmt.Print(bootstrap)
	return nil
}

// -----------------------------------------------------------------------------
// run: create one orchestrated run in a project (managed spawn)
// -----------------------------------------------------------------------------

type repeatedRoleMapValue struct {
	name   string
	target *string
	seen   map[string]string
}

func registerRepeatedRoleMapFlag(fs *flag.FlagSet, name, usage string) *string {
	target := ""
	fs.Var(&repeatedRoleMapValue{
		name: name, target: &target, seen: map[string]string{},
	}, name, usage)
	return &target
}

func (f *repeatedRoleMapValue) String() string {
	if f == nil || f.target == nil {
		return ""
	}
	return *f.target
}

func (f *repeatedRoleMapValue) Set(raw string) error {
	type assignment struct {
		role  string
		value string
	}
	var pending []assignment
	local := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 || eq == len(pair)-1 {
			return fmt.Errorf("--%s expects role=value assignments, got %q", f.name, pair)
		}
		role := strings.TrimSpace(pair[:eq])
		value := strings.TrimSpace(pair[eq+1:])
		if role == "" || value == "" {
			return fmt.Errorf("--%s expects role=value assignments, got %q", f.name, pair)
		}
		key := strings.ToLower(role)
		if previous, ok := f.seen[key]; ok {
			return fmt.Errorf("--%s repeats role %q with values %q and %q", f.name, key, previous, value)
		}
		if previous, ok := local[key]; ok {
			return fmt.Errorf("--%s repeats role %q with values %q and %q", f.name, key, previous, value)
		}
		local[key] = value
		pending = append(pending, assignment{role: role, value: value})
	}
	for _, item := range pending {
		f.seen[strings.ToLower(item.role)] = item.value
	}
	if strings.TrimSpace(*f.target) == "" {
		*f.target = raw
	} else if strings.TrimSpace(raw) != "" {
		*f.target += "," + raw
	}
	return nil
}

// nocDispatchCommand builds the exact line typed into the NOC pane.
//
// Extracted so the test asserts on PRODUCTION's construction rather than rebuilding the same
// string beside it. A test that composes its own command proves the technique works; it does
// not prove this code uses it, so reverting production would leave it green. That is the
// vacuity flavour this milestone has already paid for more than once.
//
// agentArgv arrives with the bootstrap prompt as its LAST element (appendGeneratedBootstrapPrompt
// puts it after "--"). That element is dropped from the typed line and substituted back from
// promptPath, so the line length is independent of bootstrap size. exec is preserved because
// this path relies on the pane PID surviving dispatch.
// #577 round 2 finding 1: the read FAILED OPEN. `exec agent "$(cat path)"` still execs when
// cat fails or returns partial content -- the shell substitutes what it got, the agent starts
// with an empty or truncated prompt, #{pane_pid} verification passes, and the NOC generation
// ACTIVATES WITHOUT ITS SAFETY CONTRACT. A missing prompt is the one failure that must not
// look like a launch.
//
// The guard is a digest comparison inside the typed line: read once into a variable, compare
// against the digest recorded at write time, exec only on a match, otherwise exit nonzero so
// the PID watch fails and the generation transitions to failed. Same disease as everything
// else in this review -- absence treated as success -- so it fails closed.
//
// I also RETRACT a claim from round 1: I said the agent receives the bootstrap
// "byte-identical". Command substitution strips trailing newlines, so that was false when
// written. The digest is therefore taken over the SUBSTITUTED form (trailing newlines
// removed), which is what the agent actually receives; comparing against the file's own digest
// would fail every time and the guard would be useless.
func nocDispatchCommand(agentArgv []string, promptPath, promptDigest string) string {
	if len(agentArgv) == 0 {
		return ""
	}
	promptless := agentArgv[:len(agentArgv)-1]
	if len(promptless) == 0 {
		return ""
	}
	// Double quotes are load-bearing: bare $(cat ...) word-splits the prompt into hundreds
	// of arguments.
	return "p=\"$(cat " + shellQuote(promptPath) + ")\"; " +
		"test \"$(printf %s \"$p\" | shasum -a 256 | cut -d' ' -f1)\" = " + shellQuote(promptDigest) +
		" || { echo 'amq-squad: NOC bootstrap payload failed verification; refusing to launch' >&2; exit 1; }; " +
		"exec " + shellCommand(promptless[0], promptless[1:]...) + " \"$p\""
}

// nocDispatchLineBound is the maximum composed dispatch line this path will type.
//
// #577 round 2 finding 4: the line is NOT inherently bounded. A deep control root or long
// model/passthrough args grows it without limit, and at 1,024 bytes the pane tty silently
// drops it -- reproducing the exact #571 defect the file substitution was meant to end. The
// bound is well under MAX_CANON because the digest guard adds a fixed prefix and a line at 1000
// bytes is one flag away from truncating.
const nocDispatchLineBound = 700

// checkNOCDispatchLineBound refuses LOUDLY rather than typing a line that may be truncated.
// The error names the composed length and the payload path, because the usual cause is a deep
// control root and the operator cannot otherwise tell which input pushed it over.
func checkNOCDispatchLineBound(command, promptPath string) error {
	if len(command) <= nocDispatchLineBound {
		return nil
	}
	return fmt.Errorf("NOC dispatch line is %d bytes, over the %d-byte safe bound (tty MAX_CANON is 1024 and an over-length line is dropped SILENTLY): shorten the control root or the model/passthrough args. Payload path %s is %d bytes of that",
		len(command), nocDispatchLineBound, promptPath, len(promptPath))
}
