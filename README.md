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

- [What's new in v2.29.4](#whats-new-in-v2294)
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

## What's new in v2.29.4

v2.29.4 matures the drafter configuration story shipped in v2.29.3. The
drafter block graduates from profile-only to a layered model (#696): a
user-level global config at `~/.config/amq-squad/config.json` (override with
`AMQ_SQUAD_CONFIG`) carries machine-scoped defaults, resolved with
whole-block precedence — profile block over global config over the implicit
`in_session` default — and an ordered fallback chain
(`"chain": ["yoetz", "claude", "codex"]`) tries each backend in configured
order, records exact command evidence for every attempt and fall-through,
and lands on the in-session prompt only after exhaustion. Trust stays
explicit: custom argv templates are honored only from the user-level global
config, never from project or profile files, and unknown or typoed config
keys fail closed instead of silently changing backend selection. The new
`amq-squad setup` command (#697) owns that global layer interactively —
probing PATH for backends with versions, prompting for chain and knobs, and
writing the config atomically at mode 0600 — with `--drafter-*` flags for
non-interactive CI provisioning. The binary's own generation surfaces now
route through the configured drafter (#700): `--goal` brief drafting with
exact six-section validation staged for operator review before write,
per-seat personas reusing the `role draft` path end to end, and team-rules
charter prose wrapped in deterministic structure validation — with config
source plus the complete ordered attempt chain reported on every success,
fallback, and failure path. A safety fix (#698) bounds configured `{out}`
file reads to the same 4 MiB budget as captured stdout/stderr, failing
oversized drafts through the normal failure policy. Full detail in
[the v2.29.4 release notes](docs/v2.29.4-release-notes.md).

The README describes the latest release only. Earlier releases live in
[GitHub Releases](https://github.com/omriariav/amq-squad/releases) and
[docs/](docs/) (per-release notes files).

## Install

Install the v2 module path:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest
amq-squad version
```

For a pinned release, replace `@latest` with the tag you want, for example:

```sh
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@v2.29.4
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

The authoritative skills are `amq-squad:wizard` for setup and launch preview,
`amq-squad:cli` for direct operations, and `amq-squad:orchestrator` for a
verified live lead. `amq-squad`, `amq-squad-orchestrator`, `amq-team-setup`,
and `amq-squad-role-creator` are compatibility redirects only. The CLI and
skills are versioned together.

## Quickstart

Enable the default attention-only desktop notification policy when creating a
profile with `team init --operator-notifications`. Existing profiles remain
authoritative and are never rewritten. Live `start` and `resume` supervise one
profile/session notification watcher on the launch host. `status` and `doctor`
surface watcher health. Notifications never approve gates.

Notification delivery is honestly **at least once**, not exactly once. The
supervised watcher and manual `operator watch` share the same
per-event/per-sink reservation and success-commit state in
`.amq-squad/notify-state.json`. A reservation lasts for the configured sink
timeout plus a 5-second commit margin (15 seconds by default, up to 65 seconds
for the supported maximum timeout). If a sink side effect succeeds but the
process dies before committing success, other drivers suppress that event only
until the reservation expires and then retry it. This bounds concurrent replay
and retry delay, not the total duplicate count: repeated ambiguous crashes,
committed delivery errors and explicit resend can cause further attempts.
Command sinks should therefore be idempotent.

The shortest working path for a visible project lead and workers:

```sh
cd ~/Code/my-project

# Create the roster and its shared rules.
amq-squad new team --roles cto,fullstack,qa --orchestrated --lead cto --sync

# Preview the complete launch plan. The prompt defaults to No.
amq-squad start issue-96 --project . --goal "fix issue 96"

# After approving that exact plan, automation can use --yes.
amq-squad start issue-96 --project . --goal "fix issue 96" --yes

# Watch the run.
amq-squad status --session issue-96

# Queue durable work to a role. Pane nudges are optional; AMQ is authoritative.
amq-squad dispatch \
  --session issue-96 \
  --role qa \
  --subject "Run smoke tests" \
  --body-file ./qa-task.md

# Stop and resume without losing launch records, briefs, or task state.
amq-squad down --session issue-96 --all
amq-squad resume --session issue-96 --exec
```

`start` has one mutation gate, default No. It resolves the roster and active
brief, renders one plan, and starts only after approval. Under the launch lock it
keeps verified live roles, respawns missing or stopped roles, and verifies every
child process before reporting success. An optional goal is sent to the lead
only after the whole roster is live. After interruption, rerun `start`; no
prepared manifest, readiness digest, bootstrap acknowledgement, or separate go
step exists.

Roster changes use the same reconciler. Add a configured role and rerun
`start`; verified live roles remain untouched and only the missing role starts.
Use `down --role <role>` before changing and replacing a live role. For a
deterministic visible arrangement, use `--target new-window` or select
`--layout vertical|horizontal|tiled`; the recorded pane IDs, not window names,
remain the runtime identity.

The canonical copy/paste flow is:

```sh
amq-squad start issue-96 --project . --profile default
amq-squad start issue-96 --project . --profile default --yes
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

Each schema-5 member also has an explicit `actor_mode`: `implementation` or
`review`. A planner lead remains a delegating reviewer, an implementation
worker may edit only within its assigned role and durable task, and a review
actor remains read-only. Bootstrap capability lookup uses the exact trimmed
role and handle; case drift does not inherit another actor's permissions. Set
modes when creating a team with `--actor-mode role=implementation,...` or when
adding a member with `team member add --actor-mode review|implementation`.
Legacy schema-1 through schema-4 profiles retain their historical effective
behavior until explicitly migrated; once a profile is written as schema 5,
every member must carry an explicit mode.

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
  `Target:`. Raise it with `amq-squad gate raise`; the command sends a typed
  `authorization_request` context as part of the durable AMQ question.
  Use `amq-squad verify action` before default/protected branch
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
| tmux | Tier A | Managed panes in current window, sibling windows, or detached session. | Available only while the recorded pane is live; otherwise reason is `agent pane is not live`. | Available only while the recorded pane is live; otherwise reason is `agent pane is not live`. | Available when the row proves an exact namespace, handle, and initialized durable AMQ mailbox. | Full managed stop/resume through launch records and tmux pane identity. |
| iTerm2 | Tier B | One visible native iTerm2 window per agent. Terminal metadata is captured and then stripped from the agent env. | Available only with a recorded window id and verified agent PID/binary liveness. Missing id reports `iTerm2 window id is unavailable`; dead/mismatched process reports `iTerm2 focus requires verified agent PID liveness`. | Native send/capture/busy/local-input and effective goal delivery remain unsupported after the #374 evidence review because the current goal command requires a live native prompt target. | Available only with an exact durable AMQ member route. | Agent process stop/resume is managed; native prompt injection is not. |
| Terminal.app | Tier C | Visible native Terminal.app tabs/windows. Window identity is derived from the launched tab TTY when available. | Disabled: `Terminal.app focus requires stable window/tab addressing; manual focus is required`. | Native send/capture/busy/local-input and effective goal delivery remain unsupported after the #375 Accessibility and targeting review because the current goal command requires a live native prompt target. | Available only with an exact durable AMQ member route. | Agent process stop/resume is managed; native focus/input remain manual. |
| cmux | Pending | No backend is shipped. | Pending #330 re-entry bar. | Pending #330 re-entry bar. | Requires an exact durable AMQ member route once a backend exists. | Pending #330 re-entry bar. |

Manual smoke flows live in
[docs/iterm2-tier-b-smoke.md](docs/iterm2-tier-b-smoke.md) and
[docs/terminal-app-tier-c-smoke.md](docs/terminal-app-tier-c-smoke.md). The
capability contract is implemented in `internal/runtimecontrol` and documented
in [docs/terminal-runtime-contract.md](docs/terminal-runtime-contract.md).

## Command map

Common setup and run commands:

```sh
amq-squad new team --roles cto,qa --sync
amq-squad new profile review --roles cto,qa --sync
amq-squad start issue-96 --project . --profile review --goal "..."
amq-squad start issue-96 --project . --profile review --goal "..." --yes
```

Lifecycle:

```sh
amq-squad status --session issue-96
amq-squad doctor --session issue-96
amq-squad down --session issue-96 --all
amq-squad resume --session issue-96 --exec
```

`status`, `doctor`, and `down` share one record-first identity pipeline. A
selected launch record supplies the captured root, team home, cwd, actor, PID,
TTY, pane, binary, and argv; current runtime state comes from probing those
recorded coordinates. Multiple live matches fail as `duplicate_live`, an
invalid record fails as `record_invalid`, and a launcher-stamped pane without a
record is labeled `unmanaged`. Rerun `start` to roll a partial launch forward.

`doctor` has three severities: `ok`, `warn`, and `fail`. Only `fail` makes the
command exit non-zero; warnings remain visible diagnostic notes. A shared Git
index is therefore a failure only when two or more affected members are live.
Stopped or unplanned members sharing an index produce a warning with the exact
`worktree plan` / `worktree materialize` remedy. Doctor uses the same
replacement-pane discovery as status; if a member's runtime environment cannot
be resolved, the affected role and resolution error remain visible in the
diagnostic detail.

Coordination:

Use `--body-file FILE` or `--body-file -` (stdin) for `amq-squad send` and
`dispatch` bodies containing code, commands, backticks, or `$()` syntax.
Inline `--body` is only for short plain prose because the caller shell expands
it before amq-squad receives argv. For bare `amq send`, use `--body -` or
`--body @file` instead; raw AMQ does not accept `--body-file`.

```sh
amq-squad task add --session issue-96 --title "Implement fix" --assign fullstack
amq-squad task claim t1 --session issue-96 --me fullstack
amq-squad dispatch --session issue-96 --role fullstack --task t1 --subject "Implement fix" --body-file ./task.md
amq-squad task done t1 --session issue-96 --me fullstack
amq-squad task list --session issue-96 --json
amq thread --root /absolute/path/to/session-root --me fullstack --id p2p/cto__fullstack --include-body
```

The native task list is a flat persisted queue. `claim` atomically assigns one
task; AMQ delivery and task status are separate observations. Workers report
progress, blockers, review readiness, and completion with ordinary durable AMQ
messages on the task thread. No local delivery receipt or task reconciliation
state machine certifies those messages.

Task-backed lifecycle events are schema-bound records, not subject heuristics.
They carry the exact actor, task and claim generation, namespace, prepared-run
generation reference, dispatch/outbox anchors, and—where required—immutable
command-evidence reference. ACK, progress, checkpoint, and review remain
nonterminal; only the matching terminal task transaction can publish DONE,
BLOCK, or cancellation. Delayed, duplicate, stale-generation,
cross-namespace, or prose-only messages remain inspectable but cannot change
task state.

`evidence run` executes argv without a shell for the active structured task
assignee. It binds canonical namespace, exact task and executable bytes, cwd,
bounded explicit environment, and attempt identity; publishes immutable
process/outcome/summary records; and compare-and-swap links their digests to the
task. A repeated attempt ID returns the original result only for the same full
request. `evidence show`, `list`, and `lookup` are bounded read-only projections;
`evidence recover` explicitly reconciles an interrupted finalization. Its AMQ
report uses only the task's recorded dispatch route and cannot erase evidence
when delivery fails.

Safety preflights:

```sh
amq-squad gate raise --project . --session issue-96 --me cto \
  --gate release --kind release --action github_release \
  --target "publish v2.21.0 GitHub release"
amq-squad operator answer --project . --session issue-96 \
  --gate release --approved
amq-squad verify action --project . --session issue-96 \
  --gate release --action github_release --target "publish v2.21.0 GitHub release" \
  --emit-authorization --signing-key-file /secure/operator-authz.pem \
  --authorization-out /secure/release-authz.json
amq-squad verify authorization --file /secure/release-authz.json \
  --action github_release --target "publish v2.21.0 GitHub release" \
  --trust-store /secure/operator-authz-trust.json
amq-squad verify merge --evidence merge-evidence.json
amq-squad verify release --evidence release-evidence.json
```

`gate raise --list-kinds --json`, `operator answer --list-kinds --json`, and
`verify action --list-kinds --json` expose the same context-free versioned
action catalog. The verifier listing keeps custom actions outside the hard-kind
array and carries explicit guidance that they require an exact Action/Target
operator gate plus manual verification. Canonical gate topics reject empty,
dot, dot-dot, whitespace,
control, and backslash path segments. Typed `Target`, `Note`, and answer
`Reason` values are exact, valid UTF-8, single-line, trim-canonical, and
control-free; optional action/target overrides must match exactly. Decisions
come only from the exact `APPROVED: <topic>` or `DENIED: <topic>` subject, while
the body must repeat each typed binding exactly once. V2 receipts, reservations,
and preflight evidence use collision-resistant hashed identities and immutable
tuple validation. Legacy raw answers remain unstructured readable diagnostics
and cannot authorize an action. A human-approved typed PASS can emit an
immutable Ed25519 authorization envelope when the caller supplies an explicit
owner-controlled PKCS#8 key (`0600`). `verify authorization` checks an explicit
public trust store, exact caller action/target, and the current namespace, gate,
answer, receipt bytes, policy/preflight, and compound-release generation before
returning PASS. Revoked/untrusted keys, stale evidence, symlinks, and changed
authority fail closed. The envelope is a normalized callable boundary for CLI,
reviewers, and connectors; it never performs the external action.

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
amq-squad send --session issue-96 --role qa --body-file ./prompt.md
```

`focus` and `send` are runtime capabilities. They may be unavailable on native
terminal tiers even while durable AMQ `dispatch` remains available.

Removed lifecycle verbs now return usage errors:

| Removed | Use |
| --- | --- |
| `up`, `run start`, `wizard` | `start` |
| `stop`, `rm`, `archive` | `down` |
| `console`, `monitor`, `context`, `history`, `next` | `status`, `doctor`, and `operator status` |
| `collect`, `threads`, `thread` | exact-root raw `amq drain/list/read/thread` |
| `review-worktree` | retained `worktree` workflow |

Full upgrade notes live in `MIGRATION.md`.

## Skills and model guidance

The skills are the source of truth for agent behavior and current model
selection guidance.

| Skill | Use it for |
| --- | --- |
| `amq-squad:wizard` | Goal intake, brief/rules/roles/profile setup, and one default-No `start` approval. |
| `amq-squad:cli` | Direct status, doctor, task, AMQ, gate, recovery, evidence, and read-only release planning. |
| `amq-squad:orchestrator` | Verified live-lead operation: dispatch, status review, convergence, recovery, and final evidence. |
| Legacy names | `amq-squad`, `amq-squad-orchestrator`, `amq-team-setup`, and `amq-squad-role-creator` are compatibility redirects only. |

Invoke skills in Claude Code as `/amq-squad:<skill>` and in Codex as
`$<skill>`.

During wizard setup, the recommended tool policy keeps the visible lead
broad and assigns each built-in worker its catalog-minimum profile. Choosing
`full_all` is an explicit opt-in, never an implicit default. Two or more
`full` members duplicate MCP/plugin context and increase memory and concurrency
pressure, so the review screen warns before that configuration proceeds.

Model guidance is intentionally skill-owned because it changes faster than the
binary. For v2.25.0, use the current model family and per-role model/effort
recommendations in the installed v2.25.0 skills; confirm the startup marker
`amq-squad skill v2.25.0` matches `amq-squad version`. Treat cost as a
tie-breaker after output quality for shippable work, and prefer installed-skill
guidance over copying model examples from this README.

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

To generate a role file instead of authoring one, `amq-squad role draft <id>
--binary claude|codex --purpose TEXT` drafts a reusable persona through the
profile's optional headless drafter
([yoetz](https://github.com/avivsinai/yoetz), a CLI-first multi-provider LLM
gateway; `claude -p`; `codex exec`; or a
custom argv template), validates its shape and neutrality deterministically,
and stages it without adding or launching a member. Without a configured
backend it prints the filled prompt for manual completion. See
[drafter backends](docs/drafter-backends.md) for the profile `drafter` block
and preset commands.

Model and effort picker suggestions can be overlaid globally in
`~/.amq-squad/catalog.json` and per project in
`<team-home>/.amq-squad/catalog.json`; the project layer wins. The catalog is
advisory and is not stored in `team.json`: explicit values still pass through,
with a warning for an effort tier that is not listed. A version-1 overlay uses
ordered object entries:

```json
{
  "schema_version": 1,
  "binaries": {
    "claude": {
      "models": [{"value": "opus", "label": "Opus", "enabled": true}],
      "efforts": [{"value": "max", "label": "Maximum", "enabled": true}]
    }
  }
}
```

Matching is case-insensitive while the winning entry's `value` spelling is
preserved. Later entries replace the same value without moving it;
`enabled:false` hides it. Missing files are normal, and a malformed or future
schema layer warns and falls back to the lower-precedence catalog.

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
`"permission_allowlist": ["Bash(go test ./internal/cli:*)"]`.
amq-squad
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
An allowlist grants native tool permission; it does not override the generated
team rules' `## Workspace Safety and Cleanup` prohibition on `rm -rf`.

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
- `amq` 0.60.0 on `PATH`
- `tmux` on `PATH` for Tier A managed panes
- macOS with iTerm2 for the Tier B backend
- macOS Terminal.app for the Tier C backend
- `pandoc` only when regenerating or checking `README.html`

amq-squad is tracker-neutral. Fetching GitHub, Jira, Confluence, or other goal
sources happens in the skills or operator tooling; the core binary owns team,
runtime, and coordination state.

AMQ 0.60.x is the supported series, with 0.60.0 as the minimum supported
release. Both real-AMQ matrices validate pinned v0.60.0 and `latest`; `latest`
remains a forward-compatibility canary and is not a support claim. Releases
older than 0.60.0 are rejected fail-closed. Because the version assertion is
skipped for `latest`, the pinned lane is the one that records what was proved.

| Real-AMQ lane | Runner | Versions |
| --- | --- | --- |
| Queue, routing, receipts, doctor, lifecycle | Ubuntu | `v0.60.0`, `latest` |
| Native real-PTY wake and teardown | macOS | `v0.60.0`, `latest` |

For externally injected lead and orchestrator wakes, new launch records use
`amq wake --retry-until injected` and persist that retry policy so exact
retirement replays the same target identity. Legacy records that omit the
field retain AMQ's historical `drained` default. AMQ owns lock/state binding,
quarantine, exact retirement, Darwin running-image identity, safe restart, and
newer installed-image adoption. amq-squad's cross-platform session notifier
remains the project-level tmux nudge; `amq-keepalive` is an optional,
operator-managed Darwin companion and is not installed or supervised by
amq-squad.

AMQ 0.47.1 introduced the supervised `coop exec` wake contract used by every
supported release: instead of injecting message headers or subjects, managed
coop wakes submit the fixed, shell-inert doorbell
`: AMQ doorbell run amq drain --include-body then act on it`. AMQ 0.49.1
extended that fixed doorbell to standalone/default wake injection, and every
supported release is now above that boundary, so the lane that used to pin it
is retired along with the pre-0.51 floors. All managed coop lanes require the
fixed doorbell. Agents must drain the durable mailbox to discover the sender,
subject, and body.

AMQ 0.49.1 also hardens wake delivery without changing amq-squad production
branches: transient foreground-process-group handoffs retry pending notices,
active input through max hold demotes to out-of-band output, quiet detection
requires consecutive samples, and periodic capability checks report only what
they can prove. Its optional Claude Stop hook guards by fresh message content
and recovers incomplete session context; amq-squad does not install or invoke
that upstream hook.

On Linux, AMQ 0.48.0 probes the legacy TIOCSTI capability. When the kernel
disables it, wake degrades to a non-input notifier and records
`injector_unsupported` diagnostics instead of pretending synthetic input was
delivered. The macOS real-PTY lane proves native injection where TIOCSTI is
available; AMQ's upstream tests own the Linux-only
`/proc/sys/dev/tty/legacy_tiocsti=0` fallback.

AMQ can inspect malformed configured mailbox layouts with
`amq doctor --json` and create only missing safe directories with
`amq doctor --fix-mailboxes --json`. Repair is explicit and fail-closed:
existing messages are not moved, overwritten, or deleted, discovered-only
mailboxes are not repair eligible, and unsafe filesystem state is refused. AMQ
0.49.0 adds actionable remedies for discovered-only mailboxes. `amq-squad
doctor` keeps its read-only ops check and never runs mutating repair
automatically; use upstream `amq doctor --json` for the canonical remedy text.

AMQ 0.49.0 also adds read-only `amq trace <message-id> --root <path> --json`,
which joins current message, route, delivery, DLQ, receipt, and thread evidence.
It is useful for diagnostics, but it does not prove historical directory-sync
or notification success and therefore is not retry or authorization authority
for amq-squad evidence/receipt flows. See the
[AMQ 0.51.x support assessment](docs/amq-0.51.x-assessment.md).

AMQ 0.42.1 historically introduced the complete injected identity contract;
0.60.0 is the supported floor for the 0.60.x series. After upgrading AMQ, stop and
resume/relaunch agents so their parent shells refresh the complete identity
tuple; a child command cannot repair stale parent environment variables.
Default-profile sessions use
`AM_ROOT`, `AM_BASE_ROOT`, non-empty `AM_SESSION`, and `AM_ME`. Named profiles
use their exact root with `AM_ROOT=AM_BASE_ROOT` and no `AM_SESSION`. Run
`amq-squad doctor` before
resuming if it reports a legacy or inconsistent pin.
