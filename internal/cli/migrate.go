package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/migrate"
)

// runMigrate is `amq-squad migrate <v2 profile path>` (decision 6, brief
// v3-5-migrate part B): the one-shot converter from a v2 profile to
// squad.yaml schema 1. It is also the one output funnel (M1): everything the
// verb would print, on stdout or as the error the CLI renders, passes
// migrate.UnsafeText once, here; if any of it would change under redact.Block
// the verb prints a fixed line naming only a location class and exits 2,
// and nothing else.
func runMigrate(args []string) error {
	var out strings.Builder
	profile, remove, err := migrateRun(args, &out)
	text := out.String()
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	if migrate.UnsafeText(text) || migrate.UnsafeText(errText) {
		return &codedExitError{code: ExitSystem, msg: migrate.LeakLine(migrate.LeakClass(profile, remove))}
	}
	fmt.Print(text)
	return err
}

func migrateRun(args []string, out *strings.Builder) (profile string, remove bool, err error) {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	write := fs.Bool("write", false, "write the files (default: a dry run that prints the report and the files it would write)")
	removeV2 := fs.Bool("remove-v2", false, "remove the v2 files that are dead under decision 6 (needs a squad.yaml that loads; dry run without --write)")
	usage := func() {
		fmt.Fprint(os.Stderr, `amq-squad migrate - convert a v2 profile to squad.yaml (one shot)

Usage:
  amq-squad migrate [--write] <v2 profile path>
  amq-squad migrate --remove-v2 [--write] <v2 profile path>

The profile path is <project>/.amq-squad/team.json (or .amq-squad/teams/<name>.json).
migrate reads the v2 files read-only (the profile, launch.local.json, team-rules.md,
the briefs, the staged roles and each agent's role.md) and prints what it mapped,
every v2 field with no 3.0 meaning, and the files it would write. Without --write it
changes nothing on disk. With --write it writes, each only when absent:
  <project>/squad.yaml                       (0644; refused with squad_yaml_exists if present)
  <project>/.amq-squad/roles/<id>.md         (a custom role's role.md, as a role prompt)
  ~/.amq-squad/grants.yaml.proposed          (0600; the grant-side fields the profile implies)
It never writes grants.yaml or registry.yaml, never trusts anything (it prints the
amq-squad trust command for you to run) and copies no env block. It refuses with
migrate_refused_safety_default when a v2 field would change a safety default (a
launcher, an allow list, a trust or approval mode) and with migrate_refused_credential
when a file holds a value shaped like a real credential. Nothing is deleted unless you
pass --remove-v2, which refuses when squad.yaml is absent. The plan refuses before anything
is touched; a change during --write or --remove-v2 stops at the first refusal with a partial
report and exit 3 (there is no transaction).

Examples:
  amq-squad migrate ~/Code/app/.amq-squad/team.json
  amq-squad migrate --write ~/Code/app/.amq-squad/team.json
  amq-squad migrate --remove-v2 --write ~/Code/app/.amq-squad/team.json
`)
	}
	// the flag package prints nothing of its own: a parse error is returned
	// (and passes the funnel), and only an explicit -h prints the static usage
	fs.Usage = func() {}
	if err := parseFlags(fs, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage()
		}
		return "", false, err
	}
	if fs.NArg() != 1 {
		return "", false, usageErrorf("migrate takes exactly one v2 profile path; got %d", fs.NArg())
	}
	opts := migrate.Options{Profile: fs.Arg(0)}
	if *removeV2 {
		rp, err := migrate.BuildRemove(opts)
		if err != nil {
			return opts.Profile, true, migrateError(err)
		}
		defer rp.Close()
		if *write {
			removed, err := rp.Apply()
			if err != nil {
				return opts.Profile, true, migrateStopped("--remove-v2", removed, len(rp.Files), err, out)
			}
		}
		out.WriteString(rp.Report(*write))
		return opts.Profile, true, nil
	}
	plan, err := migrate.Build(opts)
	if err != nil {
		return opts.Profile, false, migrateError(err)
	}
	defer plan.Close()
	if *write {
		done, err := plan.Apply()
		if err != nil {
			return opts.Profile, false, migrateStopped("--write", done, len(plan.Writes), err, out)
		}
	}
	out.WriteString(plan.Report(*write))
	return opts.Profile, false, nil
}

// migrateStopped is the error of an apply that stopped at its first refusal.
// When something was already done it prints the partial report and is a
// PartialError (exit 3, as uninstall); when nothing was, it is the refusal.
func migrateStopped(verb string, done []string, total int, err error, out *strings.Builder) error {
	if len(done) == 0 {
		return migrateError(err)
	}
	out.WriteString(migrate.PartialReport(verb, done, total, err))
	return &PartialError{Message: err.Error(), Cause: err}
}

// migrateError makes a migrate refusal a user error (exit 1).
func migrateError(err error) error {
	var r *migrate.Refusal
	if errors.As(err, &r) {
		return UsageError(r.Error())
	}
	return err
}
