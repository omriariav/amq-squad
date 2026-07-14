package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func runNamespace(args []string) error {
	if len(args) == 0 {
		printNamespaceUsage()
		return usageErrorf("namespace requires migrate, recover, or rollback")
	}
	switch args[0] {
	case "migrate":
		return runNamespaceMigrate(args[1:])
	case "recover":
		return runNamespaceRecover(args[1:])
	case "rollback":
		return runNamespaceRollback(args[1:])
	case "-h", "--help", "help":
		printNamespaceUsage()
		return nil
	default:
		return usageErrorf("unknown namespace subcommand %q: use migrate, recover, or rollback", args[0])
	}
}

func printNamespaceUsage() {
	fmt.Fprint(os.Stderr, `amq-squad namespace - migrate stopped namespace state safely

Usage:
  amq-squad namespace migrate --from PROFILE/SESSION --to PROFILE/SESSION [--project DIR] [--dry-run] [--json]
  amq-squad namespace recover --id MIGRATION_ID [--project DIR] [--dry-run] [--json]
  amq-squad namespace rollback --id MIGRATION_ID [--project DIR] [--dry-run] [--json]

Migration is cold-only. Both endpoints must be stopped, existing profile
configs must be compatible, and target artifact paths must not exist. Dry-run
performs the same read-only inventory, liveness, lock, collision, device, space,
schema, and digest checks without creating a journal, lock, stage, or backup.

Examples:
  amq-squad namespace migrate --from default/issue-359 --to recovery/issue-359 --dry-run --json
  amq-squad namespace recover --id migration-0123456789abcdef --json
  amq-squad namespace rollback --id migration-0123456789abcdef --dry-run
`)
}

func runNamespaceMigrate(args []string) error {
	fs := flag.NewFlagSet("namespace migrate", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	fromFlag := fs.String("from", "", "source namespace as PROFILE/SESSION")
	toFlag := fs.String("to", "", "target namespace as PROFILE/SESSION")
	dryRun := fs.Bool("dry-run", false, "perform a read-only preflight and print the migration plan")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned migration envelope")
	fs.Usage = printNamespaceUsage
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("namespace migrate takes no positional arguments")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	project, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	source, err := parseNamespaceMigrationSelector(project, *fromFlag, "--from")
	if err != nil {
		return err
	}
	target, err := parseNamespaceMigrationSelector(project, *toFlag, "--to")
	if err != nil {
		return err
	}
	plan, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: project, Source: source, Target: target, DryRun: *dryRun})
	if err != nil {
		return err
	}
	if *dryRun {
		return renderNamespaceMigrationPlan(plan, *jsonOut)
	}
	if len(plan.Blockers) > 0 {
		if *jsonOut {
			if err := printJSONEnvelope("namespace_migration_plan", plan); err != nil {
				return err
			}
		}
		return fmt.Errorf("namespace migration refused: %s", strings.Join(plan.Blockers, "; "))
	}
	result, err := executeNamespaceMigration(plan)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("namespace_migration", result)
	}
	return renderNamespaceMigrationResult(result)
}

func runNamespaceRecover(args []string) error {
	fs := flag.NewFlagSet("namespace recover", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	id := fs.String("id", "", "migration id")
	dryRun := fs.Bool("dry-run", false, "inspect recovery without writing")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned recovery envelope")
	fs.Usage = printNamespaceUsage
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	project, err := namespaceMigrationProjectFlag(*projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	result, err := recoverNamespaceMigration(project, strings.TrimSpace(*id), *dryRun)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("namespace_migration_recovery", result)
	}
	return renderNamespaceMigrationResult(result)
}

func runNamespaceRollback(args []string) error {
	fs := flag.NewFlagSet("namespace rollback", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	id := fs.String("id", "", "committed migration id")
	dryRun := fs.Bool("dry-run", false, "inspect rollback without writing")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned rollback envelope")
	fs.Usage = printNamespaceUsage
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	project, err := namespaceMigrationProjectFlag(*projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	result, err := rollbackNamespaceMigration(project, strings.TrimSpace(*id), *dryRun)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("namespace_migration_rollback", result)
	}
	return renderNamespaceMigrationResult(result)
}

func namespaceMigrationProjectFlag(value string, explicit bool) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return resolveProjectDirFlag(cwd, value, explicit)
}

func parseNamespaceMigrationSelector(project, raw, flagName string) (squadnamespace.Ref, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return squadnamespace.Ref{}, usageErrorf("namespace migrate requires %s PROFILE/SESSION", flagName)
	}
	if strings.Count(raw, "/") != 1 {
		return squadnamespace.Ref{}, usageErrorf("%s must be exactly PROFILE/SESSION", flagName)
	}
	profile, session, _ := strings.Cut(raw, "/")
	profile, session = strings.TrimSpace(profile), strings.TrimSpace(session)
	if profile == "" || session == "" {
		return squadnamespace.Ref{}, usageErrorf("%s must contain non-empty PROFILE and SESSION", flagName)
	}
	if err := team.ValidateProfileName(profile); err != nil {
		return squadnamespace.Ref{}, usageErrorf("%s: %v", flagName, err)
	}
	if err := team.ValidateSessionName(session); err != nil {
		return squadnamespace.Ref{}, usageErrorf("%s: %v", flagName, err)
	}
	return squadnamespace.Resolve(filepath.Clean(project), profile, session), nil
}

func renderNamespaceMigrationPlan(plan namespaceMigrationPlan, jsonOut bool) error {
	if jsonOut {
		return printJSONEnvelope("namespace_migration_plan", plan)
	}
	fmt.Printf("Namespace migration plan %s: %s -> %s\n", plan.ID, plan.Source.ID, plan.Target.ID)
	fmt.Printf("Artifacts: %d migratable, %d historical references\n", len(plan.Artifacts), len(plan.History))
	for _, artifact := range plan.Artifacts {
		fmt.Printf("- %s: %s (%d bytes, %s)\n", artifact.Name, artifact.Policy, artifact.Bytes, artifact.SHA256)
	}
	if len(plan.Blockers) > 0 {
		fmt.Println("Blocked:")
		for _, blocker := range plan.Blockers {
			fmt.Println("- " + blocker)
		}
		return nil
	}
	fmt.Printf("Ready. Recovery command: %s\n", plan.Recovery)
	return nil
}

type namespaceMigrationResult struct {
	ID       string                  `json:"id"`
	Status   string                  `json:"status"`
	Phase    namespaceMigrationPhase `json:"phase"`
	Source   squadnamespace.Ref      `json:"source"`
	Target   squadnamespace.Ref      `json:"target"`
	Manifest string                  `json:"manifest"`
	Backup   string                  `json:"backup,omitempty"`
	Recovery string                  `json:"recovery_command,omitempty"`
	Rollback string                  `json:"rollback_command,omitempty"`
	DryRun   bool                    `json:"dry_run"`
	Detail   string                  `json:"detail,omitempty"`
}

func renderNamespaceMigrationResult(result namespaceMigrationResult) error {
	fmt.Printf("Namespace migration %s: %s (%s)\n", result.ID, result.Status, result.Phase)
	if result.Detail != "" {
		fmt.Println(result.Detail)
	}
	if result.Manifest != "" {
		fmt.Println("Manifest: " + result.Manifest)
	}
	if result.Backup != "" {
		fmt.Println("Retained backup: " + result.Backup)
	}
	if result.Recovery != "" && result.Status != "committed" {
		fmt.Println("Recovery: " + result.Recovery)
	}
	if result.Rollback != "" {
		fmt.Println("Rollback: " + result.Rollback)
	}
	return nil
}
