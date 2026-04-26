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
	teamHome := fs.String("team-home", "", "team-home directory used to find .amq-squad/team-rules.md for bootstrap")
	conversation := fs.String("conversation", "", "resume and store a Codex or Claude conversation name/id")
	conversationID := fs.String("conversation-id", "", "alias for --conversation")
	noBootstrap := fs.Bool("no-bootstrap", false, "do not pass the generated bootstrap prompt to the agent")
	noDefaultArgs := fs.Bool("no-default-args", false, "do not prepend Codex or Claude default permission args")
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
  4. Prepends Codex and Claude default permission args unless
     --no-default-args is set.
  5. Translates --conversation for Codex or Claude resume when provided.
  6. Adds a generated bootstrap prompt unless --no-bootstrap is set or
     non-default binary args were provided.
  7. Execs 'amq coop exec --session <session> <binary> -- <binary-flags>'.

With --dry-run, none of the above run: the resolved coop exec command is
printed and amq-squad exits. Disk state is untouched.
`)
	}

	if err := fs.Parse(squadArgs); err != nil {
		return err
	}
	conversationRef, err := conversationRefFromFlags(*conversation, *conversationID)
	if err != nil {
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
	if !*noDefaultArgs {
		childArgs = ensureDefaultChildArgs(binary, childArgs)
	}
	if conversationRef != "" {
		var err error
		childArgs, err = applyConversationRestoreArgs(binary, childArgs, conversationRef)
		if err != nil {
			return err
		}
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
		CWD:          cwd,
		Binary:       binary,
		Argv:         childArgs,
		Session:      *session,
		Conversation: conversationRef,
		Handle:       handle,
		Role:         *roleFlag,
		Root:         root,
		StartedAt:    time.Now().UTC(),
	}

	// Keep generated bootstrap out of launch.json so restore stays compact
	// and does not replay stale startup text.
	effectiveChildArgs := append([]string(nil), childArgs...)
	if !*noBootstrap && shouldAppendBootstrap(binary, childArgs) {
		prompt, err := buildBootstrapPrompt(bootstrapContextFor(rec, agentDir, *teamHome))
		if err != nil {
			return err
		}
		effectiveChildArgs = append(effectiveChildArgs, prompt)
	}

	// Build the coop exec invocation. Done before any disk writes so
	// --dry-run is a true preview with zero side effects.
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
	if len(effectiveChildArgs) > 0 {
		coopArgs = append(coopArgs, "--")
		coopArgs = append(coopArgs, effectiveChildArgs...)
	}

	if *dryRun {
		fmt.Println(shellCommand("amq", coopArgs...))
		fmt.Fprintln(os.Stderr, "(dry run - no files written, not execing)")
		return nil
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

	amqBin, err := exec.LookPath("amq")
	if err != nil {
		return fmt.Errorf("amq not found in PATH: %w", err)
	}
	return syscall.Exec(amqBin, append([]string{"amq"}, coopArgs...), os.Environ())
}

func conversationRefFromFlags(conversation, conversationID string) (string, error) {
	conversation = strings.TrimSpace(conversation)
	conversationID = strings.TrimSpace(conversationID)
	if conversation != "" && conversationID != "" && conversation != conversationID {
		return "", fmt.Errorf("use only one of --conversation or --conversation-id")
	}
	if conversation != "" {
		return conversation, nil
	}
	return conversationID, nil
}

func applyConversationRestoreArgs(binary string, childArgs []string, conversation string) ([]string, error) {
	conversation = strings.TrimSpace(conversation)
	if conversation == "" {
		return childArgs, nil
	}
	switch normalizedAgentBinary(binary) {
	case "codex":
		if ref, ok := codexResumeRef(childArgs); ok {
			if ref == "" {
				return nil, fmt.Errorf("--conversation cannot be combined with existing codex resume without a ref")
			}
			if ref != conversation {
				return nil, fmt.Errorf("--conversation %q conflicts with existing codex resume %q", conversation, ref)
			}
			return childArgs, nil
		}
		out := append([]string(nil), childArgs...)
		return append(out, "resume", conversation), nil
	case "claude":
		if ref, ok := claudeResumeRef(childArgs); ok {
			if ref == "" {
				return nil, fmt.Errorf("--conversation cannot be combined with existing claude resume or continue without a ref")
			}
			if ref != conversation {
				return nil, fmt.Errorf("--conversation %q conflicts with existing claude resume %q", conversation, ref)
			}
			return childArgs, nil
		}
		out := append([]string(nil), childArgs...)
		return append(out, "--resume", conversation), nil
	default:
		return nil, fmt.Errorf("--conversation is supported for codex and claude, got %q", binary)
	}
}

func stripConversationRestoreArgs(binary string, childArgs []string, conversation string) []string {
	conversation = strings.TrimSpace(conversation)
	if conversation == "" {
		return append([]string(nil), childArgs...)
	}
	switch normalizedAgentBinary(binary) {
	case "codex":
		return stripCodexResumeRef(childArgs, conversation)
	case "claude":
		return stripClaudeResumeRef(childArgs, conversation)
	default:
		return append([]string(nil), childArgs...)
	}
}

func normalizedAgentBinary(binary string) string {
	return strings.ToLower(filepath.Base(binary))
}

func codexResumeRef(args []string) (string, bool) {
	for i, arg := range args {
		if arg != "resume" {
			continue
		}
		if i+1 < len(args) {
			return args[i+1], true
		}
		return "", true
	}
	return "", false
}

func stripCodexResumeRef(args []string, conversation string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "resume" && i+1 < len(args) && args[i+1] == conversation {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}

func claudeResumeRef(args []string) (string, bool) {
	for i, arg := range args {
		switch {
		case arg == "--resume" || arg == "-r" || arg == "--session-id":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return "", true
		case strings.HasPrefix(arg, "--resume="):
			return strings.TrimPrefix(arg, "--resume="), true
		case strings.HasPrefix(arg, "--session-id="):
			return strings.TrimPrefix(arg, "--session-id="), true
		case arg == "--continue" || arg == "-c":
			return "", true
		}
	}
	return "", false
}

func stripClaudeResumeRef(args []string, conversation string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "--resume" || arg == "-r" || arg == "--session-id") && i+1 < len(args) && args[i+1] == conversation {
			i++
			continue
		}
		if arg == "--resume="+conversation || arg == "--session-id="+conversation {
			continue
		}
		out = append(out, arg)
	}
	return out
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
