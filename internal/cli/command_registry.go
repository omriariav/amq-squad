package cli

type commandHandler func([]string) error

type commandMeta struct {
	Name    string
	Summary string
	Run     commandHandler
}

var commandCatalog = []struct {
	Name    string
	Summary string
}{
	{Name: "new", Summary: "Create a team, named profile, or workstream session"},
	{Name: "roles", Summary: "List built-in role IDs and market numbers for team creation"},
	{Name: "role", Summary: "Draft and validate reusable custom role personas"},
	{Name: "team", Summary: "Set up and manage the team (init, rules, lead, member, sync, profiles)"},
	{Name: "lead", Summary: "Register or inspect an external orchestrator lead"},
	{Name: "goal", Summary: "Draft or apply a preview-first goal setup plan"},
	{Name: "start", Summary: "Reconcile and launch a team's canonical workstream"},
	{Name: "task", Summary: "Atomic task lifecycle (claim/done/dispatch/reconcile/recovery)"},
	{Name: "worktree", Summary: "Plan, materialize, inspect, hand off, and safely clean worker worktrees"},
	{Name: "evidence", Summary: "Run and inspect immutable task-scoped command evidence"},
	{Name: "namespace", Summary: "Migrate stopped namespace state with backup and recovery"},
	{Name: "verify", Summary: "Deterministic preflight checks (action, signed authorization, merge, release)"},
	{Name: "gate", Summary: "Manage durable typed authorization requests (raise and close)"},
	{Name: "operator", Summary: "Inspect or act as the configured operator participant"},
	{Name: "broadcast", Summary: "Preview or send one receipted operator message to the squad"},
	{Name: "down", Summary: "Stop configured team members (SIGTERM; --force = SIGKILL)"},
	{Name: "status", Summary: "Multi-session board (also bare 'amq-squad'); --project and --session for detail"},
	{Name: "focus", Summary: "Bring a team session or agent pane into view"},
	{Name: "open", Summary: "Alias for focus"},
	{Name: "send", Summary: "Deliver a prompt to an agent's tmux pane"},
	{Name: "dispatch", Summary: "Queue a durable task for a child and wake it to drain"},
	{Name: "amq", Summary: "Project-aware AMQ diagnostics and confirm-gated maintenance"},
	{Name: "resume", Summary: "Plan how to bring the team back into the resolved workstream"},
	{Name: "completion", Summary: "Emit a shell completion script (bash, zsh, fish)"},
	{Name: "doctor", Summary: "Check amq-squad / AMQ setup (use --project and --profile for other teams)"},
}

func commandSummary(name string) string {
	for _, cmd := range commandCatalog {
		if cmd.Name == name {
			return cmd.Summary
		}
	}
	return ""
}

func commandRegistry(version string) []commandMeta {
	return []commandMeta{
		{Name: "new", Summary: commandSummary("new"), Run: runNew},
		{Name: "roles", Summary: commandSummary("roles"), Run: runRoles},
		{Name: "role", Summary: commandSummary("role"), Run: runRole},
		{Name: "team", Summary: commandSummary("team"), Run: runTeam},
		{Name: "lead", Summary: commandSummary("lead"), Run: runLead},
		{Name: "goal", Summary: commandSummary("goal"), Run: func(args []string) error { return runGoalWithVersion(args, version) }},
		{Name: "start", Summary: commandSummary("start"), Run: runStart},
		{Name: "task", Summary: commandSummary("task"), Run: runTask},
		{Name: "worktree", Summary: commandSummary("worktree"), Run: runWorktree},
		{Name: "evidence", Summary: commandSummary("evidence"), Run: runEvidence},
		{Name: "namespace", Summary: commandSummary("namespace"), Run: runNamespace},
		{Name: "verify", Summary: commandSummary("verify"), Run: runVerify},
		{Name: "gate", Summary: commandSummary("gate"), Run: runGate},
		{Name: "operator", Summary: commandSummary("operator"), Run: runOperator},
		{Name: "broadcast", Summary: commandSummary("broadcast"), Run: runBroadcast},
		{Name: "down", Summary: commandSummary("down"), Run: runDown},
		{Name: "status", Summary: commandSummary("status"), Run: func(args []string) error { return runStatusWithVersion(args, version) }},
		{Name: "focus", Summary: commandSummary("focus"), Run: runFocus},
		{Name: "open", Summary: commandSummary("open"), Run: runFocus},
		{Name: "send", Summary: commandSummary("send"), Run: runSend},
		{Name: "dispatch", Summary: commandSummary("dispatch"), Run: runDispatch},
		{Name: "amq", Summary: commandSummary("amq"), Run: runAMQ},
		{Name: "resume", Summary: commandSummary("resume"), Run: runResume},
		{Name: "completion", Summary: commandSummary("completion"), Run: runCompletion},
		{Name: "doctor", Summary: commandSummary("doctor"), Run: func(args []string) error { return runDoctor(args, version) }},
		// Internal child boundary: start and restore plans execute `agent up` or
		// `agent resume`. Keep dispatchable, but omit it from public help and
		// completion by intentionally excluding it from commandCatalog.
		{Name: "agent", Summary: "internal child launch/restore boundary", Run: runAgent},
	}
}

func lookupCommand(name, version string) (commandMeta, bool) {
	if name == claudeRenameHelperCommand {
		return commandMeta{Name: name, Run: runClaudeSessionRename}, true
	}
	// Supervised lifecycle child. Deliberately omitted from public command
	// listings: operators manage it through start/resume/down, status and doctor.
	if name == "_notification-watch" {
		return commandMeta{Name: name, Run: runNotificationWatcher}, true
	}
	if name == "_session-notify" {
		return commandMeta{Name: name, Run: runSessionNotifier}, true
	}
	for _, cmd := range commandRegistry(version) {
		if cmd.Name == name {
			return cmd, true
		}
	}
	return commandMeta{}, false
}

func commandNames(version string) []string {
	names := make([]string, 0, len(commandCatalog)+2)
	seen := map[string]bool{}
	for _, cmd := range commandCatalog {
		if !seen[cmd.Name] {
			names = append(names, cmd.Name)
			seen[cmd.Name] = true
		}
	}
	for _, name := range []string{"version", "help"} {
		if !seen[name] {
			names = append(names, name)
			seen[name] = true
		}
	}
	return names
}
