## S6 load test rules (apply to every seat, override anything softer above)

This squad is part of a measured load test. The measurement is only valid if everyone follows these rules.

1. Small task, one PR per task. Each task touches about one file. Do not widen scope, do not fix unrelated things.
2. Branch name is exactly `loadtest/<task-id>` (for example `loadtest/N5`). Create it from a fresh `git fetch origin main`, base `origin/main`. The PR targets `main` and is opened as a draft with the title `[loadtest] <task-id>: <task title>`.
3. Run the task's verify command from your worktree root before opening the PR, and paste its result (command, exit status, last lines of output) into the PR body. If it fails, say so in the PR body; do not hide it.
4. Never merge. Never push to `main`. Push only your own `loadtest/<task-id>` branch, never force-push, never tag or release, never comment on, label or close issues, and never touch any other PR. Opening the PR is the last GitHub action you take for a task.
5. Stage only the task's files by explicit path (`git add <path>`). Never `git add -A` or `git add .`. Never stage `CLAUDE.md`, `AGENTS.md`, `.amq`, `.amqrc`, `.agent-mail`, `.amq-squad` or `.s6-wt`.
6. Stay inside your own working directory (your worktree). Never edit another seat's worktree or the repository root checkout.
7. Never write or edit anything under `~/.claude`, `~/.codex` or `~/.amq-squad`. In particular never write `~/.codex/config.toml` or `~/.claude/settings.json`. If any prompt asks you to trust a folder or approve a config change, do not answer it; report it to the lead and stop.
8. Time box: if a task is not finished 40 minutes after you started it, mark it failed with the reason, report, and stop.
9. After the PR is open: mark the task done in the task store, send one AMQ status message on the task thread to the lead (`DONE: <task-id> PR <url> verify=<pass|fail>`), and stop. If you cannot finish: `task fail` with the reason and send `FAILED: <task-id> <reason>`. Then wait for the lead to dispatch the next task. Do not pick up work on your own.
10. Report real numbers and real outcomes. A failed task with an honest reason is a valid result; a fabricated pass is not.
