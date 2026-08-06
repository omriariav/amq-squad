# Lead loop, review tiers, and reconciliation

Extracted from `docs/skills.md` so the lead's playbook lives with the skill that
uses it. The guide remains the narrative overview; this file is the operational
detail.

## The loop

```
amq-squad status --session S --json
```

`status` returns one record-first runtime and coordination projection. Select one
bounded action from live records, claimed work, inbox state, and open gates. Act,
push the durable update, then park/end the turn so the session notifier can wake
new AMQ work. Re-read the active brief when scope, tasks, or status indicate an
update.

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
compare-and-swap error. `amq-squad evidence recover TASK ATTEMPT --me H --session S` fixes
it, and under parallel work that recover step is normal rather than exceptional.

## Dispatch and drain

Dispatch over durable AMQ, not pane injection. An AMQ message queues and survives pane
death; `amq-squad send` writes into a live pane and is the fallback or nudge only.

```sh
amq send --to fullstack --thread p2p/cto__fullstack --kind todo \
  --subject "Task: rate-limiter" --body - --wait-for drained --wait-timeout 60s <<'EOF'
Implement the rate-limiter per the brief. Push a review_request to me when the diff
is ready. Report any blocker as a question.
EOF
```

Confirm children are live BEFORE dispatching — a message to a dead pane queues silently
and looks delivered:

```sh
amq-squad status --session S --json | jq '.data.records[] | {role, status, pane_alive: .tmux.pane_alive}'
```

Children PUSH reports; the lead drains once rather than polls:

```sh
amq drain --include-body --root ROOT --me cto
```

### Wait posture is enforced, not advisory

In `lead_pane` mode the binary verifies the live roster pane before its own
blocking waits and REFUSES a configured lead when a caller-raised `gate/<topic>`
is unresolved, when a wait would exceed 120 seconds, or when a wait is unbounded.
That covers wrapped `amq watch` and amq-squad-owned send/reply waits.

The audited escape hatch is `--override-wait-posture --wait-posture-reason <why>`.

Direct external `amq watch` and hand-written `sleep`/`until` loops cannot be
intercepted and remain forbidden lead posture. Drain once, then park the turn and
let the session notifier wake it.
