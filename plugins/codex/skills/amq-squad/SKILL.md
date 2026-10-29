---
name: "amq-squad"
description: "Compatibility intent router for the amq-squad plugin. Routes goal preparation to wizard, direct operations to cli, and live lead work to orchestrator."
version: "3.0.0"  # x-release-please-version
---
# amq-squad compatibility router

This legacy skill name remains for compatibility. Route the request to one
authoritative namespaced skill and follow that skill completely:

- Goal intake, roster and custom roles, the squad.yaml digest flow
  (`team propose`, `team apply`), the brief, or the launch: `amq-squad:wizard`.
- Status, doctor, task, evidence, guard, gate status, AMQ context, or merge and
  release verification: `amq-squad:cli`.
- The lead seat of a launched squad planning tasks, dispatching, widening, running
  the merge train, raising gates or preparing a release: `amq-squad:orchestrator`.

Do not run setup, direct operations, and the live lead loop from this router.
Existing invocations keep working only as compatibility routing; authoritative
behavior and future changes live in the three namespaced skills.

amq-squad 3.0 removed the v2 lifecycle verbs (`start`, `resume`, `down`, `send`,
`init`, `wizard`). A request phrased in those terms routes by intent: "start the
team" is a launch (`amq-squad:wizard`), "who is up" is status (`amq-squad:cli`).

Disposable review worktrees follow the canonical workspace rules: create them at an
explicit temporary path with `git worktree add --detach`, and clean up that exact path
with `git worktree remove --force`. This does not authorize raw recursive deletion.
