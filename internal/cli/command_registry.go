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
	{Name: "team", Summary: "Propose, apply and show the squad (propose, apply, show, check, draft-rules)"},
	{Name: "task", Summary: "Task store with leases and pools (add, claim, done, list, reconcile)"},
	{Name: "worktree", Summary: "Plan, materialize, inspect, hand off, and safely clean worker worktrees"},
	{Name: "evidence", Summary: "Run and inspect immutable task-scoped command evidence; ingest hook payloads"},
	{Name: "context", Summary: "Hook verb: the seat's role, rules digest, current task and open gates"},
	{Name: "guard", Summary: "Hook verb: one verdict (allow, needs_gate, deny) for one tool call"},
	{Name: "seat-exec", Summary: "Seat wrapper run by AMQ's launch API: verify, set identity and role, exec the provider"},
	{Name: "seat", Summary: "Harness steps for one seat: the commit step on a done report (seat commit)"},
	{Name: "scheduler", Summary: "The squad's scheduler process (scheduler run)"},
	{Name: "verify", Summary: "Deterministic preflight checks (action, signed authorization, merge, release)"},
	{Name: "merge-train", Summary: "Land the lead's ordered PR set under the merge grant (checks, digest, audit log)"},
	{Name: "release", Summary: "The release gate flow: prepare the tag and release gates, publish after the owner's answers"},
	{Name: "gate", Summary: "Manage durable typed authorization requests (raise and close)"},
	{Name: "operator", Summary: "Inspect or act as the configured operator participant"},
	{Name: "status", Summary: "Show the team's live state in one workstream (--project, --profile, --session)"},
	{Name: "fleet", Summary: "The fleet registry ~/.amq-squad/fleet.yaml (list, add, remove, operator)"},
	{Name: "routing", Summary: "Routing table report: per class, tier and size band, one proposed change per bucket (report --week)"},
	{Name: "dispatch", Summary: "Queue a durable task for a child"},
	{Name: "amq", Summary: "Show the resolved AMQ context and explain routes (env, route)"},
	{Name: "doctor", Summary: "Probe the host: tools, versions, logins, registry, local models, trust and owner config"},
	{Name: "trust", Summary: "Print the owner-run steps to trust a repository in codex-cli (writes nothing)"},
	{Name: "migrate", Summary: "Convert a v2 profile to squad.yaml once (dry run; --write writes, nothing is deleted)"},
	{Name: "up", Summary: "Start this repository's squad from squad.yaml: install the tagged pi build, doctor, session, the lead in tmux, the roster"},
	{Name: "lead", Summary: "Start the lead on the built-in pi (solo in this directory, or the team lead)"},
	{Name: "pi", Summary: "The built-in pi runtime (run a pi seat, verify the unpacked runtime)"},
	{Name: "doctor", Summary: "Check amq-squad / AMQ setup (use --project and --profile for other teams)"},
	{Name: "remote", Summary: "Buzz attachment: bind the enrolled share, attach it to a mailbox, show presence"},
	{Name: "uninstall", Summary: "Remove what amq-squad put on this host (dry run by default; --write, --owner-files, --purge)"},
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
		{Name: "seat-exec", Summary: commandSummary("seat-exec"), Run: runSeatExec},
		{Name: "seat", Summary: commandSummary("seat"), Run: runSeat},
		{Name: "scheduler", Summary: commandSummary("scheduler"), Run: runScheduler},
		{Name: "verify", Summary: commandSummary("verify"), Run: runVerify},
		{Name: "merge-train", Summary: commandSummary("merge-train"), Run: runMergeTrain},
		{Name: "release", Summary: commandSummary("release"), Run: runRelease},
		{Name: "gate", Summary: commandSummary("gate"), Run: runGate},
		{Name: "operator", Summary: commandSummary("operator"), Run: runOperator},
		{Name: "status", Summary: commandSummary("status"), Run: func(args []string) error { return runStatusWithVersion(args, version) }},
		{Name: "fleet", Summary: commandSummary("fleet"), Run: runFleet},
		{Name: "routing", Summary: commandSummary("routing"), Run: runRouting},
		{Name: "dispatch", Summary: commandSummary("dispatch"), Run: runDispatch},
		{Name: "amq", Summary: commandSummary("amq"), Run: runAMQ},
		{Name: "doctor", Summary: commandSummary("doctor"), Run: func(args []string) error { return runDoctor(args, version) }},
		{Name: "trust", Summary: commandSummary("trust"), Run: runTrust},
		{Name: "migrate", Summary: commandSummary("migrate"), Run: runMigrate},
		{Name: "up", Summary: commandSummary("up"), Run: func(args []string) error { return runUp(args, version) }},
		{Name: "lead", Summary: commandSummary("lead"), Run: runLead},
		{Name: "pi", Summary: commandSummary("pi"), Run: runPi},
		{Name: "remote", Summary: commandSummary("remote"), Run: func(args []string) error { return runRemote(args, version) }},
		{Name: "uninstall", Summary: commandSummary("uninstall"), Run: runUninstall},
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
