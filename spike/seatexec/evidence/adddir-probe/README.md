# codex `--add-dir` probe for amq-squad upstream ask 5 (2026-10-22)

Question: does `codex exec --sandbox workspace-write --add-dir <git common dir>` let Codex commit inside a
linked git worktree whose `.git` dir lives outside the writable root?

Environment: codex-cli 0.163.0 (/opt/homebrew/bin/codex), model gpt-6-astra, provider openai, reasoning medium,
approval never (via `-c approval_policy=never`), macOS Darwin 25.6.0 (seatbelt sandbox).
Fixture: ~/.amq-squad/spike/s4/project/adddir/{main-repo,wt-a}; wt-a/.git -> main-repo/.git/worktrees/wt-a;
`git rev-parse --git-common-dir` in wt-a = .../main-repo/.git. Fixture removed after runs.

Pre-run finding (A0-flagcheck.*): `--ask-for-approval never` is NOT accepted by `codex exec` on 0.163.0
("error: unexpected argument '--ask-for-approval' found"). `-c approval_policy=never` used instead.
`--add-dir <DIR>` IS a flag of `codex exec` on 0.163.0 (00-codex-exec-help.txt).

| run | cwd | flags | exit | commit landed | refusal (verbatim from Codex exec output) |
|---|---|---|---|---|---|
| A | wt-a | workspace-write, no add-dir | 0 | no | fatal: Unable to create '.../main-repo/.git/worktrees/wt-a/index.lock': Operation not permitted |
| B | wt-a | + --add-dir .../main-repo/.git | 0 | no | same as A (sandbox banner lists main-repo/.git as writable root) |
| C | wt-a | + --add-dir .../main-repo | 0 | no | model report: "sandbox permissions blocked creating .git/worktrees/wt-a/index.lock" (exec not rendered in plain output) |
| D | main-repo (plain checkout) | workspace-write, no add-dir | 0 | no | fatal: Unable to create '.../main-repo/.git/index.lock': Operation not permitted (D-json.stdout.txt, command_execution item) |

Exit code 0 in all runs = Codex completed its turn and reported the failure in prose; no approval was requested
in any run (approval policy never; no approval events in D-json).

Model-free check via `codex sandbox` was attempted (S-*.txt) but 0.163.0 requires `--permission-profile`
backed by a `[permissions]` table in config.toml, which this spike may not add. Not pursued.

Conclusion: `--add-dir` is honoured as a writable root (banner shows it) but Codex's workspace-write sandbox
still denies writes under any `.git` directory, including the shared git dir of a linked worktree and the
worktree's own private dir (`.git/worktrees/<name>`), and including a plain checkout's own `.git`. Ask 5 cannot
be "admit --add-dir"; the seat needs a different mechanism (e.g. danger-full-access for the commit step, an
exec policy / permissions profile that exempts the git dir, or committing outside the Codex seat).
