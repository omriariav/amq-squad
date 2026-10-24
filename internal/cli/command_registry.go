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
	{Name: "roles", Summary: "List built-in role IDs and market numbers for team creation"},
	{Name: "role", Summary: "Draft and validate reusable custom role personas"},
	{Name: "brief", Summary: "Draft or seed the workstream brief (the only drafter on any launch-capable path)"},
	{Name: "team", Summary: "Set up and manage the team (init, rules, operator, sync)"},
	{Name: "task", Summary: "Atomic task lifecycle (add, claim, done, reconcile, handoff)"},
	{Name: "worktree", Summary: "Plan, materialize, inspect, hand off, and safely clean worker worktrees"},
	{Name: "evidence", Summary: "Run and inspect immutable task-scoped command evidence; ingest hook payloads"},
	{Name: "context", Summary: "Hook verb: the seat's role, rules digest, current task and open gates"},
	{Name: "guard", Summary: "Hook verb: one verdict (allow, needs_gate, deny) for one tool call"},
	{Name: "verify", Summary: "Deterministic preflight checks (action, signed authorization, merge, release)"},
	{Name: "gate", Summary: "Manage durable typed authorization requests (raise and close)"},
	{Name: "operator", Summary: "Inspect or act as the configured operator participant"},
	{Name: "status", Summary: "Show the team's live state in one workstream (--project, --profile, --session)"},
	{Name: "dispatch", Summary: "Queue a durable task for a child"},
	{Name: "amq", Summary: "Show the resolved AMQ context and explain routes (env, route)"},
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
		{Name: "roles", Summary: commandSummary("roles"), Run: runRoles},
		{Name: "role", Summary: commandSummary("role"), Run: runRole},
		{Name: "brief", Summary: commandSummary("brief"), Run: runBrief},
		{Name: "team", Summary: commandSummary("team"), Run: runTeam},
		{Name: "task", Summary: commandSummary("task"), Run: runTask},
		{Name: "worktree", Summary: commandSummary("worktree"), Run: runWorktree},
		{Name: "evidence", Summary: commandSummary("evidence"), Run: runEvidence},
		{Name: "context", Summary: commandSummary("context"), Run: runContext},
		{Name: "guard", Summary: commandSummary("guard"), Run: runGuard},
		{Name: "verify", Summary: commandSummary("verify"), Run: runVerify},
		{Name: "gate", Summary: commandSummary("gate"), Run: runGate},
		{Name: "operator", Summary: commandSummary("operator"), Run: runOperator},
		{Name: "status", Summary: commandSummary("status"), Run: func(args []string) error { return runStatusWithVersion(args, version) }},
		{Name: "dispatch", Summary: commandSummary("dispatch"), Run: runDispatch},
		{Name: "amq", Summary: commandSummary("amq"), Run: runAMQ},
		{Name: "doctor", Summary: commandSummary("doctor"), Run: func(args []string) error { return runDoctor(args, version) }},
	}
}

func lookupCommand(name, version string) (commandMeta, bool) {
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
