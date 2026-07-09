# amq-squad

**amq-squad composes, launches, and drives teams of Claude and Codex agents that coordinate over AMQ.** Hand a lead agent a goal and it builds the team — or design the roster yourself. Orchestration is binary-neutral: a Codex agent can *lead*, not just be led.

Built on [AMQ](https://github.com/avivsinai/agent-message-queue) by [Aviv Sinai](https://github.com/avivsinai): AMQ owns messaging between agents; `amq-squad` owns the layer above — who is on the team, what role each agent plays, the shared norms they follow, and how to bring the whole squad up, stop, resume, or fork it into a new workstream.

## Contents

**Start here:** [Install](#install) · [Using amq-squad](#using-amq-squad) · [Quick start](#quick-start)

**Concepts:** [Why](#why) · [Context model](#context-model) · [Goal-first dynamic teams](#goal-first-dynamic-teams) · [Workstreams &amp; threads](#workstreams-and-threads) · [Cross-project teams](#cross-project-teams)

**Command reference:** [Verbs](#verbs) · [Status board &amp; console](#status-board-and-mission-control-console) · [AMQ diagnostics](#amq-diagnostics) · [Runtime control (tmux)](#runtime-control-tmux) · [JSON envelopes](#json-envelopes) · [Exit codes](#exit-codes) · [Shell completions](#shell-completions) · [Removed legacy verbs](#removed-legacy-verbs)

**Customize:** [Custom roles](#custom-roles) · [Trust &amp; binary defaults](#trust-and-binary-defaults) · [Messaging in a squad](#messaging-inside-a-squad) · [Files amq-squad writes](#files-amq-squad-writes)

**Reference:** [AMQ swarm interop](docs/amq-swarm-interop.md) · [Known gaps](#known-gaps) · [Requires](#requires)

## Why

AMQ is the messaging and process substrate: it sets up mailboxes, routes and threads messages, tracks presence, wakes agents, federates across projects, and execs into `claude` or `codex` via `coop exec`. What it deliberately stays out of is who is on the team and what each agent is for. AMQ does not own:

- **Roles** — which agent is the "CTO" vs "Fullstack" vs "QA" vs "PM" vs "Designer"
- **The launch contract** — what command originally launched each agent (cwd, binary, flags, workstream), so it can be stopped, resumed, or forked
- **Durable team norms** — the shared rules each agent reads at session start
- **The declared roster** — the role-mapped set of peers each agent is handed up front at bootstrap (AMQ can observe who talks to whom after the fact, via presence and threads; it does not declare the intended team a priori)

`amq-squad` owns that layer. It captures roles, roster, and norms at team-setup time (`.amq-squad/team.json`, `.amq-squad/team-rules.md`) and per-agent launch state (`launch.json` + `role.md`) in AMQ's per-agent extension-metadata namespace. AMQ stays domain-agnostic: it adds generic affordances that layers build on (the `extensions/<layer>/` namespace, external wake injection, a reserved `user`/operator handle, `env --json`), which is why amq-squad v2.17.0 requires amq 0.40.0+ — but it still knows nothing about CTOs, rosters, or team rules.

## Install

Install the 2.0 line (note the `/v2` module path):

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.17.0
amq-squad version
```

For the latest 2.x build:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest
```

Requires Go 1.25+, the `amq` binary on `PATH` (v0.40.0+), and `tmux` on `PATH` for `amq-squad up`.

### Skills (plugin marketplaces)

This repo doubles as a plugin marketplace that ships the amq-squad skills for
both Claude Code and Codex. The primary skills are `amq-squad` and
`amq-squad-orchestrator`; the older `amq-team-setup` and
`amq-squad-role-creator` entries remain as redirect stubs. The CLI and the
skills are versioned together.

Claude Code:

```sh
/plugin marketplace add omriariav/amq-squad
/plugin install amq-squad@amq-squad
```

Codex:

```sh
codex plugin marketplace add omriariav/amq-squad
codex plugin add amq-squad@amq-squad
```

The Claude marketplace manifest lives at `.claude-plugin/marketplace.json` and
the Codex one at `.agents/plugins/marketplace.json`; each points at the
binary-specific plugin under `plugins/claude` and `plugins/codex`. In Claude
Code, skills are namespaced by plugin, e.g. `/amq-squad:amq-squad`. In Codex,
invoke them by skill name, e.g. `$amq-squad`.

To dogfood the skills from this working tree (instead of the marketplace
snapshot), run `make dogfood-claude` here: it launches Claude Code with
`--plugin-dir plugins/claude`, which loads the plugin live from disk and
shadows the marketplace-installed copy for that session only. Codex has no
per-session equivalent; it always serves the marketplace snapshot (refresh
with `codex plugin marketplace upgrade` after merging skill changes).

## Using amq-squad

amq-squad has two surfaces, and you use them at different times:

- **The `amq-squad` CLI is for you, the operator.** Design a team, bring it
  up / stop / resume in tmux, inspect it (`status`, `console`, `doctor`), and
  control agent panes (`focus`, `send`). Most commands are **project-scoped**
  (`--project DIR`, default cwd); the session-oriented ones (`up`, `status`,
  `send`, `stop`, `resume`, ...) are also **session-scoped** (`--session NAME`,
  the AMQ workstream). A few (`roles`, `doctor`) are neither or project-only.
- **The skills are for the agents.** They teach a Claude or Codex agent how to
  drive amq-squad + AMQ from *inside* a session: coordinate with peers, and (as
  a lead) orchestrate a whole squad. You install them once per marketplace; the
  agents invoke them by name.

### The CLI in 60 seconds

```sh
cd ~/Code/my-project
amq-squad new team --roles cto,fullstack,qa --sync   # 1. design the team
amq-squad new session issue-96                        # 2. bring it up live in tmux
amq-squad status --session issue-96                   # 3. see who is live
amq-squad send --session issue-96 --role qa --body "run the smoke suite"   # 4. drive a pane
amq-squad focus --session issue-96 --role qa          #    watch that agent work
amq-squad stop --session issue-96 --all               # 5. tear down (stays resumable)
amq-squad resume --session issue-96 --exec            # 6. bring it back (bare resume = plan only)
```

The golden rules: **`up`/`new session` is for NEW work** (it refuses an existing
session — use `resume` to continue), **`stop` preserves state** (resumable),
and **control targets the recorded pane id**, never window names. Full surface:
[Quick start](#quick-start), [Verbs](#verbs), [Runtime control](#runtime-control-tmux).

### The skills, and when to use each

| Skill | Audience | Reach for it when |
| --- | --- | --- |
| **`amq-squad`** | operator-facing and member agents | setup, role authoring, and day-to-day coordination: capture a goal, draft the brief, pick roles/profile, sync pointer stubs, drain inboxes, route review/handoff, inspect `status`/`console`/`history`, run `up`/`stop`/`resume`/`fork`, and use tmux runtime control (`focus`/`send`). |
| **`amq-squad-orchestrator`** | a **lead** agent | running a squad as a *driver*: spawn child agents, dispatch tasks to them over durable AMQ (the busy-guarded pane `send` is the fallback), monitor liveness via `status --json`, collect children's `[AGENT-EVENT]`-over-AMQ reports, and recover. The amq-squad-native equivalent of a hand-rolled tmux spawn protocol. |
| `amq-team-setup` | redirect stub | deprecated; use `amq-squad` and its Setup section. |
| `amq-squad-role-creator` | redirect stub | deprecated; use `amq-squad` and its Role Authoring section. |

Invoke a skill in Claude Code as `/amq-squad:<skill>` (e.g.
`/amq-squad:amq-squad-orchestrator`); in Codex as `$<skill>` (e.g.
`$amq-squad-orchestrator`). For routine member work use `amq-squad`; only the
lead agent driving spawnees reaches for `amq-squad-orchestrator`.

**Deeper guide:** [docs/skills.md](docs/skills.md)
([HTML](docs/skills.html)) walks each skill end to end — when to reach for it,
how to invoke it, worked examples, a decision table, an issue-to-merge
walkthrough, and troubleshooting.

## Quick start

```sh
cd ~/Code/my-project

amq-squad roles                      # list role IDs and menu numbers
amq-squad new team --dry-run --roles cto,qa
amq-squad new team --dry-run --json --roles cto,qa
amq-squad new team --sync --session issue-96
amq-squad new profile review --roles cto,qa --sync --session review

amq-squad up --dry-run               # preview the launch plan
amq-squad up --project ~/Code/other-app --dry-run
amq-squad new session issue-96       # NEW work: bring the team up live in tmux
amq-squad new session issue-98 --seed-from issue:31
amq-squad new session --project ~/Code/other-app issue-97

amq-squad                            # bare command -> multi-session status board
amq-squad status --session issue-96  # single-session detail
amq-squad status --project ~/Code/other-app --session issue-97
amq-squad console                    # live read-only Mission Control TUI
amq-squad console --project ~/Code/other-app --once
amq-squad doctor                     # AMQ / PATH / Codex skill cache / tmux / wake / pointer sync
amq-squad doctor --project ~/Code/other-app --profile release
amq-squad doctor --project ~/Code/other-app --all-profiles
amq-squad amq env --session issue-96 # resolved AMQ root/session/handle
amq-squad amq ops --session issue-96 # AMQ operational diagnostics
amq-squad amq route --session issue-96 --me cto --to fullstack

amq-squad stop --all                 # SIGTERM the team (--force = SIGKILL); stays resumable
amq-squad stop --project ~/Code/other-app --all --session issue-97
amq-squad resume                     # re-orient (plan only; add --exec to relaunch)
amq-squad resume --project ~/Code/other-app --session issue-97
amq-squad fork --from issue-96 --as issue-96-review  # branch a fresh workstream
amq-squad fork --project ~/Code/other-app --from issue-96 --as issue-96-review
amq-squad rm issue-96                # remove a finished session (confirm-gated; or `archive`)
```

### Lifecycle

A session moves through one small state machine:

```text
(none) --up--> running --stop--> stopped --rm/archive--> (none)
                  ^                  |
                  +------ resume ----+
```

- **`up [<name>]`** means NEW work. It refuses a session that already exists — use `resume` to continue it, `up --reset` to start it over, or pick a new name.
- **`new session [<name>]`** is the create-focused alias for `up [<name>]`. It follows the same NEW-work refusal rules.
- **`new team`** is the create-focused alias for `team init`.
- **`new profile NAME`** is the named-profile alias for `team init --profile NAME`.
- **`stop`** is the primary teardown: SIGTERM the live agents (`--force` = SIGKILL), but PRESERVE all on-disk state so the session stays resumable. (The `down` alias was removed in 2.0.)
- **`resume`** re-orients a stopped session. If an agent has a saved conversation, amq-squad reattaches it; otherwise it re-runs bootstrap so the agent re-reads its brief and AMQ history. It does NOT replay prior hidden reasoning.
- **`rm` / `archive`** are the session-destructive ops. Both are confirm-gated (`--yes` to skip the prompt) and refuse a session with live agents unless `--force`. `rm` deletes the session root + brief; `archive` moves them aside, recoverable. `team rm` is separate: it removes one team profile config only.
- A **restart** is just `stop` then `up` (after `rm`/`archive`) or `resume` for the same session.

### tmux targets

`amq-squad up` defaults to `--target current-window`, which splits the tmux
window where you run the command. To bring a team up inside an existing tmux
session such as `main`, switch or attach to the desired window first, then run
`up` from that pane:

```sh
tmux switch-client -t main:6
cd ~/Code/my-project
amq-squad up --session v1-0-0-qa --terminal tmux --target current-window --layout tiled
```

Use `--target new-session --terminal-session NAME` only when you want
amq-squad to create a separate tmux session. `--terminal-session` names the new
session; it does not select an existing tmux session for `current-window`.

Panes are anchored to the pane you launch from (`$TMUX_PANE`), not to whatever
window happens to be focused, so a `current-window` launch is safe even under
iTerm2's `tmux -CC` integration: changing tabs mid-launch will not rehome the
panes onto an unrelated window. If you launch from outside tmux, `current-window`
refuses with a hint rather than guessing a target.

`up` is for NEW work and refuses a session that already exists. To continue an
existing session use `amq-squad resume`; to start it over use `amq-squad up
--reset` (destructive: it tears down and removes the session first, with a
confirmation prompt unless `--yes`). Add `--force-duplicate` only when stale
live-agent signals remain and you deliberately want a second agent alongside.

Single-agent primitives:

```sh
amq-squad agent up codex --role cto --session issue-96
amq-squad agent resume fullstack
```

For Claude-binary agents launched in a tmux pane, `agent up`, `up`, and managed
resume/replay schedule a best-effort `/rename <role>-<session>` injection after
launch so Claude Code's resume picker shows the squad role/workstream. The
launch is not blocked if that pane delivery fails. Codex has no equivalent
slash command, so Codex agents are intentionally unaffected.

The legacy verbs (`down`, `launch`, `restore`, `list`, `team show`, `team launch`) are **removed in 2.0**. Invoking one returns a usage error (exit 1); the replacements are listed in [Removed legacy verbs](#removed-legacy-verbs) and [`MIGRATION.md`](MIGRATION.md).

### Custom launchers

A managed member can be launched through a project-specific wrapper script while
still receiving AMQ identity, bootstrap, a launch record, and status/resume. Pass
`--launcher` (and optional `--launcher-args`) on `agent up`, or set `launcher` /
`launcher_args` on the member in `team.json`:

```sh
amq-squad agent up claude --role qa --session beta --team-workstream \
  --launcher /path/claude-pm-os-dev.sh --launcher-args "--pull --workspace /ws"
```

The launcher is exec'd in place of the binary. `--launcher-args` come first, then
amq-squad appends the agent's normal child args (bootstrap + defaults), so **the
launcher script must forward its trailing args to the agent** (e.g. end with
`exec claude "$@"`). amq-squad refuses with a clear error if the launcher path is
missing or not executable. `up --dry-run` prints the resolved launcher command.

### Per-member native args and context overlays

A `team.json` member may carry `claude_args` / `codex_args` — extra native CLI
args applied to **that member only**, appended after the team-level
`binary_args` so the member value wins by position. The field must match the
member's binary (`team sync` rejects a mismatch), launch records persist it,
and resume reproduces it.

The flagship use is the **context-budget overlay** for same-cwd squads: only
the lead needs the full plugin/hook surface, while workers on smaller-context
models burn tokens on plugins and per-prompt hook output they never use.
Generate and wire it in one command (v1.9.0+):

```sh
amq-squad team overlay init --workers --disable-all-hooks \
  --disable-plugins gws@workspace,document-skills@anthropics
```

This writes a Claude settings overlay per worker
(`.amq-squad/overlays/<role>.claude.json`, human-editable, never clobbered on
re-runs) and wires `claude_args: ["--settings", <path>]` into the member.
`--workers` targets every claude member (the orchestration lead is excluded);
`--role R` targets one. Launch planning (`up`, `resume`, the tmux backend)
fails fast, naming the member, when a referenced `--settings` file is missing. Codex members use
the native equivalent: a `$CODEX_HOME/<name>.config.toml` profile wired via
`codex_args: ["--profile", "<name>"]`.

## Goal-first, dynamic teams

**The shift (2.0).** Until now you *designed a team, then ran it*: `team init` authored a static roster, `up` spawned exactly that roster, and composition was frozen for the session. 2.0 inverts it — **you hand a lead a goal and the lead composes the team**, proposing, spawning, and pruning agents at runtime as the work reveals what it needs. Goal-first changes the default *mental model*; manual still works exactly as before.

The load-bearing constraint, and why this is amq-squad-native rather than "just use Claude Code Agent Teams": **orchestration is binary-neutral — a Codex agent can *lead*, not just be led.** No core primitive depends on `~/.claude/`.

Composition is a spectrum, and **manual stays the floor**:

| Mode | Who composes the team | Status |
| --- | --- | --- |
| **Manual** | You design the roster up front (`team init` / the setup wizard). | first-class, unchanged |
| **Seeded** | The lead **proposes** each spawn from the goal; the **operator approves** it over a `gate/<topic>` thread. | shipped |
| **Autonomous** | The lead spawns/prunes within an explicit policy, no per-spawn approval. | opt-in MVP |

Three binary-neutral primitives make it work, and all of them round-trip through stop/resume so a resumed session rebuilds the team the lead **built**, not the seed:

- **Mutable roster** — `amq-squad team member add/rm/list` grows or shrinks the team mid-session (atomic, file-locked, re-validated, persisted). Add `--launch --dry-run` or `rm --stop --dry-run` to preview exact runtime actions before running them.
- **Native task store** — `amq-squad task add/list/show/claim/done/fail/block/reset`: a pull-based, dependency-gated queue under `.amq-squad/tasks/<session>/` for the default profile, or `.amq-squad/tasks/<profile>/<session>/` for named profiles, so a lead of either binary decomposes the goal into claimable work.
- **Agent activity heartbeats** — `amq-squad activity set/clear`: workers can atomically publish current task/phase activity under their AMQ agent directory. `status --json` and `console` show fresh/stale/unknown activity, with task-store ownership as a weaker source-separated fallback.
- **Compose-from-goal playbook** — the `amq-squad-orchestrator` skill (in both the Claude and Codex marketplaces) drives propose → approve → `team member add` → `task add` → prune.

AMQ `swarm` interop is supported as an external notification/adoption boundary,
not as a replacement task store. See
[docs/amq-swarm-interop.md](docs/amq-swarm-interop.md) for the v2.7.0 decision.

In practice — you stand up an orchestrated squad, then the lead composes and drives it:

```sh
# You (operator): create an orchestrated team and bring it up, seeded from a goal.
amq-squad new team --roles cto --orchestrated --lead cto --session issue-96
amq-squad new session issue-96 --seed-from issue:96 --target new-window

# The cto lead loads the amq-squad-orchestrator skill and, as the work reveals needs:
amq-squad team member add fullstack --binary codex --session issue-96  # grow the roster
amq-squad dispatch --session issue-96 --role fullstack --create-task --subject "implement the fix" --body "..."
```

### Breaking changes

2.0 is a major version; the breaking surface is small and mechanical (full upgrade notes in [`MIGRATION.md`](MIGRATION.md)):

- **Removed verbs** — `down`, `launch`, `restore`, `list`, `team show`, `team launch` now return a usage error. Use `stop`, `agent up`, `history` / `agent resume`, `status` / `history`, `up --dry-run`, and `up` respectively.
- **`/v2` module path** — install from `github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest` (note the `/v2`).
- **No data migration** — existing `team.json` (schema v3) loads unchanged.

## Context model

The context model is three durable layers. Each layer has exactly one source of truth.

| Layer | File | Owner |
| --- | --- | --- |
| Team norms | `.amq-squad/team-rules.md` | shared, hand-edited |
| Per-agent persona | `<agent-dir>/role.md` | seeded by launch, then user-editable |
| Workstream brief | `.amq-squad/briefs/<session>.md` or `.amq-squad/briefs/<profile>/<session>.md` | one per profile/session namespace |

`CLAUDE.md` and `AGENTS.md` carry a small managed pointer block that links to the three layers above; they never duplicate team-rules content. `amq-squad team sync --apply` writes and refreshes that block. Anything outside the markers is yours and is preserved.

```
<!-- amq-squad:managed:begin -->
This project uses amq-squad for agent team coordination.

- **Team norms:** `.amq-squad/team-rules.md`
- **Your role:** when launched via amq-squad, `<your-agent-dir>/role.md` carries your persona.
- **Active brief:** read `.amq-squad/briefs/<session>.md` for the current workstream (bootstrap names the exact path).

These files are the source of truth. Do not duplicate their content here.
<!-- amq-squad:managed:end -->
```

### Workstream briefs

A brief lives at `.amq-squad/briefs/<session>.md` for the default profile, or
`.amq-squad/briefs/<profile>/<session>.md` for a named profile, and carries the
goal, scope, and source-of-truth pointers for one profile/session namespace.
Every team member in that namespace reads the same file.

```sh
amq-squad up --dry-run --seed-from file:./brief.md     # preview candidate (no write)
amq-squad up --dry-run --seed-from issue:31            # preview from current-repo issue
amq-squad up --dry-run --seed-from gh:owner/repo#31    # preview from explicit GH issue

amq-squad up --seed-from issue:31                      # write brief + bring team up
amq-squad new session issue-96 --seed-from issue:31    # create-focused spelling
amq-squad up --seed-from issue:31 --force              # overwrite an existing brief
amq-squad up                                           # preserve existing brief
amq-squad brief --session issue-96                     # read the current brief
amq-squad brief seed --session issue-96 --seed-from issue:31
```

`--seed-from` semantics:

- With `--dry-run`: prints the candidate brief envelope and writes nothing.
- Without `--dry-run`: writes the selected namespace's brief and brings the team up in the same call. `--seed-from` needs `--force` to overwrite an existing brief; a bare `up` (no `--seed-from`) keeps it.

The `amq-squad` skill's Setup section wraps this in a wizard: it captures a
goal from **any** source you have — an inline prompt, a local `.md`, a GitHub
issue or PR, a Jira key, or a doc URL — fetches it agent-side (amq-squad core
stays tracker-neutral), and drafts a **canonical brief** (Goal / Source / Scope
/ Out of scope / Acceptance) for you to confirm before it is saved. The brief
is per-session.

### Goal draft

`amq-squad goal draft` is the fast, preview-first setup helper for repeated
goal-first runs. It turns a short goal, and optionally a GitHub milestone, into
a deterministic setup plan without writing files, mutating rosters, creating
tasks, sending AMQ messages, or launching agents.

```sh
amq-squad goal draft \
  --goal "deliver GitHub milestone v2.7.0" \
  --repo omriariav/amq-squad \
  --milestone v2.7.0 \
  --session v2-7-0 \
  --profile codex-v2-7-0

amq-squad goal draft --goal "fix issue 96" --session issue-96 --json
```

The draft includes a brief skeleton, proposed roster, task-store plan, seeded
spawn-gate prompts, initial dispatch prompts, and the equivalent orchestrator
`/goal --goal` prompt. Use it before the `amq-squad` Setup section or the
`amq-squad-orchestrator` skill when you want a fast starting point, then review
and explicitly approve any real setup mutations. Manual setup remains the right
path when the team shape is already known or the goal needs unusual constraints.
Execution ownership is explicit in the draft. `--mode project_lead` is the
default: a visible project-root lead owns implementation and evidence.
`--mode project_team` makes multiple project-root agents visible to the
operator. `--mode direct_lead_session` is the explicit single-session exception
where the current project-root lead may code directly. `--mode
global_orchestrator` is a control-plane session only; it previews, creates or
registers a project lead/team, routes approvals, and monitors evidence, but it
does not inspect or edit project code. `--control-root`, `--target-project-root`,
and `--target-contract` are emitted in the JSON `execution` object and generated
prompts so amq-noc can show the mutable actor and version-compatibility state.
The JSON envelope and generated brief also include `goal_binding`. Drafts prefer
native binding (`mode: "native_goal_pending"`) by emitting a visible-lead
`agent up ... -- '/goal ...'` command; `status --json` reports
`mode: "native_goal"` only after the configured lead's launch record proves that
native command was used or after `amq-squad goal deliver` records a successful
native goal-control delivery. A live project lead without native evidence reports
`mode: "native_goal_missing"`; older/runtime-fallback flows report the explicit
`mode: "amq_task_brief"`, where the binding is the durable AMQ task, active
brief, and task store.

`amq-squad goal deliver --goal TEXT --session S --role LEAD` is the
first-class control path for native Codex `/goal` delivery. It is not the same
as ordinary `amq-squad send`: normal prompts keep the busy guard, while native
`/goal` delivery may target a busy Codex lead because the runtime accepts goal
control messages safely. Successful delivery writes a delivery receipt and
updates the lead launch record's `goal_binding` source to `goal-control`, so
`status --json` can report verified native binding. When delivery starts from a
non-agent operator/global-orchestrator pane, pass
`--register-orchestrator[=HANDLE]` to add an `orchestrator` team member
(default handle `orchestrator`), register the current pane as an external lead,
and start/repair its wake sidecar before delivering the goal. This registers
the control-plane orchestrator identity only; it does not adopt that pane as the
project `cto` or other project lead.

#### Wakeable orchestrator identity in one command

From a neutral control root (a pane that is not itself a launched team agent),
a single confirm-gated command gives the orchestrator a fully wakeable AMQ
identity. No manual `team member add` or separate `lead register` is needed:

```sh
amq-squad goal start \
  --project /path/to/repo \
  --session v2-15-0 \
  --register-orchestrator=orchestrator \
  --goal "deliver GitHub milestone v2.17.0" \
  --yes
```

In one verified step this produces (1) a durable `orchestrator` team member,
(2) a launch record bound to the live control pane, (3) the orchestrator set as
lead where appropriate, and (4) a running wake sidecar targeting that pane. The
roster write is deferred until after `--yes` is confirmed (run without `--yes`,
or with `--dry-run`, to preview without mutating anything). Re-running the same
command is idempotent: it does not duplicate the member or launch record, and
re-uses the existing wake sidecar. If the wake sidecar cannot start, the command
fails loudly rather than reporting an identity it did not create.

Verify the binding with `status --json`. The envelope proves it on two surfaces:
top-level `goal_binding` reports `mode: "native_goal"` with `verified: true`, and
the orchestrator's per-member record carries `external: true` together with a
live wake signal (`status: "wake-live"`, `signals.wake_alive: true`, and
`signals.wake_pid`). The `external` field lets a client positively identify the
registered orchestrator instead of inferring it from the lead role alone.

The registered orchestrator's wake sidecar is started so that each inbound
durable AMQ message injects a drain instruction (via AMQ's `amq wake`
injector, not a raw `tmux send-keys`). The orchestrator therefore drains and
acts reactively on wake, with no periodic `amq drain` polling loop, even after
its own `/goal` reaches a terminal "achieved" state. `status --json` reports
`wake_auto_drain: true` on that member when the drain injection is configured;
this is a configuration signal, so a dead sidecar still surfaces as degraded
(`status` not `wake-live`, `signals.wake_alive: false`) rather than silently
appearing healthy. The virtual operator (`user`) handle is non-runnable and
stays poll-only (`operator_delivery.poll_required: true`); auto-drain on wake
applies to the orchestrator and lead handles, not the operator mailbox.

To keep that wake-driven loop from stalling on routine permission prompts, an
amq-squad-launched **orchestrated Claude worker** (a configured non-lead role) is
launched with `gh pr create` pre-authorized, so creating its PR never blocks on a
prompt. In this slice **PR creation only** is pre-authorized; feature-branch
`git push` may still prompt and is tracked as future work (it needs a constrained
wrapper that can enforce current-branch / no-tags / no-extra-refs before it can be
safely allowlisted). Pushes to default/protected branches, tags, GitHub releases
(`gh release create`/draft/publish/edit/upload), external sends, and destructive
git are high-risk actions. Leads must run `amq-squad verify action` with the
exact action and target before executing them, regardless of trust profile. The
active allowlist is recorded on the launch record and surfaced in `status --json`
as `preauthorized_actions` for audit. Pass `--no-preauthorize-inscope` to opt
out; Codex workers and lead/operator sessions are unaffected.

For an Autonomous preview, opt in explicitly and include a bounded policy:

```sh
amq-squad goal draft \
  --goal "deliver GitHub milestone v2.7.0" \
  --session v2-7-0 \
  --composition autonomous \
  --max-agents 4 \
  --max-total-spawns 3 \
  --allowed-roles goal-dev,runtime-dev,cli-dev \
  --budget-turns 20
```

Autonomous mode is never inferred from the goal text. A profile must be
orchestrated and must declare `--composition autonomous` with positive
`--max-agents`, `--max-total-spawns`, and `--budget-turns`, plus either
`--allowed-roles` or `--allowed-role-classes`. `amq-squad status --json` and the
status board expose the effective policy, counters, and remaining budget.

Pause or permanently shut off an autonomous profile without editing JSON:

```sh
amq-squad team autonomous show --json
amq-squad team autonomous pause
amq-squad team autonomous resume
amq-squad team autonomous disable
```

Autonomous only covers composition decisions inside that policy. It does not
authorize merges, pushes, releases, destructive filesystem actions, external
communications, provider side effects, or child-agent self-spawn authority.
Those still require the normal operator/lead path. The runtime authorization
path records JSONL audit evidence under
`.amq-squad/autonomous/<session>/audit.jsonl` and persists policy counters
before returning an allowed spawn/prune decision. Prune requests must include
measured idle age, explicit evidence that active task linkage was checked, and
no linked active tasks; `--idle-reap-minutes` sets the minimum idle age before
pruning is allowed.

High-risk actions carry a bound operator gate. Before a default/protected branch
push, tag creation/push, GitHub release draft/publish action, or external send,
run:

```sh
amq-squad verify action \
  --project <repo> --profile <profile> --session <session> \
  --gate <topic> --action github_release \
  --target "draft v1.41.0 release for owner/repo" --json
```

The matching gate question and operator answer must both reference the same
`Action:` and `Target:`. `verify action` exits `0` only for an approved bound
answer from the configured operator handle on that exact `gate/<topic>` thread;
it exits `10` for pending, `11` for denied, `12` for no matching gate, and `13`
for an unbound or mismatched answer. Its JSON result includes `action`, `target`,
`gate`, `decision`, `answered_by`, and `message_id`. If the operator approved in
a live pane, resolve the board with `amq-squad operator answer --gate <topic>
--to <lead> --approved --reason "Action: <kind>\nTarget: <exact-target>"`;
p2p prose and mirrored ACKs do not satisfy the guard.

This is a callable boundary, not command interception. A lead or wrapper that
never calls `verify action` is not blocked by the operating system, shell, Git,
or GitHub CLI. The guard's threat model is confused or overreaching agents that
use the normal amq-squad CLI path; it is not a tamper-proof security boundary
against an actor with direct filesystem write access to AMQ mailboxes, who can
forge message bodies, `Created` timestamps, or message IDs. The harder
enforcement path is a PATH shim or launch-hook
interceptor that wraps `git`, `gh`, and external-send commands and forces this
check before execution. That follow-up is tracked separately in #354 and should be
referenced from release-hardening work.

Releases carry one additional evidence gate. Before any publish step (`git push`
of the release commit, `git tag`, `gh release create`), `amq-squad verify
release --evidence <file>` requires the **final assembled release commit SHA** to
carry both a **developer co-sign** (a second actor,
`cosign.distinct_from_release_lead: true`, `cosign.sha` equal to the release
commit SHA — a co-sign on an earlier per-delta SHA is rejected as stale) and an
**approved operator release gate**. The two are non-substitutable: an "APPROVED
to release" operator decision alone never authorizes publish without the
exact-SHA developer co-sign, and the co-sign never substitutes for the operator
gate. `verify release` only validates normalized evidence; it never pushes,
tags, or releases — those remain operator-performed. The release commit's own
final SHA must pass this gate before it becomes immutable.

### Profiles (schema 3)

Profiles let one team-home hold parallel team shapes (for example a release team and a research team).

```sh
amq-squad new team --session issue-96        # default profile -> .amq-squad/team.json
amq-squad new profile release --session qa   # named profile -> .amq-squad/teams/release.json
amq-squad team profiles                      # list configured profiles
amq-squad team profiles --project ~/Code/app # list another team-home
amq-squad team profiles --json
amq-squad team rm --profile release          # delete one profile config only
amq-squad up --profile release               # operate on a named profile
amq-squad doctor --profile release           # check that profile's config, wake, and pointer sync
```

New `team.json` writes use `schema: 3` (the JSON key in persisted team profiles is `schema`; `schema_version` is reserved for the read-only JSON command envelopes documented below). Schema 1/2 profiles without an `operator` field are still supported and treated as implicit non-runnable `user` operator-gate teams until they are rewritten. Omit `--profile` (or pass `--profile default`) for the default profile.

Default-profile storage remains backward compatible: `.agent-mail/<session>`,
`.amq-squad/briefs/<session>.md`, and `.amq-squad/tasks/<session>`. Named
profiles are storage-isolated under `.agent-mail/<profile>/<session>`,
`.amq-squad/briefs/<profile>/<session>.md`, and
`.amq-squad/tasks/<profile>/<session>`. Status, thread/threads, notify,
dispatch, task, brief, launch, resume, and lifecycle commands resolve against
the selected profile/session namespace rather than scanning a sibling legacy
session root.

Namespace-safety rule: mutating commands with `--session` in a multi-profile
project should pass `--profile`. If an unprofiled mutation would write the
default profile while a named profile already owns that session, amq-squad
fails closed before writing and tells you to rerun with `--profile <name>` or
the explicit escape hatch `--profile default`. `status --session <name>` stays
read-only and warns when the default view is shadowed by a named profile.

Schema 3 adds an optional virtual operator participant for human gates:

```sh
amq-squad new team --roles cto,qa                 # default operator handle: user
amq-squad new team --roles cto,qa --operator ops  # custom operator handle
amq-squad new team --roles cto,qa --no-operator   # explicit opt-out
```

The operator is not a runnable team member. AMQ 0.38.0+ reserves the conventional `user` mailbox for this human/operator role, and amq-squad v2.17.0 requires AMQ 0.40.0+ overall; custom operator handles use the same amq-squad protocol. JSON discovery derives `operator` and `capabilities.operator_gates`; `capabilities` is not persisted in `team.json`.

Operator gates are structural AMQ handoffs, not authentication. Send human-only decisions or manual actions to the configured operator handle on stable `gate/<topic>` threads, with `--kind question --subject "APPROVAL: <decision>"` for approvals and `--kind decision --subject "DONE: <goal>"` for manual closeout. The operator replies on the same thread with `--kind answer` and subjects such as `APPROVED:`, `DENIED:`, or `ANSWER:`. If the operator answers a pending gate in a live pane/chat instead of AMQ, the lead treats it as operator input, immediately ACKs or mirrors it on the matching `gate/<topic>` thread without spoofing the operator handle, and checks both the live channel and AMQ gate/inbox state before declaring the gate blocked. P2P prose like "pending operator" is evidence only; it is not a gate. `amq-squad notify` surfaces new or stale needs-you gates with inspect/respond commands and de-duplicates unchanged items, but notification output never authorizes or clears a gate.

Unanswered operator gates age through visible escalation bands: `initial` immediately, `reminder` after 30 minutes, and `strong-warning` after 2 hours. The age is measured from the last unanswered operator-facing gate message on the `gate/<topic>` thread, so re-raising a decision starts a fresh clock while later status chatter does not hide the pending gate. `notify` records the last emitted escalation band and bypasses normal de-duplication when a gate crosses into `reminder` or `strong-warning`; `status --json` emits `data.warnings[]` for aged gates, and `console` labels them as `needs-you/reminder` or `needs-you/strong-warning`.

### Orchestration (opt-in)

By default a squad is flat: members coordinate peer-to-peer over AMQ. You can instead run an **orchestrated** squad, where one lead agent spawns, dispatches, and monitors the others and owns the deliverable. It is wired by a structured flag, not by hand-edited prose, so it cannot drift:

```sh
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto
```

This records `orchestrated`/`lead` in `team.json` and injects a generated `## Orchestration` reporting norm into `.amq-squad/team-rules.md`: the lead loads the `amq-squad-orchestrator` skill, dispatches durable AMQ tasks, and children push ACK/start, progress, blockers, review requests, and DONE reports back to the sender/lead over AMQ. Default off; **exactly one lead**; the lead is a team member, **never the operator**. `--lead ROLE` implies `--orchestrated`; with `--orchestrated` alone a single-member team self-selects and a team with a `cto` defaults to `cto`. The `team_profile_plan` / `team_plan` JSON envelopes carry `orchestrated`/`lead`. If `team-rules.md` already exists, `new team` leaves it untouched, so regenerate with `amq-squad team rules init --force` to pick up the norm.

Execution modes make the ownership boundary machine-readable:

- `global_orchestrator`: control-plane only. It may preview, select a target project/profile/session, create or register a project lead/team, route gates, poll when wake is unavailable, and report evidence. It must not inspect or edit project code directly unless explicitly converted to `direct_lead_session`.
- `project_lead`: one visible project-root lead owns implementation, tests, child delegation, blockers, and final evidence. This is the default orchestrated project execution mode.
- `project_team`: a visible project-root team with a visible lead and visible members. Use it only when the operator intentionally wants to inspect multiple project agents.
- `direct_lead_session`: the current session is explicitly the project lead and may mutate the target project. This is appropriate for single-project quick work from the project root, not for NOC/control-root workflows.

Because `target_project_root` is where the project lead actually edits code — and from a neutral control root there is no reliable `owner/repo` → local-path mapping — amq-squad never silently guesses it for a `global_orchestrator` run. `team init --mode global_orchestrator` **requires an explicit `--target-project-root`** and refuses to default to the current directory. `goal draft` classifies the value in `target_project_root_source`: `provided` (explicit), `resolved_unconfirmed` (exactly one local checkout matched by git remote `owner/repo` under the control root — a proposal that still must be confirmed or passed explicitly), `unresolved` (no single match; pass `--target-project-root`), or `default` (non-global mode, the lead runs inside the project). A `resolved_unconfirmed` match is a hint, not a confirmation.

`team init --mode ...` persists this contract in the profile, and `goal draft` generates matching `team init` and visible-lead prompts. `status --json` and the multi-session board JSON expose `execution.mode`, `control_root`, `target_project_root`, `visible_lead`, `visible_team_members`, `mutable_actor`, `implementation_allowed`, `goal_binding`, `visibility_topology`, `polling_required`, `mode_error`, and `version_compatibility` so clients do not infer who is allowed to mutate files. They also expose `operator_delivery.poll_required`, `operator_delivery.durable_amq`, and `operator_delivery.wake_supported`; a virtual/non-runnable operator handle always has `wake_supported=false`, so operator-facing updates and gates require polling/draining instead of wake delivery.

Merge and lifecycle authority is explicit in the same status surface. `execution.release_readiness.merge_authority.default_actor` is `visible_lead`, and `worker_policy` is `workers_do_not_merge_by_default`: workers can report readiness evidence, but they do not merge, push, tag, release, close issues, or run other irreversible lifecycle actions from AMQ prose. The documented alternative is a verifiable authorization artifact that binds the operator/lead approval to the same subject, head SHA, and gate/evidence thread; otherwise a worker escalates the request back to the visible lead. This answers **who** owns the action path, while the exact-head review, `verify merge`, normalized evidence, and operator gate rules still answer **when** merge-ready can be claimed.

The operator normally steers the workstream through the lead/orchestrator. The operator can steer the lead directly from amq-noc (v0.8.0+), or with plain AMQ: a **directive** arrives on the lead's operator p2p thread as a `--kind todo` message whose subject starts with `DIRECTIVE:`. The lead treats directives as operator steering with priority over child reports, acknowledges on the same thread, and never treats one as a gate answer (a directive never clears a `gate/<topic>` thread). Direct operator-to-worker messages are exceptional; when they affect scope, priority, merge readiness, release state, or external actions, the worker reports them to the lead before acting or includes the lead/thread metadata in the AMQ report.

For NOC and orchestrator visibility, leads must surface blockers and approval
requests immediately on the operator/orchestrator-visible surface. Approval
requests use stable `gate/<topic>` threads; blockers use an operator-visible
status or question. A blocker or approval request is not complete if it exists
only in a child pane, internal worker thread, or hidden gate.

When the top-level orchestrator or NOC is not wake-enabled, it must poll the
visible goal leads instead of waiting for wake delivery. The operating contract
is one `/goal` per visible lead; leads push status, blockers, approval requests,
and final evidence to AMQ/NOC-visible surfaces; the parent orchestrator or NOC
polls each lead's inbox, gate threads, and `status --json` on a cadence; child
agents remain internal unless the lead escalates them.
When a `global_orchestrator` owns more than one active or recently active
workstream in one conversation, it keeps an in-conversation board and refreshes
it after every poll, gate answer, spawn, stop, final report, or recovery action.
The board records run name, repo, profile/session, lead and pane id, state
(`running`, `gated`, `blocked`, `paused`, `stale`, `done`, `closed`), last
checked time, next poll or wake source, current gate/blocker, last action, next
action, and deterministic polling commands. Closed runs are demoted with
`next action: none - closed` so they stop competing with active gates or stale
runs. For `poll_required=true`, use deterministic commands such as
`amq-squad monitor --once --json`, scoped `status --json`, `operator status`,
`next --json`, and root-correct gate-thread reads. Recovery uses native
amq-squad paths first (`dispatch` or drain-only `send` re-nudges, `resume` or
`actions[]`, native `/goal resume` for understood blockers); raw
`tmux send-keys Enter` is a recorded last resort after operator direction or
when native recovery is unavailable.
When `operator_delivery.poll_required=true`, the same polling rule applies to
the virtual operator mailbox: lead reports, blockers, and approval gates are
durable AMQ records, and no client should claim wake support for the
non-runnable operator recipient.
`status --json` exposes `goal_binding` so the NOC can tell whether a visible
lead has verified native `/goal` binding (`native_goal`), a generated but
not-yet-launched native plan (`native_goal_pending` in `goal draft --json`), a
live project lead missing native evidence (`native_goal_missing`), or the
explicit AMQ task + brief + task-store fallback (`amq_task_brief`). Recovery for
a missing visible lead uses `amq-squad goal deliver`, which is a first-class
native `/goal` control action with delivery receipt evidence; ordinary
`amq-squad send` remains busy-guarded for normal prompts.

For an existing profile, use `amq-squad team lead set <role>` to opt into
orchestration without rebuilding the roster, `team lead clear` to return to a
flat squad, and `team lead show --json` for discovery. A lead that is already
running in an operator-owned pane can register itself with
`amq-squad lead register --role <role> --session <session>`; this writes an
explicit external launch record so `status` / `focus` / `send` can target the
pane. Project-lead registration is fail-closed: the current pane must already
prove the exact project/profile/session/role through runtime identity, matching
launch record, native goal binding, or explicit `--adopt-project-lead` from the
actual project-lead pane. A global-orchestrator/NOC pane cannot be adopted as
project `cto` merely by passing `--role cto`; status/resume report
`lead_role_boundary_violation` and tell the operator to launch/resume a real
project lead in a sibling tab/new managed pane, or keep the current pane as
global orchestrator only. By default, registration also starts or repairs the
lead's AMQ wake sidecar; pass `--wake` to make that default explicit,
`--no-require-wake` to warn instead of failing, or `--wake-inject-via` with
repeated `--wake-inject-arg` for external wake injectors. `--no-wake` remains
normal for global orchestrator/NOC pollers, but project-lead `--no-wake`
requires the explicit escape hatch `--compat-no-wake --reason <why>` and
records that reason. External lead records are visible and directable, but
lifecycle commands do not own them: `stop` reports that the pane must be stopped
manually, `rm` / `archive` leave it open, and `resume` asks the operator to run
`lead register` again instead of replaying the pane.

## Verbs

Team-level verbs:

```text
amq-squad new team [--project DIR] [--sync] [--dry-run [--json]] [team init options]
                                  Create the default team profile. Alias for
                                  team init.
                                  --dry-run previews the profile, rules path,
                                  workstream, trust, and member roster without
                                  writing files. Add --json for a
                                  team_profile_plan envelope.
                                  Add --sync to also write CLAUDE.md / AGENTS.md
                                  managed pointer stubs. --roles accepts IDs,
                                  menu numbers, or all; --session sets the
                                  initial shared workstream; --operator sets
                                  the virtual operator handle; --no-operator
                                  disables operator gates;
                                  --orchestrated [--lead ROLE] wires lead-agent
                                  orchestration (default off; one lead, a team
                                  member, never the operator);
                                  --project targets a team-home without cd.
amq-squad new profile NAME [--project DIR] [--sync] [--dry-run [--json]] [team init options]
                                  Create a named team profile. Alias for
                                  team init --profile NAME. Supports --sync,
                                  --roles IDs, menu numbers, all, and
                                  role=binary overrides, plus --session for
                                  the initial shared workstream.
amq-squad roles [--json]
                                  List built-in role IDs, menu numbers, default
                                  CLIs, and short profile copy for team creation.
amq-squad new session [--project DIR] [--profile NAME] [<session>] [up options]
                                  Create NEW work. Alias for up, with the same
                                  refusal when the session already exists.
                                  Supports --profile and --seed-from for named
                                  profiles and seeded briefs. --project targets
                                  a team-home without cd.

amq-squad team init [--project DIR] [--profile NAME] [--roles a,b|numbers|all] [--binary role=bin,...]
                     [--session ws] [--trust sandboxed|approve-for-me|trusted] [--orchestrated [--lead ROLE]]
                     [--mode project_lead|project_team|direct_lead_session|global_orchestrator]
                     [--model role=model,...] [--codex-args ...] [--claude-args ...] [--dry-run [--json]]
                                  Write a team profile and seed .amq-squad/team-rules.md.
                                  --dry-run builds and prints the profile plan
                                  without writing team.json or team-rules.md.
                                  Add --json for a team_profile_plan envelope.
                                  --orchestrated [--lead ROLE] opts the squad
                                  into lead-agent orchestration (see Orchestration).
                                  --project targets a team-home without cd.
amq-squad team rules templates
                                  List available team-rules templates.
amq-squad team rules init [--project DIR] [--profile NAME]
                     [--template auto|dev-only|product-squad|scrum|custom] [--force]
                                  Seed or refresh .amq-squad/team-rules.md.
                                  auto selects from the configured roster:
                                  dev-only for engineering teams,
                                  product-squad when product/design roles are
                                  present, scrum for Scrum accountabilities,
                                  and custom otherwise. --profile renders a
                                  named profile while keeping team-rules.md
                                  shared per team-home.
amq-squad team rules show [--project DIR]
                                  Print .amq-squad/team-rules.md.
amq-squad team lead set <role> [--project DIR] [--profile NAME]
amq-squad team lead clear [--project DIR] [--profile NAME]
amq-squad team lead show [--project DIR] [--profile NAME] [--json]
                                  Mutate or inspect the profile's orchestration
                                  lead. `set` validates that <role> is a team
                                  member and records orchestrated=true.
                                  `clear` returns the profile to flat mode.
amq-squad team overlay init (--role R | --workers) [--disable-plugins id@market,...]
                        [--disable-all-hooks] [--force] [--dry-run]
                                  Generate .amq-squad/overlays/<role>.claude.json
                                  and wire the member's claude_args to load it
                                  via --settings: trim a worker's plugin/hook
                                  surface in a same-cwd squad. --workers targets
                                  every claude member (orchestration lead
                                  excluded). Plan emission fails fast when a
                                  referenced --settings file is missing.
amq-squad team sync [--project DIR] [--apply] [--allow-outside]
                                  Sync the pointer stub into CLAUDE.md and AGENTS.md.
                                  Exit 1 on drift when --apply is not set.
amq-squad team profiles [--project DIR] [--json]
                                  List configured profiles (default + named).
amq-squad team rm [PROFILE] [--project DIR] [--profile NAME] [--dry-run] [--yes|-y]
                                  Delete one team profile config. Prompts by
                                  default and does not delete AMQ sessions,
                                  briefs, team-rules.md, or pointer stubs.

amq-squad global start [--root DIR] [--agent claude|codex] [--name WINDOW]
                       [--model M] [--codex-args A] [--claude-args A] [--go]
                                  Stand up a global/NOC orchestrator: a poller
                                  (no wake) at a neutral root that supervises
                                  many runs. Preview by default; --go opens a
                                  tmux window and launches the agent.
amq-squad run start -p PROJECT -s SESSION [--profile P] [--lead ROLE]
                    [--roles r,...] [--binary role=bin,...] [--model role=model,...]
                    [--codex-args A] [--claude-args A]
                    [--visibility detached|sibling-tabs|current]
                    [--goal TEXT] [--seed-from REF] [--go]
                                  Create one orchestrated run: wraps new team
                                  (if --roles) then up, so the namespace is typed
                                  once. Preview by default (runs --dry-run
                                  validation); --go creates. Visibility defaults
                                  to detached (hidden); supervise via status/
                                  console/monitor + wake, attach to intervene.
amq-squad up [<session>] [--project DIR] [--profile NAME] [--session ws] [--reset [--yes] [--force]]
             [--dry-run [--json]] [--seed-from file:|issue:|gh: [--force]]
             [--terminal tmux] [--target current-window|new-window|new-session]
             [--layout vertical|horizontal|tiled] [--terminal-session name]
             [--stagger 750ms] [--no-bootstrap] [--force-duplicate] [--no-gitignore]
                                  NEW work. Bring the configured team up live (tmux) or
                                  print the plan with --dry-run. REFUSES a session that
                                  already exists (use `resume`, or `up --reset` to start
                                  over). The name comes from the <session> positional or
                                  --session (both is an error); inferred otherwise. With
                                  no --seed-from the brief is AUTO-STUBBED (with a
                                  one-line notice) so CI/send-keys flows keep working.
                                  --project targets a team-home without cd.
amq-squad stop (--role R | --all) [--project DIR] [--force] [--close-panes]
                                  Stop members: SIGTERM the live, binary-matched
                                  agent PID (--force = SIGKILL), reap the wake
                                  sidecar, flip presence offline. On-disk state is
                                  preserved, so the session stays resumable.
                                  --project targets a team-home without cd.
                                  --close-panes also closes each stopped agent's
                                  tmux pane (default: keep, so final output stays
                                  readable; resume re-creates panes).
                                  (The 'down' alias was removed in 2.0.)
amq-squad resume [--project DIR] [--profile NAME] [--session ws] [--restore-existing]
                 [--exec] [--dry-run] [--force-duplicate]
                 [--no-bootstrap] [--trust sandboxed|approve-for-me|trusted]
                 [--model role=model,...]
                 [--codex-args args] [--claude-args args]
                                  Re-orient an existing session. Reattaches a saved
                                  conversation if present; otherwise re-runs bootstrap so
                                  the agent re-reads its brief + AMQ history (it does NOT
                                  replay prior hidden reasoning). Plan-only by default;
                                  classifies each member as live / restore / launch fresh
                                  / blocked and prints copy-pasteable commands. With
                                  --exec it opens them through the terminal backend, like
                                  `up`. --project targets a team-home without cd. Use
                                  `fork --from <current> --as <new>` for a NEW workstream.
amq-squad fork --from <current> --as <new> [--project DIR] [--force-duplicate]
                                  Plan fresh launches in a new workstream branched off
                                  the current one. Plan-only; does not copy launch
                                  records, briefs, conversations, or team.json. The
                                  workstream brief at .amq-squad/briefs/<new>.md is
                                  created or preserved by the subsequent
                                  `up --session <new>` (or `agent up`) live launch.
                                  --project targets a team-home without cd.
amq-squad rm <session> [--project DIR] [--profile NAME] [--yes] [--force] [--keep-panes]
                                  Permanently remove a finished session (its AMQ root dir
                                  + brief). Previews + prompts
                                  (default No) unless --yes; refuses a live session unless
                                  --force; never touches a sibling session. Closes the
                                  torn-down agents' tmux panes by default (live agents
                                  excluded); --keep-panes to leave them. --project
                                  targets a team-home without cd; --profile selects the
                                  profile/session namespace.
amq-squad archive <session> [--project DIR] [--profile NAME] [--yes] [--force] [--keep-panes]
                                  Move a finished session aside instead of deleting it
                                  (to <baseRoot>/.archive/<session>/, recoverable).
                                  Confirm-gated; refuses a live session unless --force.
                                  Closes the agents' tmux panes by default; --keep-panes
                                  to leave them. --project targets a team-home without cd;
                                  --profile selects the profile/session namespace.
amq-squad status [--project DIR] [--json]
                                  Multi-session BOARD over every discovered session
amq-squad status --session NAME [--project DIR] [--json]
                                  (rolled-up state, agent health, brief, last-activity).
                                  With --session: the single-session detail table. The
                                  bare `amq-squad` runs the board too. --project targets
                                  a team-home without cd.
amq-squad brief --session NAME [--project DIR] [--json]
                                  Print the full workstream brief and classify it as
                                  missing, stub, or real. --project targets a team-home
                                  without cd.
amq-squad brief seed --session NAME --seed-from REF [--project DIR] [--force]
                                  Write a workstream brief from file:<path>,
                                  issue:<n>, or gh:owner/repo#<n> without
                                  launching the team. Use --dry-run to preview.
amq-squad task add --title T [--desc D] [--depends-on id,...] [--assign role] [--json] --session S
amq-squad task list [--status S] [--json] --session S
amq-squad task show <id> [--json] --session S
amq-squad task claim <id> --me HANDLE [--json] --session S
amq-squad task done <id> --me HANDLE [--evidence E] [--json] --session S
amq-squad task fail|block <id> --me HANDLE [--reason R] [--json] --session S
amq-squad task reset <id> --me HANDLE [--reason R] [--json] --session S
                                  Native pull-based, dependency-gated task store
                                  under .amq-squad/tasks/<session>/ for the
                                  default profile or
                                  .amq-squad/tasks/<profile>/<session>/ for
                                  named profiles. A task is
                                  claimable only once its --depends-on tasks are
                                  completed. Terminal/reset transitions on an
                                  assigned task require the assignee's --me.
                                  All subcommands require --session.
amq-squad activity set --session S --me HANDLE --phase PHASE [--task ID] [--detail TEXT] [--profile P] [--json]
amq-squad activity clear --session S --me HANDLE [--profile P] [--json]
                                  Write or clear
                                  <amq-root>/agents/<handle>/activity.json
                                  atomically. Status and console surface this as
                                  an honest busy/current-task signal with
                                  source and quality; stale or malformed files
                                  degrade to unknown rather than progress.
amq-squad dispatch --session S --role R --subject SUBJ --body BODY [--create-task | --task ID] [--json]
                                  Queue a durable AMQ message and best-effort
                                  drain nudge. Plain dispatch stays AMQ-only;
                                  --create-task creates and links a native task,
                                  while --task links an existing task id. A
                                  task-backed dispatch auto-claims a pending
                                  task for the target handle after the durable
                                  send and task link succeed.
amq-squad lead register [--role ROLE] [--session S] [--project DIR] [--profile NAME]
                         [--wake|--no-wake] [--require-wake|--no-require-wake]
                         [--adopt-project-lead] [--compat-no-wake --reason TEXT]
                         [--wake-inject-via PATH] [--wake-inject-arg ARG]
                                  Adopt the current tmux pane as an
                                  operator-owned external lead for an
                                  orchestrated profile. The pane becomes
                                  visible/directable in status/focus/send JSON,
                                  but stop/rm/archive/resume do not kill,
                                  close, or replay it.
amq-squad console [--project DIR] [--session NAME] [--refresh 2s] [--at-risk-wait 5m]
                  [--review-age 15m] [--once]
                                  Mission Control TUI over this project. Renders
                                  to /dev/tty (stdout stays clean). --once prints
                                  a single static board to stdout for CI / non-TTY.
                                  --project targets a team-home without cd.
amq-squad notify [--project DIR] [--profile NAME] [--session NAME]
                 [--renotify-after 30m] [--dry-run] [--json]
                                  Emit de-duplicated operator attention items for
                                  live needs-you gates, with inspect/respond
                                  commands. Does not approve or clear gates.
amq-squad history [--json] [--project a,b]
                                  Restorable launch records across known projects.
amq-squad threads --session NAME [--project DIR] [--limit N] [--json]
                                  One collapsed row per AMQ thread in the
                                  workstream (read-only).
amq-squad thread --session NAME --id THREAD [--project DIR] [--include-body=false] [--json]
                                  Read one AMQ thread transcript (read-only; does
                                  not move unread mail).
amq-squad doctor [--project DIR] [--profile NAME|--all-profiles] [--json]
                                  AMQ version, AMQ ops diagnostics, the amq-squad
                                  on PATH vs this build (version skew — spawned
                                  agents inherit the PATH binary), Codex/Claude
                                  plugin cache and skill-version alignment,
                                  profile config, tmux, wake, marker integrity,
                                  and pointer-sync drift.
amq-squad amq env [--project DIR] [--session NAME] [--me HANDLE] [--json]
                                  Show the AMQ context amq-squad resolved for
                                  this project/session.
amq-squad amq ops [--project DIR] [--session NAME] [--me HANDLE] [--json]
                                  Run `amq doctor --ops` under the resolved
                                  squad AMQ environment.
amq-squad amq route --to HANDLE [--project DIR] [--session NAME] [--me HANDLE]
                    [--target-project NAME] [--target-session NAME] [--json]
                                  Explain an AMQ route before sending,
                                  including cross-project/session context.
amq-squad amq who|presence [--project DIR] [--session NAME] [--me HANDLE] [--json]
                                  Inspect AMQ sessions, agents, and presence.
amq-squad amq receipts list --me HANDLE [--project DIR] [--session NAME] [--msg-id ID] [--json]
amq-squad amq receipts wait --me HANDLE --msg-id ID [--stage drained|dlq] [--timeout 60s]
                                  Inspect or wait for AMQ delivery receipts.
amq-squad amq dlq list --me HANDLE [--project DIR] [--session NAME] [--json]
amq-squad amq dlq read --id ID --me HANDLE [--project DIR] [--session NAME] [--json]
amq-squad amq dlq retry --id ID --me HANDLE [--project DIR] [--session NAME]
amq-squad amq dlq retry-all|purge --me HANDLE [--project DIR] [--session NAME]
                                  Inspect/repair DLQ state. `read`/`retry` take
                                  `--id ID`; `purge` takes `--older-than DUR`.
                                  Mutating retry/purge commands preview and
                                  prompt by default.
amq-squad amq cleanup --session NAME --tmp-older-than 36h [--project DIR]
                                  Confirm-gated AMQ tmp cleanup for one
                                  session.
amq-squad version [--json]        Print the installed amq-squad version.
amq-squad completion <bash|zsh|fish>
                                  Print a shell completion script to stdout.
```

Single-agent primitives:

```text
amq-squad agent up <binary> [--project DIR] [--role R] [--session ws] [--team-profile NAME]
                            [--conversation ref] [--no-bootstrap]
                            [--trust sandboxed|approve-for-me|trusted] [--model NAME]
                            [--codex-args ...] [--claude-args ...]
                            [--force-duplicate] [--no-gitignore] [-- <native flags>]
                                  Launch one agent. Writes launch.json + role.md
                                  in the AMQ mailbox, injects bootstrap, then execs
                                  amq coop exec. --project targets a team-home
                                  without cd.
amq-squad agent resume <role> [--project a,b]
                                  Replay one saved launch record.
```

For `agent up`, recognized launcher flags after `<binary>` (such as `--role`, `--session`, `--trust`, `--model`, `--codex-args`, `--claude-args`, `--help`) keep flowing into the launcher; unrecognized flags and the first non-flag positional are treated as child args. Use `--` for an explicit child boundary. `amq-squad agent up codex --help` prints launcher help; `amq-squad agent up codex -- --help` passes native help to the child. `--codex-args` and `--claude-args` accept dash-prefixed values such as `--codex-args '--enable goals'`.

Global output flags work before or after the subcommand: `--quiet`, `--verbose`, `--color auto|always|never`. `NO_COLOR` overrides `--color=always`. `--quiet` and `--verbose` are mutually exclusive.

## Status board and Mission Control console

`amq-squad status` (and the bare `amq-squad`) prints a **multi-session board** over every discovered session — docker-ps / `git branch -v` style: session name, rolled-up state (running / stopped / degraded), agent health (N/M alive + at-risk), a one-line brief, and last-activity. Add `--session NAME` for the single-session detail table.

Per-agent rows may include activity from `<amq-root>/agents/<handle>/activity.json`, written by `amq-squad activity set` or cheap task-transition stamps. `status --json` exposes this under `records[].activity` with `source` (`heartbeat-file`, `task-store`, or `unknown`) and `quality` (`fresh`, `stale`, or `unknown`) so clients can distinguish an agent-written heartbeat from task-store ownership fallback.

Managed child rows may also include `records[].local_input` when a read-only pane-tail blind-spot detection heuristic sees a local approval/input prompt. This is not a coordination or progress primitive: capture failures, dead panes, and unparseable tails produce no field, so absence means "not observed", not "not blocked". When present, `data.warnings[]` includes `kind:"local_input_blocked"` with the role, pane, prompt summary, and recovery guidance; destructive prompts require an operator decision or a non-destructive alternative.

Status warnings also include aged operator gates when a poll-required operator decision would otherwise sit silently. Gate escalation bands are `initial`, `reminder` at 30 minutes, and `strong-warning` at 2 hours; warning kinds are `operator_gate_reminder` and `operator_gate_strong_warning`, with a suggested `amq-squad thread ... --include-body` inspection command.

`amq-squad console` is the project-scoped Mission Control TUI for the current team-home.

```sh
amq-squad console                    # interactive TUI on /dev/tty
amq-squad console --once             # single static board to stdout (CI / no TTY)
amq-squad console --project ~/Code/app --once
amq-squad console --session issue-96 --at-risk-wait 5m
amq-squad console --filter needs-you
```

The console gives you:

- a **board** of all sessions, grouped attention-first (needs-you > blocked > gated > at-risk > running > stopped),
- per-session **detail** with each agent's liveness and a **collapsed-thread bus** ("qa ↔ cto  blocked · subject  N msgs · 7m"),
- aged operator gates rendered distinctly as `needs-you/reminder` or `needs-you/strong-warning`, using the pending gate age rather than later thread chatter,
- **peek** (`space`) for a read-only view of an agent's recent output, unread inbox, and what it is blocked on,
- an **action palette** (`a`) with copy-ready commands such as `focus`, `send`, `resume`, `stop`, `task list`, and `dispatch`; the console never runs commands directly. This is the user-facing closure for #220.
- a **triage rollup** headline (`N needs-you threads · N blocked threads · N gated threads · N at-risk threads`) and `/`-filters (`needs-you`, `needs-user`, `gated`, `at-risk`, `blocked`, `stale-blocked`, `unread`, `agent:<h>`, `model:<m>`, `session:<n>`, `label:<l>`, `orchestrator:<o>`).

It renders to `/dev/tty`, so `stdout` stays clean for the other verbs. With `--once` it emits one static board to stdout and exits — use this when there is no terminal attached.

## AMQ diagnostics

`amq-squad amq ...` is a project-aware wrapper around AMQ diagnostics. It resolves the same AMQ root, base root, session, and handle that the squad launcher uses, then delegates to AMQ.

amq-squad v2.17.0 requires AMQ 0.40.0 or newer. That floor includes the wake-inject stale-process fix (AMQ-owned `--inject-via` wake processes exit when their recorded owner is gone or no longer matches), eval-safe `amq env --export`, the reserved human `user` mailbox behavior used by operator gates and notification surfaces, and AMQ 0.40.0's stricter queue/DLQ/receipt/wake metadata file hardening.

Read-only diagnostics run directly:

```sh
amq-squad amq env --session issue-96
amq-squad amq ops --session issue-96 --json
amq-squad amq route --session issue-96 --me cto --to fullstack
amq-squad amq who --session issue-96
amq-squad amq presence --session issue-96
amq-squad amq receipts list --session issue-96 --me cto
```

Mutating maintenance stays preview-first and confirm-gated:

```sh
amq-squad amq dlq retry --session issue-96 --me qa --id dlq_123
amq-squad amq dlq retry-all --session issue-96 --me qa --dry-run
amq-squad amq dlq purge --session issue-96 --me qa --older-than 168h
amq-squad amq cleanup --session issue-96 --tmp-older-than 36h
```

Use route diagnostics before uncertain cross-project sends, receipt waits for important handoffs, and DLQ/cleanup only when debugging stuck delivery or stale temporary files.

## JSON envelopes

amq-squad's own verbs that produce machine-readable output accept `--json` and emit a schema-versioned envelope on stdout. Diagnostics stay on stderr; stdout under `--json` is pure JSON. (Some `amq-squad amq …` wrappers emit an amq-squad envelope — e.g. `amq env --json` is kind `amq_env` — while others delegate to AMQ's own JSON.)

Team discovery payloads include derived operator metadata for external clients: `operator.enabled`, `operator.handle` when enabled, `operator.runnable=false`, and `capabilities.operator_gates`.

Goal, team-plan, status, and board payloads also include an `execution` object
when execution ownership is relevant. The mode-safe fields name the control
root, target project root, profile/session namespace, visible lead/team,
mutable actor, whether implementation is allowed, goal binding, topology,
polling requirement, mode errors, and runtime-vs-target version compatibility.
`status --json` and `doctor --json` also include a `versions` object with the
running binary, the `amq-squad` found on `PATH`, Codex and Claude plugin-cache
manifest versions, the loaded skill marker where detectable, and per-source
`matches_running` / warning details. `up` emits the same mismatch warnings before
launch when it can detect them.

```sh
amq-squad status --json | jq .
amq-squad history --json | jq .
amq-squad resume --session issue-96 --json | jq .
amq-squad doctor --json | jq .
amq-squad team profiles --json | jq .
amq-squad roles --json | jq .
amq-squad dispatch --session issue-96 --role qa --subject "Review" --body "..." --json | jq .
amq-squad task add --title "Review PR" --session issue-96 --json | jq .
amq-squad task claim t1 --me qa --session issue-96 --json | jq .
amq-squad activity set --session issue-96 --me qa --task t1 --phase testing --json | jq .
amq-squad team member add qa --binary codex --json | jq .
amq-squad team init --dry-run --json --roles cto,qa | jq .
amq-squad new team --sync --dry-run --json --roles cto,qa | jq .
amq-squad up --dry-run --json | jq .
amq-squad version --json | jq .
```

Envelope shape:

```json
{
  "schema_version": 1,
  "kind": "<verb>",
  "data": { /* verb-specific payload */ }
}
```

High-value mutating commands also support JSON success envelopes for
orchestrators and NOC tooling. Initial stable mutator envelopes include
`dispatch`, `task add`, task transitions (`claim`, `done`, `fail`, `block`),
`activity set/clear`, and `team member add/rm`. Human output remains unchanged
when `--json` is not passed. This is the user-facing closure for #222.

## Runtime control (tmux)

amq-squad owns the tmux execution/control contract for a team so external
clients (such as amq-noc) can make agents actionable without scraping tmux or
reconstructing pane layouts themselves.

When an agent is launched inside tmux, its launch record persists the **exact
tmux identity** of its pane — `session`, `window_id`, `window_name`, `pane_id`,
and how the pane was created (`target`). Pane and window ids (`%265`, `@42`) are
stable control addresses; window names are labels and are never used to target
control.

`status --session NAME --json`, `history --json`, and `resume --json` expose that
identity as a `tmux` block plus a computed `pane_alive` (does the recorded pane
still exist?). Those `status --session NAME --json` members also carry an
`actions` array of stable, project-scoped commands a client can render or copy,
each with an `available` flag. The same shared action catalog feeds the console
palette, and selected AMQ-facing commands resolve project/session/root/identity
through the shared AMQ context helper; together these are the user-facing closure
for #223:

```json
{
  "role": "cto",
  "status": "live",
  "tmux": { "session": "main", "window_id": "@42", "pane_id": "%265",
            "target": "current-window", "pane_alive": true },
  "actions": [
    { "kind": "focus",  "available": true, "command": "amq-squad focus --project DIR --session issue-96 --role cto" },
    { "kind": "send",   "available": true, "command": "amq-squad send --project DIR --session issue-96 --role cto --body-file -" },
    { "kind": "goal_deliver", "available": true, "command": "amq-squad goal deliver --project DIR --session issue-96 --role cto --goal <goal>" },
    { "kind": "resume", "available": true, "command": "amq-squad resume --project DIR --session issue-96 --exec" }
  ]
}
```

The `tmux` block is present only when the agent has a known tmux identity (absent
otherwise — e.g. an agent not launched under tmux). Its presence means "has a
recorded pane", not "is reachable": a pane that has since died still carries a
`tmux` block with `pane_alive: false`, so use `pane_alive` (and each
`actions[].available` flag) for whether control is currently possible.
`status --session NAME --json` and `resume --json` (and the `focus`/`send` verbs)
**adopt** a live agent's pane
even when it was launched *outside* amq-squad's tmux backend (a raw
`tmux new-window`): the recorded, verified agent pid is matched into a live
pane's process subtree, so `focus`/`send`/`attach_control` and `pane_alive` work
for it too. (`history --json` reflects persisted launch records only and does
not adopt.) Launching through amq-squad is still preferred (it records the role,
binary, and brief, not just a pane).

High-level control verbs target the exact pane id (falling back to a neutral
title/cwd resolver) and are all project-scoped:

```sh
amq-squad focus --session issue-96 --role cto   # bring the agent's pane into view
amq-squad focus --session issue-96              # focus the session
amq-squad open --session issue-96               # alias for focus
amq-squad send  --session issue-96 --role cto --body "please review PR #65"
amq-squad send  --session issue-96 --role qa --body-file ./prompt.md
cat prompt.md | amq-squad send --session issue-96 --role cto --body-file -
amq-squad resume --session issue-96 --exec      # relaunch the team's panes
```

`send` delivers the prompt deterministically: it stages the text in a tmux paste
buffer (via stdin, never a shell string) and pastes it into the exact pane, then
submits a single Enter — so multi-line prompts and text with quotes or shell
metacharacters arrive verbatim. It errors clearly if the target pane is gone.

## Exit codes

| Code | Meaning |
| --- | --- |
| `0` | success |
| `1` | usage / user error (unknown flag, bad argument, missing required input) |
| `2` | system / runtime error (IO, process, config, environment) |
| `3` | partial success (some targets succeeded, some failed; e.g. `stop` with mixed stopped + failed) |

## Shell completions

GitHub Actions runs `make ci` on pull requests and pushes to `main`. The workflow
installs `pandoc` so formatting, tests, generated HTML freshness, and skill
frontmatter checks run with the same baseline expected locally. This is the
user-facing closure for #221.

```sh
amq-squad completion bash > /etc/bash_completion.d/amq-squad
amq-squad completion zsh  > "${fpath[1]}/_amq-squad"
amq-squad completion fish > ~/.config/fish/completions/amq-squad.fish
```

`completion zsh` is followed by a `compinit` step on most shells.

## Custom roles

`--roles`/`--personas` accept built-in personas (`cpo, cto, senior-dev,
fullstack, frontend-dev, backend-dev, mobile-dev, junior-dev, qa, pm,
designer, scribe`) and **custom roles** that are not in the catalog. A custom role is
any valid slug (lowercase `a-z`, `0-9`, `-`, `_`) and must carry an explicit
`--binary` because there is no catalog default to fall back to. Built-in roles
keep their catalog defaults unless overridden. Custom roles are first-class
team members in `team.json`, `team-rules.md`, the bootstrap prompt,
status/history, and launch/resume.

```sh
# inline: id + CLI, minimal role.md (generic custom-role fallback text)
amq-squad new team --roles researcher --binary researcher=codex
amq-squad new team --roles researcher,reviewer --binary researcher=codex,reviewer=claude
amq-squad new profile discovery --roles researcher --binary researcher=codex
```

Missing a binary fails clearly: `custom role "researcher" requires --binary researcher=<cli>`.

For a richer persona, author a **role file** and pass it with `--role-file`
(comma-separated) or inline in `--roles`. Supported formats: Markdown with
optional YAML frontmatter, `.yaml`, or `.json`. The `binary:` field satisfies
the binary requirement (`--binary` overrides it). The authored document is
staged at `.amq-squad/roles/<id>.md` and seeds that agent's `role.md` at launch
(later user edits are preserved). A role file whose id matches a built-in
persona is rejected — pick a different id for a custom role.

```sh
amq-squad new team --role-file ./roles/researcher.md --roles cto
amq-squad team init --roles "cto,./roles/researcher.md"
```

```markdown
---
id: researcher
label: Research Engineer
binary: codex
peers: [cto, qa]
---
# Role: Research Engineer

## Description
Owns deep technical investigation, prototypes, and written findings.
```

The `amq-squad` skill's Role Authoring section walks through authoring role
files and wiring them into a team.

## Trust and binary defaults

Generated launch commands include these per-binary defaults:

- **Claude:** `--permission-mode auto`
- **Codex:** approve-for-me by default, which uses Codex Auto with `workspace-write`, `on-request`, and `approvals_reviewer="auto_review"`. Pass `--trust sandboxed` to opt into manual approval/sandbox prompts. Pass `--trust trusted` only for the local power-user profile that prepends `--dangerously-bypass-approvals-and-sandbox`.

Explicit `--trust sandboxed`, `--trust approve-for-me`, and `--trust trusted` selections are persisted in the team profile by `team init` and are re-emitted by `up`, `agent up`, `resume`, and `fork`. Combining `--trust trusted` with `--no-default-args` is rejected, as is sandboxed or approve-for-me mode with the bypass flag smuggled through `--codex-args`.

`run start` uses the same launch trust vocabulary, but its default lead autonomy
is conservative for release work: trust controls local permission prompts, not
authorization to push default/protected branches, create or push tags, draft or
publish GitHub releases, or send external communications. Those actions require
an explicit bound `verify action` approval. Autonomous release actions are an
opt-in integration concern for wrappers that deliberately call and record that
guard; they are never implied by `approve-for-me` or by starting a run without a
goal.

Pass `--model NAME` to set the native `--model` flag on Codex or Claude. `team init --model role=model,...` persists per-member models. If no explicit model is set, amq-squad looks for a global default in `AMQ_SQUAD_CONFIG`, then `$XDG_CONFIG_HOME/amq-squad/config.json`, then `~/.amq-squad/config.json`; Codex also falls back to local Codex config (`$CODEX_HOME/config.toml`, profile config when `--profile` is present, or `~/.codex/config.toml`). Supported amq-squad config keys are `model`, `codex_model`, `claude_model`, or a `models` map keyed by binary name.

### Model, binary, and effort guidance

Context stamp: this guidance reflects the current operator setup as of
2026-07-02. Availability, aliases, and pricing are deployment-dependent; treat
cost as a local tie-breaker, not as universal list-price guidance.

Defaults are not limits. Agents should escalate binary, model, or effort when
the output does not meet the bar. For shippable work, optimize for
`intelligence > taste > cost`; cost matters only after the work is good enough.

- Bulk or mechanical work defaults to Codex CLI on `gpt-5.5` in the current
  operator setup. Use lower effort for truly mechanical edits, but raise effort
  when the diff or tests show reasoning gaps.
- User-facing UI, copy, API shape, or product design needs taste `>= 7`; do
  not hand it to a purely mechanical worker just because it is cheaper.
- Plan and implementation reviews should use `fable-5` or `opus-4.8`, with
  `gpt-5.5` as an optional independent extra perspective.
- Never use Haiku for amq-squad work.

Direct amq-squad configuration keeps three decisions separate:

- `binary` chooses the runtime CLI (`codex` or `claude`).
- `model` chooses the native model for that runtime.
- Codex reasoning effort is separate from model and rides through
  `codex_args`, for example `-c model_reasoning_effort=high`. Claude has no
  Codex effort dial; use model choice plus `claude_args` such as settings
  overlays.

amq-squad does not maintain an Anthropic model whitelist. For Claude members,
`model` is passed through to the installed `claude --model <model>`, so accepted
aliases depend on that Claude CLI build and account. Current expected aliases
include `default`, `opus`, `fable`, `sonnet`, and `haiku`, plus full names the
Claude CLI accepts such as `claude-fable-5`. That is mechanical support only:
the amq-squad policy above still says never choose Haiku for amq-squad work.

amq-squad has first-class `--model`, but no first-class `--effort`. Effort stays
native to the target CLI: Claude effort rides through `--claude-args`, for
example `--effort high`, and Codex effort rides through `--codex-args`, for
example `-c model_reasoning_effort=xhigh`. The same model surface is available
on `team init --model role=model`, `up --model role=model`,
`resume --model role=model`, the `team member add` `--model <alias>` flag, and
the persisted member `model` field in `team.json`.

```sh
amq-squad team member add plan-reviewer --binary claude --model claude-fable-5 \
  --claude-args "--effort high" --session issue-96
amq-squad team member add implementer --binary claude --model sonnet \
  --claude-args "--effort medium" --session issue-96
amq-squad team member add opus-reviewer --binary claude --model opus \
  --claude-args "--effort high" --session issue-96
amq-squad team member add codex-worker --binary codex \
  --codex-args "-c model_reasoning_effort=xhigh" --session issue-96
```

Use `up` and `resume` overrides when the profile is durable but a launch needs
session-specific model choices. Resume overrides apply to members launched fresh
during that resume plan; already-live members keep their running process.

```sh
amq-squad up issue-96 --model plan-reviewer=claude-fable-5,implementer=sonnet
amq-squad resume --session issue-96 --model plan-reviewer=opus,implementer=sonnet --exec
```

For durable rosters, the same values live in `team.json`: `binary`, `model`,
per-member `codex_args` / `claude_args`, or team-level `binary_args` for every
member of one binary. Prefer an explicit Codex-binary member when the job needs
`gpt-5.5`. A thin Claude wrapper is only a compatibility pattern for
Claude-only workflow or subagent systems. In those systems, a Claude
workflow/agent `model:` parameter selects a Claude model only; it does not
select a Codex model or effort level. Keep the wrapper minimal, have it delegate
the real task to Codex CLI on `gpt-5.5`, and make that indirection visible in
the role/scope so reviewers do not mistake it for a native Claude model choice.

```sh
amq-squad team init --personas cto,fullstack --trust trusted
amq-squad team init --personas cto,fullstack --trust approve-for-me
amq-squad team init --personas cto,fullstack --model cto=gpt-5.5,fullstack=fable-5 \
  --codex-args "-c model_reasoning_effort=medium"
amq-squad agent up codex --model gpt-5.5 --codex-args "-c model_reasoning_effort=medium"
```

amq-squad v2.17.0 requires amq **0.40.0+**. Launches pass `--require-wake` to
`amq coop exec`, so a launch **fails at the door** when the AMQ wake sidecar
cannot start and acquire its lock, instead of surfacing later as a stale or
orphaned wake. `--no-require-wake` opts out for environments where wake cannot
run; the opt-out is persisted in the launch record so resume reproduces it.
Use `--no-gitignore` on `agent up`, `up`, or `up --dry-run` when AMQ coop
auto-init should leave `.gitignore` unchanged; the opt-out is persisted in the
launch record and replayed by `agent resume`.

For external-injector wake setups, pass `--wake-inject-via /absolute/injector`
and repeat `--wake-inject-arg=value` as needed on `agent up`, `up`, or
`up --dry-run` launch-plan output. `lead register` accepts the same injector
flags for externally registered orchestrator panes. For spawned agents, these
flags are forwarded to `amq coop exec`, persisted in `launch.json`, and replayed
by `agent resume`; for external leads, they are passed to `amq wake` and stored
on the external launch record.
Use the `--flag=value` form for dash-prefixed injector arguments such as
`--wake-inject-arg=--pane`.

## Workstreams and threads

- **Workstream** = AMQ `--session`. All members in one team run share it.
- AMQ session names are strict: lowercase `a-z`, digits, `-`, `_`. Use `v0-5-0`, not `v0.5.0`.
- **Threads** are focused conversations inside a workstream. Canonical p2p threads use sorted handles (`p2p/cto__fullstack`); decisions go under `decision/<topic>`.

Session (workstream) resolution: the `<session>` positional or `--session` > inference from team members and the sanitized team-home directory name. A pinned `team.json` `workstream` default still gets a one-line deprecation warning; pass `--session` (or the positional) or rely on inference instead.

## Cross-project teams

Members do not have to share a working directory. The dir where you run `team init` becomes the team-home; individual members can live in other repos.

```sh
cd ~/Code/project-a
amq-squad team init --personas cto,fullstack,qa --cwd qa=~/Code/project-b
```

`up --dry-run` emits a `cd <member-cwd>` per launch command, and `team sync --apply --allow-outside` walks each unique member cwd and writes `CLAUDE.md` / `AGENTS.md` in each one. Add `--project DIR` to sync another team-home without changing directories. Cwds outside the team-home need `--allow-outside` so a hand-edited `team.json` cannot write into unrelated folders by surprise.

For cross-project AMQ routing, each project's `.amqrc` needs a `peers` entry pointing at the other project's `.agent-mail/`. `team sync` does not touch `.amqrc`; that step is manual today.

```json
{
  "root": ".agent-mail",
  "project": "project-a",
  "peers": {
    "project-b": "/Users/you/Code/project-b/.agent-mail"
  }
}
```

## Messaging inside a squad

Once launched, agents use plain AMQ commands. `amq-squad` injects a routing block into each agent's bootstrap prompt with the live roster, handles, threads, and per-agent project context.

```sh
amq list --new
amq read --id <id>
amq drain --include-body

amq send \
  --to fullstack \
  --thread p2p/cto__fullstack \
  --kind review_request \
  --subject "Review: PR" \
  --body "Please review tests and framing." \
  --wait-for drained --wait-timeout 60s
```

`amq send` reads stdin when `--body` is omitted. There is no `--body-file` flag.

### Which messaging primitive should I use?

| Intent | Use | Why |
| --- | --- | --- |
| Supervise a squad | `amq-squad status`, `console`, `task`, `collect` | These resolve the project/profile/session and show the squad model. Use `collect` for lead-side reports when raw AMQ would say `refusing collect` of a `lead-owned mailbox`; it follows the #322 collect-vs-drain contract. |
| Tell a live visible lead something now | `amq-squad send --session S --role lead --body "..."` | This is tmux pane delivery to the recorded live pane. It is **not** a durable AMQ protocol message: no `--kind`, no `--thread`, no mailbox receipt. |
| Assign durable work and wake a recipient | `amq-squad dispatch --session S --role worker --kind todo --subject "..." --body "..."` | Dispatch queues durable AMQ in the resolved workstream root and wakes or nudges the agent to drain it, especially for lead-to-worker tasks. |
| Read or write AMQ mailboxes directly | Raw `amq send/read/drain/thread` only inside the correct coop/session shell, or with explicit `--root`; otherwise prefer `amq-squad amq ...`. | Raw AMQ is mailbox plumbing. From an external pane, the wrong root can reproduce the #328 class of namespace mistakes: `implicit default-profile mutation`, `legacy/default session root`, or `refusing before write`. |

In an orchestrated squad, the operator normally steers the visible lead with
`amq-squad send` or an operator directive; the lead uses `task`, `dispatch`, and
`collect` to coordinate workers. A raw `amq send --session ...` from an external
pane is ambiguous for named-profile squads because it may write the default
`.agent-mail/<session>` while workers drain
`.agent-mail/<profile>/<session>`. Use the `amq-squad amq` wrapper or an
explicit raw `--root` only when direct mailbox plumbing is intentional:

```sh
# Ambiguous from an external pane:
amq send --session issue-96 --to developer --thread p2p/cto__developer \
  --kind todo --subject "Task" --body "..."

# Root-resolving wrapper:
amq-squad amq send --project /path/to/repo --profile release --session issue-96 \
  --to developer --thread p2p/cto__developer \
  --kind todo --subject "Task" --body "..."

# Explicit raw AMQ root:
amq send --root /path/to/repo/.agent-mail/release/issue-96 \
  --to developer --thread p2p/cto__developer \
  --kind todo --subject "Task" --body "..."
```

Inside an amq-squad-launched shell, use bare `amq` commands. The launcher already injected `AM_ROOT`, `AM_BASE_ROOT`, and `AM_ME`; override them only when intentionally inspecting a different project or handle. For important handoffs, use `--wait-for drained --wait-timeout 60s` and keep the AMQ message id. If routing is unclear, run `amq route explain` or `amq-squad amq route --to <handle>` first.

From an external lead or operator-owned pane, prefer the root-correct wrapper:
`amq-squad amq send --session <S> --me <handle> ...`. When `--me` names a
team role/handle, the wrapper verifies that role is bound in the namespace
before sending; a global orchestrator cannot raise gates as `cto` before a real
`cto` exists. When `--me` or `--from` names the configured operator handle
(default `user`), the wrapper refuses the normal send/reply path because the
operator is mailbox-only; use `amq-squad operator answer/directive` where
applicable. Emergency recovery sends or replies as a team role or operator
handle require the explicit audited override
`--unsafe-send-as --reason <why>`.

For lead-side report collection, prefer `amq-squad collect --session <S> --me
<lead> --timeout 120s --include-body` over raw `amq drain`. Raw AMQ `drain`
is intentionally destructive: it moves unread files from `inbox/new` to
`inbox/cur` and emits drained receipts before the caller has necessarily
persisted or displayed the body. `collect` is the kill-safe path for
orchestrators: it snapshots unread bodies into a
profile/session/recipient-scoped journal before acknowledging each message.
The contract is at-least-once, not exactly-once: an interrupted output may replay
a body on the next collect, but it should not lose the body.

## Files amq-squad writes

```text
<project>/.amq-squad/team.json           Default team profile (schema: 3 on new writes).
<project>/.amq-squad/teams/<name>.json   Named team profiles (schema: 3 on new writes).
<project>/.amq-squad/team-rules.md       Durable team norms (user-edited).
<project>/.amq-squad/briefs/<session>.md Workstream brief for the default profile.
<project>/.amq-squad/briefs/<profile>/<session>.md
                                         Workstream brief for a named profile.
<project>/.amq-squad/tasks/<session>/     Task store for the default profile.
<project>/.amq-squad/tasks/<profile>/<session>/
                                         Task store for a named profile.
<project>/.amq-squad/collect-journal/<profile>/<session>/<handle>/
                                         Kill-safe collect journal. Pending
                                         entries replay until delivered; delivered
                                         entries are retained for 7 days or the
                                         latest 200 entries per recipient.
<project>/.agent-mail/<session>/          AMQ root for the default profile.
<project>/.agent-mail/<profile>/<session>/
                                         AMQ root for a named profile.
<project>/CLAUDE.md, AGENTS.md           Managed pointer block; user content outside markers preserved.
<AM_ROOT>/agents/<handle>/extensions/io.github.omriariav.amq-squad/
                                         Per-agent launch.json and role.md inside the
                                         AMQ mailbox. Legacy direct-agent records
                                         remain readable.
```

`<AM_ROOT>` is resolved via AMQ's JSON env contract (`amq env --json`).

## Removed legacy verbs

These verbs are **removed in 2.0**. Invoking one returns a usage error (exit 1, not a silent "unknown command"). Use the replacement. The full upgrade notes live in [`MIGRATION.md`](MIGRATION.md).

| Removed verb | Replacement |
| --- | --- |
| `amq-squad down` | `amq-squad stop` |
| `amq-squad launch <binary>` | `amq-squad agent up <binary>` |
| `amq-squad restore` (print) | `amq-squad history` |
| `amq-squad restore --exec --role R` | `amq-squad agent resume R` |
| `amq-squad list` | `amq-squad status` (live) or `amq-squad history` (records) |
| `amq-squad team show` | `amq-squad up --dry-run` |
| `amq-squad team launch` | `amq-squad up` |
| `amq-squad team launch --fresh --session X` | `amq-squad fork --from <current> --as X` |

Replay paths that emit copy-paste commands use the modern `agent up <binary>` command shape.

## Known gaps

- True cross-workstream sends from a setup terminal still depend on upstream AMQ semantics tracked in [avivsinai/agent-message-queue#96](https://github.com/avivsinai/agent-message-queue/issues/96). The normal team flow avoids that path by launching one workstream and routing peer conversations with `--thread`.
- Multi-cwd teams need manual `peers` config in each project's `.amqrc` for cross-project AMQ routing. `team sync` does not touch `.amqrc`.

## Requires

- Go 1.25+
- `amq` binary on `PATH` (v0.40.0+)
- `tmux` on `PATH` for `amq-squad up`
