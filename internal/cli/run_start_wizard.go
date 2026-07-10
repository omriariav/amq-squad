package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
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
  amq-squad wizard [run start prefill flags] [--wizard-ui auto|tui|numbered]

This is an alias for 'amq-squad run start --interactive'. It requires an
interactive terminal, never runs in CI, and is preview-only in this release
slice. The wizard prints and validates the equivalent flag-form command.
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
	prefill, opts, err := prepareRunStartWizard(args)
	if err != nil {
		return err
	}
	opts.Defaults = prefill
	spec, err := runwizard.RunNumbered(runStartWizardInput, runStartWizardOutput, opts)
	if err != nil {
		return err
	}
	return finishRunStartWizard(spec, version)
}

func runBubbleRunStartWizard(args []string, version string) error {
	prefill, opts, err := prepareRunStartWizard(args)
	if err != nil {
		return err
	}
	opts.Defaults = prefill
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(runStartWizardOutput, "Full-screen wizard unavailable (%v); using the numbered adapter.\n", err)
		return runNumberedRunStartWizard(args, version)
	}
	defer tty.Close()
	result, err := runwizard.RunBubbleTea(tty, tty, opts)
	if err != nil {
		return err
	}
	if result.Cancelled {
		fmt.Fprintln(runStartWizardOutput, "Wizard cancelled. Nothing changed.")
		return nil
	}
	return finishRunStartWizard(result.Spec, version)
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

func finishRunStartWizard(spec runwizard.Spec, version string) error {
	preflight := runStartPreflight(runStartPreflightInput{
		Project:         spec.Project,
		Profile:         spec.Profile,
		ProfileExplicit: true,
		Session:         spec.Session,
		Roles:           spec.Roles,
		Binary:          spec.Binary,
		Visibility:      spec.Visibility,
		LeadMode:        spec.LeadMode,
		LeadModeSet:     strings.TrimSpace(spec.LeadMode) != "",
		Effort:          spec.Effort,
		EffortSet:       strings.TrimSpace(spec.Effort) != "",
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
	commandArgs := append([]string{"run", "start"}, canonicalArgs...)
	fmt.Printf("\nEquivalent flag command (preview only):\n  %s\n\n", shellCommand("amq-squad", commandArgs...))
	return runRunStart(canonicalArgs, version)
}

func parseRunStartWizardPrefill(args []string) (runwizard.Spec, error) {
	fs := flag.NewFlagSet("run start wizard", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	project := fs.String("project", "", "")
	session := fs.String("session", "", "")
	profile := fs.String("profile", "", "")
	registerScopedFlagAliases(fs, project, session, profile)
	roles := fs.String("roles", "", "")
	binary := fs.String("binary", "", "")
	model := fs.String("model", "", "")
	effort := fs.String("effort", "", "")
	codexArgs := fs.String("codex-args", "", "")
	claudeArgs := fs.String("claude-args", "", "")
	lead := fs.String("lead", "", "")
	leadMode := fs.String("lead-mode", "", "")
	visibility := fs.String("visibility", visibilitySiblingTabs, "")
	externalLead := fs.Bool("external-lead", false, "")
	goal := fs.String("goal", "", "")
	seedFrom := fs.String("seed-from", "", "")
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
		Project:      *project,
		Profile:      *profile,
		Session:      *session,
		Roles:        *roles,
		Binary:       *binary,
		Model:        *model,
		Effort:       *effort,
		CodexArgs:    *codexArgs,
		ClaudeArgs:   *claudeArgs,
		Lead:         *lead,
		LeadMode:     *leadMode,
		Visibility:   *visibility,
		ExternalLead: *externalLead,
		Goal:         *goal,
		SeedFrom:     *seedFrom,
	}, nil
}
