---
name: "amq-squad"
description: "Compatibility intent router for the amq-squad plugin. Routes goal preparation to wizard, direct operations to cli, and live lead work to orchestrator."
---
**Skill version: 2.23.1** - Start the first response by stating the loaded identity as `amq-squad skill v2.23.1` before routing status or analysis.

# amq-squad compatibility router

This legacy skill name remains during the v2.22 migration. Route the request to
one authoritative namespaced skill and follow that skill completely:

- Goal intake, team design, custom roles, artifact preparation, readiness, or
  launch preview: `amq-squad:wizard`.
- Status, doctor, task, activity, gate, context, AMQ inspection, lifecycle
  commands, verification, or evidence: `amq-squad:cli`.
- A verified visible lead coordinating an already-launched squad:
  `amq-squad:orchestrator`.

Do not run setup, direct operations, and the live lead loop from this router.
Existing invocations keep working only as compatibility routing; authoritative
behavior and future changes live in the three namespaced skills.

When a Claude role is explicitly allowed to clean up its own disposable review
worktree, keep the permission narrow: `Bash(amq-squad review-worktree remove:*)`.
This does not authorize raw recursive deletion.
