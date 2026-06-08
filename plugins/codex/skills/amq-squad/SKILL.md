---
name: amq-squad
description: Project-aware skill for live amq-squad team coordination after `.amq-squad/team.json` exists. Covers inbox drains, routing, review/handoff, status board/console/history, up/stop/resume/fork/rm/archive (down is a deprecated alias), agent up/resume, doctor, workstream briefs, and ACTIVE-EPIC startup. For first-time team design (personas, profile choice, team rules, pointer stubs, brief authoring, sync), prefer the companion `amq-team-setup` skill. Use raw `amq-cli` only for AMQ debugging outside the squad.
---

# amq-squad

Use this skill once a team is configured (`.amq-squad/team.json` exists) to run live coordination: drain inboxes, route handoffs, request reviews, bring members up and down, plan fresh forks, and check live state with `status`/`doctor`. For first-time setup work - choosing personas, writing team rules, syncing pointer stubs, authoring the workstream brief - switch to the companion `amq-team-setup` skill.

Launch priming is automatic. `up` / `agent up` inject the bootstrap prompt; agents do not paste it by hand.

This skill is named `amq-squad`; the binary is also named `amq-squad`.

## Context model

The context model has three durable layers. The skill never asks you to duplicate any of them into another file.

- **Team rules (`.amq-squad/team-rules.md`)** - the project's durable norms (skills, workflow, approvals, communication, escalation, style). Source of truth for every member.
- **Per-agent role (`<agent-dir>/role.md`)** - persona/system-prompt for one role. Seeded by `agent up` / `up`; the user can edit freely; later launches preserve user edits.
- **Workstream brief (`.amq-squad/briefs/<session>.md`)** - the active workstream's goal, scope, and pointers to source-of-truth issues/PRs. Lives at team-home so every member points at the same file. Created on first live `up`; preserved on reruns. `up --seed-from REF` writes a fresh brief from `file:<path>`, `issue:<n>`, or `gh:<owner>/<repo>#<n>`.

`CLAUDE.md` and `AGENTS.md` carry a small **pointer stub** that links to the three files above - never a copy of team-rules content. `amq-squad team sync --apply` writes/updates that stub.

If `.amq-squad/ACTIVE-EPIC.md` is present, read it at session start (transitional pointer to the current GitHub epic / milestone).

## Verbs you will use

The lifecycle is one small state machine: `(none) --up--> running --stop--> stopped --rm/archive--> (none)`, with `resume` returning a stopped session to running.

| Goal | Command |
| --- | --- |
| Bring the team up on NEW work (tmux) | `amq-squad up [<session>]` |
| Print the launch plan only | `amq-squad up --dry-run` |
| Seed the workstream brief from a deterministic source | `amq-squad up --dry-run --seed-from file:./brief.md` or `--seed-from issue:31` or `--seed-from gh:owner/repo#31` |
| Stop members (SIGTERM; state preserved, resumable) | `amq-squad stop --all` or `stop --role R` (`--force` = SIGKILL) |
| Re-orient / reattach an existing session | `amq-squad resume` (plan-only; `--exec` to open) |
| Plan a fresh new workstream branched off the current one | `amq-squad fork --from <current> --as <new>` |
| Permanently remove a finished session (destructive) | `amq-squad rm <session>` (`--yes`; `--force` if live) |
| Move a finished session aside (non-destructive) | `amq-squad archive <session>` |
| Multi-session board (also the bare command) | `amq-squad status` / `amq-squad` |
| Single-session detail | `amq-squad status --session <name>` |
| Live read-only Mission Control TUI | `amq-squad console` (`--once` for CI) |
| Inspect restorable launch records (project history) | `amq-squad history` |
| Launch a single agent (modern verb) | `amq-squad agent up <binary>` |
| Resume a saved single agent by role | `amq-squad agent resume <role>` |
| Diagnose AMQ/tmux/markers/wake health | `amq-squad doctor` |
| List configured profiles | `amq-squad team profiles` |
| Sync the pointer stub into `CLAUDE.md` / `AGENTS.md` | `amq-squad team sync --apply` |

`up` means NEW work and **refuses** a session that already exists — use `resume` to continue it, or `up --reset` to start over. `stop` is the primary teardown (`down` is a deprecated alias for one release). With no `--seed-from`/`--brief`, `up` AUTO-STUBS the brief and prints a one-line notice — so before `up`, decide whether to author the brief first (`up --dry-run --seed-from ...`) or let `up` stub it and edit afterward. `rm`/`archive` are the only destructive ops; both confirm-gate (default No, `--yes` to skip) and refuse a live session unless `--force`.

Pass `--profile NAME` to operate on a named profile under `.amq-squad/teams/<name>.json`. Omit (or pass `--profile default`) for `.amq-squad/team.json`.

Every command accepts `--json` where machine-readable output makes sense (`status`, `history`, `doctor`, `team profiles`, `version`, and `up --dry-run`). JSON outputs are schema-versioned envelopes `{ schema_version, kind, data }`. Diagnostics stay on stderr; stdout under `--json` is pure JSON.

Global output flags work before or after the subcommand: `--quiet`, `--verbose`, `--color auto|always|never`. `NO_COLOR` wins over `--color=always`. `--quiet` and `--verbose` are mutually exclusive.

## Rules

- Team roster lives in `.amq-squad/team.json`. The active roster is the source of truth for routing; `amq-squad history` is record-only.
- Workstream = the AMQ `--session` for one issue, release, or focused piece of work. All members of one team run share it.
- AMQ session names are strict: lowercase `a-z`, digits, `-`, `_`. Use `v0-5-0`, not `v0.5.0`.
- Threads are focused conversations inside a workstream: canonical p2p is sorted handles (`p2p/cto__fullstack`); decisions go under `decision/<topic>`.
- Sibling workstreams are history/context only; do not load their message bodies unless the user asks.
- Default scope is the current working directory. Do not inspect or modify other repos unless the user explicitly names them.
- Codex trusted mode (`--trust trusted`) is the only path that prepends `--dangerously-bypass-approvals-and-sandbox`. The default `sandboxed` mode emits no implicit bypass.

## Workflow

1. **Confirm the team-home and active workstream.**
   - Default team-home is `cwd`. Default profile is `default` (maps to `.amq-squad/team.json`); pass `--profile NAME` to scope to a named profile.
   - Session resolution: the `<session>` positional or `--session` > inference from team members and the sanitized team-home basename. The pinned `team.json` `workstream` default was dropped in 2.0 (a profile that still carries it warns; removal in 2.1) — pass `--session`/the positional or rely on inference.
   - Read `.amq-squad/ACTIVE-EPIC.md` if present.

2. **Read the workstream brief.**
   - `.amq-squad/briefs/<session>.md` carries the workstream's goal, scope, and source-of-truth pointers. Skim it before drains.
   - If it is a stub, seed it with `amq-squad up --dry-run --seed-from issue:<n>` (or `file:`/`gh:`), inspect the candidate envelope, then `up --seed-from issue:<n>` to write it live (use `--force` to overwrite an existing brief).

3. **Discover live state and history.**
   - `amq-squad status` (or bare `amq-squad`) for the multi-session board; `status --session <name>` for the single-session detail.
   - `amq-squad console` for the live read-only Mission Control TUI (`--once` for a static board in CI / no-TTY).
   - `amq-squad doctor` for AMQ version / tmux / wake / marker integrity.
   - `amq-squad history` for restorable records in this project (use `--project a,b` to widen scope only when the user explicitly asks).

4. **Bring members up / stop / back.**
   - NEW work: `amq-squad up [<session>]` opens the team in tmux. It REFUSES an existing session — use `resume` to continue, or `up --reset` to start over. With no `--seed-from`, the brief auto-stubs (one-line notice); decide brief-first vs stub-then-edit before launching.
   - Preview-only: `amq-squad up --dry-run` prints one launch command per member; share or paste into separate panes.
   - Restart someone: `amq-squad agent resume <role>` (delegates to the saved launch record). Use `agent up <binary> [flags]` for ad-hoc single-agent launches.
   - Stop: `amq-squad stop --role R` (or `--all`); `--force` escalates to SIGKILL. State is preserved, so the session stays resumable. (`down` is a deprecated alias.)
   - Re-orient: `amq-squad resume` reattaches a saved conversation if present, else re-runs bootstrap so the agent re-reads its brief + AMQ history (no replay of prior reasoning). Plan-only; `--exec` opens the commands.
   - Tear down for good: `amq-squad rm <session>` (destructive, confirm-gated) or `amq-squad archive <session>` (recoverable). Both refuse a live session unless `--force` — stop first.

5. **Fork into a new workstream.**
   - `amq-squad fork --from <current> --as <new>` plans fresh launches in the new session, branched off the current workstream.
   - The new workstream gets its own brief at `.amq-squad/briefs/<new>.md`.
   - Existing target workstreams need `--force-duplicate` to overwrite.

6. **Route messages.**
   - Same-project role handoffs use the shared workstream and a canonical p2p thread:
     ```sh
     amq send --to fullstack --thread p2p/cto__fullstack --kind review_request \
       --subject "Review: X" --body "Please review."
     ```
   - Decisions: `--thread decision/<topic> --kind decision`.
   - Valid `--kind` values (enforced by `amq`): `brainstorm, review_request, review_response, question, answer, decision, status, todo`. **There is no `handoff` kind** — send a role-to-role handoff as `--kind review_request` (work to take over) or `--kind todo` (a queued task). Emitting `--kind handoff` fails validation and falls back to `status`, losing the intended label.
   - **Surfacing to the human:** use the operator handle declared in the current team rules/profile. Default schema-3 teams use non-runnable handle `user`; custom `--operator HANDLE` teams use that handle; `--no-operator` teams route human-facing asks through the lead/CTO rule instead. Human gates use stable `gate/<topic>` threads, for example `amq send --to <operator> --thread gate/<topic> --subject "APPROVAL: ..." --kind question`.
   - Synchronous wait: append `--wait-for drained --wait-timeout 60s`.
   - Cross-session sends need explicit `--session` and `--thread`; avoid them in normal flow.

7. **Drain inbox.**
   ```sh
   amq list --new
   amq read --id <id>
   amq drain --include-body
   ```
   Acknowledge briefly on the same thread when useful - one factual line, not a status update.

## Inbox handling

- Read the message body before acting.
- If it asks for review: findings first, then questions, then a one-sentence summary.
- If it asks for implementation: confirm scope against current user intent and code state.
- If it is FYI/wake: acknowledge briefly after incorporating it.
- If it conflicts with the latest user instruction, follow the user and tell the peer what changed.
- If a new message arrives mid-task, finish or pause cleanly, then acknowledge before redirecting.

Human escalations follow the current team rules. When operator gates are enabled, send approval questions or manual-action requests to the virtual operator handle and do not treat it as a runnable peer. When operator gates are disabled, route through the role named by team rules.

## Common command patterns

```sh
# Live state: board, single-session detail, Mission Control, health
amq-squad                            # bare command -> multi-session board
amq-squad status --session issue-96  # single-session detail
amq-squad console                    # live read-only TUI (--once for CI)
amq-squad doctor

# Bring up the configured team on NEW work
amq-squad up issue-96

# Preview the launch plan
amq-squad up --dry-run

# Seed a brief from a GitHub issue and write it
amq-squad up --dry-run --json --seed-from gh:owner/repo#31 | jq .
amq-squad up --seed-from gh:owner/repo#31 --force

# Continue / re-orient an existing session
amq-squad resume                     # plan-only; add --exec to open it
amq-squad up --reset issue-96 --yes  # start the session over (destructive)

# Branch a fresh workstream
amq-squad fork --from issue-96 --as issue-96-review

# Stop members (state preserved, resumable)
amq-squad stop --role qa
amq-squad stop --all --force

# Tear down for good (confirm-gated; refuses live unless --force)
amq-squad rm issue-96
amq-squad archive issue-96

# Single-agent ops
amq-squad agent up codex --role cto --session issue-96
amq-squad agent resume fullstack
```

## Exit codes

- `0` success
- `1` usage / user error (unknown flag, bad argument, missing required input)
- `2` system / runtime error (IO, process, config, environment)
- `3` partial success (some targets succeeded, some failed; e.g. `stop` with mixed stopped + failed)

## Removed / deprecated verbs

These 1.x verbs were **removed in 2.0**. Each still prints a one-line stderr migration hint (then exits with a usage error). Recommend the replacement, never the removed verb.

| Removed verb | Recommend |
| --- | --- |
| `amq-squad launch <binary>` | `amq-squad agent up <binary>` |
| `amq-squad restore` (print) | `amq-squad history` |
| `amq-squad restore --exec --role R` | `amq-squad agent resume R` |
| `amq-squad list` | `amq-squad status` (live) or `amq-squad history` (records) |
| `amq-squad team show` | `amq-squad up --dry-run` |
| `amq-squad team launch` | `amq-squad up` |
| `amq-squad team launch --fresh --session X` | `amq-squad fork --from <current> --as X` |

`amq-squad down` is **deprecated** (not removed): it is an alias for `stop` that works for one more release and runs identical logic. Prefer `stop`.

## Team-rules content (generated by `team init`)

Use `references/team-rules-template.md` as the starting template. Keep the generated file concrete: name exact workstreams and project paths when known, declare role boundaries explicitly, and include the profile's operator-gate policy.

## Validation hooks

After live changes, useful checks:

```sh
amq-squad doctor                  # AMQ version, team config, tmux, wake, markers
amq-squad status                  # live members
amq-squad team sync               # pointer-stub drift preview (exit 1 if drift)
amq-squad team sync --apply       # write pointer stub into CLAUDE.md/AGENTS.md
```

For first-time setup (no team yet, or designing one from scratch) use the companion `amq-team-setup` skill.
