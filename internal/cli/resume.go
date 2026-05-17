package cli

import (
	"flag"
	"fmt"
	"os"

	"github.com/omriariav/amq-squad/internal/team"
)

func runResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "AMQ workstream session name to resume into (default: team workstream)")
	restoreExisting := fs.Bool("restore-existing", false, "fail if no team member has restorable launch records for the workstream")
	dryRun := fs.Bool("dry-run", false, "plan-only; default behavior is already plan-only and exists for parity with other commands")
	forceDuplicate := fs.Bool("force-duplicate", false, "include commands even when a live agent is detected for a member")
	noBootstrap := fs.Bool("no-bootstrap", false, "emit fresh launch commands that skip the generated bootstrap prompt")
	trustRaw := fs.String("trust", "", "Codex trust profile for fresh members: sandboxed (default) or trusted")
	modelFlag := fs.String("model", "", "per-persona model overrides for fresh members, e.g. cto=gpt-5,fullstack=sonnet")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for fresh members, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for fresh members, e.g. '--chrome'")
	profileFlag := fs.String("profile", "", "team profile to resume (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad resume - plan how to bring the team back

Usage:
  amq-squad resume [--profile NAME] [--session name] [--restore-existing]
                   [--dry-run] [--force-duplicate]
                   [--no-bootstrap] [--trust sandboxed|trusted]
                   [--model role=model,...]
                   [--codex-args args] [--claude-args args]

Inspects .amq-squad/team.json plus local launch history and live-agent
signals (wake locks, agent PID liveness, presence) to print a per-member
plan plus copy-pasteable commands.

Fresh / new-session behavior belongs to 'amq-squad fork --from S --as T'.
Use 'amq-squad up' to open the planned team in tmux from team intent.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !team.ExistsProfile(cwd, profile) {
		return fmt.Errorf("no team configured for profile %q. Run 'amq-squad team init%s' first.", profile, profileInitHint(profile))
	}
	mode := resumeModeDefault
	if *restoreExisting {
		mode = resumeModeRestoreExisting
	}
	return executeResume(resumeExecution{
		ProjectDir:       cwd,
		RequestedSession: *sessionFlag,
		ExplicitSession:  flagWasSet(fs, "session"),
		Mode:             mode,
		Force:            *forceDuplicate,
		NoBootstrap:      *noBootstrap,
		TrustRaw:         *trustRaw,
		ExplicitTrust:    flagWasSet(fs, "trust"),
		ModelRaw:         *modelFlag,
		CodexArgsRaw:     *codexArgsRaw,
		ClaudeArgsRaw:    *claudeArgsRaw,
		DryRun:           *dryRun,
		Profile:          profile,
		Style:            resumePrinterStyle{Label: "resume", FooterVerb: "up"},
	})
}
