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
	{Name: "team", Summary: "Set up and manage the team (init, rules, lead, member, sync, profiles)"},
	{Name: "lead", Summary: "Register or inspect an external orchestrator lead"},
	{Name: "goal", Summary: "Draft or apply a preview-first goal setup plan"},
	{Name: "task", Summary: "Native pull-based task store (add/list/claim/done/fail/block)"},
	{Name: "verify", Summary: "Deterministic preflight checks (verify merge)"},
	{Name: "operator", Summary: "Show operator inbox and polling-loop status"},
	{Name: "up", Summary: "Bring the team up (use --dry-run to print the launch plan)"},
	{Name: "stop", Summary: "Stop configured team members (SIGTERM; --force = SIGKILL)"},
	{Name: "brief", Summary: "Print a workstream brief and classify it as none, stub, or real"},
	{Name: "threads", Summary: "List collapsed AMQ thread summaries for one workstream"},
	{Name: "thread", Summary: "Read one AMQ thread transcript by project and session"},
	{Name: "status", Summary: "Multi-session board (also bare 'amq-squad'); --project and --session for detail"},
	{Name: "focus", Summary: "Bring a team session or agent pane into view"},
	{Name: "open", Summary: "Alias for focus"},
	{Name: "send", Summary: "Deliver a prompt to an agent's tmux pane"},
	{Name: "dispatch", Summary: "Queue a durable task for a child and wake it to drain"},
	{Name: "collect", Summary: "Drain once, optionally wait once for a report, then drain once"},
	{Name: "prune-panes", Summary: "Reclaim orphaned amq-squad tmux panes (confirm-gated)"},
	{Name: "console", Summary: "Read-only Mission Control TUI over all sessions (--once for CI)"},
	{Name: "monitor", Summary: "No-wake polling loop: surface operator-needed events (gates, blockers, merge-ready, inbox)"},
	{Name: "notify", Summary: "Emit de-duplicated operator attention notifications"},
	{Name: "amq", Summary: "Project-aware AMQ diagnostics and confirm-gated maintenance"},
	{Name: "history", Summary: "List restorable launch records"},
	{Name: "resume", Summary: "Plan how to bring the team back into the resolved workstream"},
	{Name: "fork", Summary: "Plan fresh launches in a new workstream branched off an existing one"},
	{Name: "rm", Summary: "Permanently remove a finished session (root dir + brief; confirm-gated)"},
	{Name: "archive", Summary: "Move a finished session aside instead of deleting (confirm-gated)"},
	{Name: "completion", Summary: "Emit a shell completion script (bash, zsh, fish)"},
	{Name: "next", Summary: "Get the highest-priority operator action for this session"},
	{Name: "doctor", Summary: "Check amq-squad / AMQ setup (use --project and --profile for other teams)"},
	{Name: "agent", Summary: "Launch or resume a single agent (agent up / agent resume)"},
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
		{Name: "team", Summary: commandSummary("team"), Run: runTeam},
		{Name: "lead", Summary: commandSummary("lead"), Run: runLead},
		{Name: "goal", Summary: commandSummary("goal"), Run: func(args []string) error { return runGoalWithVersion(args, version) }},
		{Name: "task", Summary: commandSummary("task"), Run: runTask},
		{Name: "verify", Summary: commandSummary("verify"), Run: runVerify},
		{Name: "operator", Summary: commandSummary("operator"), Run: runOperator},
		{Name: "up", Summary: commandSummary("up"), Run: runUp},
		{Name: "stop", Summary: commandSummary("stop"), Run: runStop},
		{Name: "brief", Summary: commandSummary("brief"), Run: runBrief},
		{Name: "threads", Summary: commandSummary("threads"), Run: runThreads},
		{Name: "thread", Summary: commandSummary("thread"), Run: runThread},
		{Name: "status", Summary: commandSummary("status"), Run: func(args []string) error { return runStatusWithVersion(args, version) }},
		{Name: "focus", Summary: commandSummary("focus"), Run: runFocus},
		{Name: "open", Summary: commandSummary("open"), Run: runFocus},
		{Name: "send", Summary: commandSummary("send"), Run: runSend},
		{Name: "dispatch", Summary: commandSummary("dispatch"), Run: runDispatch},
		{Name: "collect", Summary: commandSummary("collect"), Run: runCollect},
		{Name: "prune-panes", Summary: commandSummary("prune-panes"), Run: runPrunePanes},
		{Name: "console", Summary: commandSummary("console"), Run: runConsole},
		{Name: "monitor", Summary: commandSummary("monitor"), Run: runMonitor},
		{Name: "notify", Summary: commandSummary("notify"), Run: runNotify},
		{Name: "amq", Summary: commandSummary("amq"), Run: runAMQ},
		{Name: "history", Summary: commandSummary("history"), Run: runHistory},
		{Name: "resume", Summary: commandSummary("resume"), Run: runResume},
		{Name: "fork", Summary: commandSummary("fork"), Run: runFork},
		{Name: "rm", Summary: commandSummary("rm"), Run: func(args []string) error { return runRm(args, rmModeDelete) }},
		{Name: "archive", Summary: commandSummary("archive"), Run: func(args []string) error { return runRm(args, rmModeArchive) }},
		{Name: "completion", Summary: commandSummary("completion"), Run: runCompletion},
		{Name: "next", Summary: commandSummary("next"), Run: runNext},
		{Name: "doctor", Summary: commandSummary("doctor"), Run: func(args []string) error { return runDoctor(args, version) }},
		{Name: "agent", Summary: commandSummary("agent"), Run: runAgent},
	}
}

func lookupCommand(name, version string) (commandMeta, bool) {
	if name == claudeRenameHelperCommand {
		return commandMeta{Name: name, Run: runClaudeSessionRename}, true
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
