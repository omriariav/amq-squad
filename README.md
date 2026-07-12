# amq-squad

**amq-squad launches and coordinates teams of Claude and Codex agents over
durable AMQ.** It owns the team layer above
[AMQ](https://github.com/avivsinai/agent-message-queue) by
[Aviv Sinai](https://github.com/avivsinai): roles, rosters, briefs, operator
gates, launch records, and the terminal/runtime controls that keep a squad
observable.

The 30-second mental model:

- **AMQ is the durable coordination rail.** Agents communicate through inboxes,
  threads, receipts, presence, and wake signals.
- **amq-squad is the project/team layer.** It declares who is on the team, what
  each role does, which workstream they share, and how to start/stop/resume
  them.
- **Terminals are runtime surfaces, not the source of truth.** tmux, iTerm2, and
  Terminal.app make agents visible and sometimes controllable; durable AMQ
  dispatch works even when pane injection is unavailable.
- **Operator gates bind high-risk actions.** Leads may plan, dispatch, review,
  and collect evidence, but default-branch pushes, tags, releases, external
  sends, and merges need verified operator approval tied to an exact action and
  target.

## Contents

- [What's new in v2.19.0](#whats-new-in-v2190)
- [Install](#install)
- [Quickstart](#quickstart)
- [Execution modes](#execution-modes)
- [Core concepts](#core-concepts)
- [Orchestration protocol and safety](#orchestration-protocol-and-safety)
- [Terminal capability matrix](#terminal-capability-matrix)
- [Command map](#command-map)
- [Skills and model guidance](#skills-and-model-guidance)
- [Customize](#customize)
- [Cross-project teams](#cross-project-teams)
- [Reference and moved details](#reference-and-moved-details)
- [Requirements](#requirements)

## What's new in v2.19.0

v2.19.0 makes squad startup guided, observable, and safer to supervise:

- **Interactive `run start` wizard** with Bubble Tea and numbered/accessibility
  adapters, structured preflight, Project and Global/NOC flows, explicit
  preview-to-live confirmation, and deterministic tmux layouts (#393).
- **Attention-only operator notifications** with profile-scoped desktop or
  direct-argv sinks, delivery de-duplication, gate escalation, and wizard/setup
  configuration that never grants approval (#390).
- **Bounded self-operator mode** for explicit, exact-session merge approval.
  Spawn, release, tag, publish, external-send, and destructive actions remain
  human-only, and the approving lead cannot execute its own merge (#391).
- **Bootstrap and launch hardening** with identity-bound bootstrap completion
  attestation and single-pass native Claude/Codex argv composition (#396).
- **Wake-friendly orchestration waits**: collect briefly for an imminent ACK or
  report, then park/end the turn for longer work and operator gates (#404).
- **Deterministic layout finalization hardening** keeps the configured lead in
  the main tmux pane, verifies exact pane/window identity, and preserves
  observable warnings without tearing down launched agents (#393).

## Install

Install the v2 module path:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest
amq-squad version
```

For a pinned release, replace `@latest` with the tag you want, for example:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.19.1
```

Install the skills from the plugin marketplace when agents should use the
amq-squad playbooks themselves.

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

The primary skills are `amq-squad` and `amq-squad-orchestrator`. The older
`amq-team-setup` and `amq-squad-role-creator` skill entries are redirect stubs.
The CLI and skills are versioned together.

## Quickstart

Enable the default attention-only desktop notification policy when creating a
profile with `team init --operator-notifications`, or pass the same flag through
`run start` when it creates a new profile. Existing profiles remain
authoritative and are never rewritten. Notifications run on the scoped
`operator watch` host and never approve gates.

The shortest working path for a visible project lead and workers:

```sh
cd ~/Code/my-project

# Guided preview with an explicit, default-No live confirmation. In an
# interactive terminal, zero-argument `amq-squad run start` opens the same
# wizard.
amq-squad wizard

# Preview, then create, an orchestrated run. --go is the mutation switch.
amq-squad run start \
  --project . \
  --session issue-96 \
  --roles cto,fullstack,qa \
  --lead cto \
  --goal "fix issue 96" \
  --go

# Watch the run.
amq-squad status --session issue-96
amq-squad console --session issue-96

# Queue durable work to a role. Pane nudges are optional; AMQ is authoritative.
amq-squad dispatch \
  --session issue-96 \
  --role qa \
  --subject "Run smoke tests" \
  --body "Validate the current PR and report findings."

# Stop and resume without losing launch records, briefs, or task state.
amq-squad stop --session issue-96 --all
amq-squad resume --session issue-96 --exec
```

Use `--external-lead` when the current pane is already the project lead and only
the remaining workers should be spawned:

```sh
cd ~/Code/my-project

amq-squad run start \
  -p . \
  -s issue-96 \
  --roles cto,fullstack,qa \
  --lead cto \
  --external-lead \
  --goal "fix issue 96" \
  --go
```

`run start` previews by default; `--go` creates. With `--goal`, it waits until
the lead is live before delivering the goal. If goal delivery fails, it exits
non-zero and prints an exact retry command.

For a deterministic visible arrangement, pass `--layout-preset lead-left`,
`lead-top`, `even-grid`, or `one-window-per-agent`. Presets close the launcher
pane after a successful start by default; use `--launcher-pane keep` to retain
it. External-lead and detached runs always keep the launcher. The final layout
is applied asynchronously by exact tmux pane/window IDs only after spawn, goal
delivery, and the final CLI output, so renaming a pane or window is safe. A
finalization failure never tears down agents and remains visible as a status
warning.

In an interactive terminal, `amq-squad run start` with no arguments now opens
the guided wizard instead of returning the old missing-flag usage error.
`amq-squad wizard` and `run start --interactive` are equivalent. The wizard
first runs the exact canonical preview, then asks `Launch now? [y/N]`; only an
explicit `y`/`yes` reruns the identical argv with `--go` added. The second call
rechecks current state, so a collision introduced after preview is still
refused. The wizard also offers a Global/NOC branch backed by canonical
`amq-squad global start`. It is disabled in CI and never triggers when stdin or
stderr is not a TTY; non-TTY zero-argument calls retain the usage error. Partial
flag commands without `--interactive` keep fail-fast parser behavior.

Copy/paste equivalents remain canonical:

```sh
amq-squad run start --project . --profile default --session issue-96
amq-squad global start --root ~/Code --agent claude --name global-orch
```

Manual setup still works when the team shape is known:

```sh
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto --sync
amq-squad new session issue-96 --seed-from issue:96
```

## Execution modes

amq-squad separates the control root, target project root, visible lead, and
implementation authority. The mode should be explicit in goal-first runs.

| Mode | What it means | Use it when |
| --- | --- | --- |
| `global_orchestrator` | A neutral control-plane session supervises one or more project runs. It previews, creates/registers project leads, routes gates, and watches evidence; it does not edit target project code. | NOC/global coordination across repos or milestones. |
| `project_lead` | One visible project-root lead owns the run, delegates implementation over durable AMQ tasks, and produces final evidence. | Default for most issue or milestone delivery. |
| `project_team` | Multiple visible project-root agents are launched as first-class members. | The operator wants to watch and address several roles directly. |
| `direct_lead_session` | The visible project lead may code directly in the project root. | Single-lead exceptions where delegation would add no value. |

`--external-lead` is a project-lead binding mode: the current tmux pane becomes
the configured lead for that run, while amq-squad spawns the rest of the team.
It must run from the lead member's project root. It does not adopt a separate
global orchestrator handle as the project lead.

Operational recipes live in
[docs/global-orchestrator-runbook.md](docs/global-orchestrator-runbook.md) and
[docs/operator-cookbook.md](docs/operator-cookbook.md).

### Bounded self-operator mode

`self_operator` is an explicit, exact-session policy for delegated merge-gate approval. New profiles require `--operator-mode self_operator --self-operator-lead <lead> --self-operator-allow merge`; there is no default allowlist. Spawn remains human-only until strict spawn evidence exists, as do release, tag, publish, external-send, and destructive-filesystem gates. The approving lead cannot execute the merge; a different strongly verified roster actor must run the final verifier. Human denial/intervention and policy pause or revision revoke self approval. Notifications are attention-only and never authorize an action.

## Core concepts

| Concept | Meaning |
| --- | --- |
| **Project root** | The repository or workspace whose `.amq-squad/` directory owns team state. Most commands default to cwd and accept `--project DIR`. |
| **Profile** | A team shape. The default profile is `.amq-squad/team.json`; named profiles live under `.amq-squad/teams/<name>.json`. |
| **Session / workstream** | The AMQ namespace for one issue, release, or focused run. Session names use lowercase letters, digits, `-`, and `_`. |
| **Team rules** | `.amq-squad/team-rules.md`, shared norms for all members. `CLAUDE.md` and `AGENTS.md` only point to it. |
| **Role file** | Per-agent persona seeded into the AMQ agent directory at launch and preserved on later resumes. |
| **Brief** | The goal/scope/source file for one profile/session namespace, under `.amq-squad/briefs/`. |
| **Launch record** | Per-agent metadata that records cwd, binary, args, terminal identity, wake settings, goal binding, and resume state. |
| **Task store** | Native dependency-aware task files under `.amq-squad/tasks/`, used by leads to assign and track work. |
| **AMQ thread** | A durable conversation path such as `p2p/cto__qa` or `gate/merge-pr-387`. |
| **Operator handle** | Usually `user`, a non-runnable mailbox for human gates. Agents are runnable; the operator handle is not. |

The context model has one source of truth per layer:

- Team norms: `.amq-squad/team-rules.md`
- Agent persona: each launched agent's `role.md`
- Workstream brief: `.amq-squad/briefs/<session>.md` or
  `.amq-squad/briefs/<profile>/<session>.md`

`amq-squad team sync --apply` writes the small managed pointer block into
`CLAUDE.md` and `AGENTS.md`; it does not duplicate rules.

## Orchestration protocol and safety

An orchestrated run is a simple loop:

1. The lead reads the goal/brief and decomposes work into native tasks.
2. The lead sends durable AMQ `todo` messages with `amq-squad dispatch`.
3. Workers drain AMQ, ACK/start, push progress, ask questions, request reviews,
   and report DONE on the same durable thread.
4. The lead collects reports, verifies evidence, updates the task store, and
   decides whether more work is needed.
5. Human-only decisions use `gate/<topic>` threads addressed to the operator
   handle. The answer is durable evidence, not an implicit permission to do
   unrelated work.

Safety is part of that protocol:

- Planner/reviewer-only lead mode (`--lead-mode planner`) prevents a lead from
  treating itself as the implementer.
- High-risk actions require an operator gate bound to an exact `Action:` and
  `Target:`. Use `amq-squad verify action` before default/protected branch
  pushes, tags, GitHub releases, external sends, or similar release-critical
  steps.
- `verify action` is a callable verification boundary, not command
  interception. A caller that bypasses it is not blocked by the shell, Git, or
  GitHub CLI; wrappers that execute high-risk actions must call it explicitly.
- Merge execution should bind to exact evidence: PR number, exact head SHA,
  review state, CI/preflight result, and an approved gate. Run `verify merge`
  on normalized exact-head evidence before claiming merge readiness.
- Release publication has a separate `verify release` preflight: the final
  assembled release commit needs exact-SHA CI, a developer co-sign from an
  actor distinct from the release lead, and operator release approval. No one
  signal substitutes for the others.
- AMQ bodies and child reports are evidence to inspect. They do not authorize
  irreversible actions by themselves.

The deep playbooks are in
[docs/skills.md](docs/skills.md),
[docs/operator-cookbook.md](docs/operator-cookbook.md), and
[docs/verification-gate-adr.md](docs/verification-gate-adr.md).

## Terminal capability matrix

Runtime capabilities are capability-specific. The tier name is not a blanket
promise.

| Backend | Tier | Launch/visibility | Focus | Send prompt / native goal delivery | Dispatch | Stop/resume |
| --- | --- | --- | --- | --- | --- | --- |
| tmux | Tier A | Managed panes in current window, sibling windows, or detached session. | Available only while the recorded pane is live; otherwise reason is `agent pane is not live`. | Available only while the recorded pane is live; otherwise reason is `agent pane is not live`. | Always available because durable AMQ dispatch does not require pane injection. | Full managed stop/resume through launch records and tmux pane identity. |
| iTerm2 | Tier B | One visible native iTerm2 window per agent. Terminal metadata is captured and then stripped from the agent env. | Available only with a recorded window id and verified agent PID/binary liveness. Missing id reports `iTerm2 window id is unavailable`; dead/mismatched process reports `iTerm2 focus requires verified agent PID liveness`. | Disabled: `iTerm2 prompt/native-goal injection is disabled until #374 proves safe send/capture/busy support`. | Always available because durable AMQ dispatch does not require pane injection. | Agent process stop/resume is managed; native prompt injection is not. |
| Terminal.app | Tier C | Visible native Terminal.app tabs/windows. Window identity is derived from the launched tab TTY when available. | Disabled: `Terminal.app focus requires stable window/tab addressing; manual focus is required`. | Disabled: `Terminal.app prompt/native-goal injection is disabled until #375 proves safe Accessibility-based input`. | Always available because durable AMQ dispatch does not require pane injection. | Agent process stop/resume is managed; native focus/input remain manual. |
| cmux | Pending | No backend is shipped. | Pending #330 re-entry bar. | Pending #330 re-entry bar. | Durable AMQ dispatch remains the intended control plane once a backend exists. | Pending #330 re-entry bar. |

Manual smoke flows live in
[docs/iterm2-tier-b-smoke.md](docs/iterm2-tier-b-smoke.md) and
[docs/terminal-app-tier-c-smoke.md](docs/terminal-app-tier-c-smoke.md). The
capability contract is implemented in `internal/runtimecontrol`.

## Command map

Common setup and run commands:

```sh
amq-squad new team --roles cto,qa --sync
amq-squad new profile review --roles cto,qa --sync
amq-squad run start -p . -s issue-96 --roles cto,qa --lead cto --goal "..." --go
amq-squad run start -p . -s issue-96 --external-lead --lead cto --roles cto,qa --go
amq-squad new session issue-96 --seed-from issue:96
```

Lifecycle:

```sh
amq-squad status --session issue-96
amq-squad console --session issue-96
amq-squad stop --session issue-96 --all
amq-squad resume --session issue-96 --exec
amq-squad fork --from issue-96 --as issue-96-review
amq-squad archive issue-96
amq-squad rm issue-96
```

Coordination:

```sh
amq-squad task add --session issue-96 --title "Implement fix" --assign fullstack
amq-squad dispatch --session issue-96 --role fullstack --task t1 --subject "Implement fix" --body "..."
amq-squad activity set --session issue-96 --me fullstack --task t1 --phase testing
amq-squad threads --session issue-96
amq-squad thread --session issue-96 --id p2p/cto__fullstack --include-body=false
```

Safety preflights:

```sh
amq-squad verify action --project . --session issue-96 \
  --gate release --action github_release --target "draft v2.19.0 release"
amq-squad verify merge --evidence merge-evidence.json
amq-squad verify release --evidence release-evidence.json
```

The action, merge, and final-release-commit contracts are documented together
in [docs/verification-gate-adr.md](docs/verification-gate-adr.md).

Diagnostics:

```sh
amq-squad doctor
amq-squad doctor --project ~/Code/other-app --profile release
amq-squad amq env --session issue-96
amq-squad amq ops --session issue-96
amq-squad amq route --session issue-96 --me cto --to fullstack
```

Runtime control:

```sh
amq-squad focus --session issue-96 --role cto
amq-squad send --session issue-96 --role qa --body "run the smoke suite"
```

`focus` and `send` are runtime capabilities. They may be unavailable on native
terminal tiers even while durable AMQ `dispatch` remains available.

Removed 1.x verbs now return usage errors:

| Removed | Use |
| --- | --- |
| `down` | `stop` |
| `launch <binary>` | `agent up <binary>` |
| `restore` | `history` or `agent resume` |
| `list` | `status` or `history` |
| `team show` | `up --dry-run` |
| `team launch` | `up` |

Full upgrade notes live in `MIGRATION.md`.

## Skills and model guidance

The skills are the source of truth for agent behavior and current model
selection guidance.

| Skill | Use it for |
| --- | --- |
| `amq-squad` | Setup, role authoring, live coordination, draining AMQ, routing review requests, status/history/doctor, runtime controls, and lifecycle commands. |
| `amq-squad-orchestrator` | Lead-agent operation: spawn, dispatch, monitor, collect reports, coordinate reviews, recover, and produce final evidence. |
| `amq-team-setup` | Deprecated redirect to `amq-squad`. |
| `amq-squad-role-creator` | Deprecated redirect to `amq-squad`. |

Invoke skills in Claude Code as `/amq-squad:<skill>` and in Codex as
`$<skill>`.

Model guidance is intentionally skill-owned because it changes faster than the
binary. For v2.19.0, use the current model family and per-role model/effort
recommendations in the installed skills; treat cost as a tie-breaker after
output quality for shippable work. Prefer that guidance over copying model
examples from this README.

Deep guide: [docs/skills.md](docs/skills.md)
([HTML](docs/skills.html)).

That guide also defines goal-first composition modes: manual rosters, seeded
per-spawn approval, and explicitly bounded autonomous composition. Autonomous
composition never grants merge, release, destructive, or external-send
authority.

## Customize

Profiles and roles:

```sh
amq-squad new team --roles cto,researcher --binary researcher=codex --sync
amq-squad new team --role-file ./roles/researcher.md --roles cto --sync
amq-squad team lead set cto --lead-mode planner
amq-squad team lead clear
```

Custom role files can be Markdown with YAML frontmatter, plain Markdown with a
`# Role:` heading, or metadata-only YAML/JSON. They are staged under
`.amq-squad/roles/<id>.md`; launch seeds each agent's role file and does not
clobber later edits.

Launch customization:

```sh
amq-squad agent up claude --role qa --session beta \
  --launcher /path/claude-wrapper.sh --launcher-args "--pull --workspace /ws"

amq-squad team overlay init --workers --disable-all-hooks
```

`--launcher-args` are placed before the normal child arguments that carry
bootstrap and binary defaults. A wrapper must forward its trailing arguments to
the real agent, for example by ending with `exec claude "$@"`; otherwise the
managed agent can lose required startup behavior.

Per-member `claude_args` / `codex_args` apply native CLI flags to one member and
are replayed by resume. Worker overlays trim Claude plugin/hook surface for
same-cwd squads; Codex workers use native Codex profiles via `codex_args`.

Claude members may also carry an explicit, role-scoped
`permission_allowlist`, for example
`"permission_allowlist": ["Bash(rm -rf /tmp/qa-review/*:*)"]`. amq-squad
merges those patterns into one effective `--allowedTools` grant for that member
only, records the result in launch history, and shows both the configured and
effective lists in `up --dry-run --json`. Values beginning with `-` are rejected
and generated grants use the single-token `--allowedTools=<grant>` form. Resume
removes the prior launcher-owned grant before rebuilding from current policy,
so narrowing or removing the field revokes old access; the
`--no-preauthorize-inscope` choice also survives replay. Preview commands never
embed launcher-owned policy in executable child argv: `agent up` recomputes it
from current profile state, and launch history records launcher-owned and
explicit-native provenance separately even when their values are identical.
Keep each pattern as
narrow as the member's own scratch or review workspace; the field is rejected
on non-Claude members and is intentionally not a team-wide trust switch.

Profiles using `permission_allowlist` are written as team schema 4; profiles
without it remain schema 3. v2.20+ readers accept both and reject future
schemas. Pre-v2.20 binaries do not understand this field: they can silently
ignore it and lossily rewrite a schema-4 profile. Upgrade every amq-squad binary
that may read or write the profile before configuring an allowlist, and use
`amq-squad doctor` to detect version skew.

Trust and binary defaults are explicit. Codex trusted mode is the only path that
prepends `--dangerously-bypass-approvals-and-sandbox`; the default sandboxed
mode does not.

## Cross-project teams

Members may work from different repositories while one team-home owns the
roster. Set a per-member cwd during team creation:

```sh
cd ~/Code/project-a
amq-squad team init --roles cto,fullstack,qa --cwd qa=~/Code/project-b
```

`up --dry-run` emits the corresponding `cd <member-cwd>` launch commands.
`team sync --apply --allow-outside` writes the managed `CLAUDE.md` / `AGENTS.md`
pointer block in each member cwd; `--allow-outside` is required so a hand-edited
profile cannot write into unrelated directories silently.

Cross-project AMQ replies also require each project to declare its peers in
`.amqrc`; `team sync` does not edit this file:

```json
{
  "root": ".agent-mail",
  "project": "project-a",
  "peers": {
    "project-b": "/Users/you/Code/project-b/.agent-mail"
  }
}
```

Configure the reciprocal peer entry when both projects need to initiate and
reply to messages. Use AMQ `--project` routing for another project and
`--session` for another workstream in the same project; do not substitute a raw
cross-project `--root`, which lacks reply-origin metadata.

## Reference and moved details

This README is the map, not the runbook. Older long-form material is preserved
or compressed here and lives in the docs below:

| Topic | Where to read |
| --- | --- |
| Operator milestone runs, CLI-only flow, common failures | [docs/operator-cookbook.md](docs/operator-cookbook.md) |
| Global orchestrator and external lead runbook | [docs/global-orchestrator-runbook.md](docs/global-orchestrator-runbook.md) |
| Skill workflows, AGENT-EVENT protocol, issue-to-merge walkthrough | [docs/skills.md](docs/skills.md) |
| Action, merge, and release verification preflights | [docs/verification-gate-adr.md](docs/verification-gate-adr.md) |
| Native task store internals | [docs/task-store-design.md](docs/task-store-design.md) |
| JSON action object contract and availability semantics | [docs/action-object-contract.md](docs/action-object-contract.md) |
| AMQ swarm interop boundary | [docs/amq-swarm-interop.md](docs/amq-swarm-interop.md) |
| iTerm2 and Terminal.app manual smoke checks | [docs/iterm2-tier-b-smoke.md](docs/iterm2-tier-b-smoke.md), [docs/terminal-app-tier-c-smoke.md](docs/terminal-app-tier-c-smoke.md) |
| Release history | `docs/v2.*-release-notes.md` |
| Migration from 1.x verbs | `MIGRATION.md` |

Machine-readable command outputs use JSON envelopes with a `kind` and `data`
payload. Prefer `--json` for automation and the action objects surfaced by
`status --json` instead of inferring tmux/window state.

Exit codes:

- `0` success
- `1` usage or user error
- `2` system/runtime error
- `3` partial success

Shell completions are available from the CLI:

```sh
amq-squad completion zsh
amq-squad completion bash
amq-squad completion fish
```

## Requirements

- Go 1.25+
- `amq` 0.42.0+ on `PATH`
- `tmux` on `PATH` for Tier A managed panes
- macOS with iTerm2 for the Tier B backend
- macOS Terminal.app for the Tier C backend
- `pandoc` only when regenerating or checking `README.html`

amq-squad is tracker-neutral. Fetching GitHub, Jira, Confluence, or other goal
sources happens in the skills or operator tooling; the core binary owns team,
runtime, and coordination state.
