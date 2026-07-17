# Using the amq-squad skills

amq-squad ships the same three authoritative skills to the Claude Code and
Codex marketplaces: `amq-squad:wizard` for preparation,
`amq-squad:cli` for direct operations, and `amq-squad:orchestrator` for a
verified live lead. `amq-squad`, `amq-squad-orchestrator`, `amq-team-setup`,
and `amq-squad-role-creator` are compatibility redirects only.

For the binary's full verb/flag reference, see the main [README](../README.md).
This document is about the skills.

- [The mental model](#the-mental-model)
- [Which skill do I reach for?](#which-skill-do-i-reach-for)
- [Installing and invoking](#installing-and-invoking)
- [`amq-squad:wizard` — prepare a squad](#preparation-with-amq-squadwizard)
- [`amq-squad:cli` — direct live-team operations](#amq-squadcli--direct-live-team-operations)
- [`amq-squad:orchestrator` — lead a squad](#amq-squadorchestrator--lead-a-squad)
- [Role authoring with `amq-squad:wizard`](#role-authoring-with-amq-squadwizard)
- [End-to-end walkthrough](#end-to-end-walkthrough)
- [Troubleshooting](#troubleshooting)

## The mental model

The three authoritative skills map onto the actor model:

| Phase | Skill | You use it... |
| --- | --- | --- |
| Preparation | **`amq-squad:wizard`** | goal, brief, rules, roles, profile, exact readiness evidence, and the separate default-No launch approval. |
| Direct operations | **`amq-squad:cli`** | status, doctor, task, activity, gate, AMQ, resume/stop/archive, verification, and evidence commands. |
| Verified lead orchestration | **`amq-squad:orchestrator`** | dispatch, monitoring, review convergence, recovery, pruning, and final evidence after launch. |
| Compatibility redirects | `amq-squad`, `amq-squad-orchestrator`, `amq-team-setup`, `amq-squad-role-creator` | route old invocations to one authoritative namespaced skill. |

They sit on top of three durable layers that setup creates and coordination
consumes:

- **Norms** — `.amq-squad/team-rules.md` (generated; the single source of truth).
- **Goal** — `.amq-squad/briefs/<session>.md` for the default profile, or
  `.amq-squad/briefs/<profile>/<session>.md` for a named profile.
- **Persona** — `<agent-dir>/role.md` (seeded at launch, then user-editable).

`CLAUDE.md` / `AGENTS.md` carry only a small managed **pointer stub** linking to
those three; they never duplicate the content.

## Which skill do I reach for?

| If you want to... | Reach for |
| --- | --- |
| Start from a ticket / prompt / doc and stand up a new team | `amq-squad:wizard` |
| Turn a Jira/GitHub/URL goal into a confirmed brief | `amq-squad:wizard` |
| Decide who leads an orchestrated squad | `amq-squad:wizard` wires it; `amq-squad:orchestrator` runs it after launch |
| Bring the configured team up | `amq-squad:wizard` readiness and launch stage |
| Drain your inbox, route a handoff, request a review | `amq-squad:cli` |
| Check the status board / Mission Control / health | `amq-squad:cli` |
| Spawn child agents and drive them to completion as the lead | `amq-squad:orchestrator` |
| Add a role that isn't in the catalog | `amq-squad:wizard` roles stage |
| Debug raw AMQ outside a squad | the separate `amq-cli` skill |

Rule of thumb: prepare with **wizard**, operate directly with **cli**, and enter
**orchestrator** only after the visible lead and live namespace are verified.

## Installing and invoking

Install the marketplace once; the skills are then discovered automatically.

- **Claude Code:** `/plugin install amq-squad@amq-squad`, then invoke
  `/amq-squad:wizard`, `/amq-squad:cli`, or `/amq-squad:orchestrator`.
- **Codex:** install the Codex marketplace, then invoke a skill as `$<skill>` —
  e.g. `$wizard`, `$cli`, or `$orchestrator` from the amq-squad plugin.

The primary skills are user-invocable and require the `amq-squad` binary on
`PATH` (`go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest`).
Their workflows are equivalent across the two marketplaces; the platform
metadata differs (a Claude skill carries `trigger` / `allowed-tools` /
`argument-hint`; the Codex skill omits them).

---

## Preparation with `amq-squad:wizard`

**Invoke:** `/amq-squad:wizard` (Claude) · `$wizard` (Codex). **Use when:** no team exists yet, or you are starting a
new piece of work and want a real goal/brief in place before launch.

It is a **wizard**: it walks five steps and confirms with you at each gate.
Setup stays read-only until the final create step (no live launch).

### The five steps

1. **Capture the goal (any source).** Tell the skill where the goal lives and it
   fetches it with whatever tool the agent has, detecting the source type:

   | You give it | It fetches with |
   | --- | --- |
   | a one-line prompt / inline text | the text itself |
   | a local file / path (`./design.md`) | reads the file |
   | a GitHub issue (`#96`, a URL, `owner/repo#96`) | `gh issue view` |
   | a GitHub PR | `gh pr view` |
   | a Jira key (`PROJ-123`) | the Atlassian MCP / a `jira` CLI |
   | a URL (Confluence / doc) | the Atlassian MCP / a fetch tool |

   Tracker integrations live **in the skill**, never in the amq-squad binary —
   the core stays tracker-neutral. If no integration is available, the skill
   asks you to paste rather than inventing content.

2. **Draft and confirm the brief.** The skill normalizes whatever it fetched
   into one canonical shape and **shows it to you to edit before saving** (a raw
   ticket description is not a brief):

   ```md
   # <session> brief
   ## Goal          # the outcome, 1-2 sentences
   ## Source        # JIRA PROJ-123 / gh#96 / URL / file: / "operator prompt"
   ## Scope
   ## Out of scope
   ## Acceptance     # how we know it's done; who signs off
   ```

3. **Roles, profile, and tool policy.** Pick the roster (built-in personas or
   custom roles), the binary behind each (`codex`/`claude`), the team-home, and
   whether to use the default profile or a named one. The recommended tool
   policy keeps the visible lead broad and assigns every worker its built-in
   catalog minimum (for example `coding`, `browser`, `data`, or `minimal`).
   `full_all` is an explicit opt-in, never the default. With two or more
   `full` members, review warns that each broad agent duplicates MCP/plugin
   context and increases memory and concurrency pressure; keep the recommended
   split unless the work actually requires broad access for every member.

4. **Orchestrated? Who leads?** Default is **no** (a flat, peer-to-peer squad).
   Say yes and name exactly one lead role to run an orchestrated squad.

5. **Review and create.** The skill prints a summary, then on your confirmation
   creates `team.json` + `team-rules.md`, saves the brief, writes the pointer
   stubs, validates, and prints the next commands. For a squad with two or
   more Claude members it also asks whether the **workers** should run with a
   trimmed plugin/hook surface and, if so, generates + wires the context
   overlays in one command (`amq-squad team overlay init --workers ...`,
   v1.9.0+) — the lead keeps the full configuration.

### What it runs

```sh
# flat team
amq-squad new team --roles cto,fullstack,qa --binary cto=codex --sync

# orchestrated team (step 4 said yes, cto leads)
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto --sync

# preview anything first
amq-squad new team --dry-run --json --roles cto,fullstack,qa --orchestrated --lead cto
```

`--orchestrated [--lead ROLE]` records the lead in `team.json` and writes a
generated `## Orchestration` reporting norm into `team-rules.md` when that file
is first seeded — a structured flag, not pasted prose, so it can't drift. (An
existing `team-rules.md` is left untouched; regenerate with `amq-squad team
rules init --force`.) Default off; exactly one lead; the lead is a team member,
never the operator.

When preparation is accepted, the wizard presents the separate default-No live launch approval.

---

## `amq-squad:cli` — direct live-team operations

**Invoke:** `/amq-squad:cli` (Claude) · `$cli` (Codex)
**Use when:** `.amq-squad/team.json` already exists and you want to run the team.

This is the everyday skill. The lifecycle is one small state machine:

```
(none) --up--> running --stop--> stopped --rm/archive--> (none)
                  ^                  |
                  +----- resume -----+
```

### The daily loop

1. **Orient.** Confirm the team-home + profile + workstream, then read the
   selected namespace's brief.
2. **Discover live state.** `amq-squad status` (board), `status --session <name>`
   (detail), `amq-squad console` (live TUI), `amq-squad doctor` (health,
   including PATH binary, Codex/Claude plugin cache, and skill-marker
   alignment). `status --json` and `doctor --json` expose the same alignment in
   `data.versions`, and `up` warns before launch when a detectable mismatch
   would make workers inherit different instructions or binaries.
3. **Bring members up.** `amq-squad up <session>` (NEW work; refuses an existing
   session), or `resume` to continue one.
4. **Route + drain.** Hand off over AMQ, request reviews, drain your inbox.
5. **Stop / fork / tear down** as the work finishes.

### Key verbs

| Goal | Command |
| --- | --- |
| Bring the team up on new work | `amq-squad up <session>` |
| One window/tab per agent | `amq-squad up <session> --target new-window` |
| Preview the launch plan | `amq-squad up --dry-run [--json]` |
| Continue an existing session | `amq-squad resume` (`--exec` to open) |
| Stop members (state preserved) | `amq-squad stop --role R` / `--all` |
| Branch a fresh workstream | `amq-squad fork --from <cur> --as <new>` |
| Multi-session board | `amq-squad status` (or bare `amq-squad`) |
| Single-session detail | `amq-squad status --session <name>` |
| Live Mission Control TUI | `amq-squad console` (`--once` for CI) |
| Run and retain task-bound command evidence | `amq-squad evidence run TASK --me ACTOR --subject TEXT --attempt-id ID -- COMMAND` |
| Poll with a stable terminal result | `amq-squad monitor --session S --timeout 30m --max-ticks 60 --json` |
| Build an exact read-only release plan | `amq-squad verify release-plan ... --json` |
| Trim worker context (overlays) | `amq-squad team overlay init --workers [--disable-plugins ids] [--disable-all-hooks]` |
| Tear down (destructive / recoverable) | `amq-squad rm <s>` / `amq-squad archive <s>` |

`evidence run` executes argv directly without a shell and requires the active
structured task assignee. It binds the canonical project/profile/session, exact
task digest, executable bytes, cwd, bounded explicit environment, and attempt
identity before publishing immutable process, outcome, and summary records.
The task link is compare-and-swap; replaying the same attempt returns the
original result only for the same complete request. Use bounded `evidence show`,
`list`, and `lookup` projections for inspection and `evidence recover` for an
explicit interrupted-finalization pass. Any AMQ report follows only the task's
recorded dispatch route and is a separate, non-destructive step.

Monitor suppression is exact: the heartbeat must come from the admitted
profile/session and match the canonical task and assignee. Only enumerated
phases are accepted; coding and testing use bounded extended windows, while
malformed, mismatched, arbitrary, stale, or future-skewed claims never
suppress. Loops have finite defaults and end with a schema-versioned
`monitor_final` snapshot and explicit `exit_reason`; consumers must not infer a
result from NDJSON ordering.

Task completion atomically binds its generation, canonical DONE report intent,
and any exact task-scoped gate request. An open human decision is recorded as
preserved and unsuppressed. Only an already answered, closed, withdrawn, or
durably superseded request generation may clear attention; exact repeats are
idempotent and different request identities conflict.

`verify release-plan` performs no git, GitHub, network, authorization, or file
mutation. It freezes the canonical absolute project/worktree, exact repository
and remote URL identity, branch, protected/default action taxonomy, candidate
SHA, non-force branch/tag refspecs, tag annotation/signing, immutable preflight
evidence, and repository-bound noninteractive release title/notes/draft/
prerelease/latest policy. Every git command uses `git -C <project>`. Before any
push, verification requires the named remote to resolve to the frozen URL;
file-based notes freeze a canonical path and exact SHA-256 verification step.
Its literal-newline `Action:` / `Target:` bodies are independently exact.
Verification separately requires an annotated local tag object, its peeled
candidate commit, a valid signature when signed, the remote branch SHA, a
remote tag object distinct from the commit, and the remote peeled candidate
SHA.

For a narrow Claude-only permission exception, configure a member's
`permission_allowlist` in `team.json`, for example
`["Bash(amq-squad review-worktree remove:*)"]`. The grant is scoped to that
exact role,
merged with any explicit native `--allowedTools`, visible in dry-run JSON and
launch history, and rebuilt from current policy on resume so removed/narrowed
grants are revoked. Validation rejects other binaries and values beginning with
`-`; generated grants use one `--allowedTools=<grant>` token. The
`--no-preauthorize-inscope` bit also round-trips. Preview commands keep
launcher policy out of executable child argv; `agent up` recomputes it and the
launch record stores launcher-owned versus explicit provenance separately,
including equal-valued grants. Prefer scratch/worktree-
specific patterns over broad command grants. An allowlist grants native tool
permission; it does not override the generated team rules' `## Workspace
Safety and Cleanup` prohibition on `rm -rf`. Such profiles write schema 4;
profiles without the field stay schema 3. Upgrade all readers/writers to v2.20+
before configuring it: pre-v2.20 binaries can silently ignore the field and
lossily rewrite the profile. Use `amq-squad doctor` to detect version skew.

Per-member `claude_args` / `codex_args` in `team.json` (v1.8.0+) carry native
CLI args for one member only — the overlay verb above generates the flagship
case (a `--settings` overlay that trims a worker's plugins/hooks) and wires it
for you. Plan emission fails fast when a referenced `--settings` file is
missing. AMQ floor (v2.20.0+): amq-squad requires amq 0.42.1+. AMQ 0.42.1 is
the first supported complete identity-pin contract. The minimum 0.42.1
compatibility floor is unchanged. This release is explicitly validated against
pinned 0.43.1; latest remains a forward-compatibility canary. After upgrading
AMQ, stop and resume/relaunch agents so their parent shells refresh the complete
identity tuple; a child command cannot repair stale injected environment.
Default profiles use `AM_ROOT=AM_BASE_ROOT/AM_SESSION` with a non-empty
`AM_SESSION`; named profiles use an exact root with `AM_ROOT=AM_BASE_ROOT` and
omit `AM_SESSION`. Run
`amq-squad doctor` before resume if it reports a legacy or inconsistent pin.
Launches pass
`--require-wake` so a launch fails immediately when the wake sidecar cannot
acquire its lock (`--no-require-wake` opts out and persists into resume).
Use `--wake-inject-mode none` for permission-prompt workflows that require AMQ
wake notices with zero synthetic terminal input; normal notices go to wake
stderr and urgent notices add one bell. It cannot be combined with injector
flags. The zero-input contract also suppresses dispatch's last-resort pane
nudge (even with `--force`) and the delayed Claude `/rename` command. Records
written with mode `none` must not be resumed by an amq-squad binary older than
v2.20.0, because older replay code does not understand that safety contract.
Run `amq-squad doctor` and resolve any binary/plugin/skill version skew before
`resume --exec` or `agent resume`. Mode `none` governs automatic injection paths:
the wake sidecar, dispatch's pane nudge, and delayed Claude `/rename`. It does
not disable deliberate operator control actions such as `amq-squad send` or
native `/goal` pane delivery. Layout startup send-keys that launch the process
before the agent becomes active are outside this runtime injection contract.
Use `--no-gitignore` on `agent up`, `up`, or `up --dry-run` when AMQ coop
auto-init should leave `.gitignore` unchanged; the opt-out is persisted in the
launch record and replayed by `agent resume`.
Namespace safety (v2.16.0+): mutating commands with `--session` fail closed
when an unprofiled default-profile write would collide with a named profile
that already owns that session. Rerun with `--profile <name>` to target the
named namespace, or `--profile default` to intentionally write the legacy
default root.
Operator-gate escalation (v2.16.0+): unanswered `gate/<topic>` asks addressed to
the configured operator handle escalate from `initial` to `reminder` after 30m
and `strong-warning` after 2h. `amq-squad notify` bypasses its normal throttle
when the escalation band advances, while `status --json` warnings and
`console --once` make aged gates visually distinct.

### Operator primitive decision table

| Intent | Use | Why |
| --- | --- | --- |
| Supervise a squad | `amq-squad status`, `console`, `task`, `collect` | Resolves the project/profile/session and shows the squad model. Use `collect` for lead-side reports when raw AMQ would say `refusing collect` of a `lead-owned mailbox`; it follows the #322 collect-vs-drain contract. |
| Tell a live visible lead something now | `amq-squad send --session S --role lead --body-file ./prompt.md` | Tmux pane delivery to the recorded pane. It is **not** a durable AMQ protocol message: no `--kind`, no `--thread`, no mailbox receipt. |
| Assign durable work and wake the recipient | `amq-squad dispatch --session S --role worker --kind todo --subject "..." --body-file ./task.md` | Queues durable AMQ in the resolved workstream root and wakes or nudges the agent to drain it. This is the usual lead-to-worker path. |
| Read or write AMQ mailboxes directly | Raw `amq send/read/drain/thread` only inside the correct coop/session shell, or with explicit `--root`; otherwise prefer `amq-squad amq ...`. | Raw AMQ is mailbox plumbing. From an external pane, the wrong root can trigger the same class of namespace problem as #328: `implicit default-profile mutation`, `legacy/default session root`, or `refusing before write`. |

For orchestrated squads, the operator normally talks to the visible lead with
`amq-squad send` or an operator directive; the lead uses `task`, `dispatch`, and
`collect` for workers. A raw `amq send --session ...` from an external pane is
ambiguous for named-profile squads because it may write the default
`.agent-mail/<session>` while workers drain `.agent-mail/<profile>/<session>`.
Use `amq-squad amq send --project <project> --profile <profile> --session <S>
...` or raw `amq send --root <project>/.agent-mail/<profile>/<session> ...`
when direct mailbox plumbing is intentional.

Claude-binary agents launched in tmux also get a best-effort delayed
`/rename <role>-<session>` injection, including managed `resume --exec` /
`agent resume` replay, except when wake injection mode is `none`. Failure to
deliver the rename does not block launch.
Codex agents are unaffected because Codex has no matching slash command.

Model/binary guidance (context-stamped 2026-07-10, current operator setup;
setup-dependent, not universal): defaults are not limits; escalate model or
effort when output quality misses the bar. For shippable work,
`intelligence > taste > cost`, with cost only a tie-breaker. Bulk/mechanical
work defaults to Codex CLI on `gpt-5.6-luna`; everyday balanced implementation
defaults to `gpt-5.6-terra`; frontier implementation and independent review
default to `gpt-5.6-sol`. Raise to terra/sol when quality misses the bar.
`gpt-5.5`, `gpt-5.4`, and `gpt-5.4-mini` remain valid Codex choices for
previous-frontier, strong everyday, and small/fast work respectively. UI, copy,
API, and product design need taste `>= 7`. Never use Haiku. Configure direct
agents with `binary`, `model`, Codex effort through `codex_args`
(`-c model_reasoning_effort=<level>`), and Claude effort/settings through
`claude_args` (for example `--effort high`).
amq-squad does not maintain an Anthropic whitelist: Claude member `model` is
passed through to installed `claude --model <model>`, with aliases such as
`default`, `opus`, `fable`, `sonnet`, `haiku`, and full names such as
`claude-fable-5` depending on CLI/account support. Mentioning `haiku` here is
mechanical pass-through support only; the policy remains never choose Haiku for
amq-squad work. Use a thin Claude wrapper for Codex models such as
`gpt-5.6-sol` or `gpt-5.6-terra` only when a Claude-only workflow/subagent slot
forces that shape; a Claude workflow/agent `model:` parameter still selects a
Claude model only. Prefer an explicit Codex-binary member otherwise. Exact
override paths include
`amq-squad team init --model cto=gpt-5.6-sol,fullstack=fable-5`,
`amq-squad team member add plan-reviewer --binary claude --model claude-fable-5 --claude-args "--effort high"`,
`amq-squad up issue-96 --model plan-reviewer=claude-fable-5,implementer=sonnet`,
and
`amq-squad resume --session issue-96 --model plan-reviewer=opus,implementer=sonnet --exec`.

### Runtime control (tmux)

amq-squad owns the tmux control contract — drive agents by stable command, never
raw `tmux send-keys`. Control targets the recorded **pane id**, never window
names.

Use `--body-file FILE` or `--body-file -` (stdin) for wrapper bodies containing
code, commands, backticks, or `$()` syntax. Inline `--body` is only for short
plain prose because the caller shell expands it before execution. Bare
`amq send` instead uses `--body -` or `--body @file`; raw AMQ does not accept
`--body-file`.

```sh
amq-squad focus --session issue-96 --role cto                       # bring a pane into view
amq-squad send  --session issue-96 --role cto --body-file ./prompt.md # deliver a prompt + submit
cat prompt.md | amq-squad send --session issue-96 --role qa --body-file -
```

`send` stages text in a tmux paste buffer (multi-line and shell metacharacters
arrive verbatim) and has a **built-in busy-guard**: it refuses to deliver into a
mid-turn pane unless you pass `--force`.

> `amq-squad send` is **pane delivery**, not an AMQ message — it has no
> `--kind`/`--thread`. To post an inter-agent message, use `amq send ... --kind
> <kind>` (see below).

### Routing messages over AMQ

```sh
amq send --to fullstack --thread p2p/cto__fullstack --kind review_request \
  --subject "Review: rate limiter" --body @review.md
amq drain --include-body            # read your inbox
```

Valid kinds (enforced): `brainstorm, review_request, review_response, question,
answer, decision, status, todo`. **There is no `handoff` kind** — send a handoff
as `review_request` (work to take over) or `todo` (a queued task). When operator
gates are enabled, human approvals go to the operator handle on a `gate/<topic>`
thread; with `--no-operator`, follow `team-rules.md` and route human-facing asks
through the lead/CTO instead. If the operator approves a pending gate in a live
pane/chat instead of AMQ, the lead treats it as operator input, ACKs or mirrors
it on the matching gate thread without spoofing the operator handle, and checks
both the live channel and AMQ gate/inbox state before declaring the gate blocked.

---

## `amq-squad:orchestrator` — lead a squad

**Invoke:** `/amq-squad:orchestrator` (Claude) · `$orchestrator` (Codex)
**Use when:** you are the **lead** agent of an orchestrated squad — you spawn
child agents, dispatch tasks to them over durable AMQ, monitor them, handle
their reports, and own the deliverable to the human.

This is the discipline on top of the shipped runtime primitives. Routine member
coordination still belongs to `amq-squad:cli`; this skill is specifically the
lead's playbook.

For `/goal` runs, keep the operator interface as a three-step flow:

1. Preview the goal, repo, source issues, profile/session namespace, visible
   lead, proposed mutations, topology, spawn policy, validation, and gates.
2. Create or launch exactly one operator-visible project lead. Use `--lead ROLE`
   when generated commands should route through a non-`cto` lead. Register an
   existing pane only when it already proves the exact project/profile/session
   lead identity, or with explicit safe project-lead adoption from that pane;
   never adopt a global-orchestrator pane as project `cto`.
3. Monitor through that lead. Child agents stay implementation details unless an
   approval gate, blocker, release risk, or final evidence needs surfacing.
   Leads must surface blockers and approval requests immediately to the
   operator/orchestrator-visible surface, not only in a child pane or internal
   worker thread.

Team rules and custom role contracts remain part of the prompt and handoff
contract throughout the flow.

When wake is unavailable for the parent orchestrator or NOC, use a polling
contract: one `/goal` per visible lead; each lead pushes status, blockers,
approval requests, and final evidence to AMQ/NOC-visible surfaces; the parent
polls lead inboxes, gate threads, and `status --json` on a cadence. Child agents
remain internal unless the lead escalates them.
Poll with finite deadline/tick bounds and use the final snapshot rather than
stream order. Batch review by invariant and risk: low-risk docs/projections get
focused regular tests plus drift checks; medium-risk state changes add
adversarial identity/idempotence coverage and focused race tests; high-risk
authority, lifecycle, release, or recovery changes require immutable exact-head
evidence and full regular/race suites. Completion reconciliation may clear
only an exact task-scoped request generation already terminal or superseded;
it must never answer or close an unresolved human gate.
When a `global_orchestrator` owns more than one active or recently active
workstream in the same conversation, it must keep an in-conversation board and
refresh it after every poll, gate answer, spawn, stop, final report, or recovery
action. The board tracks run name, repo, profile/session, lead and pane id,
state (`running`, `gated`, `blocked`, `paused`, `stale`, `done`, `closed`),
last checked time, next poll or wake source, current gate/blocker, last action,
next action, and deterministic polling commands. Closed runs are demoted with
`next action: none - closed` so they stop competing with active gates or stale
runs.
For `poll_required=true`, prefer concrete poll commands such as
`amq-squad monitor --once --json`, scoped `status --json`, `operator status`,
`next --json`, and root-correct gate-thread reads. Recovery follows the native
amq-squad ladder first: inspect status/monitor/gates/tasks, re-nudge queued work
with `dispatch` or drain-only `send`, resume stale agents with `resume` or
`actions[]`, mark native `/goal` blockers as `paused`, and use raw
`tmux send-keys Enter` only as a recorded last resort after operator direction
or when native recovery is unavailable.
`status --json.records[].local_input` is a read-only pane-tail blind-spot
detection heuristic for managed child local approval/input prompts, not a
coordination or progress primitive. Treat `warnings[].kind=="local_input_blocked"`
as a hint to inspect or escalate the named role and pane; absence only means the
heuristic did not observe a prompt, and destructive prompts require an operator
decision or a non-destructive alternative.
Use `goal_binding` in `goal draft --json` and `status --json` to distinguish a
generated native `/goal` plan (`native_goal_pending`), verified launch-record
native binding (`native_goal`), and the explicit AMQ task + active brief +
task-store fallback (`amq_task_brief`). Recovery sends a durable AMQ directive
first; managed-pane `/goal` injection is only a follow-up when the pane is idle,
and force-interrupt requires an operator gate.

`status --json` also exposes `execution.release_readiness.merge_authority` so
clients and agents do not infer merge authority from prose. The default actor is
`visible_lead`, `worker_policy` is `workers_do_not_merge_by_default`, and the
only documented worker exception is a verifiable authorization artifact tied to
the same subject, head SHA, and gate/evidence thread. This answers who owns the
merge/lifecycle action path; exact-head review, `verify merge`, normalized
evidence, and operator gates still answer when merge-ready can be claimed.

### Composition modes and guardrails

Goal-first composition is opt-in and defaults to seeded mode once selected.
Manual rosters remain first-class, and a short goal never authorizes worker
spawning by itself:

| Mode | Contract |
| --- | --- |
| **Manual** | The operator defines the roster up front. Runtime composition is not required. |
| **Seeded** | The lead proposes each worker and waits for explicit operator approval on a stable `gate/spawn-<role>` thread before adding or launching it. |
| **Autonomous** | The operator explicitly selects `--composition autonomous` and supplies a bounded policy. The runtime may add or prune workers only inside that policy. |

Seeded mode requires a clear `APPROVED:` answer from the configured operator.
`DENIED:`, silence, emoji, or vague assent means do not spawn. A live-channel
approval must be acknowledged or mirrored onto the same durable gate thread
without spoofing the operator handle. Approval covers that spawn only; it does
not authorize implementation details, merges, releases, or other side effects.

After approval, persist the member with `team member add` and launch it through
the managed resume/up path so stop, resume, focus, and status retain a stable
runtime identity. The durable roster and task store must rebuild the team the
lead created, not merely the initial seed.

Autonomous mode is never inferred. It requires positive `max-agents`,
`max-total-spawns`, and `budget-turns`, plus an allowed-role or
allowed-role-class boundary; `idle-reap-minutes` constrains pruning. Before an
allowed spawn/prune decision returns, the runtime persists policy counters and
writes `.amq-squad/autonomous/<session>/audit.jsonl`. A prune request must carry
measured idle age, evidence that active task linkage was checked, and proof that
no active task remains linked.

Pause, resume, inspect, or permanently disable the policy without editing the
profile directly:

```sh
amq-squad team autonomous show --json
amq-squad team autonomous pause
amq-squad team autonomous resume
amq-squad team autonomous disable
```

Autonomous composition grants no authority to merge, push, tag, release,
perform destructive filesystem operations, send externally, invoke provider
side effects, or delegate child self-spawn. Those actions retain their normal
lead/operator gates and verification preflights.

### The loop: spawn → dispatch → monitor → coordinate → recover

```sh
# 1. SPAWN — window-per-agent (captures each child's pane id into the record)
amq-squad up issue-96 --target new-window

# 2. CONFIRM the children are live before dispatching
amq-squad status --session issue-96 --json \
  | jq '.data.records[] | {role, status, pane_alive: .tmux.pane_alive}'

# 3. DISPATCH — over durable AMQ (queues, survives pane death; the busy-guarded
#    `amq-squad send` pane injection is the fallback/nudge only)
amq send --to fullstack --thread p2p/cto__fullstack --kind todo \
  --subject "Task: rate-limiter" --body - --wait-for drained --wait-timeout 60s <<'EOF'
Implement the rate-limiter per the brief. When the diff is ready, push a
review_request to me (cto) over AMQ. Report any blocker as a question.
EOF

# 4. MONITOR — loop on liveness; the lead stays engaged
amq-squad focus --session issue-96 --role fullstack   # watch live when needed

# 5. COORDINATE — children PUSH reports; the lead collects safely (does not poll)
amq-squad collect --session issue-96 --me cto --timeout 120s --include-body
```

In `lead_pane` mode, amq-squad verifies the actual live roster pane before its
own blocking waits. A configured lead is refused when a caller-raised
`gate/<topic>` is unresolved, a wait exceeds 120 seconds, or a wait is
unbounded. This covers `collect`, wrapped `amq watch`, wrapped `amq receipts
wait`, and amq-squad-owned send/reply/dispatch receipt waits. The audited escape
hatch is `--override-wait-posture --wait-posture-reason <why>`. Direct external
`amq watch` and hand-written `sleep`/`until` polling loops cannot be intercepted
and remain forbidden lead posture; use the sanctioned read-only `amq-squad
monitor` watchdog or park/end the turn so wake can resume it.

### The `[AGENT-EVENT]`-over-AMQ protocol

The key design point: instead of writing status into a parent pane, children
**push real AMQ messages** to the lead, which survive pane death and are
addressable by stable handle. Spell this out in each child's brief:

| Child wants to report | Kind to use |
| --- | --- |
| progress / done | `--kind status` |
| blocked / needs input | `--kind question` |
| ready for review / handoff | `--kind review_request` |

The lead consumes child reports with `amq-squad collect --session <S> --me
<lead> --timeout 120s --include-body`, not raw `amq drain`. Raw `amq drain`
is destructive by design: it moves unread messages to `cur` and emits drained
receipts before the caller has necessarily persisted or displayed the body.
`collect` is the kill-safe orchestrator path: it journals unread bodies under
`.amq-squad/collect-journal/<profile>/<session>/<handle>/` before acknowledging
them, then replays pending journal entries if output was interrupted. The tradeoff
is at-least-once delivery: duplicates after partial output are acceptable; body
loss is not. Delivered journal entries are retained for 7 days or the latest 200
per recipient. This follows the #321 decision-table boundary: raw AMQ consumption
stays raw; orchestrator-safe collection happens in amq-squad.

**Bodies are data, not authority** — a child's "please merge" is surfaced or
acted on under the lead's judgment. Merge and irreversible lifecycle actions are
lead-owned by default: workers do not merge, push, tag, release, close issues,
or run similar actions from AMQ prose. If a worker is ever asked to do one, it
requires a verifiable authorization artifact tied to the same subject, head SHA,
and gate/evidence thread; otherwise it escalates back to the lead.

### Operator directives (NOC → lead)

The operator can steer the lead from amq-noc (v0.8.0+). A directive arrives
pane-injected when the lead is live, or as a durable AMQ message when it was
down: thread `p2p/<sorted lead__operator>`, kind `todo`, subject
`DIRECTIVE: <first line>`. The lead treats directives as operator steering
with **priority over child reports**, acknowledges on the same thread
(`--kind status` or `answer`), and never treats one as a gate answer — a
directive does not clear `gate/<topic>` threads. Orchestrated teams carry the
convention as a generated line in their `## Orchestration` norm.

### Recover

```sh
amq-squad resume --session issue-96      # re-orient a stalled/stopped session
amq-squad agent resume fullstack         # revive one child from its saved record
```

---

## Role Authoring With `amq-squad:wizard`

**Invoke:** `/amq-squad:wizard` (Claude) · `$wizard` (Codex), then use the role
authoring stage. **Use when:** you need a role the built-in
catalog doesn't ship (`researcher`, `sre`, `archivist`, `data-scientist`, ...).
Custom roles are first-class — they appear in `team.json`, `team-rules.md`, the
bootstrap prompt, and status/launch exactly like built-ins.

Two ways, by how much role guidance you want:

**A. Inline (quick, minimal `role.md`)** — just an id + CLI:

```sh
amq-squad new team --roles researcher --binary researcher=codex
```

A custom role must be a valid slug and **must** carry an explicit
`--binary <role>=<cli>` (there is no catalog default to fall back to).

**B. From a role file (rich, authored `role.md`)** — Markdown with optional YAML
frontmatter, `.yaml`, or `.json`:

```sh
amq-squad new team --role-file ./roles/researcher.md --roles cto
```

```markdown
---
id: researcher
label: Research Engineer
binary: codex
peers: [cto, qa]
skills: [/deep-research]
---
# Role: Research Engineer
## Description
Owns deep technical investigation, prototypes, and written findings.
```

The id comes from the file (`id:`, a `# Role:` heading, or the filename); the
binary from the file's `binary:` field (`--binary` overrides). The authored
document is staged at `.amq-squad/roles/<id>.md` and seeds the agent's `role.md`
at launch (never clobbering later user edits). Preview with `--dry-run --json`
before writing.

---

## End-to-end walkthrough

Shipping GitHub issue #96 with an orchestrated squad, start to finish.

```sh
# 0. (once) install the marketplace, then in your project:
cd ~/Code/my-project
```

1. **Propose and prepare** — invoke `/amq-squad:wizard` and say *"the goal is
   GitHub issue #96."* The wizard runs `gh issue view 96`, drafts a canonical
   brief, shows it for your edit, asks for roles (`cto`, `fullstack`, `qa`), asks
   *"orchestrated? who leads?"* (yes, `cto`), and renders the exact read-only
   preparation proposal. Only an explicit answer to the default-No preparation
   gate writes the accepted artifacts:

   ```sh
   amq-squad run start --project . --session issue-96 \
     --roles cto,fullstack,qa --lead cto \
     --launch-shape working-team-together --goal "fix issue 96" --prepare-plan

   # Default No: run only after accepting the proposal.
   amq-squad run start --project . --session issue-96 \
     --roles cto,fullstack,qa --lead cto \
     --launch-shape working-team-together --goal "fix issue 96" --prepare
   ```

2. **Prove readiness and launch separately** — preparation launches nothing.
   The wizard checks the accepted manifest and generated bootstraps, then shows
   a second default-No launch gate. The equivalent commands are:

   ```sh
   amq-squad run start --project . --session issue-96 \
     --launch-shape working-team-together --readiness-json

   # Default No: copy the source/digest from accepted readiness exactly.
   amq-squad run start --project . --session issue-96 \
     --launch-shape working-team-together --goal "fix issue 96" \
     --goal-source operator_goal --goal-digest 'sha256:<accepted-digest>' --go
   ```

   `--go` never repairs a brief, profile, role, rule, pointer, tool policy, or
   manifest. Drift returns to proposal and preparation.

3. **Lead the work** — the `cto` agent (its `team-rules.md` now carries the
   orchestration norm, so it loads `/amq-squad:orchestrator`) dispatches
   to `fullstack`, monitors, and drains pushed reports:

   ```sh
   amq send --to fullstack --thread p2p/cto__fullstack --kind todo --wait-for drained \
     --subject "Task: #96" --body @task.md
   amq-squad collect --session issue-96 --me cto --timeout 120s --include-body
   amq send --to qa --thread p2p/cto__qa --kind todo --wait-for drained \
     --subject "Task: review #96" --body @review-task.md
   ```

4. **Converge and tear down** — the lead verifies the artifacts, owns the merge
   and lifecycle-action path after the readiness gates align, reports up to the
   human, then:

   ```sh
   amq-squad stop --all
   amq-squad archive issue-96
   ```

## Troubleshooting

| Symptom | Likely cause / fix |
| --- | --- |
| "no team configured" | No `team.json` yet — use the `amq-squad` Setup section (or `amq-squad new team`) first. |
| `up` refuses the session | `up` is NEW work and refuses an existing session — use `resume` to continue, or `up --reset` to start over. |
| A prompt didn't reach an agent | The pane was busy — `send` refuses a mid-turn pane; re-send when idle or pass `--force` to interrupt deliberately. |
| `amq send` rejected the message | Invalid `--kind` (there is no `handoff`) — use `review_request`/`todo`/`status`/`question`. |
| The brief is a stub the board warns about | Author a real brief via the `amq-squad` Setup section. For an existing session use `amq-squad brief seed --session <session> --seed-from issue:<n> --force`; `up --seed-from` is for a not-yet-launched session. |
| Orchestration norm missing after adding `--orchestrated` | `new team` leaves an existing `team-rules.md` untouched — regenerate with `amq-squad team rules init --force`. |
| Codex loads an old amq-squad skill after upgrade | Run `amq-squad doctor`; the Codex skill-cache check warns when the released bundle is missing, stale, or only present through a compatibility symlink. Refresh the plugin/skill cache instead of relying on manual symlinks. |
| Shift+Enter doesn't submit in a tmux window | `doctor` warns when tmux `extended-keys` is off; opening under iTerm2 `tmux -CC` (the `attach_control` action) makes it work natively. |
| Custom role rejected | A custom role needs an explicit `--binary <role>=<cli>` (no catalog default). |

For anything below the skills — the binary's verbs, JSON envelopes, tmux
targets, profiles, and [cross-project teams](../README.md#cross-project-teams)
— see the [README](../README.md). Each
skill's full instructions live in its `SKILL.md` under
`plugins/{claude,codex}/skills/<skill>/`.
