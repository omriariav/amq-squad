package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/internal/catalog"
	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/role"
)

func runLaunch(args []string) error {
	// Split at "--" so launcher flags aren't consumed by amq-squad's parser.
	squadArgs, childArgs := splitDashDash(args)

	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	roleFlag := fs.String("role", "", "role label for this agent (e.g. cpo, cto, dev, qa)")
	session := fs.String("session", "", "AMQ session name (passed through to coop exec)")
	me := fs.String("me", "", "override the agent handle (defaults to binary basename)")
	rootFlag := fs.String("root", "", "override AMQ root directory")
	dryRun := fs.Bool("dry-run", false, "print the coop exec command without executing")

	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad launch - launch an agent with role metadata

Usage:
  amq-squad launch [options] <binary> [-- <binary-flags>]

Options:
`)
		fs.PrintDefaults()
		fmt.Fprint(os.Stderr, `
The <binary> is the agent launcher (claude, codex, etc). Flags after "--"
are passed through to that binary via 'amq coop exec'.

Side effects before exec:
  1. Resolves AMQ root via 'amq env --json' for the target session.
  2. Writes <root>/agents/<handle>/launch.json with cwd, binary, argv, role.
  3. Writes a role.md stub if one does not already exist.
  4. Execs 'amq coop exec --session <session> <binary> -- <binary-flags>'.
`)
	}

	if err := fs.Parse(squadArgs); err != nil {
		return err
	}
	remaining := fs.Args()
	if len(remaining) == 0 {
		return usageErrorf("launch requires a binary (e.g. 'amq-squad launch --role cpo codex')")
	}
	binary := remaining[0]
	// Positional args before "--" get folded into childArgs.
	if len(remaining) > 1 {
		childArgs = append(remaining[1:], childArgs...)
	}

	handle := *me
	if handle == "" {
		handle = strings.ToLower(filepath.Base(binary))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	// Resolve the AMQ root via the amq CLI. This respects .amqrc, --session,
	// and --root exactly as coop exec will, so launch.json and the actual
	// mailbox agree.
	root, err := resolveAMQRoot(*rootFlag, *session, handle)
	if err != nil {
		return fmt.Errorf("resolve amq root: %w", err)
	}

	agentDir := filepath.Join(root, "agents", handle)
	rec := launch.Record{
		CWD:       cwd,
		Binary:    binary,
		Argv:      childArgs,
		Session:   *session,
		Handle:    handle,
		Role:      *roleFlag,
		Root:      root,
		StartedAt: time.Now().UTC(),
	}
	if err := launch.Write(agentDir, rec); err != nil {
		return fmt.Errorf("write launch record: %w", err)
	}

	// Seed role.md from the catalog when the role is known. Never
	// overwrites existing user edits.
	if *roleFlag != "" {
		if err := seedRoleStub(agentDir, *roleFlag); err != nil {
			fmt.Fprintf(os.Stderr, "warning: seed role.md: %v\n", err)
		}
	}

	// Build the coop exec invocation.
	coopArgs := []string{"coop", "exec"}
	if *session != "" {
		coopArgs = append(coopArgs, "--session", *session)
	}
	if *rootFlag != "" {
		coopArgs = append(coopArgs, "--root", *rootFlag)
	}
	if *me != "" {
		coopArgs = append(coopArgs, "--me", *me)
	}
	coopArgs = append(coopArgs, binary)
	if len(childArgs) > 0 {
		coopArgs = append(coopArgs, "--")
		coopArgs = append(coopArgs, childArgs...)
	}

	if *dryRun {
		fmt.Println("amq", strings.Join(coopArgs, " "))
		fmt.Fprintln(os.Stderr, "(dry run - launch.json written, not execing)")
		return nil
	}

	amqBin, err := exec.LookPath("amq")
	if err != nil {
		return fmt.Errorf("amq not found in PATH: %w", err)
	}
	return syscall.Exec(amqBin, append([]string{"amq"}, coopArgs...), os.Environ())
}

// resolveAMQRoot shells out to `amq env --json` to discover the final root
// path that coop exec will use. This keeps amq-squad out of the root
// resolution business - amq owns it, we just ask.
func resolveAMQRoot(rootFlag, session, handle string) (string, error) {
	args := []string{"env", "--json", "--me", handle}
	if rootFlag != "" {
		args = append(args, "--root", rootFlag)
	}
	if session != "" {
		args = append(args, "--session", session)
	}
	cmd := exec.Command("amq", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("amq env: %w", err)
	}
	var parsed struct {
		Root string `json:"root"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", fmt.Errorf("parse amq env output: %w", err)
	}
	if parsed.Root == "" {
		return "", fmt.Errorf("amq env returned empty root")
	}
	return parsed.Root, nil
}

// seedRoleStub writes a role.md stub for the given agent directory based on
// the catalog entry for roleID. If the role isn't in the catalog, it still
// writes a minimal stub with the label = roleID.
func seedRoleStub(agentDir, roleID string) error {
	stub := role.Stub{RoleID: roleID, Label: roleID}
	if r := catalog.Lookup(roleID); r != nil {
		stub.Label = r.Label
		stub.Description = r.Description
		stub.Skills = r.Skills
		stub.Peers = r.DefaultPeers
	}
	_, err := role.EnsureStub(agentDir, stub)
	return err
}

// splitDashDash splits argv at the first "--" separator.
func splitDashDash(args []string) ([]string, []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}
