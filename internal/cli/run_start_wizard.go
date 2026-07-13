package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
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
	runStartWizardResumeExecute    = runResume
	runStartWizardInspectProject   = inspectRunStartWizardProject
	runStartWizardConfirm          = promptRunStartWizardLaunch
	runStartWizardResumeConfirm    = promptRunStartWizardResume
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
	reader := bufio.NewReader(runStartWizardInput)
	prefill, opts, err := prepareRunStartWizard(args)
	if err != nil {
		return err
	}
	opts.Defaults = prefill
	for {
		spec, runErr := runwizard.RunNumbered(reader, runStartWizardOutput, opts)
		if runErr != nil {
			if errors.Is(runErr, runwizard.ErrCancelled) {
				fmt.Fprintln(runStartWizardOutput, "Wizard cancelled. Nothing changed.")
				return nil
			}
			return runErr
		}
		finishErr := finishRunStartWizard(spec, version, reader, runStartWizardOutput)
		var restart *wizardRestartError
		if !errors.As(finishErr, &restart) {
			return finishErr
		}
		discardWizardBufferedInput(reader)
		opts.Defaults = restart.Defaults
		opts.StartAtProfile = true
		opts.RestartMessage = restart.Message
	}
}

func runBubbleRunStartWizard(args []string, version string) error {
	tty, err := runStartWizardOpenTTY()
	if err != nil {
		fmt.Fprintf(runStartWizardOutput, "Full-screen wizard unavailable (%v); using the numbered adapter.\n", err)
		return runNumberedRunStartWizard(args, version)
	}
	defer tty.Close()
	reader := bufio.NewReader(tty)
	prefill, opts, err := prepareRunStartWizard(args)
	if err != nil {
		return err
	}
	opts.Defaults = prefill
	// Bubble Tea enables raw mode only when its input is the tty file itself;
	// a wrapping reader leaves the terminal cooked and every key is swallowed.
	for {
		result, runErr := runStartWizardBubbleProgram(tty, tty, opts)
		if runErr != nil {
			return runErr
		}
		if result.Cancelled {
			fmt.Fprintln(runStartWizardOutput, "Wizard cancelled. Nothing changed.")
			return nil
		}
		finishErr := finishRunStartWizard(result.Spec, version, reader, tty)
		var restart *wizardRestartError
		if !errors.As(finishErr, &restart) {
			return finishErr
		}
		discardWizardBufferedInput(reader)
		opts.Defaults = restart.Defaults
		opts.StartAtProfile = true
		opts.RestartMessage = restart.Message
	}
}

func discardWizardBufferedInput(reader *bufio.Reader) {
	if reader == nil || reader.Buffered() == 0 {
		return
	}
	_, _ = reader.Discard(reader.Buffered())
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
	// Preserve the established initial read-only refresh for project-prefilled
	// flows. The adapters still own the scope screen; an explicit global prefill
	// avoids project discovery entirely.
	if !strings.EqualFold(strings.TrimSpace(prefill.Scope), "global") {
		initialContext, inspectErr := runStartWizardInspectProject(prefill.Project)
		if inspectErr != nil {
			return runwizard.Spec{}, runwizard.NumberedOptions{}, inspectErr
		}
		if strings.TrimSpace(initialContext.Project) != "" {
			prefill.Project = initialContext.Project
		}
	}
	if strings.TrimSpace(prefill.Profile) == "" {
		prefill.Profile = team.DefaultProfile
	}
	if strings.TrimSpace(prefill.GlobalRoot) == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return runwizard.Spec{}, runwizard.NumberedOptions{}, fmt.Errorf("resolve default global root: %w", homeErr)
		}
		prefill.GlobalRoot = filepath.Join(home, "Code")
	}
	if strings.TrimSpace(prefill.GlobalWindow) == "" {
		prefill.GlobalWindow = "global-orch"
	}
	if prefill.Backend == "" {
		prefill.Backend = runwizard.BackendRunStart
	}
	opts := runwizard.NumberedOptions{
		InspectProject: runStartWizardInspectProject,
		ProfileExists: func(project, profile string) bool {
			return team.ExistsProfile(strings.TrimSpace(project), strings.TrimSpace(profile))
		},
	}
	return prefill, opts, nil
}

const wizardDiscoveryChangedMessage = "The selected profile or run changed while the wizard was open. Review the refreshed facts before continuing."

type wizardRestartError struct {
	Defaults runwizard.Spec
	Message  string
	Cause    error
}

func (e *wizardRestartError) Error() string {
	if e.Cause != nil {
		return e.Message + " (" + e.Cause.Error() + ")"
	}
	return e.Message
}

func (e *wizardRestartError) Unwrap() error { return e.Cause }

func refreshWizardExistingSelection(spec runwizard.Spec) error {
	if spec.ProfileBranch != runwizard.ProfileBranchExisting {
		return nil
	}
	stale := func(cause error) error {
		defaults := spec.Clone()
		defaults.InvalidateExistingRun()
		return &wizardRestartError{Defaults: defaults, Message: wizardDiscoveryChangedMessage, Cause: cause}
	}
	if strings.TrimSpace(spec.DiscoveryFingerprint) == "" {
		return stale(fmt.Errorf("reviewed discovery fingerprint is empty"))
	}
	ctx, err := runStartWizardInspectProject(spec.Project)
	if err != nil {
		return stale(fmt.Errorf("refresh project discovery: %w", err))
	}
	for _, profile := range ctx.Profiles {
		if !squadnamespace.ProfilesEqual(profile.Name, spec.Profile) {
			continue
		}
		for _, session := range profile.Sessions {
			if session.Name != spec.Session {
				continue
			}
			if strings.TrimSpace(session.Fingerprint) == "" {
				return stale(fmt.Errorf("refreshed discovery fingerprint is empty"))
			}
			if session.Fingerprint != spec.DiscoveryFingerprint {
				return stale(fmt.Errorf("discovery fingerprint changed"))
			}
			if session.Classification.Backend != spec.Backend || session.Classification.State != spec.RunState || session.Classification.Executable != spec.RunExecutable || session.RecordCount != spec.RecordCount || (session.RecordCount > 0) != spec.RestoreExisting || !reflect.DeepEqual(session.Members, spec.ResumeMembers) {
				return stale(fmt.Errorf("refreshed run contract changed without a fingerprint change"))
			}
			return nil
		}
		return stale(fmt.Errorf("selected session %q is missing", spec.Session))
	}
	return stale(fmt.Errorf("selected profile %q is missing", spec.Profile))
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
	if spec.ProfileBranch == runwizard.ProfileBranchExisting && spec.OperatorNotificationsSet {
		visibility := spec.Visibility
		if strings.TrimSpace(visibility) == "" {
			visibility = visibilitySiblingTabs
		}
		prefillCheck := runStartPreflight(runStartPreflightInput{
			Project:                  spec.Project,
			Profile:                  spec.Profile,
			ProfileExplicit:          true,
			Session:                  spec.Session,
			Visibility:               visibility,
			OperatorNotifications:    spec.OperatorNotificationsRequested,
			OperatorNotificationsSet: true,
		})
		for _, issue := range prefillCheck.Issues {
			if issue.Code != runStartPreflightExistingOperatorNotifications {
				continue
			}
			fmt.Fprintf(runStartWizardOutput, "\nPreflight blocked [%s]: %s\n", issue.Code, issue.Detail)
			for _, fix := range issue.SuggestedFixes {
				fmt.Fprintf(runStartWizardOutput, "  - %s\n", fix)
			}
			return fmt.Errorf("%s", issue.Detail)
		}
	}
	if spec.ProfileBranch == runwizard.ProfileBranchExisting && !spec.RunExecutable {
		fmt.Fprintf(out, "Selected existing run %s/%s is %s (backend=%s). This state is read-only; nothing was previewed or launched.\n", spec.Profile, spec.Session, spec.RunState, spec.Backend)
		return nil
	}
	if err := refreshWizardExistingSelection(spec); err != nil {
		return err
	}
	if spec.Backend == runwizard.BackendResume {
		canonicalArgs, err := spec.ResumeArgs()
		if err != nil {
			return err
		}
		liveArgs := append(append([]string{}, canonicalArgs...), "--exec")
		fmt.Printf("\nEquivalent flag command (preview only):\n  %s\n\n", shellCommand("amq-squad", append([]string{"resume"}, canonicalArgs...)...))
		fmt.Printf("Equivalent flag command (live, only after explicit Yes):\n  %s\n\n", shellCommand("amq-squad", append([]string{"resume"}, liveArgs...)...))
		if err := runStartWizardResumeExecute(canonicalArgs); err != nil {
			return err
		}
		resume, err := runStartWizardResumeConfirm(in, out)
		if err != nil || !resume {
			return err
		}
		if err := refreshWizardExistingSelection(spec); err != nil {
			return err
		}
		return runStartWizardResumeExecute(liveArgs)
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
		Lead:                     spec.Lead,
		LeadModeSet:              strings.TrimSpace(spec.LeadMode) != "",
		Effort:                   spec.Effort,
		EffortSet:                strings.TrimSpace(spec.Effort) != "",
		OperatorMode:             spec.OperatorMode,
		OperatorModeSet:          strings.TrimSpace(spec.OperatorMode) != "" && strings.TrimSpace(spec.OperatorMode) != team.OperatorInteractionUnspecified,
		SelfOperatorLead:         spec.SelfOperatorLead,
		SelfOperatorAllow:        spec.SelfOperatorAllow,
		SelfOperatorPolicySet:    strings.TrimSpace(spec.SelfOperatorLead) != "" || strings.TrimSpace(spec.SelfOperatorAllow) != "",
		OperatorNotifications:    spec.OperatorNotificationsRequested,
		OperatorNotificationsSet: spec.OperatorNotificationsSet,
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
	if err := refreshWizardExistingSelection(spec); err != nil {
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

func promptRunStartWizardResume(in io.Reader, out io.Writer) (bool, error) {
	fmt.Fprint(out, "Resume now? [y/N] ")
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
	selfOperatorLead := fs.String("self-operator-lead", "", "")
	selfOperatorAllow := fs.String("self-operator-allow", "", "")
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
		Scope:                          strings.ToLower(strings.TrimSpace(*scope)),
		Project:                        *project,
		Profile:                        *profile,
		Session:                        *session,
		Roles:                          *roles,
		Binary:                         *binary,
		Model:                          *model,
		Effort:                         *effort,
		OperatorMode:                   *operatorMode,
		SelfOperatorLead:               *selfOperatorLead,
		SelfOperatorAllow:              *selfOperatorAllow,
		OperatorNotifications:          *operatorNotifications,
		OperatorNotificationsRequested: *operatorNotifications,
		OperatorNotificationsSet:       flagWasSet(fs, "operator-notifications"),
		CodexArgs:                      *codexArgs,
		ClaudeArgs:                     *claudeArgs,
		Lead:                           *lead,
		LeadMode:                       *leadMode,
		Visibility:                     *visibility,
		VisibilityExplicit:             flagWasSet(fs, "visibility"),
		LayoutPreset:                   *layoutPreset,
		LayoutExplicit:                 flagWasSet(fs, "layout-preset"),
		LauncherPane:                   *launcherPane,
		ExternalLead:                   *externalLead,
		Goal:                           *goal,
		SeedFrom:                       *seedFrom,
		GlobalRoot:                     *globalRoot,
		GlobalAgent:                    *globalAgent,
		GlobalModel:                    *model,
		GlobalEffort:                   *effort,
		GlobalCodexArgs:                *codexArgs,
		GlobalClaudeArgs:               *claudeArgs,
		GlobalWindow:                   *globalWindow,
	}, nil
}
