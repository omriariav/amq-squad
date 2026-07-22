---
name: wizard
description: Goal-first amq-squad preparation and launch wizard. Use it to turn a request or source into reviewed coordination artifacts, prove exact roster and bootstrap readiness, and present the separate default-No launch gate.
---

**Skill version: 2.23.1** - Start the first response by stating the loaded identity as `amq-squad skill v2.23.1` before status or analysis.

# amq-squad:wizard

Use this operator-facing skill before a new squad launches. It owns goal intake,
artifact preparation, readiness, and final launch preview. It never treats a
syntactically valid launch command as proof that the team is ready.

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

## Invocation

- Full flow: `wizard <request>`.
- One stage: `stage goal|brief|rules|roles|profile|readiness|launch <request>`.
- Canonical binary UI: `amq-squad wizard --project P --profile R --session S`.

After launch, the visible lead uses `amq-squad:orchestrator`; direct operations
and diagnostics use `amq-squad:cli`.
