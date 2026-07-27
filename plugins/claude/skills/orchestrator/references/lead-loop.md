# Lead loop, review tiers, and reconciliation

Extracted from `docs/skills.md` so the lead's playbook lives with the skill that
uses it. The guide remains the narrative overview; this file is the operational
detail.

## The loop

```
amq-squad next --session S --json
```

`next` returns one action object. Its priority order is the binary's: open
operator gates, then operator inbox backlog, then unacknowledged directives, then
stale operator poll loops. Do not re-derive that ordering in prose.

Exit 0 means an action is ready. **Exit 1 means idle** — a healthy state, not a
failure. Anything above 1 is a real error.

Act on the single returned action, then call `next` again. Re-read the brief only
when its digest changes.

## Dispatch

Dispatch durable `todo` messages linked to native tasks. Pane input is wake or
fallback only — when a durable task exists, its body is the authoritative task
body and a pane prompt is not.

Verify actor capability and task intent before dispatching. An implementation
dispatch requires an implementation-capable actor plus structured intent,
artifact, expected base, implementer, reviewer, and dependencies.

## Review risk tiers

Batch review at invariant or candidate-head boundaries, not after every
micro-edit. Match depth to risk:

| Risk | Change shape | Required evidence |
|---|---|---|
| Low | docs, projections | focused regular tests plus drift checks |
| Medium | state transitions | adversarial identity/idempotence tests, focused race tests |
| High | authority, lifecycle, release, recovery | exact-head full regular and race suites, immutable evidence |

Before any merge-ready claim: two independent reviewers verify the **exact head
SHA** being proposed, and `amq-squad verify merge` runs for that head. A review
against a branch name, a stale checkout, or an earlier SHA does not count.

## Reconciliation

Reconcile one invariant batch at a time. Completion atomically binds the
completion generation, the DONE report intent, and any exact task-scoped gate
correlation.

Completion reconciliation may clear only a request generation that is already
terminal or superseded. **It must never answer or close an unresolved human
gate.** An open human decision stays unsuppressed.

## Evidence

Record command evidence for anything a reviewer would otherwise re-run. Prefer
`amq-squad evidence run` so the attempt is immutable and bound to the task rather
than pasted into a message.

Two lifecycle constraints that cost real time when missed:

- The evidence cwd must be **inside the project**. A sibling worktree is
  refused, so create a detached worktree under the project and record there.
- That worktree must stay alive until `task done` completes, because DONE
  re-validates the attempt's command-subject snapshot in the recorded cwd.

If a blocker task completes during an evidence run, the link fails with a
compare-and-swap error. `amq-squad evidence recover TASK ATTEMPT --me H` fixes
it, and under parallel work that recover step is normal rather than exceptional.
