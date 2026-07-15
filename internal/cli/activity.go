package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/activity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var activityNow = func() time.Time { return time.Now().UTC() }

func runActivity(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad activity - write or clear an agent activity heartbeat

Usage:
  amq-squad activity set --session S --me HANDLE --phase PHASE [--task ID] [--detail TEXT] [--profile P] [--json]
  amq-squad activity clear --session S --me HANDLE [--profile P] [--json]

Writes <amq-root>/agents/<handle>/activity.json atomically so status and console
can show an honest current task / phase / last-activity signal without peeking
at panes. The heartbeat goes stale after 90 seconds unless the agent writes a
new one; stale or malformed files never imply progress.
`)
		if len(args) == 0 {
			return usageErrorf("activity requires a subcommand (set or clear)")
		}
		return nil
	}
	switch args[0] {
	case "set":
		return runActivitySet(args[1:])
	case "clear":
		return runActivityClear(args[1:])
	default:
		return usageErrorf("unknown 'activity' subcommand: %q. Try set or clear.", args[0])
	}
}

func runActivitySet(args []string) error {
	fs := flag.NewFlagSet("activity set", flag.ContinueOnError)
	session := fs.String("session", "", "workstream session")
	me := fs.String("me", "", "agent handle writing the heartbeat")
	phase := fs.String("phase", "", "current phase, e.g. reading, testing, waiting-on-command")
	taskID := fs.String("task", "", "current task id")
	detail := fs.String("detail", "", "short human-readable detail")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, session, profileFlag)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, ns, err := activityContext(*session, *me, *projectFlag, *profileFlag, fs, "activity set")
	if err != nil {
		return err
	}
	if strings.TrimSpace(*phase) == "" {
		return usageErrorf("activity set requires --phase")
	}
	ctx, admission, err := acquireRevalidatedAMQWriter(ctx, func() (amqContext, error) {
		return resolveAMQContext(*projectFlag, *profileFlag, *session, *me, flagWasSet(fs, "project"))
	})
	if err != nil {
		return err
	}
	defer admission.close()
	ns = squadnamespace.Resolve(ctx.ProjectDir, ctx.Profile, ctx.Session)
	if err := ensureNoNamespaceConflict("activity set", ctx.ProjectDir, ctx.Profile, ctx.Session, flagWasSet(fs, "profile")); err != nil {
		return err
	}
	path := activity.Path(filepath.Join(ctx.Root, "agents", ctx.Me))
	file := activity.File{
		Handle:    ctx.Me,
		TaskID:    *taskID,
		Phase:     *phase,
		Detail:    *detail,
		WrittenAt: activityNow(),
	}
	if err := activity.Write(filepath.Dir(path), file); err != nil {
		return err
	}
	if jsonOut != nil && *jsonOut {
		return printJSONEnvelope("activity", mutationResult{
			Command:   "activity set",
			Status:    "written",
			Project:   ctx.ProjectDir,
			Session:   ctx.Env.SessionName,
			Profile:   ctx.Profile,
			Namespace: ns,
			ID:        strings.TrimSpace(*taskID),
			TaskID:    strings.TrimSpace(*taskID),
			Handle:    ctx.Me,
			Root:      ctx.Root,
		})
	}
	fmt.Printf("activity written for %s", ctx.Me)
	if task := strings.TrimSpace(*taskID); task != "" {
		fmt.Printf(" task %s", task)
	}
	fmt.Printf(" phase %s\n", strings.TrimSpace(*phase))
	return nil
}

func runActivityClear(args []string) error {
	fs := flag.NewFlagSet("activity clear", flag.ContinueOnError)
	session := fs.String("session", "", "workstream session")
	me := fs.String("me", "", "agent handle clearing the heartbeat")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, session, profileFlag)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, ns, err := activityContext(*session, *me, *projectFlag, *profileFlag, fs, "activity clear")
	if err != nil {
		return err
	}
	ctx, admission, err := acquireRevalidatedAMQWriter(ctx, func() (amqContext, error) {
		return resolveAMQContext(*projectFlag, *profileFlag, *session, *me, flagWasSet(fs, "project"))
	})
	if err != nil {
		return err
	}
	defer admission.close()
	ns = squadnamespace.Resolve(ctx.ProjectDir, ctx.Profile, ctx.Session)
	if err := ensureNoNamespaceConflict("activity clear", ctx.ProjectDir, ctx.Profile, ctx.Session, flagWasSet(fs, "profile")); err != nil {
		return err
	}
	if err := activity.Clear(filepath.Join(ctx.Root, "agents", ctx.Me)); err != nil {
		return err
	}
	if jsonOut != nil && *jsonOut {
		return printJSONEnvelope("activity", mutationResult{
			Command:   "activity clear",
			Status:    "cleared",
			Project:   ctx.ProjectDir,
			Session:   ctx.Env.SessionName,
			Profile:   ctx.Profile,
			Namespace: ns,
			Handle:    ctx.Me,
			Root:      ctx.Root,
		})
	}
	fmt.Printf("activity cleared for %s\n", ctx.Me)
	return nil
}

func activityContext(session, me, projectFlag, profileFlag string, fs *flag.FlagSet, operation string) (amqContext, squadnamespace.Ref, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return amqContext{}, squadnamespace.Ref{}, usageErrorf("%s requires --session", operation)
	}
	if err := team.ValidateSessionName(session); err != nil {
		return amqContext{}, squadnamespace.Ref{}, usageErrorf("invalid --session: %v", err)
	}
	me = strings.TrimSpace(me)
	if me == "" {
		return amqContext{}, squadnamespace.Ref{}, usageErrorf("%s requires --me", operation)
	}
	if err := team.ValidateHandle(me); err != nil {
		return amqContext{}, squadnamespace.Ref{}, usageErrorf("invalid --me: %v", err)
	}
	if fs.NArg() > 0 {
		return amqContext{}, squadnamespace.Ref{}, usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	ctx, err := resolveAMQContext(projectFlag, profileFlag, session, me, flagWasSet(fs, "project"))
	if err != nil {
		return amqContext{}, squadnamespace.Ref{}, err
	}
	return ctx, squadnamespace.Resolve(ctx.ProjectDir, ctx.Profile, ctx.Session), nil
}
