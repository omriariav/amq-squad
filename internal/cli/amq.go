package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type amqCommandRequest struct {
	Dir   string
	Env   []string
	Arg   []string
	Stdin io.Reader // optional; nil means no stdin
}

type amqCommandRunner func(amqCommandRequest) ([]byte, error)

var runAMQCommand amqCommandRunner = defaultRunAMQCommand

var resolveAMQEnvForAMQCommand = resolveAMQEnvInDir

type amqPassthroughOptions struct {
	OverrideBoundary bool
	BoundaryReason   string
	UnsafeSendAs     bool
}

type amqBoundaryAuditRecord struct {
	At         time.Time `json:"at"`
	Subcommand string    `json:"subcommand"`
	ProjectDir string    `json:"project_dir"`
	Session    string    `json:"session,omitempty"`
	Actor      string    `json:"actor,omitempty"`
	Target     string    `json:"target"`
	Root       string    `json:"root"`
	Reason     string    `json:"reason"`
}

func defaultRunAMQCommand(req amqCommandRequest) ([]byte, error) {
	cmd := exec.Command("amq", req.Arg...)
	cmd.Env = req.Env
	cmd.Dir = req.Dir
	cmd.Stdin = req.Stdin
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail != "" {
			return nil, fmt.Errorf("%w: %s", err, detail)
		}
		return nil, err
	}
	return out, nil
}

type amqContext struct {
	ProjectDir string
	Profile    string
	Env        amqEnv
	Root       string
	Me         string
}

func runAMQ(args []string) error {
	if len(args) == 0 {
		return usageErrorf("amq requires a subcommand: env, ops, route, who, presence, send, reply, drain, watch, list, read, thread, receipts, dlq, cleanup")
	}
	switch args[0] {
	case "env":
		return runAMQEnv(args[1:])
	case "ops":
		return runAMQOps(args[1:])
	case "route":
		return runAMQRoute(args[1:])
	case "who":
		return runAMQWho(args[1:])
	case "presence":
		return runAMQPresence(args[1:])
	case "send", "reply", "drain", "watch", "list", "read", "thread":
		// Root-resolving passthroughs for an EXTERNAL lead (no AM_ROOT injected):
		// the write/consume verbs (send/reply/drain/watch) AND the inspection
		// verbs (list/read/thread) all resolve the queue root, so bare `amq` from
		// a non-bootstrapped shell would silently hit the default `.agent-mail`.
		return runAMQPassthrough(args[0], args[1:])
	case "receipts":
		return runAMQReceipts(args[1:])
	case "dlq":
		return runAMQDLQ(args[1:])
	case "cleanup":
		return runAMQCleanup(args[1:])
	default:
		return usageErrorf("unknown amq subcommand %q. Use env, ops, route, who, presence, send, reply, drain, watch, list, read, thread, receipts, dlq, or cleanup.", args[0])
	}
}

func amqCommonFlagSet(name, usage string) (*flag.FlagSet, *string, *string, *string, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	project := fs.String("project", "", "project/team-home directory to resolve AMQ from (default: cwd)")
	session := fs.String("session", "", "AMQ session/workstream name")
	me := fs.String("me", "", "AMQ handle to resolve as")
	jsonOut := fs.Bool("json", false, "emit JSON output when the underlying AMQ command supports it")
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	return fs, project, session, me, jsonOut
}

func resolveAMQContext(projectFlag, session, me string, projectSet bool) (amqContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return amqContext{}, fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, projectFlag, projectSet)
	if err != nil {
		return amqContext{}, err
	}
	return resolveAMQContextForProject(projectDir, session, me)
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
	return amqContext{
		ProjectDir: projectDir,
		Profile:    team.DefaultProfile,
		Env:        env,
		Root:       absoluteAMQRoot(projectDir, env.Root),
		Me:         handle,
	}, nil
}

func resolveAMQContextForNamespace(projectDir, profile, session, me string) (amqContext, error) {
	if squadnamespace.NormalizeProfile(profile) == team.DefaultProfile {
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
	}, nil
}

func inferAMQContextProfileFromRoot(ctx amqContext, profileExplicit bool) amqContext {
	if profileExplicit {
		return ctx
	}
	if profile, ok := profileFromResolvedAMQRoot(ctx.ProjectDir, ctx.Root); ok {
		ctx.Profile = profile
	}
	return ctx
}

func profileFromResolvedAMQRoot(projectDir, root string) (string, bool) {
	root = absoluteAMQRoot(projectDir, root)
	if root == "" {
		return "", false
	}
	base := filepath.Join(filepath.Clean(projectDir), ".agent-mail")
	rel, err := filepath.Rel(base, root)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", false
	}
	parts := strings.Split(filepath.Clean(rel), string(os.PathSeparator))
	switch len(parts) {
	case 1:
		return team.DefaultProfile, true
	case 2:
		// AMQRoot stores the profile name verbatim as this path segment; keep
		// inference in lockstep so ReadProfile targets the same on-disk name.
		profile := squadnamespace.NormalizeProfile(parts[0])
		if err := team.ValidateProfileName(profile); err != nil {
			return "", false
		}
		return profile, true
	default:
		return "", false
	}
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
	if ctx.Root != "" {
		env = append(env, "AM_ROOT="+ctx.Root)
	}
	if root := absoluteAMQRoot(ctx.ProjectDir, ctx.Env.BaseRoot); root != "" {
		env = append(env, "AM_BASE_ROOT="+root)
	}
	if ctx.Me != "" {
		env = append(env, "AM_ME="+ctx.Me)
	}
	return env
}

func runAndWriteAMQ(out io.Writer, ctx amqContext, args []string) error {
	return runAndWriteAMQWithStdin(out, ctx, args, nil)
}

func runAndWriteAMQWithStdin(out io.Writer, ctx amqContext, args []string, stdin io.Reader) error {
	if out == nil {
		out = os.Stdout
	}
	data, err := runAMQCommand(amqCommandRequest{Dir: ctx.ProjectDir, Env: amqCommandEnv(ctx), Arg: args, Stdin: stdin})
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	return err
}

func runAMQEnv(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq env", `amq-squad amq env - show resolved AMQ context

Usage:
  amq-squad amq env [--project DIR] [--session NAME] [--me HANDLE] [--json]
`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
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

func runAMQOps(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq ops", `amq-squad amq ops - run AMQ operational diagnostics

Usage:
  amq-squad amq ops [--project DIR] [--session NAME] [--me HANDLE] [--json]
`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	cmd := []string{"doctor", "--ops"}
	if *jsonOut {
		cmd = append(cmd, "--json")
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func runAMQRoute(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq route", `amq-squad amq route - explain an AMQ route from this project/session

Usage:
  amq-squad amq route --to HANDLE [--project DIR] [--session NAME] [--me HANDLE] [--target-project NAME] [--target-session NAME] [--json]
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
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
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

func runAMQWho(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq who", `amq-squad amq who - list AMQ sessions and agents

Usage:
  amq-squad amq who [--project DIR] [--session NAME] [--me HANDLE] [--json]
`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	cmd := []string{"who", "--root", ctx.Root}
	if *jsonOut {
		cmd = append(cmd, "--json")
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func runAMQPresence(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq presence", `amq-squad amq presence - list AMQ presence for a session

Usage:
  amq-squad amq presence [--project DIR] [--session NAME] [--me HANDLE] [--json]
`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	cmd := []string{"presence", "list", "--root", ctx.Root}
	if *jsonOut {
		cmd = append(cmd, "--json")
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

// runAMQStreaming is the seam for a long-running passthrough (`amq watch`),
// which streams output until it exits rather than returning a buffered blob like
// runAMQCommand. Production wires the child's stdio to the operator's terminal;
// tests override it.
var runAMQStreaming = defaultRunAMQStreaming

func defaultRunAMQStreaming(ctx amqContext, cmd []string) error {
	c := exec.Command("amq", cmd...)
	c.Env = amqCommandEnv(ctx)
	c.Dir = ctx.ProjectDir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Stdin = os.Stdin
	return c.Run()
}

// runAMQPassthrough wraps a root-resolving raw `amq` verb (send, reply, drain,
// watch, list, read, thread) so an EXTERNAL
// lead — a human-driven session with no AM_ROOT/AM_ME injected — reaches the
// correct workstream root instead of the default `.agent-mail`. Bare `amq send`
// from such a session silently delivers to `.agent-mail` while a named-profile
// worker drains `.agent-mail/<session>`, so the message never arrives (#152).
//
// It consumes ONLY --project/--session/--me (to resolve the queue root + acting
// handle, exactly like every other `amq-squad amq` subcommand), injects them as
// AM_ROOT/AM_BASE_ROOT/AM_ME plus an explicit --root, and forwards every other
// argument to `amq` verbatim. For send, actor/audit guard flags (--me/--from,
// --unsafe-send-as, --reason) are also extracted from the forwarded tail before
// context resolution so they cannot bypass amq-squad's authority checks. It
// deliberately does NOT reimplement amq's flag surface; unknown flags flow
// straight through. A user-supplied --root is rejected so the resolved root can
// never be silently overridden into ambiguity.
//
// Because the wrapper OWNS --project/--session/--me as resolution inputs, amq's
// own --project/--session TARGET flags (cross-project / cross-session delivery)
// must be expressed with inline `--to handle@project:session` addressing or
// placed after a `--` terminator, which forwards the remainder untouched.
func runAMQPassthrough(sub string, args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, amqPassthroughUsage(sub))
		return nil
	}
	project, profile, session, me, projectSet, passthrough, opts, err := splitAMQPassthroughArgsWithOptions(sub, args)
	if err != nil {
		return err
	}
	if sub == "send" {
		passthrough, me, opts, err = consumeAMQSendGuardFlags(passthrough, me, opts)
		if err != nil {
			return err
		}
	}
	projectDir, resolvedProfile, err := resolveProjectProfile(project, profile, projectSet)
	if err != nil {
		return err
	}
	ctx, err := resolveAMQContextForNamespace(projectDir, resolvedProfile, session, me)
	if err != nil {
		return err
	}
	ctx = inferAMQContextProfileFromRoot(ctx, strings.TrimSpace(profile) != "")
	cmd := append([]string{sub, "--root", ctx.Root}, passthrough...)
	if err := guardAMQPassthrough(sub, ctx, passthrough, opts); err != nil {
		return err
	}
	if sub == "watch" {
		return runAMQStreaming(ctx, cmd)
	}
	if passthroughNeedsStdin(passthrough) {
		return runAndWriteAMQWithStdin(os.Stdout, ctx, cmd, os.Stdin)
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func guardAMQPassthrough(sub string, ctx amqContext, passthrough []string, opts amqPassthroughOptions) error {
	switch sub {
	case "send":
		if err := guardAMQSelfSend(ctx, passthrough); err != nil {
			return err
		}
		return guardAMQSendAsAuthority(ctx, opts)
	case "drain", "watch":
		return guardAMQMailboxConsume(sub, ctx, opts)
	default:
		return nil
	}
}

func guardAMQSendAsAuthority(ctx amqContext, opts amqPassthroughOptions) error {
	me := strings.TrimSpace(ctx.Me)
	if me == "" {
		return nil
	}
	t, err := team.ReadProfile(ctx.ProjectDir, ctx.Profile)
	if err != nil {
		return nil
	}
	if isOperatorHandle(t, me) {
		if opts.UnsafeSendAs {
			reason := strings.TrimSpace(opts.BoundaryReason)
			if reason == "" {
				return usageErrorf("amq send --unsafe-send-as requires --reason <why>")
			}
			return writeAMQBoundaryAudit(ctx, "send-as", strings.TrimSpace(os.Getenv("AM_ME")), me, reason)
		}
		return usageErrorf("refusing amq send as operator handle %q: the operator is mailbox-only and cannot be impersonated through the normal send path. Use amq-squad operator answer/directive where applicable, or pass --unsafe-send-as --reason <why> for an audited recovery send.", me)
	}
	member, ok := teamMemberByHandleOrRole(t, me)
	if !ok {
		return nil
	}
	if currentEnvProvesTeamRole(memberHandle(member), member.Role, ctx.Root) {
		return nil
	}
	if teamRoleRecordAuthorizesSendAs(t, member, ctx) {
		return nil
	}
	if opts.UnsafeSendAs {
		reason := strings.TrimSpace(opts.BoundaryReason)
		if reason == "" {
			return usageErrorf("amq send --unsafe-send-as requires --reason <why>")
		}
		return writeAMQBoundaryAudit(ctx, "send-as", strings.TrimSpace(os.Getenv("AM_ME")), me, reason)
	}
	return usageErrorf("refusing amq send as team role %q: current runtime is not verified live/bound for this namespace. Launch/resume the role first, or pass --unsafe-send-as --reason <why> for an audited recovery send.", me)
}

func isOperatorHandle(t team.Team, handle string) bool {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return false
	}
	op := team.EffectiveOperator(t)
	return op.Enabled && strings.TrimSpace(op.Handle) == handle
}

func teamRoleRecordAuthorizesSendAs(t team.Team, member team.Member, ctx amqContext) bool {
	handle := memberHandle(member)
	if handle == "" {
		handle = member.Role
	}
	agentDir := filepath.Join(ctx.Root, "agents", handle)
	rec, err := launch.Read(agentDir)
	if err != nil {
		return false
	}
	mode := effectiveTeamExecutionMode(t)
	session := strings.TrimSpace(ctx.Env.SessionName)
	if session == "" {
		session = strings.TrimSpace(member.Session)
	}
	if projectExecutionMode(mode) && strings.TrimSpace(member.Role) == strings.TrimSpace(t.Lead) {
		return launchRecordAuthorizesProjectLead(rec, member.Role, handle, ctx.Profile, session, ctx.Root)
	}
	return launchRecordMatchesIdentity(rec, member.Role, handle, ctx.Profile, session, ctx.Root)
}

func guardAMQSelfSend(ctx amqContext, passthrough []string) error {
	me := strings.TrimSpace(ctx.Me)
	if me == "" {
		return nil
	}
	to := strings.TrimSpace(amqFlagValue(passthrough, "to"))
	if to == "" || to != me {
		return nil
	}
	thread := strings.TrimSpace(amqFlagValue(passthrough, "thread"))
	a, b, ok := parseP2PThread(thread)
	if !ok {
		return nil
	}
	if (a == me && b != me) || (b == me && a != me) {
		other := a
		if other == me {
			other = b
		}
		return usageErrorf("refusing self-send on p2p thread %q: --me/AM_ME and --to are both %q. Reply to the other participant with --to %s, or use a non-p2p/private thread for an intentional self-note.", thread, me, other)
	}
	return nil
}

func guardAMQMailboxConsume(sub string, ctx amqContext, opts amqPassthroughOptions) error {
	target := strings.TrimSpace(ctx.Me)
	if target == "" {
		return nil
	}
	t, err := team.ReadProfile(ctx.ProjectDir, ctx.Profile)
	if err != nil {
		return nil
	}
	mode := effectiveTeamExecutionMode(t)
	if mode != executionModeProjectLead && mode != executionModeProjectTeam {
		return nil
	}
	member, ok := teamMemberByHandleOrRole(t, target)
	if !ok {
		return nil
	}
	leadHandle := teamLeadHandle(t)
	actor := strings.TrimSpace(os.Getenv("AM_ME"))
	if actor == target {
		return nil
	}
	if actor == "" && target == leadHandle {
		return nil
	}
	if opts.OverrideBoundary {
		reason := strings.TrimSpace(opts.BoundaryReason)
		if reason == "" {
			return usageErrorf("%s --override-boundary requires --reason <why>", boundaryCommandLabel(sub))
		}
		return writeAMQBoundaryAudit(ctx, sub, actor, target, reason)
	}
	role := member.Role
	if strings.TrimSpace(role) == "" {
		role = target
	}
	return usageErrorf("refusing %s of lead-owned mailbox %q (role %s) in %s mode: only the mailbox owner may consume it. Use amq list/read/thread for read-only inspection, or pass --override-boundary --reason <why> to audit an emergency drain.", boundaryCommandLabel(sub), target, role, mode)
}

func boundaryCommandLabel(sub string) string {
	if sub == "collect" {
		return "collect"
	}
	return "amq " + sub
}

func teamMemberByHandleOrRole(t team.Team, handleOrRole string) (team.Member, bool) {
	want := strings.TrimSpace(handleOrRole)
	for _, m := range orderedTeamMembers(t.Members) {
		if strings.TrimSpace(m.Handle) == want || strings.TrimSpace(m.Role) == want {
			return m, true
		}
	}
	return team.Member{}, false
}

func teamLeadHandle(t team.Team) string {
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 {
		lead = t.Members[0].Role
	}
	if m, ok := teamMemberByRole(t, lead); ok {
		if h := strings.TrimSpace(m.Handle); h != "" {
			return h
		}
		return strings.TrimSpace(m.Role)
	}
	return lead
}

func writeAMQBoundaryAudit(ctx amqContext, sub, actor, target, reason string) error {
	dir := filepath.Join(ctx.ProjectDir, team.DirName, "boundary-audit")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure boundary audit dir: %w", err)
	}
	session := strings.TrimSpace(ctx.Env.SessionName)
	if session == "" {
		session = "unknown-session"
	}
	rec := amqBoundaryAuditRecord{
		At:         time.Now().UTC(),
		Subcommand: sub,
		ProjectDir: ctx.ProjectDir,
		Session:    session,
		Actor:      strings.TrimSpace(actor),
		Target:     target,
		Root:       ctx.Root,
		Reason:     strings.TrimSpace(reason),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal boundary audit: %w", err)
	}
	path := filepath.Join(dir, sanitizeWorkstreamName(session)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open boundary audit: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write boundary audit: %w", err)
	}
	return nil
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

func parseP2PThread(thread string) (string, string, bool) {
	thread = strings.TrimSpace(thread)
	if !strings.HasPrefix(thread, "p2p/") {
		return "", "", false
	}
	pair := strings.TrimPrefix(thread, "p2p/")
	parts := strings.Split(pair, "__")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

// passthroughNeedsStdin reports whether the passthrough args include a --body
// value of "-", which means the amq subprocess will read its body from stdin.
func passthroughNeedsStdin(args []string) bool {
	for i, a := range args {
		if a == "--body=-" {
			return true
		}
		if a == "--body" && i+1 < len(args) && args[i+1] == "-" {
			return true
		}
	}
	return false
}

// splitAMQPassthroughArgs separates the wrapper's resolution flags
// (--project/--session/--me, single- or double-dash, space- or =-joined) from
// the arguments forwarded to `amq`.
//
// Wrapper flags are consumed ONLY from the LEADING run of args: the first token
// that is not one of them ends wrapper parsing, and the entire remainder is
// forwarded to `amq` verbatim. This makes it impossible to misread a passthrough
// flag's VALUE as a wrapper flag (e.g. `--subject --session` forwards both — the
// `--session` is the subject's value, not a re-resolution), and keeps the
// wrapper from ever silently sending to the wrong root. A user-supplied
// --root/--from-root in the wrapper position is rejected (the wrapper owns the
// root); an explicit `--` terminator forces the boundary so wrapper-shaped target
// flags can be passed through. Pure and table-testable.
func splitAMQPassthroughArgs(sub string, args []string) (project, session, me string, projectSet bool, passthrough []string, err error) {
	project, _, session, me, projectSet, passthrough, _, err = splitAMQPassthroughArgsWithOptions(sub, args)
	return project, session, me, projectSet, passthrough, err
}

func splitAMQPassthroughArgsWithOptions(sub string, args []string) (project, profile, session, me string, projectSet bool, passthrough []string, opts amqPassthroughOptions, err error) {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			i++ // drop the terminator; forward everything after it verbatim
			break
		}
		name, inlineVal, hasInline := amqFlagName(a)
		switch name {
		case "project", "profile", "session", "me":
			val := inlineVal
			next := i + 1
			if !hasInline {
				if next >= len(args) {
					return "", "", "", "", false, nil, opts, usageErrorf("flag --%s needs a value", name)
				}
				val = args[next]
				next++
			}
			switch name {
			case "project":
				project, projectSet = val, true
			case "profile":
				profile = val
			case "session":
				session = val
			case "me":
				me = val
			}
			i = next
			continue
		case "from":
			// --from is a --me alias for send/reply only, matching dispatch
			// ergonomics. For other verbs (drain, watch, list, etc.) it is not
			// a wrapper flag; stop wrapper parsing and forward --from verbatim.
			if sub != "send" && sub != "reply" {
				break
			}
			val := inlineVal
			next := i + 1
			if !hasInline {
				if next >= len(args) {
					return "", "", "", "", false, nil, opts, usageErrorf("flag --from needs a value")
				}
				val = args[next]
				next++
			}
			me = val
			i = next
			continue
		case "root", "from-root":
			return "", "", "", "", false, nil, opts, usageErrorf(
				"do not pass --%s to 'amq-squad amq %s'; amq-squad resolves the queue root from --project/--session. Use bare 'amq %s' for manual root control.",
				name, sub, sub)
		case "override-boundary":
			if sub != "drain" && sub != "watch" {
				break
			}
			opts.OverrideBoundary = true
			i++
			continue
		case "unsafe-send-as":
			if sub != "send" {
				break
			}
			opts.UnsafeSendAs = true
			i++
			continue
		case "reason":
			if sub != "drain" && sub != "watch" && sub != "send" {
				break
			}
			val := inlineVal
			next := i + 1
			if !hasInline {
				if next >= len(args) {
					return "", "", "", "", false, nil, opts, usageErrorf("flag --reason needs a value")
				}
				val = args[next]
				next++
			}
			opts.BoundaryReason = val
			i = next
			continue
		}
		// First non-wrapper token: stop here and forward the rest untouched.
		break
	}
	passthrough = append(passthrough, args[i:]...)
	// --body-file is a send/reply parity flag: rewrite it to --body @<path>
	// (or --body - for stdin) anywhere in the passthrough, not just the leading
	// position. Other verbs (drain, list, etc.) receive --body-file verbatim.
	if sub == "send" || sub == "reply" {
		passthrough = normalizeBodyFileFlag(passthrough)
	}
	return project, profile, session, me, projectSet, passthrough, opts, nil
}

func consumeAMQSendGuardFlags(args []string, initialMe string, opts amqPassthroughOptions) ([]string, string, amqPassthroughOptions, error) {
	me := strings.TrimSpace(initialMe)
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		name, inlineVal, hasInline := amqFlagName(args[i])
		switch name {
		case "me", "from":
			val := inlineVal
			if !hasInline {
				if i+1 >= len(args) {
					return nil, "", opts, usageErrorf("flag --%s needs a value", name)
				}
				i++
				val = args[i]
			}
			val = strings.TrimSpace(val)
			if val == "" {
				return nil, "", opts, usageErrorf("flag --%s needs a non-empty value", name)
			}
			if me != "" && me != val {
				return nil, "", opts, usageErrorf("conflicting amq send actor: wrapper --me/--from is %q but passthrough --%s is %q", me, name, val)
			}
			me = val
			continue
		case "unsafe-send-as":
			opts.UnsafeSendAs = true
			continue
		case "reason":
			val := inlineVal
			if !hasInline {
				if i+1 >= len(args) {
					return nil, "", opts, usageErrorf("flag --reason needs a value")
				}
				i++
				val = args[i]
			}
			val = strings.TrimSpace(val)
			if opts.BoundaryReason != "" && opts.BoundaryReason != val {
				return nil, "", opts, usageErrorf("conflicting --reason values for amq send")
			}
			opts.BoundaryReason = val
			continue
		}
		out = append(out, args[i])
		if amqSendPassthroughFlagConsumesValue(name, hasInline) && i+1 < len(args) {
			i++
			out = append(out, args[i])
		}
	}
	return out, me, opts, nil
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

// normalizeBodyFileFlag rewrites every --body-file <path> or --body-file=<path>
// token in args to --body @<path> (or --body - for stdin). Safe to call on the
// full passthrough slice because amq has no native --body-file flag.
func normalizeBodyFileFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		name, inlineVal, hasInline := amqFlagName(args[i])
		if name != "body-file" {
			out = append(out, args[i])
			continue
		}
		val := inlineVal
		if !hasInline {
			if i+1 >= len(args) {
				out = append(out, args[i]) // malformed; forward as-is
				continue
			}
			i++
			val = args[i]
		}
		bodyVal := "@" + val
		if val == "-" {
			bodyVal = "-"
		}
		out = append(out, "--body", bodyVal)
	}
	return out
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

func amqPassthroughUsage(sub string) string {
	return fmt.Sprintf(`amq-squad amq %s - run 'amq %s' against the resolved workstream root

Usage:
  amq-squad amq %s [--project DIR] [--session NAME] [--me HANDLE] [amq %s flags...]

amq-squad consumes --project/--session/--me (pass them FIRST, before the amq
flags) to resolve the queue root — so an external lead reaches
.agent-mail/<session> instead of the default .agent-mail — and forwards every
remaining flag to 'amq %s'. For 'send', passthrough --me/--from and
--unsafe-send-as/--reason are treated as amq-squad guard flags wherever they
appear. Do not pass --root; it is resolved for you. For a cross-project/
cross-session target, use inline addressing (--to handle@project:session) or
place amq's own target flags after '--'.

See 'amq %s --help' for the full flag surface.
`, sub, sub, sub, sub, sub, sub)
}

func runAMQReceipts(args []string) error {
	if len(args) == 0 {
		return usageErrorf("amq receipts requires list or wait")
	}
	switch args[0] {
	case "list":
		return runAMQReceiptsList(args[1:])
	case "wait":
		return runAMQReceiptsWait(args[1:])
	default:
		return usageErrorf("unknown amq receipts subcommand %q. Use list or wait.", args[0])
	}
}

func runAMQReceiptsList(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq receipts list", `amq-squad amq receipts list - inspect delivery receipts

Usage:
  amq-squad amq receipts list --me HANDLE [--project DIR] [--session NAME] [--msg-id ID] [--json]
`)
	msgID := fs.String("msg-id", "", "filter receipts for one message id")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if ctx.Me == "" {
		return usageErrorf("amq receipts list requires --me")
	}
	cmd := []string{"receipts", "list", "--root", ctx.Root, "--me", ctx.Me}
	if *msgID != "" {
		cmd = append(cmd, "--msg-id", *msgID)
	}
	if *jsonOut {
		cmd = append(cmd, "--json")
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func runAMQReceiptsWait(args []string) error {
	fs, project, session, me, _ := amqCommonFlagSet("amq receipts wait", `amq-squad amq receipts wait - wait for one delivery receipt

Usage:
  amq-squad amq receipts wait --me HANDLE --msg-id ID [--stage drained|dlq] [--timeout 60s] [--project DIR] [--session NAME]
`)
	msgID := fs.String("msg-id", "", "message id to wait for")
	stage := fs.String("stage", "drained", "receipt stage to wait for: drained or dlq")
	timeout := fs.String("timeout", "60s", "maximum wait duration")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *msgID == "" {
		return usageErrorf("amq receipts wait requires --msg-id")
	}
	if *stage != "drained" && *stage != "dlq" {
		return usageErrorf("--stage must be drained or dlq")
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if ctx.Me == "" {
		return usageErrorf("amq receipts wait requires --me")
	}
	cmd := []string{"receipts", "wait", "--root", ctx.Root, "--me", ctx.Me, "--msg-id", *msgID, "--stage", *stage, "--timeout", *timeout}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func runAMQDLQ(args []string) error {
	if len(args) == 0 {
		return usageErrorf("amq dlq requires list, read, retry, retry-all, or purge")
	}
	switch args[0] {
	case "list":
		return runAMQDLQList(args[1:])
	case "read":
		return runAMQDLQRead(args[1:])
	case "retry":
		return runAMQDLQMutation("retry", args[1:])
	case "retry-all":
		return runAMQDLQMutation("retry-all", args[1:])
	case "purge":
		return runAMQDLQMutation("purge", args[1:])
	default:
		return usageErrorf("unknown amq dlq subcommand %q. Use list, read, retry, retry-all, or purge.", args[0])
	}
}

func runAMQDLQList(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq dlq list", `amq-squad amq dlq list - inspect one agent's dead-letter queue

Usage:
  amq-squad amq dlq list --me HANDLE [--project DIR] [--session NAME] [--json]
`)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if ctx.Me == "" {
		return usageErrorf("amq dlq list requires --me")
	}
	cmd := []string{"dlq", "list", "--root", ctx.Root, "--me", ctx.Me}
	if *jsonOut {
		cmd = append(cmd, "--json")
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func runAMQDLQRead(args []string) error {
	fs, project, session, me, jsonOut := amqCommonFlagSet("amq dlq read", `amq-squad amq dlq read - read one DLQ item

Usage:
  amq-squad amq dlq read --me HANDLE --id ID [--project DIR] [--session NAME] [--json]
`)
	id := fs.String("id", "", "DLQ id from amq dlq list")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *id == "" {
		return usageErrorf("amq dlq read requires --id")
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if ctx.Me == "" {
		return usageErrorf("amq dlq read requires --me")
	}
	cmd := []string{"dlq", "read", "--root", ctx.Root, "--me", ctx.Me, "--id", *id}
	if *jsonOut {
		cmd = append(cmd, "--json")
	}
	return runAndWriteAMQ(os.Stdout, ctx, cmd)
}

func runAMQDLQMutation(kind string, args []string) error {
	fs, project, session, me, _ := amqCommonFlagSet("amq dlq "+kind, `amq-squad amq dlq mutation - retry or purge DLQ items with confirmation

Usage:
  amq-squad amq dlq retry --me HANDLE --id ID [--project DIR] [--session NAME] [--dry-run] [--yes|-y]
  amq-squad amq dlq retry-all --me HANDLE [--project DIR] [--session NAME] [--dry-run] [--yes|-y]
  amq-squad amq dlq purge --me HANDLE [--older-than 168h] [--project DIR] [--session NAME] [--dry-run] [--yes|-y]
`)
	id := fs.String("id", "", "DLQ id from amq dlq list")
	olderThan := fs.String("older-than", "168h", "purge DLQ entries older than this duration")
	dryRun := fs.Bool("dry-run", false, "print the AMQ command without executing it")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if kind == "retry" && *id == "" {
		return usageErrorf("amq dlq retry requires --id")
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if ctx.Me == "" {
		return usageErrorf("amq dlq %s requires --me", kind)
	}
	cmdKind := kind
	if kind == "retry-all" {
		cmdKind = "retry"
	}
	cmd := []string{"dlq", cmdKind, "--root", ctx.Root, "--me", ctx.Me}
	switch kind {
	case "retry":
		cmd = append(cmd, "--id", *id)
	case "retry-all":
		cmd = append(cmd, "--all")
	case "purge":
		cmd = append(cmd, "--older-than", *olderThan, "--yes")
	}
	return previewConfirmAndRunAMQ(os.Stdout, os.Stdin, ctx, cmd, *dryRun, *yes)
}

func runAMQCleanup(args []string) error {
	fs, project, session, me, _ := amqCommonFlagSet("amq cleanup", `amq-squad amq cleanup - confirm-gated AMQ tmp cleanup for one session

Usage:
  amq-squad amq cleanup --session NAME --tmp-older-than 36h [--project DIR] [--me HANDLE] [--dry-run] [--yes|-y]
`)
	olderThan := fs.String("tmp-older-than", "", "clean AMQ tmp files older than this duration")
	dryRun := fs.Bool("dry-run", false, "print the AMQ command without executing it")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *olderThan == "" {
		return usageErrorf("amq cleanup requires --tmp-older-than")
	}
	if strings.TrimSpace(*session) == "" {
		return usageErrorf("amq cleanup requires --session")
	}
	ctx, err := resolveAMQContext(*project, *session, *me, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	cmd := []string{"cleanup", "--root", ctx.Root, "--tmp-older-than", *olderThan, "--yes"}
	return previewConfirmAndRunAMQ(os.Stdout, os.Stdin, ctx, cmd, *dryRun, *yes)
}

func previewConfirmAndRunAMQ(out io.Writer, in io.Reader, ctx amqContext, cmd []string, dryRun, yes bool) error {
	if out == nil {
		out = os.Stdout
	}
	preview := shellCommand("amq", cmd...)
	fmt.Fprintln(out, "AMQ command preview")
	fmt.Fprintf(out, "project: %s\n", ctx.ProjectDir)
	fmt.Fprintf(out, "root:    %s\n", ctx.Root)
	if ctx.Me != "" {
		fmt.Fprintf(out, "me:      %s\n", ctx.Me)
	}
	fmt.Fprintf(out, "command: %s\n", preview)
	if dryRun {
		fmt.Fprintln(out, "Dry run: command not executed.")
		return nil
	}
	if !yes && !confirmAMQCommand(out, in) {
		fmt.Fprintln(out, "amq command aborted.")
		return nil
	}
	data, err := runAMQCommand(amqCommandRequest{Dir: ctx.ProjectDir, Env: amqCommandEnv(ctx), Arg: cmd})
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		fmt.Fprintln(out, "(no AMQ output)")
		return nil
	}
	_, err = out.Write(data)
	return err
}

func confirmAMQCommand(out io.Writer, r io.Reader) bool {
	if r == nil {
		r = os.Stdin
	}
	fmt.Fprint(out, "Run this AMQ command? [y/N] ")
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
