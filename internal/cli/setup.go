package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/userconfig"
)

const setupVersionOutputLimit = 8 << 10

var setupPresetBackends = []string{
	drafter.BackendYoetz,
	drafter.BackendClaude,
	drafter.BackendCodex,
}

type setupBackendProbe struct {
	Name       string
	Path       string
	Version    string
	VersionErr error
}

type setupDependencies struct {
	In          io.Reader
	Out         io.Writer
	Err         io.Writer
	LookPath    func(string) (string, error)
	Version     func(string) (string, error)
	ReadConfig  func() (userconfig.Config, error)
	WriteConfig func(userconfig.Config) (string, error)
}

type setupOptions struct {
	chain          string
	model          string
	effort         string
	timeoutSeconds int
	onFailure      string
	chainSet       bool
	modelSet       bool
	effortSet      bool
	timeoutSet     bool
	onFailureSet   bool
}

func runSetup(args []string) error {
	return runSetupWithDependencies(args, setupDependencies{
		In:          os.Stdin,
		Out:         os.Stdout,
		Err:         os.Stderr,
		LookPath:    exec.LookPath,
		Version:     setupCommandVersion,
		ReadConfig:  userconfig.Read,
		WriteConfig: userconfig.Write,
	})
}

func runSetupWithDependencies(args []string, deps setupDependencies) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(deps.Err)
	var opts setupOptions
	fs.StringVar(&opts.chain, "drafter-chain", "", "ordered comma-separated drafter backends (yoetz,claude,codex) or in_session")
	fs.StringVar(&opts.model, "drafter-model", "", "drafter model passed to configured backends")
	fs.StringVar(&opts.effort, "drafter-effort", "", "drafter reasoning effort passed to configured backends")
	fs.IntVar(&opts.timeoutSeconds, "drafter-timeout", 0, "drafter timeout in seconds (0 uses the default)")
	fs.StringVar(&opts.onFailure, "drafter-on-failure", "", "failure policy: in_session or error")
	fs.Usage = func() {
		fmt.Fprint(deps.Err, `amq-squad setup - configure machine-level amq-squad defaults

Usage:
  amq-squad setup
  amq-squad setup --drafter-chain yoetz,claude,codex [options]

Without setup flags the command probes PATH and prompts interactively. Any
setup flag selects non-interactive mode for scripts and CI images. Setup writes
only the user-level global config; team profiles keep their own overrides.

Options:
`)
		fs.PrintDefaults()
		fmt.Fprint(deps.Err, `
Examples:
  amq-squad setup
  amq-squad setup --drafter-chain yoetz,claude,codex --drafter-timeout 180 --drafter-on-failure in_session
  AMQ_SQUAD_CONFIG=/tmp/amq-squad-config.json amq-squad setup --drafter-chain codex
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("setup takes no positional arguments; got %d", fs.NArg())
	}
	opts.chainSet = flagWasSet(fs, "drafter-chain")
	opts.modelSet = flagWasSet(fs, "drafter-model")
	opts.effortSet = flagWasSet(fs, "drafter-effort")
	opts.timeoutSet = flagWasSet(fs, "drafter-timeout")
	opts.onFailureSet = flagWasSet(fs, "drafter-on-failure")

	current, err := deps.ReadConfig()
	if err != nil {
		return fmt.Errorf("read user-level config: %w", err)
	}
	probes := setupProbeBackends(deps)
	setupPrintProbes(deps.Out, probes)

	nonInteractive := opts.chainSet || opts.modelSet || opts.effortSet || opts.timeoutSet || opts.onFailureSet
	var next userconfig.Config
	if nonInteractive {
		next, err = setupApplyOptions(current, opts)
	} else {
		next, err = setupPromptConfig(current, probes, deps.In, deps.Out)
	}
	if err != nil {
		return err
	}
	setupWarnMissingBackends(deps.Err, next.Drafter, probes)
	path, err := deps.WriteConfig(next)
	if err != nil {
		return fmt.Errorf("write user-level config: %w", err)
	}
	fmt.Fprintf(deps.Out, "Configured drafter: %s\n", setupDrafterSelection(next.Drafter))
	fmt.Fprintf(deps.Out, "Wrote user config: %s\n", path)
	return nil
}

func setupApplyOptions(current userconfig.Config, opts setupOptions) (userconfig.Config, error) {
	next := current
	draft := setupCloneDrafter(current.Drafter)
	if opts.chainSet {
		chain, inSession, err := setupParseChain(opts.chain)
		if err != nil {
			return userconfig.Config{}, err
		}
		if inSession {
			draft = nil
		} else {
			draft = &drafter.Config{Chain: chain}
		}
	}
	if draft == nil {
		if opts.modelSet || opts.effortSet || opts.timeoutSet || opts.onFailureSet {
			return userconfig.Config{}, usageErrorf("drafter knobs require an external --drafter-chain")
		}
		next.Drafter = nil
		return next, nil
	}
	if opts.modelSet {
		draft.Model = strings.TrimSpace(opts.model)
	}
	if opts.effortSet {
		draft.Effort = strings.TrimSpace(opts.effort)
	}
	if opts.timeoutSet {
		draft.TimeoutSeconds = opts.timeoutSeconds
	}
	if opts.onFailureSet {
		draft.OnFailure = strings.TrimSpace(opts.onFailure)
	}
	if err := drafter.ValidateGlobal(draft); err != nil {
		return userconfig.Config{}, usageErrorf("invalid global drafter config: %v", err)
	}
	next.Drafter = draft
	return next, nil
}

func setupPromptConfig(current userconfig.Config, probes []setupBackendProbe, in io.Reader, out io.Writer) (userconfig.Config, error) {
	next := current
	scanner := bufio.NewScanner(in)
	defaultSelection := setupDrafterSelection(current.Drafter)
	if current.Drafter == nil {
		available := make([]string, 0, len(probes))
		for _, probe := range probes {
			available = append(available, probe.Name)
		}
		if len(available) > 0 {
			defaultSelection = strings.Join(available, ",")
		}
	}
	selection, err := setupPrompt(scanner, out, "Drafter chain (yoetz,claude,codex or in_session)", defaultSelection)
	if err != nil {
		return userconfig.Config{}, err
	}

	var draft *drafter.Config
	if current.Drafter != nil && selection == setupDrafterSelection(current.Drafter) {
		draft = setupCloneDrafter(current.Drafter)
	} else {
		chain, inSession, err := setupParseChain(selection)
		if err != nil {
			return userconfig.Config{}, err
		}
		if inSession {
			next.Drafter = nil
			return next, nil
		}
		draft = &drafter.Config{Chain: chain}
	}
	if draft == nil || draft.EffectiveBackend() == drafter.BackendInSession {
		next.Drafter = nil
		return next, nil
	}

	model, err := setupPrompt(scanner, out, "Drafter model (blank uses backend default)", strings.TrimSpace(draft.Model))
	if err != nil {
		return userconfig.Config{}, err
	}
	effort, err := setupPrompt(scanner, out, "Drafter effort (blank uses backend default)", strings.TrimSpace(draft.Effort))
	if err != nil {
		return userconfig.Config{}, err
	}
	timeoutDefault := draft.TimeoutSeconds
	if timeoutDefault == 0 {
		timeoutDefault = drafter.DefaultTimeoutSeconds
	}
	timeoutText, err := setupPrompt(scanner, out, "Drafter timeout seconds", strconv.Itoa(timeoutDefault))
	if err != nil {
		return userconfig.Config{}, err
	}
	timeoutSeconds, err := strconv.Atoi(timeoutText)
	if err != nil || timeoutSeconds < 1 || timeoutSeconds > drafter.MaxTimeoutSeconds {
		return userconfig.Config{}, usageErrorf("drafter timeout must be between 1 and %d seconds", drafter.MaxTimeoutSeconds)
	}
	onFailure, err := setupPrompt(scanner, out, "Failure policy (in_session or error)", draft.EffectiveFailureMode())
	if err != nil {
		return userconfig.Config{}, err
	}
	draft.Model = strings.TrimSpace(model)
	draft.Effort = strings.TrimSpace(effort)
	draft.TimeoutSeconds = timeoutSeconds
	draft.OnFailure = strings.TrimSpace(onFailure)
	if err := drafter.ValidateGlobal(draft); err != nil {
		return userconfig.Config{}, usageErrorf("invalid global drafter config: %v", err)
	}
	next.Drafter = draft
	return next, nil
}

func setupPrompt(scanner *bufio.Scanner, out io.Writer, label, defaultValue string) (string, error) {
	if defaultValue == "" {
		fmt.Fprintf(out, "%s: ", label)
	} else {
		fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
	}
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("read interactive setup input: %w", err)
		}
		return "", usageErrorf("interactive setup requires input; use --drafter-chain for non-interactive setup")
	}
	value := strings.TrimSpace(scanner.Text())
	if value == "" {
		return defaultValue, nil
	}
	return value, nil
}

func setupParseChain(value string) ([]string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, false, usageErrorf("drafter chain cannot be empty; use in_session for no external backend")
	}
	if value == drafter.BackendInSession {
		return nil, true, nil
	}
	parts := strings.Split(value, ",")
	chain := make([]string, 0, len(parts))
	for i, part := range parts {
		backend := strings.TrimSpace(part)
		switch backend {
		case drafter.BackendYoetz, drafter.BackendClaude, drafter.BackendCodex:
			chain = append(chain, backend)
		case "":
			return nil, false, usageErrorf("drafter chain entry %d cannot be empty", i+1)
		default:
			return nil, false, usageErrorf("unsupported setup drafter backend %q: use yoetz, claude, codex, or in_session", backend)
		}
	}
	return chain, false, nil
}

func setupDrafterSelection(config *drafter.Config) string {
	if config == nil || config.EffectiveBackend() == drafter.BackendInSession {
		return drafter.BackendInSession
	}
	if len(config.Chain) > 0 {
		return strings.Join(config.EffectiveBackends(), ",")
	}
	return strings.TrimSpace(config.Backend)
}

func setupCloneDrafter(config *drafter.Config) *drafter.Config {
	if config == nil {
		return nil
	}
	clone := *config
	clone.Chain = append([]string(nil), config.Chain...)
	clone.Command = append([]string(nil), config.Command...)
	return &clone
}

func setupProbeBackends(deps setupDependencies) []setupBackendProbe {
	probes := make([]setupBackendProbe, 0, len(setupPresetBackends))
	for _, backend := range setupPresetBackends {
		path, err := deps.LookPath(backend)
		if err != nil {
			continue
		}
		version, versionErr := deps.Version(path)
		version = setupFirstLine(version)
		if version == "" {
			version = "unknown version"
		}
		probes = append(probes, setupBackendProbe{
			Name:       backend,
			Path:       path,
			Version:    version,
			VersionErr: versionErr,
		})
	}
	return probes
}

func setupPrintProbes(out io.Writer, probes []setupBackendProbe) {
	fmt.Fprintln(out, "Detected drafter backends:")
	if len(probes) == 0 {
		fmt.Fprintln(out, "  none (in_session remains available)")
		return
	}
	for _, probe := range probes {
		if probe.VersionErr != nil {
			fmt.Fprintf(out, "  %s: %s (%s; version probe failed: %v)\n", probe.Name, probe.Path, probe.Version, probe.VersionErr)
			continue
		}
		fmt.Fprintf(out, "  %s: %s (%s)\n", probe.Name, probe.Path, probe.Version)
	}
}

func setupWarnMissingBackends(out io.Writer, config *drafter.Config, probes []setupBackendProbe) {
	if config == nil {
		return
	}
	found := make(map[string]bool, len(probes))
	for _, probe := range probes {
		found[probe.Name] = true
	}
	for _, backend := range config.EffectiveBackends() {
		if backend == drafter.BackendCustom || found[backend] {
			continue
		}
		fmt.Fprintf(out, "warning: configured drafter backend %s was not found on PATH\n", backend)
	}
}

func setupCommandVersion(path string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	var output setupVersionBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if ctx.Err() != nil {
		return output.String(), fmt.Errorf("version probe timed out: %w", ctx.Err())
	}
	return output.String(), err
}

func setupFirstLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

type setupVersionBuffer struct {
	value []byte
}

func (b *setupVersionBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := setupVersionOutputLimit - len(b.value)
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		b.value = append(b.value, p...)
	}
	return original, nil
}

func (b *setupVersionBuffer) String() string { return string(b.value) }
