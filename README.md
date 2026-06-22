# amq-squad

**amq-squad composes, launches, and drives teams of Claude and Codex agents that coordinate over AMQ.** Hand a lead agent a goal and it builds the team — or design the roster yourself. Orchestration is binary-neutral: a Codex agent can *lead*, not just be led.

Built on [AMQ](https://github.com/avivsinai/agent-message-queue) by [Aviv Sinai](https://github.com/avivsinai): AMQ owns messaging between agents; `amq-squad` owns the layer above — who is on the team, what role each agent plays, the shared norms they follow, and how to bring the whole squad up, stop, resume, or fork it into a new workstream.

## Contents

**Start here:** [Install](#install) · [Using amq-squad](#using-amq-squad) · [Quick start](#quick-start)

**Concepts:** [Goal-first dynamic teams](#goal-first-dynamic-teams) · [Why](#why) · [Context model](#context-model) · [Workstreams &amp; threads](#workstreams-and-threads) · [Cross-project teams](#cross-project-teams)

**Command reference:** [Verbs](#verbs) · [Status board &amp; console](#status-board-and-mission-control-console) · [AMQ diagnostics](#amq-diagnostics) · [Runtime control (tmux)](#runtime-control-tmux) · [JSON envelopes](#json-envelopes) · [Exit codes](#exit-codes) · [Shell completions](#shell-completions) · [Removed legacy verbs](#removed-legacy-verbs)

**Customize:** [Custom roles](#custom-roles) · [Trust &amp; binary defaults](#trust-and-binary-defaults) · [Messaging in a squad](#messaging-inside-a-squad) · [Files amq-squad writes](#files-amq-squad-writes)

**Reference:** [Known gaps](#known-gaps) · [Requires](#requires)

## Goal-first, dynamic teams

**The shift (2.0).** Until now you *designed a team, then ran it*: `team init` authored a static roster, `up` spawned exactly that roster, and composition was frozen for the session. 2.0 inverts it — **you hand a lead a goal and the lead composes the team**, proposing, spawning, and pruning agents at runtime as the work reveals what it needs. Goal-first changes the default *mental model*; manual still works exactly as before.

The load-bearing constraint, and why this is amq-squad-native rather than "just use Claude Code Agent Teams": **orchestration is binary-neutral — a Codex agent can *lead*, not just be led.** No core primitive depends on `~/.claude/`.

Composition is a spectrum, and **manual stays the floor**:

| Mode | Who composes the team | Status in 2.0 |
| --- | --- | --- |
| **Manual** | You design the roster up front (`team init` / the setup wizard). | first-class, unchanged |
| **Seeded** | The lead **proposes** each spawn from the goal; the **operator approves** it over a `gate/<topic>` thread. | shipped |
| **Autonomous** | The lead spawns/prunes within guardrails, no per-spawn approval. | future |

Three binary-neutral primitives make it work, and all of them round-trip through stop/resume so a resumed session rebuilds the team the lead **built**, not the seed:

- **Mutable roster** — `amq-squad team member add/rm/list` grows or shrinks the team mid-session (atomic, file-locked, re-validated, persisted).
- **Native task store** — `amq-squad task add/list/claim/done/fail/block`: a pull-based, dependency-gated queue under `.amq-squad/tasks/<session>/`, so a lead of either binary decomposes the goal into claimable work.
- **Compose-from-goal playbook** — the `amq-squad-orchestrator` skill (in both the Claude and Codex marketplaces) drives propose → approve → `team member add` → `task add` → prune.

In practice — you stand up an orchestrated squad, then the lead composes and drives it:

```sh
# You (operator): create an orchestrated team and bring it up, seeded from a goal.
amq-squad new team --roles cto --orchestrated --lead cto --session issue-96
amq-squad new session issue-96 --seed-from issue:96 --target new-window

# The cto lead loads the amq-squad-orchestrator skill and, as the work reveals needs:
amq-squad team member add fullstack --binary codex --session issue-96  # grow the roster
amq-squad task add --title "implement the fix" --session issue-96      # decompose the goal
```

### Breaking changes

2.0 is a major version; the breaking surface is small and mechanical (full upgrade notes in [`MIGRATION.md`](MIGRATION.md)):

- **Removed verbs** — `down`, `launch`, `restore`, `list`, `team show`, `team launch` now return a usage error. Use `stop`, `agent up`, `history` / `agent resume`, `status` / `history`, `up --dry-run`, and `up` respectively.
- **`/v2` module path** — install from `github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest` (note the `/v2`).
- **No data migration** — existing `team.json` (schema v3) loads unchanged.

## Why

AMQ's `coop exec` is a generic launcher. It sets up a mailbox and execs into `claude` or `codex`, but it does not know:

- Which agent is the "CTO" vs "Fullstack" vs "QA" vs "PM" vs "Designer"
- What command originally launched each one (cwd, binary, flags, workstream)
- What durable team norms each agent should read at session start
- Which peers a given agent actively talks to

`amq-squad` captures this at team-setup time (`.amq-squad/team.json`, `.amq-squad/team-rules.md`) and per-agent at launch time (`launch.json` + `role.md` inside the AMQ mailbox). AMQ itself stays unchanged.

## Install

Install the 2.0 line (note the `/v2` module path):

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.4.0
amq-squad version
```

For the latest 2.x build:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest
```

Requires Go 1.25+, the `amq` binary on `PATH` (v0.34+), and `tmux` on `PATH` for `amq-squad up`.

### Skills (plugin marketplaces)

This repo doubles as a plugin marketplace that ships the amq-squad skills
(`amq-squad`, `amq-squad-orchestrator`, `amq-team-setup`, `amq-squad-role-creator`)
for both Claude Code and Codex. The CLI and the skills are versioned together.

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
Code, skills are namespaced by plugin, e.g.
`/amq-squad:amq-squad-role-creator`. In Codex, invoke them by skill name, e.g.
`$amq-squad-role-creator`.

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

### The four skills, and when to use each

| Skill | Audience | Reach for it when |
| --- | --- | --- |
| **`amq-squad`** | an agent on a live team | day-to-day coordination: inbox drains, routing, review/handoff, `status`/`console`/`history`, `up`/`stop`/`resume`/`fork`, `agent up`/`resume`, and tmux runtime control (`focus`/`send`). |
| **`amq-team-setup`** | designing a team | wizard-style first-time setup: capture a goal from any source (prompt / `.md` / GitHub issue or PR / Jira / URL) into a canonical brief, pick personas + profile, optionally wire orchestration (who leads), team rules, pointer stubs, `sync`, validation. |
| **`amq-squad-role-creator`** | adding a role type | authoring a new custom role (persona + `role.md`), inline or as a reusable role file. |
| **`amq-squad-orchestrator`** | a **lead** agent | running a squad as a *driver*: spawn child agents, dispatch tasks to them over durable AMQ (the busy-guarded pane `send` is the fallback), monitor liveness via `status --json`, collect children's `[AGENT-EVENT]`-over-AMQ reports, and recover. The amq-squad-native equivalent of a hand-rolled tmux spawn protocol. |

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
amq-squad doctor                     # AMQ version / tmux / wake / pointer sync
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

## Context model

The context model is three durable layers. Each layer has exactly one source of truth.

| Layer | File | Owner |
| --- | --- | --- |
| Team norms | `.amq-squad/team-rules.md` | shared, hand-edited |
| Per-agent persona | `<agent-dir>/role.md` | seeded by launch, then user-editable |
| Workstream brief | `.amq-squad/briefs/<session>.md` | one per AMQ session |

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

A brief lives at `.amq-squad/briefs/<session>.md` and carries the goal, scope, and source-of-truth pointers for one AMQ session. Every team member reads the same file.

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
- Without `--dry-run`: writes `.amq-squad/briefs/<session>.md` and brings the team up in the same call. `--seed-from` needs `--force` to overwrite an existing brief; a bare `up` (no `--seed-from`) keeps it.

The `amq-team-setup` skill wraps this in a wizard: it captures a goal from **any** source you have — an inline prompt, a local `.md`, a GitHub issue or PR, a Jira key, or a doc URL — fetches it agent-side (amq-squad core stays tracker-neutral), and drafts a **canonical brief** (Goal / Source / Scope / Out of scope / Acceptance) for you to confirm before it is saved. The brief is per-session.

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

Schema 3 adds an optional virtual operator participant for human gates:

```sh
amq-squad new team --roles cto,qa                 # default operator handle: user
amq-squad new team --roles cto,qa --operator ops  # custom operator handle
amq-squad new team --roles cto,qa --no-operator   # explicit opt-out
```

The operator is not a runnable team member. JSON discovery derives `operator` and `capabilities.operator_gates`; `capabilities` is not persisted in `team.json`.

### Orchestration (opt-in)

By default a squad is flat: members coordinate peer-to-peer over AMQ. You can instead run an **orchestrated** squad, where one lead agent spawns, dispatches, and monitors the others and owns the deliverable. It is wired by a structured flag, not by hand-edited prose, so it cannot drift:

```sh
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto
```

This records `orchestrated`/`lead` in `team.json` and injects a generated `## Orchestration` reporting norm into `.amq-squad/team-rules.md`: the lead loads the `amq-squad-orchestrator` skill, and children push `status` / `question` / `review_request` messages to the lead over AMQ. Default off; **exactly one lead**; the lead is a team member, **never the operator**. `--lead ROLE` implies `--orchestrated`; with `--orchestrated` alone a single-member team self-selects and a team with a `cto` defaults to `cto`. The `team_profile_plan` / `team_plan` JSON envelopes carry `orchestrated`/`lead`. If `team-rules.md` already exists, `new team` leaves it untouched — regenerate with `amq-squad team rules init --force` to pick up the norm.

The operator can steer the lead directly from amq-noc (v0.8.0+): a **directive** arrives on the lead's operator p2p thread as a `--kind todo` message whose subject starts with `DIRECTIVE:` — pane-injected when the lead is live, a durable AMQ message when it was down. The `amq-squad-orchestrator` skill teaches the lead to treat directives as operator steering with priority over child reports, acknowledge on the same thread, and never treat one as a gate answer (a directive never clears a `gate/<topic>` thread); orchestrated teams get the convention as a generated line in their `## Orchestration` norm.

For an existing profile, use `amq-squad team lead set <role>` to opt into orchestration without rebuilding the roster, `team lead clear` to return to a flat squad, and `team lead show --json` for discovery. A lead that is already running in an operator-owned pane can register itself with `amq-squad lead register --role <role> --session <session>`; this writes an explicit external launch record so `status` / `focus` / `send` can target the pane. External lead records are visible and directable, but lifecycle commands do not own them: `stop` reports that the pane must be stopped manually, `rm` / `archive` leave it open, and `resume` asks the operator to run `lead register` again instead of replaying the pane.

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
                     [--session ws] [--trust sandboxed|trusted] [--orchestrated [--lead ROLE]]
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

amq-squad up [<session>] [--project DIR] [--profile NAME] [--session ws] [--reset [--yes] [--force]]
             [--dry-run [--json]] [--seed-from file:|issue:|gh: [--force]]
             [--terminal tmux] [--target current-window|new-window|new-session]
             [--layout vertical|horizontal|tiled] [--terminal-session name]
             [--stagger 750ms] [--no-bootstrap] [--force-duplicate]
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
                 [--no-bootstrap] [--trust sandboxed|trusted]
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
amq-squad rm <session> [--project DIR] [--yes] [--force] [--keep-panes]
                                  Permanently remove a finished session (its AMQ root dir
                                  + brief). Previews + prompts
                                  (default No) unless --yes; refuses a live session unless
                                  --force; never touches a sibling session. Closes the
                                  torn-down agents' tmux panes by default (live agents
                                  excluded); --keep-panes to leave them. --project
                                  targets a team-home without cd.
amq-squad archive <session> [--project DIR] [--yes] [--force] [--keep-panes]
                                  Move a finished session aside instead of deleting it
                                  (to <baseRoot>/.archive/<session>/, recoverable).
                                  Confirm-gated; refuses a live session unless --force.
                                  Closes the agents' tmux panes by default; --keep-panes
                                  to leave them. --project targets a team-home without cd.
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
amq-squad task add --title T [--desc D] [--depends-on id,...] [--assign role] --session S
amq-squad task list [--status S] [--json] --session S
amq-squad task claim <id> --me HANDLE --session S
amq-squad task done <id> [--evidence E] --session S
amq-squad task fail|block <id> [--reason R] --session S
                                  Native pull-based, dependency-gated task store
                                  under .amq-squad/tasks/<session>/. A task is
                                  claimable only once its --depends-on tasks are
                                  completed. All subcommands require --session.
amq-squad lead register [--role ROLE] [--session S] [--project DIR] [--profile NAME]
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
                                  agents inherit the PATH binary), profile config,
                                  tmux, wake, marker integrity, and pointer-sync
                                  drift.
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
                            [--trust sandboxed|trusted] [--model NAME]
                            [--codex-args ...] [--claude-args ...]
                            [--force-duplicate] [-- <native flags>]
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
- **peek** (`space`) for a read-only view of an agent's recent output, unread inbox, and what it is blocked on,
- a **triage rollup** headline (`N needs-you threads · N blocked threads · N gated threads · N at-risk threads`) and `/`-filters (`needs-you`, `needs-user`, `gated`, `at-risk`, `blocked`, `stale-blocked`, `unread`, `agent:<h>`, `model:<m>`, `session:<n>`, `label:<l>`, `orchestrator:<o>`).

It renders to `/dev/tty`, so `stdout` stays clean for the other verbs. With `--once` it emits one static board to stdout and exits — use this when there is no terminal attached.

## AMQ diagnostics

`amq-squad amq ...` is a project-aware wrapper around AMQ diagnostics. It resolves the same AMQ root, base root, session, and handle that the squad launcher uses, then delegates to AMQ.

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

```sh
amq-squad status --json | jq .
amq-squad history --json | jq .
amq-squad resume --session issue-96 --json | jq .
amq-squad doctor --json | jq .
amq-squad team profiles --json | jq .
amq-squad roles --json | jq .
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
each with an `available` flag:

```json
{
  "role": "cto",
  "status": "live",
  "tmux": { "session": "main", "window_id": "@42", "pane_id": "%265",
            "target": "current-window", "pane_alive": true },
  "actions": [
    { "kind": "focus",  "available": true, "command": "amq-squad focus --project DIR --session issue-96 --role cto" },
    { "kind": "send",   "available": true, "command": "amq-squad send --project DIR --session issue-96 --role cto --body-file -" },
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

The `/amq-squad-role-creator` skill walks through authoring role files and
wiring them into a team.

## Trust and binary defaults

Generated launch commands include these per-binary defaults:

- **Claude:** `--permission-mode auto`
- **Codex:** nothing by default (sandboxed). Pass `--trust trusted` to prepend `--dangerously-bypass-approvals-and-sandbox` for the local power-user profile.

`--trust trusted` is persisted in the team profile by `team init` and is re-emitted by `up`, `agent up`, `resume`, and `fork`. Combining `--trust trusted` with `--no-default-args` is rejected, as is sandboxed mode with the bypass flag smuggled through `--codex-args`.

Pass `--model NAME` to set the native `--model` flag on Codex or Claude. `team init --model role=model,...` persists per-member models.

```sh
amq-squad team init --personas cto,fullstack --trust trusted
amq-squad team init --personas cto,fullstack --model cto=gpt-5,fullstack=sonnet
amq-squad agent up codex --model gpt-5
```

With amq **0.34.1+**, launches pass `--require-wake` to `amq coop exec`, so a
launch **fails at the door** when the AMQ wake sidecar cannot start and acquire
its lock — instead of surfacing later as a stale or orphaned wake. Older amq
versions are detected and skip the flag. `--no-require-wake` opts out for
environments where wake cannot run; the opt-out is persisted in the launch
record so resume reproduces it.

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

Inside an amq-squad-launched shell, use bare `amq` commands. The launcher already injected `AM_ROOT`, `AM_BASE_ROOT`, and `AM_ME`; override them only when intentionally inspecting a different project or handle. For important handoffs, use `--wait-for drained --wait-timeout 60s` and keep the AMQ message id. If routing is unclear, run `amq route explain` or `amq-squad amq route --to <handle>` first.

## Files amq-squad writes

```text
<project>/.amq-squad/team.json           Default team profile (schema: 3 on new writes).
<project>/.amq-squad/teams/<name>.json   Named team profiles (schema: 3 on new writes).
<project>/.amq-squad/team-rules.md       Durable team norms (user-edited).
<project>/.amq-squad/briefs/<session>.md Workstream brief, one per AMQ session.
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
- `amq` binary on `PATH` (v0.34+)
- `tmux` on `PATH` for `amq-squad up`
