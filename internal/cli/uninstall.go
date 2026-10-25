package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/uninstall"
)

const uninstallUsage = `amq-squad uninstall - remove what amq-squad 3.0 put on this host

Usage:
  amq-squad uninstall [--root DIR | --project P --session S] [--write [--owner-files] [--purge]]

Without --write this is a dry run: it prints every path it would remove, in
groups (session state under the AMQ root's extensions/amq-squad, per-seat
config dirs, the core tools dir, the worktrees it created under
.claude/worktrees, the owner files under ~/.amq-squad), and the commands and
edits it will not do for you. Nothing changes.

  --write         remove the groups above (not the owner files, not the sealed ledgers)
  --owner-files   with --write: also registry.yaml, grants.yaml, claude-token and
                  gate-binding.json under ~/.amq-squad. The two secrets go first,
                  overwritten once with zeros and then unlinked (best effort on a
                  copy-on-write file system); their contents are never printed.
  --purge         with --write: also the sealed ledgers (commits ledger, exits queue,
                  outcome log, hook ledger), the evidence refs under
                  refs/amq-squad/evidence (deleted through the hardened git; no
                  branch or object of the seat repository is touched), the clear
                  records and the gate store. Kept by default.

It refuses (uninstall_busy) while a scheduler process, a live seat, an open
proof window or a commit step runs, and refuses (uninstall_refused_symlink),
removing nothing, when a symlink stands where it expects a directory. It never
edits ~/.claude/settings.json, ~/.codex/config.toml or Claude's plugin cache:
it prints the claude plugin uninstall command and the codex trust entries for
you to remove. A second run reports nothing to do; a partial failure lists what
remains and exits non-zero.
`

func runUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, uninstallUsage) }
	rootFlag := fs.String("root", "", "AMQ session root (default AM_ROOT)")
	project := fs.String("project", "", "project/team home, to resolve the session root")
	profile := fs.String("profile", "", "team profile, to resolve the session root")
	session := fs.String("session", "", "session, to resolve the session root")
	registerScopedFlagAliases(fs, project, session, profile)
	write := fs.Bool("write", false, "remove (the default is a dry run)")
	ownerFiles := fs.Bool("owner-files", false, "with --write: also the owner files under ~/.amq-squad")
	purge := fs.Bool("purge", false, "with --write: also the sealed ledgers, evidence refs, clear records and gate store")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("uninstall takes no positional arguments")
	}
	root, err := uninstallRoot(fs, *rootFlag, *project, *profile, *session)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	res, err := uninstall.Run(uninstall.Options{Root: root, Home: home, CodexHome: os.Getenv("CODEX_HOME"),
		Write: *write, OwnerFiles: *ownerFiles, Purge: *purge})
	res.Render(os.Stdout)
	if !*write && (*ownerFiles || *purge) {
		fmt.Fprintln(os.Stdout, "\n--owner-files and --purge take effect only with --write; this was a dry run")
	}
	var ue *uninstall.Error
	if errors.As(err, &ue) && ue.Code == uninstall.CodePartial {
		return &PartialError{Message: err.Error(), Cause: err}
	}
	return err
}

// uninstallRoot resolves the session root: --root, else AM_ROOT, else the
// AMQ root of --project/--profile/--session. It opens nothing and creates
// nothing.
func uninstallRoot(fs *flag.FlagSet, rootFlag, project, profile, session string) (string, error) {
	scoped := flagWasSet(fs, "session") || flagWasSet(fs, "project") || flagWasSet(fs, "profile")
	root := strings.TrimSpace(rootFlag)
	if root == "" && !scoped {
		root = strings.TrimSpace(os.Getenv("AM_ROOT"))
	}
	if root == "" {
		if !scoped {
			return "", usageErrorf("uninstall: no session root; pass --root, set AM_ROOT, or pass --session")
		}
		ctx, err := resolveScopedCommandContext(project, profile, session, "", fs)
		if err != nil {
			return "", err
		}
		emitContextDiagnostics(ctx)
		root = absAMQRoot(ctx.ProjectDir, ctx.Root)
	}
	return root, nil
}
