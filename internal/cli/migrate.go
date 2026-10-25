package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/omriariav/amq-squad/v3/internal/migrate"
)

// runMigrate is `amq-squad migrate <v2 profile path>` (decision 6, brief
// v3-5-migrate part B): the one-shot converter from a v2 profile to
// squad.yaml schema 1.
func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	write := fs.Bool("write", false, "write the files (default: a dry run that prints the report and the files it would write)")
	remove := fs.Bool("remove-v2", false, "remove the v2 files that are dead under decision 6 (needs a squad.yaml that loads; dry run without --write)")
	fs.Usage = func() {
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
pass --remove-v2, which refuses when squad.yaml is absent.

Examples:
  amq-squad migrate ~/Code/app/.amq-squad/team.json
  amq-squad migrate --write ~/Code/app/.amq-squad/team.json
  amq-squad migrate --remove-v2 --write ~/Code/app/.amq-squad/team.json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return usageErrorf("migrate takes exactly one v2 profile path; got %d", fs.NArg())
	}
	opts := migrate.Options{Profile: fs.Arg(0)}
	if *remove {
		rp, err := migrate.BuildRemove(opts)
		if err != nil {
			return migrateError(err)
		}
		if *write {
			if _, err := rp.Apply(); err != nil {
				return err
			}
		}
		fmt.Print(rp.Report(*write))
		return nil
	}
	plan, err := migrate.Build(opts)
	if err != nil {
		return migrateError(err)
	}
	if *write {
		if _, err := plan.Apply(); err != nil {
			return migrateError(err)
		}
	}
	fmt.Print(plan.Report(*write))
	return nil
}

// migrateError makes a migrate refusal a user error (exit 1).
func migrateError(err error) error {
	var r *migrate.Refusal
	if errors.As(err, &r) {
		return UsageError(r.Error())
	}
	return err
}
