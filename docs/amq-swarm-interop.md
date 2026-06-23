# AMQ swarm interop decision

Issue: [#202](https://github.com/omriariav/amq-squad/issues/202)

## Decision

For v2.7.0, amq-squad ships AMQ swarm interop as documented guidance only.
There is no native bridge, import command, export command, or status/console
swarm view in this release.

The source of truth for amq-squad work remains `.amq-squad/tasks/<session>/`.
AMQ `swarm` is treated as an external Claude Code Agent Teams interop surface:
useful for receiving or publishing task lifecycle evidence through AMQ, but not
authoritative for amq-squad goal decomposition, dependencies, rosters, profiles,
or role ownership.

## Evidence

AMQ 0.38.0 exposes these swarm verbs:

- `amq swarm list`
- `amq swarm join`
- `amq swarm leave`
- `amq swarm tasks`
- `amq swarm claim`
- `amq swarm complete`
- `amq swarm fail`
- `amq swarm block`
- `amq swarm bridge`

The command help defines the storage and ownership boundary:

- `amq swarm list` discovers Claude Code Agent Teams from `~/.claude/teams/`.
- `amq swarm tasks` reads shared task lists from `~/.claude/tasks/{team-name}/`.
- `amq swarm join` mutates the Agent Teams `config.json`.
- `amq swarm bridge` watches an Agent Teams task list and delivers
  notifications through AMQ.

The amq-squad native task store has different ownership:

- It stores tasks under `.amq-squad/tasks/<session>/`, scoped to the project and
  workstream.
- It includes `task add`, so an amq-squad lead can decompose a goal into work.
- It gates `claim` on dependency completion.
- It is binary-neutral. Claude and Codex workers use the same task store.
- It is tied to amq-squad rosters, profiles, role handles, and workstream
  briefs.

These semantics make `amq swarm` a peer interop protocol, not a replacement
task store.

## Semantics comparison

| Capability | `amq-squad task` | `amq swarm` |
| --- | --- | --- |
| Primary storage | `.amq-squad/tasks/<session>/` | `~/.claude/tasks/{team-name}/` |
| Owner | amq-squad project workstream | Claude Code Agent Teams |
| Goal decomposition | `task add` creates native tasks | no create verb in the exposed AMQ surface |
| Dependencies | native `depends_on` gate before claim | not exposed in AMQ swarm help |
| Assignment identity | amq-squad role or handle | Agent Teams `agent_id` from team config |
| State names | `pending`, `in_progress`, `completed`, `failed`, `blocked` | same visible state vocabulary |
| Mutation verbs | add, claim, done, fail, block | join, leave, claim, complete, fail, block, bridge |
| Binary model | Claude and Codex neutral | Claude Code Agent Teams interop |
| AMQ integration | dispatch and worker reports are AMQ messages | bridge emits AMQ notifications from swarm changes |

## Supported interop mode

Use AMQ swarm as a notification and adoption boundary:

1. If a Codex or amq-squad worker participates in a Claude Code Agent Team, run
   `amq swarm join` and `amq swarm bridge` according to AMQ's help.
2. Treat bridge-delivered AMQ messages as external evidence.
3. If the work should become part of an amq-squad run, create or update a native
   amq-squad task through `amq-squad task add`, `claim`, `done`, `fail`, or
   `block`.
4. Preserve provenance in the native task title, description, evidence, failure
   reason, or block reason, including the swarm team and task ID when known.

This keeps mutation single-owned while still allowing Claude Code Agent Teams
events to influence an amq-squad workstream.

## Unsupported in v2.7.0

The following flows are intentionally not implemented:

- A status or console pane that reads `~/.claude/tasks` directly.
- A mutable status or console action that claims, completes, fails, or blocks a
  swarm task.
- A bidirectional sync loop between `.amq-squad/tasks` and `~/.claude/tasks`.
- Automatic import of all swarm tasks into `.amq-squad/tasks`.
- Automatic export of all amq-squad tasks to an Agent Teams task list.

The reason is ownership clarity. A mirrored task would need stable ID mapping,
conflict resolution, provenance storage, assignee identity mapping, dependency
translation, and clear failure/block propagation. Without those pieces, a bridge
would make status/console output look more authoritative than it is.

## Status and console guidance

Until a real bridge exists, status and console output must not present swarm
tasks as native, claimable amq-squad work.

If a future read-only view is added, each row must visibly identify:

- `source=amq-squad` or `source=amq-swarm`
- the authoritative store path or external team name
- the external task ID when applicable
- whether the row is mutable from amq-squad

Read-only swarm rows must not expose copy-ready mutation actions unless the
mutation path is implemented and tested.

## Future implementation gates

An MVP bridge is worth revisiting only with tests and fixtures for:

- deterministic ID mapping between swarm task IDs and amq-squad task IDs
- state mapping for `pending`, `in_progress`, `completed`, `failed`, and
  `blocked`
- failure and block reason propagation
- assignee mapping between Agent Teams `agent_id` and amq-squad handles
- conflict behavior when both stores change
- status/console rendering that labels source of truth and mutability

Until then, docs-only interop is the safer supported mode.
