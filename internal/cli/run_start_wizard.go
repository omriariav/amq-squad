package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

var (
	runStartWizardInput            io.Reader = os.Stdin
	runStartWizardOutput           io.Writer = os.Stderr
	runStartWizardStdinIsTerminal            = stdinIsTerminal
	runStartWizardStderrIsTerminal           = stderrIsTerminal
	runStartWizardInCI                       = detectedCI
	runStartWizardRunner           func([]string, string) error
	runStartWizardTerm             = func() string { return os.Getenv("TERM") }
	runStartWizardAccessible       = func() bool { return envTruthy("AMQ_SQUAD_WIZARD_ACCESSIBLE") }
	runStartNumberedAdapter        func([]string, string) error
	runStartBubbleAdapter          func([]string, string) error
	runStartWizardProjectExecute   = runRunStart
	runStartWizardGlobalExecute    = runGlobalStart
	runStartWizardConfirm          = promptRunStartWizardLaunch
	runStartWizardOpenTTY          = func() (io.ReadWriteCloser, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
	runStartWizardBubbleProgram    = runwizard.RunBubbleTea
)

func init() {
	runStartNumberedAdapter = runNumberedRunStartWizard
	runStartBubbleAdapter = runBubbleRunStartWizard
	runStartWizardRunner = runAdaptiveRunStartWizard
}

// runWizardCmd is the discoverable alias for run start --interactive. It owns
// no launch behavior; the interactive trigger still returns canonical run
// start arguments and executes the existing preview path.
func runWizardCmd(args []string, version string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, `amq-squad wizard - guided preview for one orchestrated run

Usage:
  amq-squad wizard [run start prefill flags] [--scope project|global]
      [--wizard-ui auto|tui|numbered] [--numbered|--accessible]

This is an alias for 'amq-squad run start --interactive'. It requires an
interactive terminal and never runs in CI. It previews first, then asks
"Launch now? [y/N]"; only an explicit yes calls the same canonical command
with --go. Project-run and global/NOC scopes are supported.
TERM=dumb and --wizard-ui numbered use the accessible numbered adapter.
`)
		return nil
	}
	return runRunStart(append([]string{"--interactive"}, args...), version)
}

func runStartInteractiveTrigger(args []string) (requested, specified bool, rest []string, err error) {
	rest = make([]string, 0, len(args))
	for _, arg := range args {
		switch {
		case arg == "--interactive":
			requested = true
			specified = true
		case strings.HasPrefix(arg, "--interactive="):
			value := strings.TrimPrefix(arg, "--interactive=")
			parsed, parseErr := strconv.ParseBool(value)
			if parseErr != nil {
				return false, true, nil, usageErrorf("invalid value %q for --interactive", value)
			}
			requested = parsed
			specified = true
		default:
			rest = append(rest, arg)
		}
	}
	return requested, specified, rest, nil
}

func runStartWizardEligible() bool {
	return runStartWizardStdinIsTerminal() && runStartWizardStderrIsTerminal() && !runStartWizardInCI()
}

func runStartHasGoFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--go" || strings.HasPrefix(arg, "--go=") {
			return true
		}
	}
	return false
}

func runStartHasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func stderrIsTerminal() bool {
	info, err := os.Stderr.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func detectedCI() bool {
	for _, key := range []string{"CI", "GITHUB_ACTIONS", "BUILDKITE", "JENKINS_URL"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if value != "" && value != "0" && value != "false" && value != "no" {
			return true
		}
	}
	return false
}

func envTruthy(key string) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return value != "" && value != "0" && value != "false" && value != "no"
}

func runAdaptiveRunStartWizard(args []string, version string) error {
	rest, mode, err := stripWizardUIArgs(args)
	if err != nil {
		return err
	}
	if mode == "numbered" || (mode == "auto" && (strings.EqualFold(strings.TrimSpace(runStartWizardTerm()), "dumb") || runStartWizardAccessible())) {
		return runStartNumberedAdapter(rest, version)
	}
	return runStartBubbleAdapter(rest, version)
}

func stripWizardUIArgs(args []string) ([]string, string, error) {
	mode := "auto"
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--numbered" || arg == "--accessible":
			mode = "numbered"
		case arg == "--wizard-ui":
			if i+1 >= len(args) {
				return nil, "", usageErrorf("--wizard-ui requires auto, tui, or numbered")
			}
			i++
			mode = strings.ToLower(strings.TrimSpace(args[i]))
		case strings.HasPrefix(arg, "--wizard-ui="):
			mode = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(arg, "--wizard-ui=")))
		default:
			rest = append(rest, arg)
		}
	}
	if mode != "auto" && mode != "tui" && mode != "numbered" {
		return nil, "", usageErrorf("unsupported --wizard-ui %q (want auto, tui, or numbered)", mode)
	}
	return rest, mode, nil
}

func runNumberedRunStartWizard(args []string, version string) error {
	prefill, err := parseRunStartWizardPrefill(args)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(runStartWizardInput)
	scope, cancelled, err := promptRunStartWizardScope(reader, runStartWizardOutput, prefill.Scope)
	if err != nil || cancelled {
		return err
	}
	prefill.Scope = scope
	if scope == "global" {
		spec, collectErr := collectGlobalWizard(reader, runStartWizardOutput, prefill)
		if collectErr != nil {
			return collectErr
		}
		return finishRunStartWizard(spec, version, reader, runStartWizardOutput)
	}
	prefill, opts, err := prepareRunStartWizard(args)
	if err != nil {
		return err
	}
	prefill.Scope = "project"
	opts.Defaults = prefill
	spec, err := runwizard.RunNumbered(reader, runStartWizardOutput, opts)
	if err != nil {
		return err
	}
	return finishRunStartWizard(spec, version, reader, runStartWizardOutput)
}

func runBubbleRunStartWizard(args []string, version string) error {
	prefill, err := parseRunStartWizardPrefill(args)
	if err != nil {
		return err
	}
	tty, err := runStartWizardOpenTTY()
	if err != nil {
		fmt.Fprintf(runStartWizardOutput, "Full-screen wizard unavailable (%v); using the numbered adapter.\n", err)
		return runNumberedRunStartWizard(args, version)
	}
	defer tty.Close()
	reader := bufio.NewReader(tty)
	scope, cancelled, err := promptRunStartWizardScope(reader, tty, prefill.Scope)
	if err != nil || cancelled {
		return err
	}
	prefill.Scope = scope
	if scope == "global" {
		spec, collectErr := collectGlobalWizard(reader, tty, prefill)
		if collectErr != nil {
			return collectErr
		}
		return finishRunStartWizard(spec, version, reader, tty)
	}
	prefill, opts, err := prepareRunStartWizard(args)
	if err != nil {
		return err
	}
	prefill.Scope = "project"
	opts.Defaults = prefill
	result, err := runStartWizardBubbleProgram(reader, tty, opts)
	if err != nil {
		return err
	}
	if result.Cancelled {
		fmt.Fprintln(runStartWizardOutput, "Wizard cancelled. Nothing changed.")
		return nil
	}
	return finishRunStartWizard(result.Spec, version, reader, tty)
}

func prepareRunStartWizard(args []string) (runwizard.Spec, runwizard.NumberedOptions, error) {
	prefill, err := parseRunStartWizardPrefill(args)
	if err != nil {
		return runwizard.Spec{}, runwizard.NumberedOptions{}, err
	}
	if strings.TrimSpace(prefill.Project) == "" {
		cwd, getwdErr := os.Getwd()
		if getwdErr != nil {
			return runwizard.Spec{}, runwizard.NumberedOptions{}, fmt.Errorf("getwd: %w", getwdErr)
		}
		prefill.Project = cwd
	}
	initialContext, err := inspectRunStartWizardProject(prefill.Project)
	if err != nil {
		return runwizard.Spec{}, runwizard.NumberedOptions{}, err
	}
	prefill.Project = initialContext.Project
	if strings.TrimSpace(prefill.Profile) == "" {
		prefill.Profile = team.DefaultProfile
	}
	opts := runwizard.NumberedOptions{
		InspectProject: inspectRunStartWizardProject,
		ProfileExists: func(project, profile string) bool {
			return team.ExistsProfile(strings.TrimSpace(project), strings.TrimSpace(profile))
		},
	}
	return prefill, opts, nil
}

func finishRunStartWizard(spec runwizard.Spec, version string, in io.Reader, out io.Writer) error {
	if spec.Scope == "global" {
		canonicalArgs := spec.GlobalArgs()
		liveArgs := append(append([]string{}, canonicalArgs...), "--go")
		fmt.Printf("\nEquivalent flag command (preview only):\n  %s\n\n", shellCommand("amq-squad", append([]string{"global", "start"}, canonicalArgs...)...))
		fmt.Printf("Equivalent flag command (live, only after explicit Yes):\n  %s\n\n", shellCommand("amq-squad", append([]string{"global", "start"}, liveArgs...)...))
		if err := runStartWizardGlobalExecute(canonicalArgs); err != nil {
			return err
		}
		launch, err := runStartWizardConfirm(in, out)
		if err != nil || !launch {
			return err
		}
		return runStartWizardGlobalExecute(liveArgs)
	}
	preflight := runStartPreflight(runStartPreflightInput{
		Project:                  spec.Project,
		Profile:                  spec.Profile,
		ProfileExplicit:          true,
		Session:                  spec.Session,
		Roles:                    spec.Roles,
		Binary:                   spec.Binary,
		Visibility:               spec.Visibility,
		LeadMode:                 spec.LeadMode,
		LeadModeSet:              strings.TrimSpace(spec.LeadMode) != "",
		Effort:                   spec.Effort,
		EffortSet:                strings.TrimSpace(spec.Effort) != "",
		OperatorMode:             spec.OperatorMode,
		OperatorModeSet:          strings.TrimSpace(spec.OperatorMode) != "" && strings.TrimSpace(spec.OperatorMode) != team.OperatorInteractionUnspecified,
		OperatorNotifications:    spec.OperatorNotifications,
		OperatorNotificationsSet: spec.OperatorNotifications,
		LayoutPreset:             spec.LayoutPreset,
		LayoutPresetSet:          strings.TrimSpace(spec.LayoutPreset) != "",
		LauncherPane:             spec.LauncherPane,
		LauncherPaneSet:          strings.TrimSpace(spec.LauncherPane) != "",
		VisibilitySet:            strings.TrimSpace(spec.Visibility) != "",
		ExternalLead:             spec.ExternalLead,
	})
	if len(preflight.Issues) > 0 {
		issue := preflight.Issues[0]
		fmt.Fprintf(runStartWizardOutput, "\nPreflight blocked [%s]: %s\n", issue.Code, issue.Detail)
		for _, fix := range issue.SuggestedFixes {
			fmt.Fprintf(runStartWizardOutput, "  - %s\n", fix)
		}
		return preflight.Err()
	}
	canonicalArgs := spec.Args()
	liveArgs := append(append([]string{}, canonicalArgs...), "--go")
	commandArgs := append([]string{"run", "start"}, canonicalArgs...)
	fmt.Printf("\nEquivalent flag command (preview only):\n  %s\n\n", shellCommand("amq-squad", commandArgs...))
	fmt.Printf("Equivalent flag command (live, only after explicit Yes):\n  %s\n\n", shellCommand("amq-squad", append([]string{"run", "start"}, liveArgs...)...))
	if err := runStartWizardProjectExecute(canonicalArgs, version); err != nil {
		return err
	}
	launch, err := runStartWizardConfirm(in, out)
	if err != nil || !launch {
		return err
	}
	return runStartWizardProjectExecute(liveArgs, version)
}

func promptRunStartWizardLaunch(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Launch now? [y/N] ")
	line, err := readWizardLine(in)
	if err == io.EOF {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "y" || line == "yes", nil
}

func promptRunStartWizardScope(in io.Reader, out io.Writer, current string) (scope string, cancelled bool, err error) {
	current = strings.ToLower(strings.TrimSpace(current))
	if current == "project" || current == "global" {
		return current, false, nil
	}
	fmt.Fprint(out, "Scope\n  1) Project run\n  2) Global / NOC orchestrator\nChoose [1]: ")
	line, err := readWizardLine(in)
	if err != nil {
		return "", false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "1", "project":
		return "project", false, nil
	case "2", "global", "noc":
		return "global", false, nil
	case "q", "quit", "cancel":
		fmt.Fprintln(out, "Wizard cancelled. Nothing changed.")
		return "", true, nil
	default:
		return "", false, usageErrorf("invalid wizard scope %q", strings.TrimSpace(line))
	}
}

func collectGlobalWizard(in io.Reader, out io.Writer, spec runwizard.Spec) (runwizard.Spec, error) {
	if _, ok := in.(*bufio.Reader); !ok {
		in = bufio.NewReader(in)
	}
	spec.Scope = "global"
	if strings.TrimSpace(spec.GlobalRoot) == "" {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			spec.GlobalRoot = filepath.Join(home, "Code")
		} else {
			return spec, fmt.Errorf("resolve default global root: %w", homeErr)
		}
	}
	var err error
	if spec.GlobalRoot, err = promptWizardText(in, out, "Neutral root", spec.GlobalRoot); err != nil {
		return spec, err
	}
	if spec.GlobalAgent, err = promptWizardChoice(in, out, "Agent", spec.GlobalAgent, []string{"claude", "codex"}); err != nil {
		return spec, err
	}
	if spec.GlobalModel, err = promptWizardText(in, out, "Model (optional)", spec.GlobalModel); err != nil {
		return spec, err
	}
	efforts := []string{"automatic", "low", "medium", "high"}
	if spec.GlobalAgent == "codex" {
		efforts = append(efforts, "minimal", "xhigh")
	}
	if spec.GlobalEffort, err = promptWizardChoice(in, out, "Effort", spec.GlobalEffort, efforts); err != nil {
		return spec, err
	}
	if spec.GlobalAgent == "codex" {
		if spec.GlobalCodexArgs, err = promptWizardText(in, out, "Codex extra native args (excluding effort)", spec.GlobalCodexArgs); err != nil {
			return spec, err
		}
	} else if spec.GlobalClaudeArgs, err = promptWizardText(in, out, "Claude extra native args (excluding effort)", spec.GlobalClaudeArgs); err != nil {
		return spec, err
	}
	if strings.TrimSpace(spec.GlobalWindow) == "" {
		spec.GlobalWindow = "global-orch"
	}
	if spec.GlobalWindow, err = promptWizardText(in, out, "Window name", spec.GlobalWindow); err != nil {
		return spec, err
	}
	fmt.Fprintln(out, "NOC contract: poll explicit project/profile/session namespaces; this global orchestrator owns no wake mailbox.")
	return spec, nil
}

func promptWizardText(in io.Reader, out io.Writer, label, current string) (string, error) {
	fmt.Fprintf(out, "%s [%s]: ", label, current)
	line, err := readWizardLine(in)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(line) == "" {
		return current, nil
	}
	return strings.TrimSpace(line), nil
}

func promptWizardChoice(in io.Reader, out io.Writer, label, current string, values []string) (string, error) {
	if strings.TrimSpace(current) == "" {
		current = values[0]
	}
	fmt.Fprintf(out, "%s (%s) [%s]: ", label, strings.Join(values, "/"), current)
	line, err := readWizardLine(in)
	if err != nil {
		return "", err
	}
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		line = strings.ToLower(strings.TrimSpace(current))
	}
	for _, value := range values {
		if line == value {
			return line, nil
		}
	}
	return "", usageErrorf("invalid %s %q", strings.ToLower(label), line)
}

func readWizardLine(in io.Reader) (string, error) {
	if reader, ok := in.(*bufio.Reader); ok {
		line, err := reader.ReadString('\n')
		if err == io.EOF && line != "" {
			return line, nil
		}
		return line, err
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err == io.EOF && line != "" {
		return line, nil
	}
	return line, err
}

func parseRunStartWizardPrefill(args []string) (runwizard.Spec, error) {
	fs := flag.NewFlagSet("run start wizard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	scope := fs.String("scope", "", "")
	project := fs.String("project", "", "")
	session := fs.String("session", "", "")
	profile := fs.String("profile", "", "")
	registerScopedFlagAliases(fs, project, session, profile)
	roles := fs.String("roles", "", "")
	binary := fs.String("binary", "", "")
	model := fs.String("model", "", "")
	effort := fs.String("effort", "", "")
	operatorMode := fs.String("operator-mode", "", "")
	operatorNotifications := fs.Bool("operator-notifications", false, "")
	codexArgs := fs.String("codex-args", "", "")
	claudeArgs := fs.String("claude-args", "", "")
	lead := fs.String("lead", "", "")
	leadMode := fs.String("lead-mode", "", "")
	visibility := fs.String("visibility", visibilitySiblingTabs, "")
	layoutPreset := fs.String("layout-preset", "", "")
	launcherPane := fs.String("launcher-pane", "", "")
	externalLead := fs.Bool("external-lead", false, "")
	goal := fs.String("goal", "", "")
	seedFrom := fs.String("seed-from", "", "")
	globalRoot := fs.String("root", "", "")
	globalAgent := fs.String("agent", "", "")
	globalWindow := fs.String("name", "", "")
	goFlag := fs.Bool("go", false, "")
	if err := parseFlags(fs, args); err != nil {
		return runwizard.Spec{}, err
	}
	if fs.NArg() > 0 {
		return runwizard.Spec{}, usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if *goFlag {
		return runwizard.Spec{}, usageErrorf("--interactive cannot be combined with --go; approve launch only at the wizard's final confirmation")
	}
	return runwizard.Spec{
		Scope:                 strings.ToLower(strings.TrimSpace(*scope)),
		Project:               *project,
		Profile:               *profile,
		Session:               *session,
		Roles:                 *roles,
		Binary:                *binary,
		Model:                 *model,
		Effort:                *effort,
		OperatorMode:          *operatorMode,
		OperatorNotifications: *operatorNotifications,
		CodexArgs:             *codexArgs,
		ClaudeArgs:            *claudeArgs,
		Lead:                  *lead,
		LeadMode:              *leadMode,
		Visibility:            *visibility,
		LayoutPreset:          *layoutPreset,
		LauncherPane:          *launcherPane,
		ExternalLead:          *externalLead,
		Goal:                  *goal,
		SeedFrom:              *seedFrom,
		GlobalRoot:            *globalRoot,
		GlobalAgent:           *globalAgent,
		GlobalModel:           *model,
		GlobalEffort:          *effort,
		GlobalCodexArgs:       *codexArgs,
		GlobalClaudeArgs:      *claudeArgs,
		GlobalWindow:          *globalWindow,
	}, nil
}
