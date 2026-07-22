package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type tmuxControlContinueDeps struct {
	Verify func(project, profile, session, handle string) (liveidentity.Result, error)
	Output func(name string, args ...string) (string, error)
	Run    func(name string, args ...string) error
}

var productionTmuxControlContinueDeps = func() tmuxControlContinueDeps {
	return tmuxControlContinueDeps{Verify: verifyTerminalActorLiveIdentity, Output: tmuxOutputCommand, Run: tmuxRunCommand}
}

type tmuxControlContinueTarget struct {
	Client   string `json:"client"`
	Session  string `json:"terminal_session"`
	WindowID string `json:"window_id"`
	PaneID   string `json:"pane_id"`
}

type tmuxControlContinueData struct {
	Project string                    `json:"project"`
	Profile string                    `json:"profile"`
	Session string                    `json:"session"`
	Role    string                    `json:"role"`
	Handle  string                    `json:"handle"`
	Target  tmuxControlContinueTarget `json:"target"`
}

func runTeamMemberControlContinue(args []string) error {
	role, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("a role is required, e.g. 'team member control-continue reviewer --client /dev/ttys001'")
	}
	role = strings.ToLower(strings.TrimSpace(role))
	if err := team.ValidateRoleID(role); err != nil {
		return fmt.Errorf("role: %w", err)
	}
	fs := flag.NewFlagSet("team member control-continue", flag.ContinueOnError)
	clientFlag := fs.String("client", "", "exact unique tmux control client name shown by status")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "managed workstream session")
	jsonFlag := fs.Bool("json", false, "emit the exact continued target as JSON")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	expectedClient, err := validateTmuxControlClientName(*clientFlag)
	if err != nil {
		return usageErrorf("--client must name the exact unique control client: %v", err)
	}

	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	if !team.ExistsProfile(ctx.ProjectDir, ctx.Profile) {
		return fmt.Errorf("no team configured for profile %q", ctx.Profile)
	}
	explicitSession := flagWasSet(fs, "session")
	initialRuntime, workstream, err := resolveMemberRuntime(ctx.ProjectDir, ctx.Profile, ctx.Session, explicitSession, role)
	if err != nil {
		return err
	}
	initialEndpoint, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(ctx.ProjectDir, ctx.Profile, workstream), initialRuntime.Handle)
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(ctx.ProjectDir, ctx.Profile, workstream)
	if err != nil {
		return err
	}
	defer admission.close()

	currentCtx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return fmt.Errorf("control-continue refused: context re-resolution under admission failed: %w", err)
	}
	if err := validateReResolvedContext(ctx, currentCtx, false); err != nil {
		return err
	}
	currentRuntime, currentWorkstream, err := resolveMemberRuntime(currentCtx.ProjectDir, currentCtx.Profile, currentCtx.Session, explicitSession, role)
	if err != nil {
		return fmt.Errorf("control-continue refused: target re-resolution under admission failed: %w", err)
	}
	currentEndpoint, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(currentCtx.ProjectDir, currentCtx.Profile, currentWorkstream), currentRuntime.Handle)
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("control-continue", initialEndpoint, currentEndpoint); err != nil {
		return err
	}
	if err := validateReResolvedMemberRuntime("control-continue", currentCtx.ProjectDir, currentCtx.Profile, initialRuntime, workstream, currentRuntime, currentWorkstream); err != nil {
		return err
	}
	if err := ensureNoNamespaceConflict("control-continue", currentCtx.ProjectDir, currentCtx.Profile, currentWorkstream, flagWasSet(fs, "profile")); err != nil {
		return err
	}

	deps := productionTmuxControlContinueDeps()
	if deps.Verify == nil || deps.Output == nil || deps.Run == nil {
		return fmt.Errorf("control-continue refused: tmux recovery dependencies are incomplete")
	}
	first, err := resolveExactTmuxControlContinue(currentCtx.ProjectDir, currentCtx.Profile, currentWorkstream, role, expectedClient, currentRuntime, deps)
	if err != nil {
		return err
	}
	secondRuntime, secondWorkstream, err := resolveMemberRuntime(currentCtx.ProjectDir, currentCtx.Profile, currentCtx.Session, explicitSession, role)
	if err != nil {
		return fmt.Errorf("control-continue refused: final target re-resolution failed: %w", err)
	}
	if err := validateReResolvedMemberRuntime("control-continue", currentCtx.ProjectDir, currentCtx.Profile, currentRuntime, currentWorkstream, secondRuntime, secondWorkstream); err != nil {
		return err
	}
	second, err := resolveExactTmuxControlContinue(currentCtx.ProjectDir, currentCtx.Profile, secondWorkstream, role, expectedClient, secondRuntime, deps)
	if err != nil {
		return err
	}
	if first != second {
		return fmt.Errorf("control-continue refused: exact client or pane identity changed between verification passes")
	}
	if err := continueExactTmuxControlClient(second, deps.Run); err != nil {
		return fmt.Errorf("control-continue refused: exact client exited or continue failed: %w", err)
	}
	data := tmuxControlContinueData{Project: currentCtx.ProjectDir, Profile: currentCtx.Profile, Session: secondWorkstream, Role: role, Handle: secondRuntime.Handle, Target: second}
	if *jsonFlag {
		return printJSONEnvelope("tmux_control_continue", data)
	}
	fmt.Printf("continued tmux control client %s for verified managed pane %s (%s/%s)\n", second.Client, second.PaneID, second.Session, role)
	return nil
}

func resolveExactTmuxControlContinue(project, profile, session, role, expectedClient string, mr memberRuntime, deps tmuxControlContinueDeps) (tmuxControlContinueTarget, error) {
	if deps.Verify == nil || deps.Output == nil {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: tmux recovery dependencies are incomplete")
	}
	if mr.ProfileMismatch || !mr.HasRecord || mr.Record.Role != role || mr.Record.Handle != mr.Handle || mr.Record.TeamProfile != profile || mr.Record.Session != session {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: managed launch record does not match the canonical target")
	}
	if err := validateLiveIdentityTerminalProjection(mr.Record); err != nil {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: %w", err)
	}
	terminal := liveIdentityTerminal(mr.Record)
	if terminal.Backend != "tmux" {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: verified terminal backend is %q, not tmux", terminal.Backend)
	}
	paneID, err := exactTmuxPaneID(terminal.PaneID)
	if err != nil {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: %w", err)
	}
	windowID, err := exactTmuxWindowID(terminal.WindowID)
	if err != nil {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: %w", err)
	}
	verified, err := deps.Verify(project, profile, session, mr.Handle)
	if err != nil || verified.Verified == nil {
		if err == nil {
			err = fmt.Errorf("authoritative resolver returned no verified identity")
		}
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: verified live identity mismatch: %w", err)
	}
	canonicalProject, err := liveidentity.CanonicalProject(project)
	if err != nil {
		return tmuxControlContinueTarget{}, err
	}
	v := verified.Verified
	if v.Key.Project != canonicalProject || v.Key.Profile != profile || v.Key.Session != session || v.Key.Handle != mr.Handle || v.Role != role || v.Terminal != terminal {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: authoritative live identity differs from the managed record target")
	}

	clients, err := listExactTmuxControlClients(terminal.Session, deps.Output)
	if err != nil {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: list exact control clients: %w", err)
	}
	matching := make([]tmuxClient, 0, 1)
	for _, client := range clients {
		if client.ControlMode && client.Session == terminal.Session {
			matching = append(matching, client)
		}
	}
	if len(matching) != 1 {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: exact terminal session %s has %d control-mode clients, want exactly one", terminal.Session, len(matching))
	}
	resolvedClient, err := validateTmuxControlClientName(matching[0].Name)
	if err != nil {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: resolved client name is invalid: %w", err)
	}
	if resolvedClient != expectedClient {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: requested client %q differs from unique resolved client %q", expectedClient, resolvedClient)
	}

	const paneFormat = "#{pane_id}\t#{window_id}\t#{session_name}"
	out, err := deps.Output("tmux", "display-message", "-p", "-t", paneID, paneFormat)
	if err != nil {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: inspect exact pane %s: %w", paneID, err)
	}
	fields := strings.Split(strings.TrimSpace(out), "\t")
	if len(fields) != 3 || fields[0] != paneID || fields[1] != windowID || fields[2] != terminal.Session {
		return tmuxControlContinueTarget{}, fmt.Errorf("control-continue refused: pane inspection differs from verified terminal identity")
	}
	return tmuxControlContinueTarget{Client: resolvedClient, Session: terminal.Session, WindowID: windowID, PaneID: paneID}, nil
}

func listExactTmuxControlClients(session string, output func(name string, args ...string) (string, error)) ([]tmuxClient, error) {
	clients, _, err := readExactTmuxControlClients(session, output)
	return clients, err
}

func readExactTmuxControlClients(session string, output func(name string, args ...string) (string, error)) ([]tmuxClient, string, error) {
	if output == nil || strings.TrimSpace(session) == "" {
		return nil, "", fmt.Errorf("tmux client discovery dependency or session is incomplete")
	}
	const format = "#{client_name}\t#{client_tty}\t#{client_control_mode}\t#{client_flags}\t#{client_session}"
	out, err := output("tmux", "list-clients", "-t", session, "-F", format)
	if err != nil {
		return nil, out, err
	}
	clients, err := parseExactTmuxControlClients(out)
	return clients, out, err
}

func parseExactTmuxControlClients(out string) ([]tmuxClient, error) {
	var clients []tmuxClient
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 5 {
			return nil, fmt.Errorf("unexpected tmux list-clients row with %d columns", len(parts))
		}
		name, tty := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		controlField, flags, session := strings.TrimSpace(parts[2]), strings.TrimSpace(parts[3]), strings.TrimSpace(parts[4])
		if name != parts[0] || tty != parts[1] || controlField != parts[2] || flags != parts[3] || session != parts[4] {
			return nil, fmt.Errorf("tmux list-clients row contains non-canonical field whitespace")
		}
		if controlField != "0" && controlField != "1" {
			return nil, fmt.Errorf("invalid client_control_mode %q", controlField)
		}
		if len(tty) > 512 || strings.ContainsAny(tty, "\x00\r\n\t") || len(flags) > 4096 || strings.ContainsAny(flags, "\x00\r\n\t") {
			return nil, fmt.Errorf("tmux list-clients row contains invalid tty or flags")
		}
		if controlField != "1" {
			if strings.Contains(flags, "control-mode") {
				return nil, fmt.Errorf("contradictory control-mode client row")
			}
			continue
		}
		if _, err := validateTmuxControlClientName(name); err != nil || session == "" {
			return nil, fmt.Errorf("control-mode client row lacks exact client_name or client_session")
		}
		clients = append(clients, tmuxClient{Name: name, TTY: tty, ControlMode: true, Flags: flags, Session: session})
	}
	return clients, nil
}

func validateTmuxControlClientName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 512 || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", fmt.Errorf("invalid tmux client name")
	}
	return value, nil
}

func continueExactTmuxControlClient(target tmuxControlContinueTarget, run func(name string, args ...string) error) error {
	if run == nil {
		return fmt.Errorf("tmux recovery run dependency is incomplete")
	}
	client, err := validateTmuxControlClientName(target.Client)
	if err != nil {
		return err
	}
	pane, err := exactTmuxPaneID(target.PaneID)
	if err != nil {
		return err
	}
	return run("tmux", "refresh-client", "-t", client, "-A", pane+":continue")
}
