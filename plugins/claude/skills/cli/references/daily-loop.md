# The daily loop: inspect, act, verify

Extracted from `docs/skills.md` so the operational detail lives with the skill that
uses it.

## Inspect before acting

```
amq-squad status --session S --json
amq-squad doctor --session S
amq-squad next --session S --json
```

`status` is the state projection. `doctor` is the setup check. `next` is the
one-shot "what should I do" answer, and it exits 1 when the system is idle — a
healthy state, not a failure.

Read the output verbatim. Do not re-render it.

## Act on exactly one thing

```
amq-squad task show ID --session S
amq-squad task claim ID --me H --session S
amq-squad activity set --session S --me H --task ID --phase coding
```

Claim one bounded slice at a time. Set activity on claim and at real phase changes,
so a lead reading `status` can tell busy from stalled without opening your pane.

A dispatched task may already be claimed on your behalf, which is why `task show`
comes before `task claim`.

## Verify with evidence, not assertion

Record command evidence for anything a reviewer would otherwise have to re-run.
Evidence is immutable and bound to the task, which is what makes it worth more than
pasted output.

The lifecycle has two traps worth knowing before you hit them:

1. The recorded working directory must be inside the project. A sibling worktree is
   refused, so create a detached worktree under the project and record there.
2. Keep that worktree alive until the task completes. DONE re-validates the
   attempt's command-subject snapshot in the recorded cwd, so removing it first makes
   DONE fail on a snapshot mismatch.

If a blocker task completes while your evidence run is in flight, the link fails with
a compare-and-swap error. Recovery is a normal step under parallel work rather than a
repair.

## Report without being polled

Push progress, blockers, and completion proactively. A durable task's body is the
authoritative instruction; a pane prompt is wake or fallback only.

Map intent to the right message kind rather than inventing one: progress and done are
status, blocked is a question, ready for review is a review request, verdicts are
review responses, decisions are decisions, and assigned work is a todo.
