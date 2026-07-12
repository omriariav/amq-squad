---
name: amq-squad
description: Project-aware skill for amq-squad setup, custom role authoring, and live team coordination. Use it to capture a goal, draft the brief, choose roles/profiles, sync pointer stubs, bring teams up, drain inboxes, route handoffs, request reviews, inspect status/console/history, manage lifecycle, and operate tmux runtime controls. For lead-agent bootstrap and child-agent orchestration, use amq-squad-orchestrator.
---


# amq-squad

Use this skill to operate amq-squad end to end. Before a team exists, use the Setup and Role Authoring sections to capture the goal, pick roles, write the brief, and sync pointer stubs. Once a team is configured (`.amq-squad/team.json` exists), use the live coordination sections to drain inboxes, route handoffs, request reviews, bring members up and down, plan fresh forks, and check live state with `status`/`doctor`.

Launch priming is automatic. `up` / `agent up` inject the bootstrap prompt; agents do not paste it by hand.

This skill is named `amq-squad`; the binary is also named `amq-squad`.

**Skill version: 2.19.1** — on your FIRST response of a session, print the line `amq-squad skill v2.19.1` before anything else, so the operator can confirm which skill build loaded. An older cached skill lacks this step and stays silent, so the *absence* of this line is itself the signal that the expected build did not load. (Pair it with `amq-squad version` for the binary: skill and binary versions should match.)

## Context model

The context model has three durable layers. The skill never asks you to duplicate any of them into another file.

- **Team rules (`.amq-squad/team-rules.md`)** - the project's durable norms (skills, workflow, approvals, communication, escalation, style). Source of truth for every member.
- **Per-agent role (`<agent-dir>/role.md`)** - persona/system-prompt for one role. Seeded by `agent up` / `up`; the user can edit freely; later launches preserve user edits.
- **Workstream brief** - the active workstream's goal, scope, and pointers to source-of-truth issues/PRs. The default profile uses `.amq-squad/briefs/<session>.md`; named profiles use `.amq-squad/briefs/<profile>/<session>.md`. Created on first live `up`; preserved on reruns. `up --seed-from REF` writes a fresh brief from `file:<path>`, `issue:<n>`, or `gh:<owner>/<repo>#<n>`.

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
| Dispatch a durable task to an agent and wake it | `amq-squad dispatch --session <name> --role R --kind todo --subject "..." --body "..."` |
| Live read-only Mission Control TUI | `amq-squad console` (`--once` for CI) |
| Inspect restorable launch records (project history) | `amq-squad history` |
| Launch a single agent (modern verb) | `amq-squad agent up <binary>` |
| Resume a saved single agent by role | `amq-squad agent resume <role>` |
| Diagnose AMQ/tmux/markers/wake health | `amq-squad doctor` |
| List configured profiles | `amq-squad team profiles` |
| Sync the pointer stub into `CLAUDE.md` / `AGENTS.md` | `amq-squad team sync --apply` |
| Mutate the live roster at runtime | `amq-squad team member add <role> --binary B` / `team member rm <role>` / `team member list` |
| Set or inspect the orchestrated lead | `amq-squad team lead set <role>` / `team lead clear` / `team lead show --json` |
| Register this pane as an external lead | `amq-squad lead register --role <role> --session S` (project leads require verified identity or `--adopt-project-lead`) |
| Native pull-based task queue for a workstream | `amq-squad task add --title T --session S` / `task list --session S` / `task claim <id> --me H --session S` / `task done <id> --session S` |
| Worker activity heartbeat | `amq-squad activity set --session S --me H --phase testing --task t1` / `activity clear --session S --me H` |

`up` means NEW work and **refuses** a session that already exists — use `resume` to continue it, or `up --reset` to start over. `stop` is the primary teardown (the `down` alias was removed in 2.0). With no `--seed-from`, `up` AUTO-STUBS the brief and prints a one-line notice — so before `up`, decide whether to author the brief first (`up --dry-run --seed-from ...`) or let `up` stub it and edit afterward. `rm`/`archive` are the only destructive ops; both confirm-gate (default No, `--yes` to skip) and refuse a live session unless `--force`.

Pass `--profile NAME` to operate on a named profile under `.amq-squad/teams/<name>.json`. Omit (or pass `--profile default`) for `.amq-squad/team.json`.

**Lifecycle safety contract:** before any lifecycle mutation (`stop`, `resume --exec`, `send`, `focus`, `rm`, `archive`, `up --reset`, `team member rm --stop`), resolve the active project, profile, and session explicitly. Run `amq-squad team profiles --json`, the multi-session `amq-squad status`, then `amq-squad status --project <repo> --profile <profile> --session <session> --json`. If more than one profile exists, never run a lifecycle command without `--profile`; omitting it can inspect or mutate the default roster while the active squad lives in a named profile. Mutating commands with `--session` fail closed when an unprofiled default-profile write would collide with a named profile that already owns that session; rerun with `--profile <name>` or the deliberate escape hatch `--profile default`. Prefer the exact commands from `.data.actions[]` and per-record `actions[]` in the scoped `status --json` output instead of hand-assembling `stop`/`resume`/`focus`/`send` commands.

`team member`, `team lead`, `lead register`, and `task` are the dynamic-team primitives. `team member add/rm` mutates `team.json` atomically under an exclusive lock and re-validates it (the new member is NOT launched — run the launch command `team member add` prints: a managed pane via `resume --exec --target new-window`, or `agent up` for an unmanaged one-off). `team lead set/clear/show` mutates or inspects profile orchestration state for an existing roster. `lead register` adopts the current tmux pane as an operator-owned external lead for the session: status/action JSON can render and target it, but lifecycle commands do not own the pane, so `stop`/`rm`/`archive`/`resume` will not kill, close, or replay it. Project-lead registration is fail-closed: the pane must prove exact project/profile/session/role identity, or the operator must use explicit `--adopt-project-lead` from the actual lead pane; a global orchestrator cannot become project `cto` by passing `--role cto`. Project-lead `--no-wake` requires `--compat-no-wake --reason <why>`, while global orchestrator/NOC no-wake remains a polling mode. `amq-squad amq send --me <team-role>` also requires verified role identity unless `--unsafe-send-as --reason <why>` is used for audited recovery. `task` is a native pull-based store under `.amq-squad/tasks/<session>/` for the default profile or `.amq-squad/tasks/<profile>/<session>/` for named profiles: a task is claimable only once all its `--depends-on` tasks are completed, and every mutation is atomic and lock-serialized. Runtime mutations take `--session` where applicable (tasks require it).

Every command accepts `--json` where machine-readable output makes sense (`status`, `history`, `resume`, `doctor`, `team profiles`, `version`, and `up --dry-run`). JSON outputs are schema-versioned envelopes `{ schema_version, kind, data }`. Diagnostics stay on stderr; stdout under `--json` is pure JSON. For machine clients, the per-member records in `status --session <name> --json` (kind `status`), `history --json`, and `resume --json` (kind `resume_plan`) carry a `tmux` runtime block (`session`, `window_id`, `window_name`, `pane_id`, `target`) plus a computed `pane_alive` — **present only for agents launched in tmux**, so detect by presence. `status --session <name> --json` records additionally carry an `actions` array (`focus`/`send`/`resume`/`status`) with the exact runnable command and an `available` flag, so a client (e.g. amq-noc) renders/copies stable commands instead of inferring tmux state. Managed child rows may also carry `local_input` and a `local_input_blocked` warning when a read-only pane-tail blind-spot detection heuristic sees a local approval/input prompt; this is not a coordination or progress primitive, and absence only means "not observed". (The bare `amq-squad status --json` is the multi-session board envelope `kind: sessions` — it has no per-member records or actions; use `--session <name>` for member detail.)

Global output flags work before or after the subcommand: `--quiet`, `--verbose`, `--color auto|always|never`. `NO_COLOR` wins over `--color=always`. `--quiet` and `--verbose` are mutually exclusive.

## Operator Primitive Decision Table

When an operator sees both `amq-squad ...` and raw `amq ...`, choose by intent:

| Intent | Use | Why |
| --- | --- | --- |
| Supervise a squad | `amq-squad status`, `amq-squad console`, `amq-squad task`, `amq-squad collect` | These commands resolve the project/profile/session and expose the squad model. `collect` is the lead-safe report collector; use it when raw AMQ would say `refusing collect` of a `lead-owned mailbox` unless an audited override is supplied. |
| Give a live agent an instruction now | `amq-squad send --session S --role R --body "..."` | This is tmux pane delivery to the recorded live pane, best for operator-to-visible-lead prompts. It is **not** a durable AMQ protocol message: no `--kind`, no `--thread`, no mailbox receipt. |
| Assign durable work and wake the recipient | `amq-squad dispatch --session S --role R --kind todo --subject "..." --body "..."` | Dispatch queues a durable AMQ task in the resolved workstream root and wakes or nudges the agent to drain it, especially lead-to-worker. |
| Inspect or write mailbox messages directly | Raw `amq send/read/drain/thread` only inside the correct coop/session shell, or with an explicit root/session contract. Prefer `amq-squad amq ...` when operating from an external pane. | Raw AMQ is mailbox plumbing. Outside the right `amq coop exec` context it can target the wrong `.agent-mail` tree. This is the same namespace rule as #328 errors such as `implicit default-profile mutation`, `legacy/default session root`, and `refusing before write`. |

For orchestrated squads, the operator normally talks to the visible lead with
`amq-squad send`; the lead then uses `amq-squad task`, `dispatch`, and
`collect` to coordinate workers. Do not send ordinary worker instructions from
an external operator pane with raw AMQ unless the root/profile/session contract
is explicit.

Failing example from an external pane:

```sh
# Ambiguous/wrong for a named-profile squad: may use the external pane's cwd or
# default .agent-mail/issue-96 while the worker drains .agent-mail/release/issue-96.
amq send --session issue-96 --to developer --thread p2p/cto__developer \
  --kind todo --subject "Task" --body "..."

# Root-resolving squad wrapper:
amq-squad amq send --project /path/to/repo --profile release --session issue-96 \
  --to developer --thread p2p/cto__developer --kind todo \
  --subject "Task" --body "..."

# Raw AMQ is acceptable only when the mailbox root contract is explicit:
amq send --root /path/to/repo/.agent-mail/release/issue-96 \
  --to developer --thread p2p/cto__developer --kind todo \
  --subject "Task" --body "..."
```

## Runtime control (tmux)

amq-squad owns the tmux execution/control contract, so drive agents by stable command — never raw `tmux send-keys`/`select-window`. Control targets the exact recorded **pane id**, never window names.

- **`amq-squad focus --session S [--role R]`** — bring an agent's pane into view (with `--role`), or the session's first live pane (no role). `open` is the session alias.
- **`amq-squad send --session S --role R --body "..."`** (or `--body-file F`, or `--body-file -` for stdin) — deliver a prompt into an agent's exact pane and submit it with one Enter. Text is staged in a tmux paste buffer (not a shell string), so **multi-line prompts and text with quotes or shell metacharacters arrive verbatim**. It errors clearly if the pane is gone. **Built-in busy-guard:** `send` REFUSES to deliver into a busy / mid-turn pane by default (a push into a working agent can land in a tool-result buffer and be missed); pass `--force` to override and deliberately interrupt.
  - This is **pane delivery, not an AMQ message**: `amq-squad send` takes `--body`/`--body-file` and has **no `--kind`/`--thread`**. To post an inter-agent AMQ message, use `amq send ... --kind <valid kind>` (see *Route messages*) — never put a `--kind` on `amq-squad send`.
- **`amq-squad dispatch --session S --role R --kind todo --subject "..." --body "..."`** — the deterministic way to hand an agent a task: it sends a **durable** AMQ message to the workstream's resolved root (root-correct even for an external lead whose bare `amq send` would misroute to the default `.agent-mail`) AND nudges the agent's pane with a fixed *drain instruction* so an idle worker wakes and runs `amq drain`. The task body rides only in the durable message (no double-delivery). The nudge is best-effort: a gone/busy pane leaves the task queued (drained on the worker's next turn); `--force` nudges a busy pane, `--no-wake` queues without nudging, `--thread`/`--from` set the AMQ thread/sender. Use the printed root-correct `collect --timeout 120s` only for an immediate ACK or imminent report; otherwise park/end the turn and let wake resume you. A drain receipt proves only that the child saw the task, not completion. For the full lead-to-child pattern, use the `amq-squad-orchestrator` skill.
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
   - The selected namespace's brief carries the workstream's goal, scope, and source-of-truth pointers. Skim it before drains.
   - If it is a stub, seed it with `amq-squad up --dry-run --seed-from issue:<n>` (or `file:`/`gh:`), inspect the candidate envelope, then `up --seed-from issue:<n>` to write it live (use `--force` to overwrite an existing brief).

3. **Discover live state and history.**
   - `amq-squad status` (or bare `amq-squad`) for the multi-session board; `status --session <name>` for the single-session detail.
   - Before lifecycle mutations, resolve the exact profile and session: `amq-squad team profiles --json`, `amq-squad status`, then `amq-squad status --project <repo> --profile <profile> --session <session> --json`. Treat the action commands in that JSON as the source of truth for follow-up control.
   - `amq-squad console` for the live read-only Mission Control TUI (`--once` for a static board in CI / no-TTY).
   - `amq-squad doctor` for AMQ version, PATH binary skew, Codex/Claude plugin
     cache + skill-marker alignment, tmux, wake, and marker integrity. JSON
     `doctor` and `status` expose the same `data.versions` alignment object,
     and `up` warns before launch when detectable binary/plugin/skill versions
     diverge.
   - `amq-squad history` for restorable records in this project (use `--project a,b` to widen scope only when the user explicitly asks).

4. **Bring members up / stop / back.**
   - NEW work: `amq-squad up [<session>]` opens the team in tmux. It REFUSES an existing session — use `resume` to continue, or `up --reset` to start over. With no `--seed-from`, the brief auto-stubs (one-line notice); decide brief-first vs stub-then-edit before launching.
   - Preview-only: `amq-squad up --dry-run` prints one launch command per member; share or paste into separate panes.
   - Restart someone: `amq-squad agent resume <role>` (delegates to the saved launch record). Use `agent up <binary> [flags]` for ad-hoc single-agent launches.
   - Stop: use the exact scoped command from `status --json` when possible, e.g. `amq-squad stop --project <repo> --profile <profile> --session <session> --all --close-panes` for managed worker teardown. `--force` escalates to SIGKILL. State is preserved, so the session stays resumable. (The `down` alias was removed in 2.0.)
   - Re-orient: `amq-squad resume` reattaches a saved conversation if present, else re-runs bootstrap so the agent re-reads its brief + AMQ history (no replay of prior reasoning). Plan-only; `--exec` opens the commands.
   - Tear down for good: `amq-squad rm <session>` (destructive, confirm-gated) or `amq-squad archive <session>` (recoverable). Both refuse a live session unless `--force` — stop first.
   - External/adopted lead panes are operator-owned. Do not kill them with raw tmux or treat `stop` output as permission to close them; report that they remain open and whether one is the current pane.

5. **Teardown checklist for release/dogfood sessions.**
   - Resolve scope first: `amq-squad team profiles --json`, `amq-squad status`, and scoped `status --project <repo> --profile <profile> --session <session> --json`.
   - Copy the `stop` command from scoped `status --json .data.actions[]` when available; for managed worker panes it should include explicit `--project`, `--profile`, `--session`, `--all`, and usually `--close-panes`.
   - Confirm external/adopted leads in the status rows and leave them to the operator.
   - Verify with a second scoped stop/status pass expecting managed workers to be `not-live`, plus recorded pane/PID checks when release evidence requires it.

6. **Fork into a new workstream.**
   - `amq-squad fork --from <current> --as <new>` plans fresh launches in the new session, branched off the current workstream.
   - The new workstream gets its own brief at `.amq-squad/briefs/<new>.md`.
   - Existing target workstreams need `--force-duplicate` to overwrite.

7. **Route messages.**
   - Same-project role handoffs use the shared workstream and a canonical p2p thread:
     ```sh
     amq send --to fullstack --thread p2p/cto__fullstack --kind review_request \
       --subject "Review: X" --body "Please review."
     ```
   - Decisions: `--thread decision/<topic> --kind decision`.
   - Valid `--kind` values (enforced by `amq`): `brainstorm, review_request, review_response, question, answer, decision, status, todo`. **There is no `handoff` kind** — send a role-to-role handoff as `--kind review_request` (work to take over) or `--kind todo` (a queued task). An unknown kind (e.g. `--kind handoff`) is **rejected** with a validation error (`--kind must be one of: ...`) and the message is **not sent** — always pass a valid kind.
   - **Surfacing to the human:** use the operator handle declared in the current team rules/profile. Default schema-3+ teams use non-runnable handle `user`; custom `--operator HANDLE` teams use that handle; `--no-operator` teams route human-facing asks through the lead/CTO rule instead. Human gates use stable `gate/<topic>` threads, for example `amq send --to <operator> --thread gate/<topic> --subject "APPROVAL: ..." --kind question`.
   - Synchronous wait: append `--wait-for drained --wait-timeout 60s`.
   - Cross-session sends need explicit `--session` and `--thread`; avoid them in normal flow.

8. **Drain inbox.**
   ```sh
   amq list --new
   amq read --id <id>
   amq drain --include-body
   ```
   Acknowledge briefly on the same thread when useful - one factual line, not a status update.
   - Leads collecting child reports should prefer `amq-squad collect --session <S> --me <lead> --timeout 120s --include-body` over raw `amq drain`; use that bounded wait immediately after dispatch only for an ACK or imminent report. For waits measured in minutes, **park/end the turn**: wake resumes on AMQ and ending the turn flushes queued operator pane input. Under `/goal`, operator-only blocked input is a sanctioned park within minutes. In live-operator mode, never hold `collect` while an operator gate is open and the answer arrives through your own pane — that self-deadlocks. Stall detection belongs to `monitor idle_with_active_task`, not collect timers. Never background `collect` or `drain`: both consume mailbox state, so a background reader races foreground processing and risks destructive double-consumption. `collect` resolves the correct root, journals unread bodies before acknowledging them, and has at-least-once replay semantics.

9. **Work the task store (when one drives the workstream).**
   - When the lead has posted work as tasks (`amq-squad task list --session <S>`), the store is the durable source of truth for who-owns-what — keep it in sync; do not just prose-ACK over AMQ.
   - Before you START a pull-style task that is still `pending`, claim it: `amq-squad task claim <id> --me <handle> --session <S>`. A task-backed `amq-squad dispatch --create-task/--task` auto-claims pending tasks for the target handle after the durable AMQ send and task link succeed; if the task already shows `in_progress` for you, do not re-claim it.
   - Keep worker activity current while you work: `amq-squad activity set --session <S> --me <handle> --task <id> --phase <reading|testing|waiting-on-command> [--detail "..."]` on task claim, meaningful phase changes, and long-running commands. `status --json` and `console` show the heartbeat as `fresh`, `stale`, or `unknown`; task-store ownership is only a weaker fallback, not proof of active progress.
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

Operator-gate escalation (v2.17.0+): unanswered `gate/<topic>` asks addressed to the configured operator handle escalate from `initial` to `reminder` after 30m and `strong-warning` after 2h, measured from the last unanswered operator-facing gate message. `amq-squad notify` bypasses its normal de-duplication when a gate crosses into a stronger band; `status --json` warnings and `console --once` labels make aged gates distinct.

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
amq-squad dispatch --session issue-96 --role qa --from cto --subject "Validate" --body-file ./task.md
amq-squad collect --session issue-96 --me cto --timeout 120s --include-body
amq-squad activity set --session issue-96 --me qa --task t1 --phase testing --detail "make ci"
amq-squad team profiles --json
amq-squad status
amq-squad status --project /Code/app --profile review --session issue-96 --json | jq '.data.records[] | {role, tmux, actions}'
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
amq-squad stop --project /Code/app --profile review --session issue-96 --all --close-panes

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

AMQ floor (v2.18.0+): amq-squad requires amq 0.41.0+. The launch wake
gate introduced in v2.5.0 passes `--require-wake` to `amq coop exec`, so a
launch fails at the door when the AMQ wake sidecar cannot start and acquire its
lock (instead of surfacing later as a stale wake). `--no-require-wake` opts out
for wake-hostile environments and persists into the launch record, so `agent
resume` reproduces it.
Use `--no-gitignore` on `agent up`, `up`, or `up --dry-run` when AMQ coop
auto-init should leave `.gitignore` unchanged; the opt-out is persisted in the
launch record and replayed by `agent resume`.
Operator-gate escalation (v2.17.0+): unanswered `gate/<topic>` asks addressed to
the configured operator handle escalate from `initial` to `reminder` after 30m
and `strong-warning` after 2h. `amq-squad notify` bypasses its normal throttle
when the escalation band advances, while `status --json` warnings and
`console --once` make aged gates visually distinct.
Claude-binary agents launched in tmux also get a best-effort delayed
`/rename <role>-<session>` injection, including managed `resume --exec` /
`agent resume` replay. Failure to deliver the rename does not block launch.
Codex agents are unaffected because Codex has no matching slash command.
External-injector wake setups can pass `--wake-inject-via /absolute/injector`
and repeat `--wake-inject-arg=value`; these flags are forwarded to
`amq coop exec`, persisted in launch.json, and replayed by resume. Use the
`--flag=value` form for dash-prefixed injector args such as
`--wake-inject-arg=--pane`.

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

For first-time setup (no team yet, or designing one from scratch), continue with the Setup section below.

## Setup

Use this section before the team runs, to design and lock in the team's
shape: **what the work is** (the goal/brief), who is on the team, where they
work, what norms they share, and whether one agent leads the others. It is a
guided wizard: walk the steps in order and **ask the user, then act** at each
gate. Setup ends when `amq-squad up --dry-run` looks right and
`amq-squad team sync` reports no drift. After that, continue with the live coordination sections above.

This section does not replace live coordination. It targets the one-time decisions that
survive across workstreams, plus the per-session goal/brief that kicks off each
workstream.

### Context model

Setup creates and aligns four durable things; ongoing coordination consumes them.

- **Protocol** — the `amq-squad-orchestrator` skill (only when the squad is
  orchestrated): the lead-agent playbook over the runtime primitives.
- **Goal** — the selected namespace's brief: `.amq-squad/briefs/<session>.md`
  for the default profile, or `.amq-squad/briefs/<profile>/<session>.md` for a
  named profile. Every member of that workstream namespace reads the same file.
- **Norms** — `.amq-squad/team-rules.md`: durable team norms, the single source
  of truth. **Generated** by amq-squad; never hand-duplicated into other files.
- **Persona** — `<agent-dir>/role.md`: per-agent system prompt. Seeded on first
  `up`; the user can edit freely; later launches preserve user edits.

`CLAUDE.md` / `AGENTS.md` carry only a small managed pointer block:

```
<!-- amq-squad:managed:begin -->
... pointers to team-rules.md, role.md, and the active brief path ...
<!-- amq-squad:managed:end -->
```

`amq-squad team sync --apply` writes and refreshes that block. Hand-editing
inside the markers is unsupported.

### The wizard

Run these five steps in order. At each gate, **ask the user the question, wait
for the answer, then act** — do not assume. Setup stays read-only until step 5
(no live launch).

#### Step 1 — Capture the goal (any source)

Ask the user where the goal lives, then **fetch it with whatever tool you
have** and detect the source type. amq-squad core is tracker-neutral: all
fetching happens here in the skill, never as an amq-squad dependency.

| Source the user gives | How to fetch (agent-side) |
| --- | --- |
| A one-line prompt / inline text | use it directly as the Goal seed |
| A local file or path (`./design.md`, a path) | read the file |
| A GitHub issue (`#96`, a URL, `owner/repo#96`) | `gh issue view <n> --json title,body` (shell) |
| A GitHub PR (`#96`, a PR URL) | `gh pr view <n> --json title,body` (shell) |
| A Jira key (`PROJ-123`) | an Atlassian MCP `getJiraIssue` tool if available, else a `jira issue view PROJ-123` CLI, else ask the user to paste |
| A URL (Confluence / doc / spec) | an Atlassian MCP `getConfluencePage` tool for Confluence, else a fetch tool, else ask the user to paste |

If no integration is available for the source, say so plainly and ask the user
to paste the content rather than inventing it. Capture the source link/id — it
becomes the brief's `## Source` line.

#### Step 2 — Draft and confirm the brief

Normalize whatever you fetched into the **canonical brief shape** from
`references/briefs-template.md`:

```md
# <session> brief
### Goal          # the outcome, 1-2 sentences
### Source        # JIRA PROJ-123 / gh#96 / URL / file: / "operator prompt"
### Scope
### Out of scope
### Acceptance     # how we know it's done; who signs off
```

A raw ticket description is **not** a brief — distill it. DRAFT the brief, then
SHOW it to the user and let them edit before accepting. Do not auto-accept a
raw ticket. Pick the workstream/session name now (lowercase `a-z`, `0-9`, `-`,
`_`; e.g. `issue-96`, `v1-7-0`); the brief is saved under the selected
profile/session namespace in step 5.

You can also preview a candidate from a deterministic source with
`amq-squad up --dry-run --seed-from issue:<n>` / `file:./brief.md` /
`gh:owner/repo#<n>` (read-only) and paste it into the draft.

#### Step 3 — Roles and profile

- Use `references/team-archetypes.md` as a starting menu (solo, pair, classic
  squad, design-led, qa-led).
- For each role decide binary (`codex` / `claude`), handle, cwd, and scope.
  Keep the roster minimal; you can add roles later by re-running `team init`.
- Custom (non-catalog) roles enter via `--role-file <path>` (comma-separated,
  or an inline path in `--roles`); author them with the Role Authoring section
  below. Two beats that matter in practice:
  - **Show, then create**: before creating the team, show the user every
    custom role's full body and let them edit — skill lists and hard rules
    get most of their fixes at this gate.
  - **Staging normalizes the file**: `new team --role-file` stages a copy
    under `.amq-squad/roles/<id>.md` with the YAML frontmatter absorbed into
    `team.json` and only the Markdown body kept. Expected behavior, not
    corruption; the authored source file is untouched.
- `--binary` takes ONE comma-separated list
  (`--binary copilot=claude,analyst=claude,...`). Repeating the flag does NOT
  accumulate — only one list survives, and the resulting error ("custom role X
  requires --binary x=<cli>") reads like a contradiction when you did pass it.
  A role file's `binary:` frontmatter satisfies the requirement for that role,
  so an all-role-file team usually needs no `--binary` at all.
- Choose the profile: default at `.amq-squad/team.json`, or a named profile at
  `.amq-squad/teams/<name>.json` for parallel team shapes (release vs research).
- **Per-member native args** (v1.8.0+): a member entry in `team.json` may
  carry `claude_args` / `codex_args` — extra native CLI args for that member
  only, appended after the team-level `binary_args` so the member value wins.
  The field must match the member's binary (`team sync` rejects a mismatch).
  Flagship use: a same-cwd squad where only the lead needs the full
  plugin/hook surface — give each worker
  `"claude_args": ["--settings", ".amq-squad/overlays/<role>.claude.json"]`
  pointing at a Claude Code settings overlay (`enabledPlugins`,
  `disableAllHooks`, ...) that trims plugins and hooks the worker never uses,
  cutting its per-prompt context cost. Do not hand-edit this: step 5 generates
  and wires it with `amq-squad team overlay init` (v1.9.0+). Plan emission
  validates that every referenced `--settings` file exists.
- **Per-member permission allowlist**: a Claude member may carry
  `permission_allowlist`, for example
  `"permission_allowlist": ["Bash(rm -rf /tmp/qa-review/*:*)"]`. amq-squad
  merges it with explicit native `--allowedTools`, applies it only to that
  exact role, shows configured/effective grants in `up --dry-run --json`, and
  persists the effective list in launch history for resume/audit. Resume strips
  the prior launcher-owned grant and rebuilds from current policy, so removal or
  narrowing revokes old access; `--no-preauthorize-inscope` also round-trips.
  Validation rejects this field on non-Claude members and rejects patterns
  beginning with `-`; emission uses one injection-safe
  `--allowedTools=<grant>` token. Keep patterns scoped to the member's own
  scratch or review workspace; this is not a team-wide trust switch and the
  setup wizard does not author it. Profiles using the field write team schema
  4; other profiles remain schema 3. Upgrade every reader/writer to v2.20+
  before configuration: pre-v2.20 binaries can silently ignore the field and
  lossily rewrite a schema-4 profile. Use `amq-squad doctor` to detect version
  skew; v2.20+ readers accept schemas 3/4 and reject future schemas.
- **Model/binary/effort choice** (context stamp: 2026-07-10, current operator
  setup; setup-dependent, not universal): defaults are not limits; escalate
  when output quality misses the bar. For shippable work use
  `intelligence > taste > cost`, with cost only as a tie-breaker. In this
  setup, bulk/mechanical work defaults to Codex CLI on `gpt-5.6-luna`;
  everyday balanced implementation defaults to `gpt-5.6-terra`; frontier
  implementation and independent review default to `gpt-5.6-sol`. Raise to
  `gpt-5.6-terra` or `gpt-5.6-sol` when quality misses the bar. `gpt-5.5`,
  `gpt-5.4`, and `gpt-5.4-mini` remain valid choices for previous-frontier,
  strong everyday, and small/fast work respectively. UI, copy, API shape, and
  product design still need taste `>= 7`. Never use Haiku. Direct agent config
  separates `binary`, `model`, Codex effort through `codex_args`
  (`-c model_reasoning_effort=<level>`), and Claude effort/settings through
  `claude_args` (for example `--effort high`). amq-squad does not maintain an
  Anthropic model whitelist: Claude member `model` is passed through to the
  installed `claude --model <model>`, with accepted aliases depending on the
  Claude CLI build and account. Current expected aliases include `default`,
  `opus`, `fable`, `sonnet`, and `haiku`, plus full names such as
  `claude-fable-5`. That is mechanical support only; the policy remains never
  choose Haiku for amq-squad work. A thin Claude wrapper that delegates to Codex
  CLI on a Codex model such as `gpt-5.6-sol` or `gpt-5.6-terra` is a
  compatibility pattern for Claude-only workflow/subagent slots; a Claude
  workflow/agent `model:` parameter still selects a Claude model only. Prefer
  an explicit Codex-binary member when amq-squad controls the roster. Exact
  override examples:
  `amq-squad team init --model cto=gpt-5.6-sol,fullstack=fable-5`,
  `amq-squad team member add plan-reviewer --binary claude --model claude-fable-5 --claude-args "--effort high"`,
  `amq-squad up issue-96 --model plan-reviewer=claude-fable-5,implementer=sonnet`,
  and
  `amq-squad resume --session issue-96 --model plan-reviewer=opus,implementer=sonnet --exec`.
- Pick the team-home (where `.amq-squad/` lives): the cwd by default; for a
  monorepo, usually the repo root. Confirm if the choice is non-obvious.

#### Step 4 — Orchestrated? (opt-in, default off)

Ask: **"Run this as an orchestrated squad? If so, who leads?"**

- **Default is no.** A flat squad coordinates peer-to-peer over AMQ and needs no
  lead. Only opt in when one agent should spawn, dispatch, and monitor the
  others and own the deliverable.
- If **yes**, the user names exactly **one** lead role (a team member — commonly
  `cto`). The lead is an agent; it is **never** the operator/NOC handle.
- This is wired by a **structured flag**, not by hand-editing prose. In step 5
  you pass `--orchestrated --lead <role>` so amq-squad records the lead in
  `team.json` and the **generated** `team-rules.md` carries the orchestration
  reporting norm (the lead loads the `amq-squad-orchestrator` skill; children
  push `status`/`question`/`review_request` to the lead over AMQ). The norm is
  written when `team-rules.md` is first seeded; if the file already exists,
  `new team` leaves it untouched, so regenerate with `amq-squad team rules init
  --force` to pick up the orchestration norm. Do **not** hand-edit
  `team-rules.md` or `role.md` to describe orchestration — the flag owns it so
  it cannot drift.

#### Step 5 — Review and create

Show the user a summary (team-home, roles + binaries, profile, workstream,
orchestrated?/lead, brief) and confirm. Then create:

1. **Team profile + rules** (one command writes both):

   ```sh
   amq-squad new team --roles cto,fullstack,qa --binary cto=codex \
     --session <workstream>
   # orchestrated variant:
   amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto \
     --session <workstream>
   ```

   Pass `--session <workstream>` explicitly — the session name you confirmed
   earlier does not apply itself. A brand-new profile has no member-session
   pin yet, so without the flag the workstream falls back to the sanitized
   team-home directory name, and the brief you write next lands under a
   session the team never boots into. (Once members carry a session pin,
   later resolution infers it from them.)

   `new team` writes `.amq-squad/team.json` and seeds the generated
   `.amq-squad/team-rules.md` **if it does not already exist** (an existing
   rules file is left untouched; use `amq-squad team rules init --force` to
   regenerate it). Preview first with `--dry-run` (add `--json` for
   the machine-readable `team_profile_plan`, which now carries
   `orchestrated`/`lead`). `--orchestrated [--lead ROLE]` is the orchestration
   opt-in from step 4; without `--lead`, a single-member team self-selects and a
   team with a `cto` defaults to `cto`.

2. **Worker overlays** (optional; ask whenever the team has two or more
   claude members — same-cwd squads are the flagship case): "Should the
   workers run with a trimmed plugin/hook surface?"
   Only the lead usually needs the full project configuration; workers on
   smaller-context models burn context on plugins and per-prompt hook output
   they never use. If yes, generate and wire the overlays in one command:

   ```sh
   amq-squad team overlay init --workers --disable-all-hooks \
     --disable-plugins <id@marketplace,...>
   # or one member at a time:
   amq-squad team overlay init --role <role> --disable-all-hooks
   ```

   This writes `.amq-squad/overlays/<role>.claude.json` (human-editable
   afterwards; re-runs never clobber it) and wires the member's
   `claude_args: ["--settings", <path>]` in `team.json`. `--workers` targets
   every claude member (on an orchestrated team the lead is excluded; a flat
   team has no lead to exclude). To find plugin ids for
   `--disable-plugins`, ask the user or check `claude plugin list` output.
   Codex members use the native equivalent instead: a
   `$CODEX_HOME/<name>.config.toml` profile wired via
   `codex_args: ["--profile", "<name>"]`.

3. **The brief**: save the confirmed step-2 draft to the selected namespace's
   brief path (a plain file write; the first live `up` preserves an existing
   brief). This kills the auto-stub the status board warns about.

4. **Pointer stubs**: `amq-squad team sync --apply` writes the managed block in
   `CLAUDE.md` / `AGENTS.md` (add `--sync` to `new team` to do it in one shot).

5. **Validate**: `amq-squad up --dry-run` (one launch command per member),
   `amq-squad team sync` (no drift), `amq-squad doctor` (AMQ / tmux / wake /
   markers).

6. **Print the next commands** and hand off:

   ```sh
   amq-squad up <workstream> --visibility sibling-tabs  # default visible sibling windows
   amq-squad up <workstream> --visibility detached      # explicit detached tmux session
   amq-squad up <workstream> --visibility current       # split the current window
   ```

   First live launch belongs to the `amq-squad` skill (or, for an orchestrated
   squad, the lead drives spawn/dispatch/monitor via `amq-squad-orchestrator`).
   The default handoff must keep the team visible to the operator:
   `--visibility sibling-tabs` opens one window per agent in the current visible
   tmux session and refuses outside tmux before spawning hidden workers. Use
   `--visibility detached` only when a separate tmux session is intentional and
   attach/open it before considering the team handed off.

   **Launch-name consistency (read before handing off):** the configured
   workstream applies only when the launch command carries NO `--session`
   override. Launching under a different free-typed name (e.g. in a NOC
   new-session form) boots a brand-new workstream with an auto-stub brief —
   the brief from this step is invisible to those agents. If the status board
   shows `(stub brief)` right after launch, the session name diverged: stop
   the squad, `rm` the accidental session, and relaunch with the configured
   workstream name explicitly (`amq-squad up <workstream>`).

### Verbs you will use

| Goal | Command |
| --- | --- |
| Create the default team profile + rules | `amq-squad new team --roles cto,fullstack,qa` |
| Create an orchestrated squad | `amq-squad new team --roles ... --orchestrated --lead cto` |
| Create a named profile | `amq-squad new profile release --roles cto` |
| Preview the profile without writing | `amq-squad new team --dry-run [--json] --roles ...` |
| Initialize interactively (prompts for roles/CLIs) | `amq-squad team init` |
| Seed/refresh `.amq-squad/team-rules.md` | `amq-squad team rules init` |
| Trim worker plugin/hook surface | `amq-squad team overlay init --workers [--disable-plugins id@market,...] [--disable-all-hooks]` |
| Preview pointer-stub drift | `amq-squad team sync` |
| Apply the pointer stub | `amq-squad team sync --apply` |
| Inspect the planned launch (dry-run) | `amq-squad up --dry-run [--json]` |
| Preview a candidate brief from a source | `amq-squad up --dry-run --seed-from issue:<n>` (or `file:./brief.md` / `gh:owner/repo#<n>`) |
| Diagnose environment / config / wake / markers | `amq-squad doctor` |

Most profile-aware commands accept `--profile NAME` to scope to
`.amq-squad/teams/<name>.json` (omit, or pass `--profile default`, for
`.amq-squad/team.json`). Exception: `new profile NAME` sets the profile via the
positional `NAME` and rejects an explicit `--profile`.

Global output flags work before or after the subcommand: `--quiet`,
`--verbose`, `--color auto|always|never`. `NO_COLOR` wins over `--color=always`.

### Rules

- One source of truth per layer. Team rules live only in
  `.amq-squad/team-rules.md`. Pointer stubs link, never copy.
- The brief is the goal layer and is **per profile/session namespace**. Draft
  from the source, confirm with the user, then save to the selected namespace's
  brief path. A raw ticket is not a brief.
- amq-squad core is **tracker-neutral**: fetch Jira/Confluence/URLs with the
  agent's own tools here in the skill; amq-squad takes no tracker dependency.
- Orchestration is **opt-in, default off**. Exactly one lead; the lead is a team
  member, never the operator/NOC. Wire it with `--orchestrated --lead`, never by
  hand-editing `team-rules.md` / `role.md` prose.
- Workstream = AMQ `--session`. Session names are strict: lowercase `a-z`,
  digits, `-`, `_`. Use `v1-7-0`, not `v1.7.0`.
- Default profile is `default` (`.amq-squad/team.json`); named profiles live
  under `.amq-squad/teams/<name>.json` and are addressed with `--profile NAME`.
- New profiles default to an enabled virtual operator handle `user`. Use
  `--operator HANDLE` for a custom human-gate mailbox or `--no-operator` to opt
  out. Schema 1/2 profiles keep implicit `user` gates until rewritten.
- Setup never executes a live launch. Use `--dry-run` until the user explicitly
  approves going live; live launches happen via the `amq-squad` skill's `up`
  flow. (Writing the brief, the profile, and the overlays in step 5 are file
  writes, not a launch.)
- Codex trusted mode (`--trust trusted`) is the only path that prepends
  `--dangerously-bypass-approvals-and-sandbox`. The default `sandboxed` mode
  emits no implicit bypass; pick the mode deliberately if non-default.
- Do not touch `README.md` or unrelated repo files during setup.
  Stay inside `.amq-squad/`, `CLAUDE.md`, and `AGENTS.md`.

### References

- `references/briefs-template.md` - the canonical per-session brief shape
  (Goal / Source / Scope / Out of scope / Acceptance) the wizard normalizes to.
- `references/team-archetypes.md` - common team shapes (solo / pair / classic
  squad / design-led / qa-led).
- `references/pointer-stub-template.md` - the exact managed block written by
  `amq-squad team sync --apply` (for reference; do not hand-author).
- `../amq-squad/references/team-rules-template.md` - a static mirror of the
  team-rules content. The CLI generates `team-rules.md` from its own Go template
  (`amq-squad team rules init`); this file mirrors that output for reference.

### Exit codes

- `0` success
- `1` usage / user error (unknown flag, bad argument, missing required input)
- `2` system / runtime error (IO, process, config, environment)
- `3` partial success (some targets succeeded, some failed)

### Related sections and skills

- The live coordination sections above cover drains, routing, status
  board/console/history, up/stop/resume/fork/rm/archive, agent up/resume, and
  doctor. Use them as soon as `up --dry-run` is clean and the user is ready to
  launch.
- `amq-squad-orchestrator` - the lead-agent playbook for an orchestrated squad
  (spawn / dispatch / monitor / the `[AGENT-EVENT]`-over-AMQ reporting protocol /
  recover). The lead loads it; this section only wires the opt-in.
- The Role Authoring section below covers custom non-catalog roles.

## Role Authoring

Use this section to add a **custom role** — one that is not in the built-in
persona catalog (`cpo, cto, senior-dev, fullstack, frontend-dev, backend-dev,
mobile-dev, junior-dev, qa, pm, designer, scribe, lead, agent`). Custom roles are first-class team
members: they appear in `team.json`, `team-rules.md`, the bootstrap prompt,
status/history, and launch/resume exactly like built-ins.

Requires amq-squad **v2.0.0+**. Check with `amq-squad version`.

There are two ways to add a custom role. Pick by how much role guidance you want.

### A. Inline (quick, minimal role.md)

For a role that only needs an id + CLI, with the generic custom-role fallback
text seeded into its `role.md`:

```sh
amq-squad new team --roles researcher --binary researcher=codex
amq-squad new team --roles researcher,reviewer --binary researcher=codex,reviewer=claude
amq-squad new profile discovery --roles researcher --binary researcher=codex
amq-squad team init --roles cto,researcher --binary researcher=codex
```

Rules:
- A `--roles`/`--personas` entry that is not a built-in persona is a custom role.
- Each custom role **must** be a valid slug (lowercase `a-z`, `0-9`, `-`, `_`)
  and **must** have an explicit `--binary <role>=<cli>` entry (there is no
  catalog default to fall back to). Built-in roles keep their catalog defaults
  unless overridden.
- Missing binary fails clearly:
  `custom role "researcher" requires --binary researcher=<cli>`.

### B. From a role file (rich, authored role.md)

When you want a real persona description, peers, and skills, author a role file
and pass it with `--role-file` (comma-separated) or inline in `--roles`:

```sh
amq-squad new team --role-file ./roles/researcher.md --roles cto
amq-squad team init --roles "cto,./roles/researcher.md"
amq-squad new profile discovery --role-file ./roles/researcher.md,./roles/sre.yaml
```

What happens:
- The role id is taken from the file (`id:` field, a `# Role: <name>` heading,
  or the filename).
- The binary comes from the file's `binary:` field; `--binary` overrides it.
  If neither is present the command fails with the same binary guidance as above.
- The authored document is staged at `.amq-squad/roles/<id>.md`. At launch,
  `up` / `agent up` seeds that agent's `role.md` from it verbatim (and never
  overwrites later user edits).
- A role file named via `--role-file` is added to the team even if it is not
  also listed in `--roles`.

#### File formats

**Markdown with YAML frontmatter** (recommended — frontmatter for metadata,
body becomes `role.md` verbatim):

```markdown
---
id: researcher
label: Research Engineer
binary: codex
peers: [cto, qa]
skills:
  - /deep-research
---
# Role: Research Engineer

### Description
Owns deep technical investigation, prototypes, and written findings. Turns
open questions into evidence the team can act on.

### Peers
- cto
- qa

### Skills
- /deep-research

### System Prompt
Stay within the research scope; hand implementation to developer roles. Use the
amq-squad protocol for handoffs.

### Priming Template
At launch, state your role and handle, summarize relevant context, then wait
for instruction.
```

**Plain Markdown, no frontmatter** (whole file is `role.md`; id from the
`# Role:` heading or filename; supply the binary with `--binary`):

```markdown
# Role: Archivist

### Description
Captures decisions and keeps the team's written record.
```

```sh
amq-squad new team --roles "cto,./roles/archivist.md" --binary archivist=claude
```

**Metadata-only YAML or JSON** (no body; `role.md` is rendered from the
fields):

```yaml
id: sre
label: Site Reliability Engineer
binary: claude
description: Owns reliability, on-call, and incident response.
skills:
  - /run
peers:
  - cto
  - backend-dev
```

```json
{ "id": "analyst", "label": "Analyst", "binary": "codex",
  "description": "Owns reporting and data pulls.", "peers": ["pm"] }
```

#### Supported metadata fields

| Field | Meaning |
| --- | --- |
| `id` (or `role`) | role slug + default handle (lowercase `a-z 0-9 - _`) |
| `label` | human title shown in listings and `role.md` |
| `binary` | `codex` or `claude` (any non-control value is accepted) |
| `description` | one-line summary seeded into the rendered `role.md` |
| `skills` | list of slash commands for this role |
| `peers` | list of role ids this role talks to most |
| `body` (JSON only) | optional verbatim `role.md` content |

### Authoring workflow

1. Decide the role id, CLI (`codex`/`claude`), and whether you need rich
   guidance (file) or just an id (inline).
2. For a file: write it under `./roles/<id>.md` (frontmatter + body is the
   sweet spot). Keep the `# Role:`, `## Description`, `## Peers`, `## Skills`,
   `## System Prompt`, `## Priming Template` sections so it matches what the
   binary renders for built-ins.
3. Preview before writing anything:
   ```sh
   amq-squad new team --role-file ./roles/<id>.md --roles cto --dry-run --json
   ```
   Confirm the member shows the right `role`, `handle`, and `binary`.
4. Create the team for real (drop `--dry-run`), or add `--sync` to also write
   the `CLAUDE.md`/`AGENTS.md` pointer stubs.
5. Edit `.amq-squad/roles/<id>.md` later to refine the persona; re-running
   `team init --force` re-stages it, and launch never clobbers an agent's
   already-seeded `role.md`.

### Verification

- `amq-squad new team --role-file ... --dry-run --json` succeeds and the plan
  lists the custom member with the expected binary.
- After a live create, `.amq-squad/roles/<id>.md` exists and `team.json`
  includes the member.
- `amq-squad up --dry-run` shows a launch command for the custom role.

### When NOT to use this section

- Built-in personas or general team design → Setup section above.
- Live coordination (drain, route, review, up/stop/resume) → live coordination sections above.
- Raw AMQ debugging → `amq-cli`.
