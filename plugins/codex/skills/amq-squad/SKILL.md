---
name: amq-squad
description: Project-aware skill for live amq-squad team coordination after `.amq-squad/team.json` exists. Covers inbox drains, routing, review/handoff, status board/console/history, up/stop/resume/fork/rm/archive, agent up/resume, doctor, workstream briefs, ACTIVE-EPIC startup, and tmux runtime control — focus/open/send to exact panes, deterministic prompt delivery, --target new-window window-per-agent, and tmux runtime metadata (pane ids, pane_alive, action commands) in status/history/resume --json for clients like amq-noc. For first-time team design (personas, profile choice, team rules, pointer stubs, brief authoring, sync), prefer the companion `amq-team-setup` skill. Use raw `amq-cli` only for AMQ debugging outside the squad.
---

# amq-squad

Use this skill once a team is configured (`.amq-squad/team.json` exists) to run live coordination: drain inboxes, route handoffs, request reviews, bring members up and down, plan fresh forks, and check live state with `status`/`doctor`. For first-time setup work - choosing personas, writing team rules, syncing pointer stubs, authoring the workstream brief - switch to the companion `amq-team-setup` skill.

Launch priming is automatic. `up` / `agent up` inject the bootstrap prompt; agents do not paste it by hand.

This skill is named `amq-squad`; the binary is also named `amq-squad`.

**Skill version: 2.0.0** — on your FIRST response of a session, print the line `amq-squad skill v2.0.0` before anything else, so the operator can confirm which skill build loaded. An older cached skill lacks this step and stays silent, so the *absence* of this line is itself the signal that the expected build did not load. (Pair it with `amq-squad version` for the binary: skill and binary versions should match.)

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
| Focus an agent's pane (or the session) | `amq-squad focus --session <name> [--role R]` (`open` = session alias) |
| Deliver a prompt to an agent's pane + submit | `amq-squad send --session <name> --role R --body "..."` |
| Live read-only Mission Control TUI | `amq-squad console` (`--once` for CI) |
| Inspect restorable launch records (project history) | `amq-squad history` |
| Launch a single agent (modern verb) | `amq-squad agent up <binary>` |
| Resume a saved single agent by role | `amq-squad agent resume <role>` |
| Diagnose AMQ/tmux/markers/wake health | `amq-squad doctor` |
| List configured profiles | `amq-squad team profiles` |
| Sync the pointer stub into `CLAUDE.md` / `AGENTS.md` | `amq-squad team sync --apply` |
| Mutate the live roster at runtime | `amq-squad team member add <role> --binary B` / `team member rm <role>` / `team member list` |
| Native pull-based task queue for a workstream | `amq-squad task add --title T --session S` / `task list --session S` / `task claim <id> --me H --session S` / `task done <id> --session S` |

`up` means NEW work and **refuses** a session that already exists — use `resume` to continue it, or `up --reset` to start over. `stop` is the primary teardown (the `down` alias was removed in 2.0). With no `--seed-from`, `up` AUTO-STUBS the brief and prints a one-line notice — so before `up`, decide whether to author the brief first (`up --dry-run --seed-from ...`) or let `up` stub it and edit afterward. `rm`/`archive` are the only destructive ops; both confirm-gate (default No, `--yes` to skip) and refuse a live session unless `--force`.

Pass `--profile NAME` to operate on a named profile under `.amq-squad/teams/<name>.json`. Omit (or pass `--profile default`) for `.amq-squad/team.json`.

`team member` and `task` are the dynamic-team primitives. `team member add/rm` mutates `team.json` atomically under an exclusive lock and re-validates it (the new member is NOT launched — run the launch command `team member add` prints: a managed pane via `resume --exec --target new-window`, or `agent up` for an unmanaged one-off). `task` is a native pull-based store under `.amq-squad/tasks/<session>/`: a task is claimable only once all its `--depends-on` tasks are completed, and every mutation is atomic and lock-serialized. Both take `--session` (tasks require it).

Every command accepts `--json` where machine-readable output makes sense (`status`, `history`, `resume`, `doctor`, `team profiles`, `version`, and `up --dry-run`). JSON outputs are schema-versioned envelopes `{ schema_version, kind, data }`. Diagnostics stay on stderr; stdout under `--json` is pure JSON. For machine clients, the per-member records in `status --session <name> --json` (kind `status`), `history --json`, and `resume --json` (kind `resume_plan`) carry a `tmux` runtime block (`session`, `window_id`, `window_name`, `pane_id`, `target`) plus a computed `pane_alive` — **present only for agents launched in tmux**, so detect by presence. `status --session <name> --json` records additionally carry an `actions` array (`focus`/`send`/`resume`/`status`) with the exact runnable command and an `available` flag, so a client (e.g. amq-noc) renders/copies stable commands instead of inferring tmux state. (The bare `amq-squad status --json` is the multi-session board envelope `kind: sessions` — it has no per-member records or actions; use `--session <name>` for member detail.)

Global output flags work before or after the subcommand: `--quiet`, `--verbose`, `--color auto|always|never`. `NO_COLOR` wins over `--color=always`. `--quiet` and `--verbose` are mutually exclusive.

## Runtime control (tmux)

amq-squad owns the tmux execution/control contract, so drive agents by stable command — never raw `tmux send-keys`/`select-window`. Control targets the exact recorded **pane id**, never window names.

- **`amq-squad focus --session S [--role R]`** — bring an agent's pane into view (with `--role`), or the session's first live pane (no role). `open` is the session alias.
- **`amq-squad send --session S --role R --body "..."`** (or `--body-file F`, or `--body-file -` for stdin) — deliver a prompt into an agent's exact pane and submit it with one Enter. Text is staged in a tmux paste buffer (not a shell string), so **multi-line prompts and text with quotes or shell metacharacters arrive verbatim**. It errors clearly if the pane is gone. **Built-in busy-guard:** `send` REFUSES to deliver into a busy / mid-turn pane by default (a push into a working agent can land in a tool-result buffer and be missed); pass `--force` to override and deliberately interrupt.
  - This is **pane delivery, not an AMQ message**: `amq-squad send` takes `--body`/`--body-file` and has **no `--kind`/`--thread`**. To post an inter-agent AMQ message, use `amq send ... --kind <valid kind>` (see *Route messages*) — never put a `--kind` on `amq-squad send`.
- Each agent launched in tmux persists its exact tmux identity in its launch record; `status --session <name> --json`, `history --json`, and `resume --json` expose it plus `pane_alive`, and the single-session `status --json` `actions[]` give the exact focus/send/resume commands. Prefer those over hand-built tmux.

**Launch topology** — `amq-squad up --target ...`:

| `--target` | topology | best for |
| --- | --- | --- |
| `current-window` (default) | one **pane** per agent, split in your current tmux window | 2 agents, in-context |
| `new-window` | one **window** per agent (an iTerm2 tab under `-CC`) — full-size terminal each | many agents |
| `new-session` | a detached squad session you `tmux attach` to | background squads |

All three share the same pane-id control contract, so `focus`/`send`/`status` work identically regardless of topology.

## Rules

- Team roster lives in `.amq-squad/team.json`. The active roster is the source of truth for routing; `amq-squad history` is record-only.
- Workstream = the AMQ `--session` for one issue, release, or focused piece of work. All members of one team run share it.
- AMQ session names are strict: lowercase `a-z`, digits, `-`, `_`. Use `v0-5-0`, not `v0.5.0`.
- Threads are focused conversations inside a workstream: canonical p2p is sorted handles (`p2p/cto__fullstack`); decisions go under `decision/<topic>`.
- Sibling workstreams are history/context only; do not load their message bodies unless the user asks.
- Default scope is the current working directory. Do not inspect or modify other repos unless the user explicitly names them.

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
   - Stop: `amq-squad stop --role R` (or `--all`); `--force` escalates to SIGKILL. State is preserved, so the session stays resumable. (The `down` alias was removed in 2.0.)
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
   - Valid `--kind` values (enforced by `amq`): `brainstorm, review_request, review_response, question, answer, decision, status, todo`. **There is no `handoff` kind** — send a role-to-role handoff as `--kind review_request` (work to take over) or `--kind todo` (a queued task). An unknown kind (e.g. `--kind handoff`) is **rejected** with a validation error (`--kind must be one of: ...`) and the message is **not sent** — always pass a valid kind.
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

8. **Work the task store (when one drives the workstream).**
   - When the lead has posted work as tasks (`amq-squad task list --session <S>`), the store is the durable source of truth for who-owns-what — keep it in sync; do not just prose-ACK over AMQ.
   - Before you START a piece of dispatched work, claim its task: `amq-squad task claim <id> --me <handle> --session <S>`. A claim is gated until the task's `--depends-on` tasks are `completed`, so a successful claim also confirms your dependencies are met.
   - When you FINISH, close it: `amq-squad task done <id> --session <S>`. If you cannot finish, record `task fail <id>` (with a reason) or `task block <id>` rather than leaving it `in_progress`.
   - The pushed AMQ report and the task transition are complementary, not redundant: the message tells the lead; the store records the state. Do both.

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
amq-squad up issue-96 --target new-window   # one window/tab per agent (full screen each)

# Preview the launch plan
amq-squad up --dry-run

# Runtime control: focus a pane, deliver a prompt, read the action contract
amq-squad focus --session issue-96 --role cto
amq-squad send  --session issue-96 --role cto --body "please review PR #69"
cat prompt.md | amq-squad send --session issue-96 --role qa --body-file -
amq-squad status --session issue-96 --json | jq '.data.records[] | {role, tmux, actions}'
amq-squad resume --session issue-96 --json | jq '.kind, .data.plan'

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

# Trim worker context (generate + wire per-member --settings overlays)
amq-squad team overlay init --workers --disable-all-hooks
amq-squad team overlay init --role analyst --disable-plugins gws@workspace
```

Per-member native args (v1.8.0+): a `team.json` member may carry
`claude_args` / `codex_args` (must match its binary; `team sync` rejects a
mismatch). They append after the team-level `binary_args`, and `up`,
`resume --exec`, `agent up`/`agent resume` apply them to that member only.
The flagship use is a worker `--settings` overlay that trims the plugins and
hooks it never uses in a same-cwd squad — generate and wire it with
`amq-squad team overlay init (--role R | --workers)
[--disable-plugins id@market,...] [--disable-all-hooks]` (v1.9.0+; writes
`.amq-squad/overlays/<role>.claude.json`, no-clobber on re-runs; `--workers`
targets every claude member, excluding the lead on orchestrated teams).
Plan emission fails fast when a referenced `--settings` file is missing;
`up --dry-run` shows the args on each member's command. Codex members use a `$CODEX_HOME/<name>.config.toml` profile wired
via `codex_args: ["--profile", "<name>"]` instead.

Launch wake gate (v1.8.0+): with amq 0.34.1+, launches pass `--require-wake`
to `amq coop exec`, so a launch fails at the door when the AMQ wake sidecar
cannot start and acquire its lock (instead of surfacing later as a stale
wake). Older amq versions are detected and skip the flag. `--no-require-wake`
opts out for wake-hostile environments and persists into the launch record,
so `agent resume` reproduces it.

## Exit codes

- `0` success
- `1` usage / user error (unknown flag, bad argument, missing required input)
- `2` system / runtime error (IO, process, config, environment)
- `3` partial success (some targets succeeded, some failed; e.g. `stop` with mixed stopped + failed)

## Removed verbs

These 1.x verbs were **removed in 2.0**. Each now returns a plain usage error (exit 1); there is no migration hint any more. Recommend the replacement, never the removed verb. Full notes live in `MIGRATION.md`.

| Removed verb | Recommend |
| --- | --- |
| `amq-squad down` | `amq-squad stop` |
| `amq-squad launch <binary>` | `amq-squad agent up <binary>` |
| `amq-squad restore` (print) | `amq-squad history` |
| `amq-squad restore --exec --role R` | `amq-squad agent resume R` |
| `amq-squad list` | `amq-squad status` (live) or `amq-squad history` (records) |
| `amq-squad team show` | `amq-squad up --dry-run` |
| `amq-squad team launch` | `amq-squad up` |
| `amq-squad team launch --fresh --session X` | `amq-squad fork --from <current> --as X` |

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
