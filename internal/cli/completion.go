package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// runCompletion emits a static shell completion script for amq-squad.
// Supported shells: bash, zsh, fish. The script goes to stdout only;
// usage/help/errors stay on stderr to match the rest of the CLI.
func runCompletion(args []string) error {
	fs := flag.NewFlagSet("completion", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad completion - emit a shell completion script

Usage:
  amq-squad completion bash
  amq-squad completion zsh
  amq-squad completion fish

Pipe the output into the appropriate location for your shell, e.g.:
  amq-squad completion bash > /etc/bash_completion.d/amq-squad
  amq-squad completion zsh  > "${fpath[1]}/_amq-squad"
  amq-squad completion fish > ~/.config/fish/completions/amq-squad.fish

Examples:
  amq-squad completion bash
  amq-squad completion zsh > "${fpath[1]}/_amq-squad" && compinit
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return usageErrorf("completion requires a shell: bash, zsh, or fish")
	}
	if fs.NArg() > 1 {
		return usageErrorf("completion takes exactly one shell argument; got %d", fs.NArg())
	}
	switch fs.Arg(0) {
	case "bash":
		fmt.Print(bashCompletionScript)
	case "zsh":
		fmt.Print(zshCompletionScript)
	case "fish":
		fmt.Print(fishCompletionScript)
	default:
		return usageErrorf("unsupported shell %q: use bash, zsh, or fish", fs.Arg(0))
	}
	return nil
}

// completionTopCommands lists the top-level public verbs included in the
// generated completion scripts. Kept in sync with cli.go dispatch by the
// completion tests, which assert representative entries appear in each
// shell's output.
var completionTopCommands = commandNames("")

// completionNewSubcommands lists the `amq-squad new` subcommands.
var completionNewSubcommands = []string{
	"team",
	"profile",
	"session",
}

// completionAgentSubcommands lists the `amq-squad agent` subcommands.
var completionAgentSubcommands = []string{
	"up",
	"resume",
}

var completionBootstrapSubcommands = []string{"ack"}

var completionContextSubcommands = []string{"explain"}

var completionNamespaceSubcommands = []string{"migrate", "recover", "rollback"}

// completionGlobalSubcommands lists the `amq-squad global` subcommands.
var completionGlobalSubcommands = []string{
	"start",
}

// completionRunSubcommands lists the `amq-squad run` subcommands.
var completionRunSubcommands = []string{
	"start",
}

var completionReviewWorktreeSubcommands = []string{
	"create",
	"exec",
	"shell",
	"remove",
}

var completionTmuxHarnessSubcommands = []string{
	"exec",
	"shell",
}

// completionTeamSubcommands lists the `amq-squad team` subcommands.
var completionTeamSubcommands = []string{
	"init",
	"resume",
	"rules",
	"lead",
	"overlay",
	"member",
	"sync",
	"profiles",
	"rm",
	"delete",
}

// completionLeadSubcommands lists the top-level `amq-squad lead` subcommands.
var completionLeadSubcommands = []string{
	"register",
}

// completionGoalSubcommands lists the top-level `amq-squad goal` subcommands.
var completionGoalSubcommands = []string{
	"apply",
	"claim",
	"deliver",
	"draft",
	"retry-attempt",
	"start",
}

// completionOperatorSubcommands lists the `amq-squad operator` subcommands.
var completionOperatorSubcommands = []string{
	"answer",
	"directive",
	"poll",
	"status",
	"watch",
}

// completionNotificationsSubcommands lists the notification observability
// views. events and history are documented aliases.
var completionNotificationsSubcommands = []string{
	"doctor",
	"probe",
	"events",
	"history",
}

// completionTeamRulesSubcommands lists `amq-squad team rules` subcommands.
var completionTeamRulesSubcommands = []string{
	"init",
	"show",
	"templates",
}

// completionTeamLeadSubcommands lists `amq-squad team lead` subcommands.
var completionTeamLeadSubcommands = []string{
	"set",
	"clear",
	"show",
}

// completionTeamMemberSubcommands lists `amq-squad team member` subcommands.
var completionTeamMemberSubcommands = []string{
	"add",
	"list",
	"ls",
	"rm",
	"remove",
}

// completionTaskSubcommands lists `amq-squad task` subcommands.
var completionTaskSubcommands = []string{
	"add",
	"list",
	"show",
	"claim",
	"renew",
	"done",
	"fail",
	"block",
	"reset",
	"cancel",
	"release",
	"deliver",
	"retry-delivery",
	"reconcile",
}

var completionReceiptSubcommands = []string{"show"}

// completionVerifySubcommands lists the `amq-squad verify` subcommands.
var completionVerifySubcommands = []string{
	"merge",
	"release",
}

// completionCommonFlags lists every flag registered by the current stdlib
// CLI surfaces. The list is exhaustive on purpose: shell completion
// shouldn't omit a flag just because it only appears on one command. Drift
// guard: `TestCompletionFlagsCoverDispatcher` walks the source tree and
// fails if a `flag.NewFlagSet` declares a flag missing here.
var completionCommonFlags = []string{
	"--accessible",
	// Root flags (also offered as first-word completions when the user
	// starts typing `-...` before picking a subcommand).
	"--help",
	"-h",
	"--version",
	"-v",
	"-p",
	"-s",
	"-P",
	"--quiet",
	"--verbose",
	"--workers",
	"--color",
	"-y",
	// Subcommand flags, sorted alphabetically.
	"--action",
	"--action-id",
	"--attempt-id",
	"--actions",
	"--agent",
	"--all",
	"--all-profiles",
	"--allow",
	"--allow-outside",
	"--allowed-role-classes",
	"--allowed-roles",
	"--adopt-project-lead",
	"--apply",
	"--approved",
	"--as",
	"--at-risk-wait",
	"--assign",
	"--base-root",
	"--binary",
	"--body",
	"--body-file",
	"--budget-turns",
	"--claude-args",
	"--close-panes",
	"--codex-args",
	"--codex-only",
	"--commands",
	"--composition",
	"--compat-no-wake",
	"--confirm-not-delivered",
	"--conversation",
	"--conversation-id",
	"--control-root",
	"--create-task",
	"--cwd",
	"--denied",
	"--depends-on",
	"--deliver",
	"--depth",
	"--desc",
	"--detail",
	"--disable-all-hooks",
	"--disable-plugins",
	"--dispatch-next",
	"--dry-run",
	"--effort",
	"--evidence",
	"--external-lead",
	"--exec",
	"--feature-prefix",
	"--filter",
	"--final-head",
	"--force",
	"--force-duplicate",
	"--force-resend",
	"--fresh",
	"--from",
	"--gate",
	"--go",
	"--goal",
	"--handled-issue",
	"--goal-id",
	"--handle",
	"--heartbeat",
	"--hide-stale",
	"--id",
	"--include-body",
	"--idle-reap-minutes",
	"--interval",
	"--interactive",
	"--intent",
	"--json",
	"--keep-panes",
	"--kind",
	"--launcher",
	"--launcher-args",
	"--layout",
	"--layout-preset",
	"--lead",
	"--lead-mode",
	"--launcher-pane",
	"--launch",
	"--lease",
	"--limit",
	"--me",
	"--name",
	"--model",
	"--msg-id",
	"--namespace-reason",
	"--milestone",
	"--mode",
	"--max-agents",
	"--max-ticks",
	"--max-total-spawns",
	"--mutating",
	"--no-attach",
	"--no-bell",
	"--no-bootstrap",
	"--no-default-args",
	"--no-gitignore",
	"--no-notify",
	"--no-operator",
	"--no-preauthorize-inscope",
	"--no-require-wake",
	"--no-redeliver-goal-prompt",
	"--no-wake",
	"--numbered",
	"--once",
	"--older-than",
	"--operator",
	"--operator-mode",
	"--operator-notifications",
	"--orchestrated",
	"--override-boundary",
	"--override-dependencies",
	"--override-namespace-conflict",
	"--owner",
	"--owner-id",
	"--owner-token",
	"--personas",
	"--phase",
	"--priority",
	"--profile",
	"--project",
	"--reason",
	"--readonly",
	"--refresh",
	"--renotify-after",
	"--rescan",
	"--repo",
	"--require-wake",
	"--register-orchestrator",
	"--reset",
	"--restore-existing",
	"--redeliver-goal",
	"--resume-transition",
	"--restore-goal-binding",
	"--replacement",
	"--review-age",
	"--review-of",
	"--role",
	"--role-file",
	"--roles",
	"--root",
	"--route",
	"--run-action",
	"--seed-from",
	"--skill-version",
	"--self",
	"--self-operator-allow",
	"--self-operator-lead",
	"--session",
	"--sink",
	"--set",
	"--scope",
	"--skill-invocation",
	"--spawn-depth",
	"--spawn-origin",
	"--stagger",
	"--stage",
	"--stale-after",
	"--status",
	"--stop",
	"--stop-agents",
	"--steps",
	"--subject",
	"--sync",
	"--symphony",
	"--target",
	"--target-contract",
	"--target-project-root",
	"--target-id",
	"--target-project",
	"--target-session",
	"--task",
	"--template",
	"--team-home",
	"--team-profile",
	"--team-workstream",
	"--terminal",
	"--terminal-session",
	"--thread",
	"--title",
	"--tmp-older-than",
	"--to",
	"--tree",
	"--trust",
	"--timeout",
	"--ttl",
	"--unsafe-send-as",
	"--visibility",
	"--wizard-ui",
	"--wait-for",
	"--wait-timeout",
	"--wake",
	"--wake-inject-arg",
	"--wake-inject-mode",
	"--wake-inject-via",
	"--yes",
}

var (
	bashCompletionScript = buildBashCompletionScript()
	zshCompletionScript  = buildZshCompletionScript()
	fishCompletionScript = buildFishCompletionScript()
)

func buildBashCompletionScript() string {
	var b strings.Builder
	b.WriteString("# amq-squad bash completion\n")
	b.WriteString("# Generated by `amq-squad completion bash`. Source this file or place it\n")
	b.WriteString("# under /etc/bash_completion.d/.\n\n")
	b.WriteString("_amq_squad_complete() {\n")
	b.WriteString("    local cur prev words cword\n")
	b.WriteString("    if declare -F _get_comp_words_by_ref >/dev/null; then\n")
	b.WriteString("        _get_comp_words_by_ref -n =: cur prev words cword\n")
	b.WriteString("    else\n")
	b.WriteString("        cur=\"${COMP_WORDS[COMP_CWORD]}\"\n")
	b.WriteString("        prev=\"${COMP_WORDS[COMP_CWORD-1]}\"\n")
	b.WriteString("        words=(\"${COMP_WORDS[@]}\")\n")
	b.WriteString("        cword=$COMP_CWORD\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    local top_commands=\"")
	b.WriteString(strings.Join(completionTopCommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local team_subcommands=\"")
	b.WriteString(strings.Join(completionTeamSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local goal_subcommands=\"")
	b.WriteString(strings.Join(completionGoalSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local bootstrap_subcommands=\"")
	b.WriteString(strings.Join(completionBootstrapSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local context_subcommands=\"")
	b.WriteString(strings.Join(completionContextSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local namespace_subcommands=\"")
	b.WriteString(strings.Join(completionNamespaceSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local operator_subcommands=\"")
	b.WriteString(strings.Join(completionOperatorSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local notifications_subcommands=\"")
	b.WriteString(strings.Join(completionNotificationsSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local review_worktree_subcommands=\"")
	b.WriteString(strings.Join(completionReviewWorktreeSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local tmux_harness_subcommands=\"")
	b.WriteString(strings.Join(completionTmuxHarnessSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local new_subcommands=\"")
	b.WriteString(strings.Join(completionNewSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local receipt_subcommands=\"")
	b.WriteString(strings.Join(completionReceiptSubcommands, " "))
	b.WriteString("\"\n")
	b.WriteString("    local common_flags=\"")
	b.WriteString(strings.Join(completionCommonFlags, " "))
	b.WriteString("\"\n\n")
	b.WriteString("    if [ \"$cword\" -eq 1 ]; then\n")
	b.WriteString("        if [[ \"$cur\" == -* ]]; then\n")
	b.WriteString("            COMPREPLY=( $(compgen -W \"$common_flags\" -- \"$cur\") )\n")
	b.WriteString("        else\n")
	b.WriteString("            COMPREPLY=( $(compgen -W \"$top_commands\" -- \"$cur\") )\n")
	b.WriteString("        fi\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"new\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$new_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"goal\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$goal_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"receipt\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$receipt_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"bootstrap\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$bootstrap_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"context\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$context_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"namespace\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$namespace_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"operator\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$operator_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"notifications\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$notifications_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"review-worktree\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$review_worktree_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"tmux-harness\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$tmux_harness_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"team\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$team_subcommands\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"team\" ] && [ \"${words[2]}\" = \"rules\" ] && [ \"$cword\" -eq 3 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionTeamRulesSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"team\" ] && [ \"${words[2]}\" = \"lead\" ] && [ \"$cword\" -eq 3 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionTeamLeadSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"team\" ] && [ \"${words[2]}\" = \"member\" ] && [ \"$cword\" -eq 3 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionTeamMemberSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"task\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionTaskSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"verify\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionVerifySubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"agent\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionAgentSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"lead\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionLeadSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"global\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionGlobalSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [ \"${words[1]}\" = \"run\" ] && [ \"$cword\" -eq 2 ]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"")
	b.WriteString(strings.Join(completionRunSubcommands, " "))
	b.WriteString("\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"$cur\" == -* ]]; then\n")
	b.WriteString("        COMPREPLY=( $(compgen -W \"$common_flags\" -- \"$cur\") )\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n")
	b.WriteString("}\n\n")
	b.WriteString("complete -F _amq_squad_complete amq-squad\n")
	return b.String()
}

func buildZshCompletionScript() string {
	var b strings.Builder
	b.WriteString("#compdef amq-squad\n")
	b.WriteString("# amq-squad zsh completion\n")
	b.WriteString("# Generated by `amq-squad completion zsh`. Place under a directory in\n")
	b.WriteString("# your $fpath, e.g. \"${fpath[1]}/_amq-squad\", then `compinit`.\n\n")
	b.WriteString("_amq_squad() {\n")
	b.WriteString("    local -a top_commands new_subcommands team_subcommands goal_subcommands receipt_subcommands bootstrap_subcommands context_subcommands namespace_subcommands operator_subcommands notifications_subcommands review_worktree_subcommands tmux_harness_subcommands common_flags\n")
	b.WriteString("    top_commands=(")
	for i, c := range completionTopCommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    receipt_subcommands=(")
	for i, c := range completionReceiptSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    bootstrap_subcommands=(")
	for i, c := range completionBootstrapSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    context_subcommands=(")
	for i, c := range completionContextSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    namespace_subcommands=(")
	for i, c := range completionNamespaceSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    new_subcommands=(")
	for i, c := range completionNewSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    team_subcommands=(")
	for i, c := range completionTeamSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    goal_subcommands=(")
	for i, c := range completionGoalSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    operator_subcommands=(")
	for i, c := range completionOperatorSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    notifications_subcommands=(")
	for i, c := range completionNotificationsSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    review_worktree_subcommands=(")
	for i, c := range completionReviewWorktreeSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    tmux_harness_subcommands=(")
	for i, c := range completionTmuxHarnessSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(c))
	}
	b.WriteString(")\n")
	b.WriteString("    common_flags=(")
	for i, f := range completionCommonFlags {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(f))
	}
	b.WriteString(")\n\n")
	b.WriteString("    if (( CURRENT == 2 )); then\n")
	b.WriteString("        if [[ \"${words[CURRENT]}\" == -* ]]; then\n")
	b.WriteString("            compadd -- \"${common_flags[@]}\"\n")
	b.WriteString("        else\n")
	b.WriteString("            compadd -- \"${top_commands[@]}\"\n")
	b.WriteString("        fi\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"new\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${new_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"goal\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${goal_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"receipt\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${receipt_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"bootstrap\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${bootstrap_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"context\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${context_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"namespace\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${namespace_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"operator\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${operator_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"notifications\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${notifications_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"review-worktree\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${review_worktree_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"tmux-harness\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${tmux_harness_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"team\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- \"${team_subcommands[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"team\" && \"${words[3]}\" == \"rules\" && CURRENT -eq 4 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionTeamRulesSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"team\" && \"${words[3]}\" == \"lead\" && CURRENT -eq 4 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionTeamLeadSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"team\" && \"${words[3]}\" == \"member\" && CURRENT -eq 4 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionTeamMemberSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"task\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionTaskSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"verify\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionVerifySubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"agent\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionAgentSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"lead\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionLeadSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"global\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionGlobalSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[2]}\" == \"run\" && CURRENT -eq 3 ]]; then\n")
	b.WriteString("        compadd -- ")
	for i, s := range completionRunSubcommands {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(zshQuote(s))
	}
	b.WriteString("\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n\n")
	b.WriteString("    if [[ \"${words[CURRENT]}\" == -* ]]; then\n")
	b.WriteString("        compadd -- \"${common_flags[@]}\"\n")
	b.WriteString("        return\n")
	b.WriteString("    fi\n")
	b.WriteString("}\n\n")
	b.WriteString("compdef _amq_squad amq-squad\n")
	return b.String()
}

func buildFishCompletionScript() string {
	var b strings.Builder
	b.WriteString("# amq-squad fish completion\n")
	b.WriteString("# Generated by `amq-squad completion fish`. Place under\n")
	b.WriteString("# ~/.config/fish/completions/amq-squad.fish.\n\n")
	b.WriteString("function __amq_squad_no_subcommand\n")
	b.WriteString("    set -l tokens (commandline -opc)\n")
	b.WriteString("    if test (count $tokens) -le 1\n")
	b.WriteString("        return 0\n")
	b.WriteString("    end\n")
	b.WriteString("    return 1\n")
	b.WriteString("end\n\n")
	for _, c := range completionTopCommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n __amq_squad_no_subcommand -a %s\n", fishQuote(c))
	}
	b.WriteString("\n")
	for _, sub := range completionNewSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from new\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionTeamSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from team\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionGoalSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from goal\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionReceiptSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from receipt\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionBootstrapSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from bootstrap\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionContextSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from context\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionNamespaceSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from namespace\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionOperatorSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from operator\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionNotificationsSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from notifications\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionReviewWorktreeSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from review-worktree\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionTmuxHarnessSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from tmux-harness\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionTeamRulesSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from rules\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionTeamLeadSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from team; and __fish_seen_subcommand_from lead\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionTeamMemberSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from member\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionTaskSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from task\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionVerifySubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from verify\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionAgentSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from agent\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionLeadSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from lead; and not __fish_seen_subcommand_from team\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionGlobalSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from global\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, sub := range completionRunSubcommands {
		fmt.Fprintf(&b, "complete -c amq-squad -n \"__fish_seen_subcommand_from run\" -a %s\n", fishQuote(sub))
	}
	b.WriteString("\n")
	for _, f := range completionCommonFlags {
		switch {
		case strings.HasPrefix(f, "--"):
			fmt.Fprintf(&b, "complete -c amq-squad -l %s\n", fishQuote(strings.TrimPrefix(f, "--")))
		case strings.HasPrefix(f, "-") && len(f) == 2:
			fmt.Fprintf(&b, "complete -c amq-squad -s %s\n", fishQuote(strings.TrimPrefix(f, "-")))
		}
	}
	return b.String()
}

// zshQuote wraps a value in single quotes for the zsh completion script.
// The inputs are static command/flag names and contain no characters that
// need escaping beyond surrounding the value in quotes.
func zshQuote(s string) string {
	return "'" + s + "'"
}

// fishQuote single-quotes a value for fish.
func fishQuote(s string) string {
	return "'" + s + "'"
}
