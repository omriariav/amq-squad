package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
)

type amqCommandRequest struct {
	Context context.Context
	Dir     string
	Env     []string
	Arg     []string
	Stdin   io.Reader // optional; nil means no stdin
}

type amqCommandRunner func(amqCommandRequest) ([]byte, error)

var runAMQCommand amqCommandRunner = defaultRunAMQCommand

var resolveAMQEnvForAMQCommand = resolveAMQEnvInDir

func defaultRunAMQCommand(req amqCommandRequest) ([]byte, error) {
	cmd := exec.Command("amq", req.Arg...)
	if req.Context != nil {
		cmd = exec.CommandContext(req.Context, "amq", req.Arg...)
	}
	cmd.Env = amqexec.NoUpdateCheckEnv(req.Env)
	cmd.Dir = req.Dir
	cmd.Stdin = req.Stdin
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return out, fmt.Errorf("%w: %s", err, detail)
		}
		return out, err
	}
	return out, nil
}

type amqContext struct {
	ProjectDir          string
	Profile             string
	Env                 amqEnv
	Root                string
	Me                  string
	Session             string
	PinMode             amqPinMode
	NamespaceGeneration string
}

// amqPinMode is deliberately independent of amqEnv.SessionName. AMQ returns a
// display session for an explicit named-profile root, but that root must still
// be launched with an exact-root/sessionless identity tuple.
type amqPinMode uint8

const (
	amqPinExactRoot amqPinMode = iota
	amqPinSessionful
)

func runAMQ(args []string) error {
	if len(args) == 0 {
		return usageErrorf("amq requires a subcommand: env, route")
	}
	switch args[0] {
	case "env":
		return runAMQEnv(args[1:])
	case "route":
		return runAMQRoute(args[1:])
	default:
		return unknownSubcommandError("amq", args[0], "env", "route")
	}
}

func amqCommonFlagSet(name, usage string) (*flag.FlagSet, *string, *string, *string, *string, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	project := fs.String("project", "", "project/team-home directory to resolve AMQ from (default: cwd)")
	profile := fs.String("profile", "", "team profile namespace (default: default profile)")
	session := fs.String("session", "", "AMQ session/workstream name")
	me := fs.String("me", "", "AMQ handle to resolve as")
	registerScopedFlagAliases(fs, project, session, profile)
	jsonOut := fs.Bool("json", false, "emit JSON output when the underlying AMQ command supports it")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	return fs, project, profile, session, me, jsonOut
}

func resolveAMQContext(projectFlag, profileFlag, session, me string, projectSet bool) (amqContext, error) {
	resolved, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: projectFlag, ProfileFlag: profileFlag, SessionFlag: session, HandleFlag: me,
		ProjectExplicit: projectSet, ProfileExplicit: strings.TrimSpace(profileFlag) != "",
		SessionExplicit: strings.TrimSpace(session) != "", HandleExplicit: strings.TrimSpace(me) != "",
	})
	if err != nil {
		return amqContext{}, err
	}
	emitContextDiagnostics(resolved)
	// The canonical winner is authoritative. Query AMQ for version/project
	// metadata using that exact root, then restore the selected tuple instead
	// of asking the legacy namespace resolver to choose a second time.
	env, err := resolveAMQEnvForAMQCommand(resolved.ProjectDir, resolved.Root, "", resolved.Handle)
	if err != nil {
		return amqContext{}, err
	}
	env.Root = resolved.Root
	env.BaseRoot = resolved.BaseRoot
	env.SessionName = resolved.Session
	env.Me = resolved.Handle
	pinMode := amqPinSessionful
	if resolved.PinMode == "exact_root" {
		pinMode = amqPinExactRoot
	}
	return amqContext{
		ProjectDir:          resolved.ProjectDir,
		Profile:             resolved.Profile,
		Env:                 env,
		Root:                resolved.Root,
		Me:                  resolved.Handle,
		Session:             resolved.Session,
		PinMode:             pinMode,
		NamespaceGeneration: resolved.NamespaceGeneration,
	}, nil
}

func resolveAMQContextForProject(projectDir, session, me string) (amqContext, error) {
	env, err := resolveAMQEnvForAMQCommand(projectDir, "", session, me)
	if err != nil {
		return amqContext{}, err
	}
	handle := strings.TrimSpace(env.Me)
	if handle == "" {
		handle = strings.TrimSpace(me)
	}
	ctx := amqContext{
		ProjectDir: projectDir,
		Profile:    squadnamespace.DefaultProfile,
		Env:        env,
		Root:       absoluteAMQRoot(projectDir, env.Root),
		Me:         handle,
		Session:    strings.TrimSpace(session),
		PinMode:    amqPinExactRoot,
	}
	if ctx.Session != "" {
		ctx.PinMode = amqPinSessionful
	}
	return ctx, nil
}

func resolveAMQContextForNamespace(projectDir, profile, session, me string) (amqContext, error) {
	if squadnamespace.NormalizeProfile(profile) == squadnamespace.DefaultProfile {
		return resolveAMQContextForProject(projectDir, session, me)
	}
	root := squadnamespace.AMQRoot(projectDir, profile, session)
	env, err := resolveAMQEnvForAMQCommand(projectDir, root, "", me)
	if err != nil {
		return amqContext{}, err
	}
	if strings.TrimSpace(env.SessionName) == "" {
		env.SessionName = strings.TrimSpace(session)
	}
	handle := strings.TrimSpace(env.Me)
	if handle == "" {
		handle = strings.TrimSpace(me)
	}
	return amqContext{
		ProjectDir: projectDir,
		Profile:    squadnamespace.NormalizeProfile(profile),
		Env:        env,
		Root:       absoluteAMQRoot(projectDir, env.Root),
		Me:         handle,
		Session:    strings.TrimSpace(session),
		PinMode:    amqPinExactRoot,
	}, nil
}

// namedProfileFromInheritedAMQRoot recognizes only an exact live named-profile
// root.
func namedProfileFromInheritedAMQRoot(projectDir, session string) (string, bool, error) {
	root, present := os.LookupEnv("AM_ROOT")
	root = strings.TrimSpace(root)
	if !present || root == "" {
		return "", false, nil
	}
	if strings.TrimSpace(session) == "" {
		return "", false, nil
	}
	lexicalBase, lexicalRoot := lexicalInheritedAMQInferencePaths(projectDir, root)
	rel, err := filepath.Rel(lexicalBase, lexicalRoot)
	if err != nil {
		return "", false, err
	}
	if rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false, usageErrorf("inherited AM_ROOT %q is outside the selected project .agent-mail root; pass an explicit --profile/--project or relaunch the agent shell", lexicalRoot)
	}
	parts := strings.Split(filepath.Clean(rel), string(os.PathSeparator))

	base, resolvedRoot, err := canonicalInheritedAMQInferencePaths(lexicalBase, lexicalRoot)
	if err != nil {
		return "", false, err
	}
	canonicalRel, err := filepath.Rel(base, resolvedRoot)
	if err != nil || canonicalRel == "." || canonicalRel == "" || canonicalRel == ".." || strings.HasPrefix(canonicalRel, ".."+string(os.PathSeparator)) {
		return "", false, usageErrorf("inherited AM_ROOT %q resolves outside selected .agent-mail base %q; pass an explicit --profile/--project or relaunch the agent shell", lexicalRoot, base)
	}
	expectedRoot := filepath.Clean(filepath.Join(base, rel))
	if filepath.Clean(resolvedRoot) != expectedRoot {
		return "", false, usageErrorf("inherited AM_ROOT %q does not preserve selected .agent-mail identity %q after resolution (got %q); nested namespace symlinks are not allowed", lexicalRoot, expectedRoot, resolvedRoot)
	}
	if len(parts) == 1 {
		if parts[0] != session {
			return "", false, usageErrorf("inherited AM_ROOT session %q does not match requested --session %q; relaunch the agent shell or pass the exact namespace", parts[0], session)
		}
		// This is the ordinary default namespace. It is valid but does not
		// infer a named profile or alter default-profile resolution.
		return "", false, nil
	}
	if len(parts) != 2 {
		return "", false, usageErrorf("inherited AM_ROOT %q is not an exact profile/session mailbox root", root)
	}
	if parts[0] == squadnamespace.DefaultProfile {
		return "", false, usageErrorf("inherited AM_ROOT %q uses reserved profile segment %q; it collides with the default session namespace", lexicalRoot, squadnamespace.DefaultProfile)
	}
	profile := squadnamespace.NormalizeProfile(parts[0])
	if err := squadnamespace.ValidateProfile(profile); err != nil {
		return "", false, usageErrorf("inherited AM_ROOT has invalid profile segment %q: %v", parts[0], err)
	}
	if parts[1] != session {
		return "", false, usageErrorf("inherited AM_ROOT session %q does not match requested --session %q; relaunch the agent shell or pass the exact namespace", parts[1], session)
	}
	return profile, true, nil
}

// lexicalInheritedAMQInferencePaths preserves the caller's namespace segments.
func lexicalInheritedAMQInferencePaths(projectDir, inheritedRoot string) (string, string) {
	return absoluteAMQRoot("", filepath.Join(filepath.Clean(projectDir), ".agent-mail")), absoluteAMQRoot(projectDir, inheritedRoot)
}

// canonicalInheritedAMQInferencePaths resolves the selected top-level .agent-
// mail container and inherited root.
func canonicalInheritedAMQInferencePaths(lexicalBase, lexicalRoot string) (string, string, error) {
	base, err := filepath.EvalSymlinks(lexicalBase)
	if err != nil {
		return "", "", usageErrorf("cannot resolve selected .agent-mail base %q while validating inherited AM_ROOT %q: %v", lexicalBase, lexicalRoot, err)
	}
	root, err := filepath.EvalSymlinks(lexicalRoot)
	if err != nil {
		return "", "", usageErrorf("cannot resolve inherited AM_ROOT %q while validating selected .agent-mail base %q: %v", lexicalRoot, base, err)
	}
	return filepath.Clean(base), filepath.Clean(root), nil
}

func resolveAMQBaseRootForProject(projectDir, session, me string) (string, error) {
	ctx, err := resolveAMQContextForProject(projectDir, session, me)
	if err != nil {
		return "", err
	}
	return chooseProjectBaseRoot(projectDir, ctx.Env), nil
}

func amqCommandEnv(ctx amqContext) []string {
	env := envWithoutAMQIdentity(os.Environ())
	root := absoluteAMQRoot(ctx.ProjectDir, ctx.Root)
	if root == "" {
		return env
	}
	baseRoot := root
	session := ""
	if ctx.PinMode == amqPinSessionful && strings.TrimSpace(ctx.Session) != "" {
		baseRoot = absoluteAMQRoot(ctx.ProjectDir, ctx.Env.BaseRoot)
		if baseRoot == "" {
			baseRoot = filepath.Dir(root)
		}
		session = strings.TrimSpace(ctx.Session)
	}
	env = append(env, "AM_ROOT="+root, "AM_BASE_ROOT="+baseRoot)
	if session != "" {
		env = append(env, "AM_SESSION="+session)
	}
	if ctx.Me != "" {
		env = append(env, "AM_ME="+ctx.Me)
	}
	return env
}

func runAndWriteAMQ(out io.Writer, ctx amqContext, args []string) error {
	if out == nil {
		out = os.Stdout
	}
	data, err := runAMQCommand(amqCommandRequest{Dir: ctx.ProjectDir, Env: amqCommandEnv(ctx), Arg: args})
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func runAMQEnv(args []string) error {
	fs, project, profile, session, me, jsonOut := amqCommonFlagSet("amq env", `amq-squad amq env - show resolved AMQ context

Usage:
  amq-squad amq env [--project DIR] [--profile NAME] [--session NAME] [--me HANDLE] [--json]
`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveAMQContext(*project, *profile, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if *jsonOut {
		return writeJSONEnvelope(os.Stdout, "amq_env", ctx.Env)
	}
	fmt.Println("# amq-squad amq env")
	fmt.Printf("project:      %s\n", ctx.ProjectDir)
	fmt.Printf("root:         %s\n", ctx.Root)
	fmt.Printf("base_root:    %s\n", absoluteAMQRoot(ctx.ProjectDir, ctx.Env.BaseRoot))
	fmt.Printf("session:      %s\n", ctx.Env.SessionName)
	fmt.Printf("me:           %s\n", ctx.Me)
	fmt.Printf("amq_version:  %s\n", ctx.Env.AMQVersion)
	fmt.Printf("root_source:  %s\n", ctx.Env.RootSource)
	if ctx.Env.Project != "" {
		fmt.Printf("amq_project:  %s\n", ctx.Env.Project)
	}
	return nil
}

func runAMQRoute(args []string) error {
	fs, project, profile, session, me, jsonOut := amqCommonFlagSet("amq route", `amq-squad amq route - explain an AMQ route from this project/session

Usage:
  amq-squad amq route --to HANDLE [--project DIR] [--profile NAME] [--session NAME] [--me HANDLE] [--target-project NAME] [--target-session NAME] [--json]
`)
	to := fs.String("to", "", "target handle or inline AMQ address")
	targetProject := fs.String("target-project", "", "cross-project AMQ project name")
	targetSession := fs.String("target-session", "", "target session name when different from source")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*to) == "" {
		return usageErrorf("amq route requires --to")
	}
	ctx, err := resolveAMQContext(*project, *profile, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	cmd := []string{"route", "explain", "--from-root", ctx.Root, "--to", *to}
	if ctx.Me != "" {
		cmd = append(cmd, "--me", ctx.Me)
	}
	if *targetProject != "" {
		cmd = append(cmd, "--project", *targetProject)
	}
	if *targetSession != "" {
		cmd = append(cmd, "--session", *targetSession)
	}
	if *jsonOut || !containsString(cmd, "--json") {
		cmd = append(cmd, "--json")
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func amqFlagValue(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		n, val, hasVal := amqFlagName(args[i])
		if n != name {
			continue
		}
		if hasVal {
			return val
		}
		if i+1 < len(args) {
			return args[i+1]
		}
		return ""
	}
	return ""
}

func amqSendPassthroughFlagConsumesValue(name string, hasInline bool) bool {
	if name == "" || hasInline {
		return false
	}
	switch name {
	case "to", "thread", "kind", "subject", "body", "body-file", "wait-for", "wait-timeout",
		"project", "session", "profile", "root", "from-root", "target-project", "target-session":
		return true
	default:
		return false
	}
}

// amqFlagName normalizes a CLI token to its flag name (leading dashes stripped,
// any =value split off) and reports whether it carried an inline value. A
// non-flag token (or a bare "-"/"--") returns name "".
func amqFlagName(tok string) (name, val string, hasVal bool) {
	if len(tok) < 2 || tok[0] != '-' {
		return "", "", false
	}
	t := strings.TrimLeft(tok, "-")
	if t == "" {
		return "", "", false
	}
	if i := strings.IndexByte(t, '='); i >= 0 {
		return t[:i], t[i+1:], true
	}
	return t, "", false
}
