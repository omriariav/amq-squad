---
name: "wizard"
description: "Goal-first amq-squad preparation and launch wizard. Use when turning a request into reviewed coordination artifacts, proving roster and bootstrap readiness, or presenting the separate default-No launch gate. Triggers include \"set up a squad for X\", \"show me the plan\", \"prepare it\", \"is it ready\", \"launch it\", \"why did readiness block\". NOT for the live lead loop after launch (use amq-squad:orchestrator) and NOT for one-off status, task, or evidence commands (use amq-squad:cli)."
version: "2.25.0"  # x-release-please-version
allowed-tools: "Bash, Read, Write, Edit, MultiEdit, Glob, Grep, WebFetch"
argument-hint: "[request | stage goal|brief|rules|roles|profile|readiness|launch]"
user-invocable: true
trigger: "/wizard"
---
# amq-squad:wizard

Use this operator-facing skill before a new squad launches. It owns goal intake,
artifact preparation, readiness, and final launch preview. It never treats a
syntactically valid launch command as proof that the team is ready.

## Output rule: the CLI renders, you pass it through

Print CLI output **verbatim in a fenced block** — the proposal, the readiness table,
the digest. Do not re-typeset and do not re-summarise.

This matters most at the launch gate. `--prepare-plan` emits a digest and `--go
--goal-digest` consumes it: the digest **is** the proof the operator approved that
exact plan. Re-summarising before asking invites approval of something textually
different from what was accepted, and a re-rendered readiness table cannot be diffed
against the next run.

## Task Routing

| Operator says | Run |
|---|---|
| "set up a squad for X" | Full flow: `wizard <request>` |
| "show me the plan, don't write anything" | `run start ... --prepare-plan` |
| "go ahead and prepare it" | `run start ... --prepare` |
| "is it ready" | `run start ... --readiness-json` |
| "launch it" | `run start ... --go --goal-digest 'sha256:<accepted>'` |
| "just the roles stage" | `stage roles <request>` |
| "why did readiness block" | Read the blocked row's `fix` field; it names the exact command |
| "it launched but agents died" | Wrong skill for recovery → `amq-squad:cli`, then `doctor` |

## Gotchas

Every row below was hit in a real wizard run during v2.25.0. Items marked **fixed in
v2.25.0** still apply when an operator is on an older build, so the recovery is kept.

| Symptom | Cause | Exact fix |
|---|---|---|
| Readiness blocks on `worktree_isolation` naming a `--cwd` you cannot find | The fix text named a flag without naming a command (#538) | Give each mutation-capable member its own directory at creation with `new profile NAME --cwd "role=path,..."`, or on an existing roster with `team member update ROLE --cwd PATH`. **Fixed in v2.25.0**: the row now names scoped commands |
| You try `team shared-cwd-exception set` after a failed `--prepare` and it reports no profile | Preparation is transactional, so a profile it created was rolled back (#538) | Apply the fix at creation time: `new profile NAME --shared-cwd-exception "<reason>"`. **Fixed in v2.25.0**: the failure now says whether the profile was created-and-removed or restored |
| `new profile NAME --actor-mode ...` fails with "takes exactly one profile name" | Value-taking flags were dropped by the argument peeler, so the value fell through as a positional (#538) | **Fixed in v2.25.0**. On older builds, set actor modes with `team member update ROLE --actor-mode ...` after creation |
| Every agent dies at bootstrap with `namespace drift: accepted=X current=X` | `--project` was relative; the identity tuple compared a relative recording against a resolved path (#540) | **Fixed in v2.25.0**. On older builds, always pass an absolute `--project` |
| `--go` fails with a tool-policy source-set change listing the same files twice | Capability sources were recorded relative and compared absolute (#539) | **Fixed in v2.25.0**. On older builds, run the member at `full` tool profile to get past it |
| `up` reports success but panes sit at a shell prompt | Agent bootstrap failure was not surfaced to the launcher (#540) | **Fixed in v2.25.0**: the launch now fails and names the member and its pane error. On older builds, read each pane before trusting the success line |
| Readiness passes every row and then `--go` fails | Readiness was not checking what spawn checks (#539) | **Fixed in v2.25.0**: both call one predicate. On older builds, treat readiness as necessary but not sufficient |
| `team shared-cwd-exception set` fails with `flag provided but not defined: -session` | The remedy text shows no flags, and unlike the rest of the flow this command takes `--profile` but NOT `--session` | Drop `--session`: `team shared-cwd-exception set "<reason>" --project P --profile R` |
| `run start` refused: profile is `pinned to workstream X, not S` | `new profile` without `--session` derives the workstream from the project directory name | Pass `--session S` when creating the profile, or use the pinned name |
| An unscoped command mutates the wrong roster | Named profiles need `--profile`; unscoped resolution may pick another live record | Always pass `--project` and, for named profiles, `--profile` |

## Before the first `--prepare`: two things that must be set at creation

Both are transactional traps. Preparation rolls back a profile it created, so a fix that
modifies an existing profile has nothing to act on — measured at 4 CLI invocations with
these set, versus a dead end without them.

**1. A roster with 2+ mutation-capable members needs its isolation decided up front.**
Readiness blocks otherwise. Choose one at creation:

```sh
# each member in its own worktree -- preferred when members really do write code
amq-squad new profile NAME --roles cto,qa --orchestrated --lead cto \
  --project P --session S --cwd "cto=/path/a,qa=/path/b"
# or record an explicit exception when they will not mutate in parallel
amq-squad new profile NAME --roles cto,qa --orchestrated --lead cto \
  --project P --session S --shared-cwd-exception "<reason>"
```

**2. Pin `--session` at creation.** Without it the profile is pinned to a workstream
derived from the project directory name, and a later `run start --session S` is refused
with "pinned to workstream X, not S; no team members would run for the requested
session".

## Immutable stage contract

The stages are `goal`, `brief`, `rules`, `roles`, `profile`, `readiness`, and
`launch`. Every stage defaults to read-only. A later stage consumes the accepted
output of the earlier stage without silently changing its goal, namespace,
rosters, topology, role contracts, or tool policy.

Preparation and launch are separate approvals:

1. Render the proposal and exact project-local mutations.
2. Obtain explicit preparation approval before writing coordination artifacts.
3. Run readiness against the written artifacts and generated bootstrap preview.
4. Present a separate default-No launch confirmation for exactly the displayed
   initial roster.

Preparation never launches panes. Launch never repairs or rewrites accepted
artifacts.

For a non-interactive operator or CI flow, preserve the same four stages and
the same argv identity:

```sh
amq-squad run start --project P --profile R --session S --roles cto,qa \
  --lead cto --launch-shape working-team-together --goal "..." --prepare-plan
# Default No: repeat only after the operator accepts the rendered proposal.
amq-squad run start --project P --profile R --session S --roles cto,qa \
  --lead cto --launch-shape working-team-together --goal "..." --prepare
amq-squad run start --project P --profile R --session S \
  --launch-shape working-team-together --readiness-json
# Separate default-No launch approval; use the exact accepted binding values.
amq-squad run start --project P --profile R --session S \
  --launch-shape working-team-together --goal "..." \
  --goal-source operator_goal --goal-digest 'sha256:<accepted-digest>' --go
```

`--prepare-plan`, `--prepare`, and `--go` are not aliases for one another.
Never tell an operator to jump from a generic preview directly to `--go`.

## Goal binding

A launch requires an actionable goal binding for the visible lead. Show its
source, exact `profile/session` namespace, text or bounded digest, delivery
method, and validation status.

- Explicit goal text is reviewed verbatim.
- When goal text is blank and the exact namespace already has a real accepted
  non-stub brief, derive the deterministic directive `Execute the accepted
  brief for namespace <profile>/<session> at <path>.` and require operator
  acceptance. Never rewrite the brief.
- Blank goal plus a missing or generated-stub brief fails readiness. It must
  never produce a live `prompt_goal_missing` run.

## Composition proposal

Render separately:

- initial launch roster: count, names, binary, model, effort, intent, mutation
  authority, and effective tool profile;
- staged-later roster: count, names, join condition, and spawn-gate requirement;
- launch shape: explicitly `working-team-together` or `lead-only-staged`.

Orchestration or a visible lead never implies lead-only launch. Existing
profiles without an accepted launch shape are `legacy/unspecified` and require
operator confirmation.

## Readiness rows

Emit machine-readable rows with `ready`, `missing`, `stub`, `generic`, `stale`,
or `drifted`, plus evidence and deterministic fix/preview actions, for:

- accepted brief and goal binding;
- team rules and operator/orchestration policy;
- every initial and staged role contract;
- profile membership, binary/model/effort/execution/tool policy;
- initial versus staged roster equality;
- one generated bootstrap row for every initial member and no staged-only role;
- binary/skill, AMQ, pointer, and launch-capability diagnostics.

Readiness fails closed when any required row is not `ready`, profile/bootstrap
membership differs from the accepted initial roster, or the goal binding is not
verified. Runtime terminal capability data is consumed through the CLI-owned
diagnostic contract; the wizard does not infer a backend.

## Tool policy

For multi-agent teams recommend a broad lead and the smallest sufficient worker
profile (`minimal`, `coding`, `browser`, `data`, or explicit `full`). Show the
effective policy and source for each member. Claude settings overlays and Codex
profiles must be materialized before the binary starts. Never silently remove a
capability the operator explicitly requested.

## References

- `LEARNINGS.md` — field failures from real preparation and launch runs
- `references/stages.md` — the seven stages, what each consumes and produces, and
  which are read-only
- `references/roles.md` — role selection, custom role contracts, and the templates
- `references/readiness.md` — every readiness row, what blocks it, and the exact fix
- `references/worktrees.md` — worktree isolation, why 2+ mutation-capable members
  need separate directories, and how to give them one
- `references/team-archetypes.md` — roster shapes and when each fits
- `references/briefs-template.md` — the brief structure preparation writes
- `references/pointer-stub-template.md` — the managed block `team sync --apply` writes
- `../amq-squad/references/team-rules-template.md` — team-rules starting point; it
  stays under the router because the binary's tests pin that path

## Invocation

- Full flow: `wizard <request>`.
- One stage: `stage goal|brief|rules|roles|profile|readiness|launch <request>`.
- Canonical binary UI: `amq-squad wizard --project P --profile R --session S`.

After launch, the visible lead uses `amq-squad:orchestrator`; direct operations
and diagnostics use `amq-squad:cli`.
