---
name: "amq-squad"
description: "Compatibility intent router for the amq-squad plugin. Routes goal preparation to wizard, direct operations to cli, and live lead work to orchestrator."
version: "2.31.0"  # x-release-please-version
allowed-tools: "Bash, Read, Write, Edit, MultiEdit, Glob, Grep"
argument-hint: "[drain | review | handoff | status | start | focus | send | resume | down | doctor]"
user-invocable: true
trigger: "/amq-squad"
---
# amq-squad compatibility router

This legacy skill name remains during the v2.22 migration. Route the request to
one authoritative namespaced skill and follow that skill completely:

- Goal intake, team design, custom roles, team/profile setup, or launch preview:
  `amq-squad:wizard`.
- Status, doctor, task, gate, AMQ inspection, lifecycle
  commands, verification, or evidence: `amq-squad:cli`.
- A verified visible lead coordinating an already-launched squad:
  `amq-squad:orchestrator`.

Do not run setup, direct operations, and the live lead loop from this router.
Existing invocations keep working only as compatibility routing; authoritative
behavior and future changes live in the three namespaced skills.

Disposable review worktrees follow the canonical workspace rules: create them at
an explicit temporary path with `git worktree add --detach`, and clean up that exact
path with `git worktree remove --force`. This does not authorize raw recursive
deletion.
