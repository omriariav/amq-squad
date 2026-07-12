# amq-squad Operator Cookbook

This cookbook is the root operator workflow for v2.12.0. It covers the public
CLI paths an operator uses to start, monitor, steer, approve, and close an
orchestrated run.

## Attention-only notifications

Create a profile with `--operator-notifications` to enable the default desktop
sink. Live `run start`/`up`/`resume --exec` supervises one scoped notification
watcher on that launch host; its lease and heartbeat are independent from the
operator poll lease, so lead-pane mode remains notification-capable even when
`poll_required=false`. `stop --all`, `rm`, and `archive` reconcile it to an
inactive state. `status` and `doctor` expose watcher health without exposing
command arguments, credentials, or other secrets. This add-on never answers a
gate, clicks approval, or sends pane input.

Delivery is **at least once**, not exactly once. The supervised watcher, a
manual `operator watch`, and `notify --deliver` all coordinate through the same
per-event/per-sink reservation and success-commit state in
`.amq-squad/notify-state.json`. Each reservation lasts for the configured sink
timeout plus a 5-second commit margin (15 seconds by default, up to 65 seconds
at the supported maximum timeout). A sink side effect can succeed and the
process can die before its success commit; the other drivers suppress that
event until reservation expiry, then retry it. The bounded window limits
concurrent replay and retry delay, not total duplicates: repeated ambiguous
crashes can replay repeatedly, committed errors retry, and renotify or
`--force-resend` intentionally repeats delivery. Use idempotent command sinks.

## Prerequisites

Confirm the installed binary and local health before starting a run:

```sh
amq-squad version
amq-squad doctor --project <project> --profile <profile>
amq-squad team profiles --project <project> --json
```

Use `--profile` whenever the project has more than one profile. A profile names
the team configuration; a session names one workstream inside that profile. For
named profiles, keep every lifecycle and operator command scoped with both
`--profile <profile>` and `--session <session>`.

Inspect the active workstream before mutating anything:

```sh
amq-squad status --project <project> --profile <profile> --session <session> --json
amq-squad operator status --project <project> --profile <profile> --session <session> --json
```

## Milestone Run: Codex Lead

Use a Codex lead when the release needs native goal control and code review in
the lead pane.

```sh
amq-squad goal start --project <project> --profile <profile> --session <session> --goal "<milestone goal>" --dry-run --json
amq-squad goal start --project <project> --profile <profile> --session <session> --goal "<milestone goal>" --yes --json
amq-squad operator watch --project <project> --profile <profile> --session <session> --once --json
```

At each decision point, ask for the single next operator action:

```sh
amq-squad next --project <project> --profile <profile> --session <session> --json
```

When a gate is ready to approve, answer it structurally on the same
`gate/<topic>` thread:

```sh
amq-squad operator answer --project <project> --profile <profile> --session <session> --gate <topic> --to <lead-handle> --approved --reason "<reason>" --json
```

After a matching gate has an `APPROVED:` answer, apply the approved lead goal:

```sh
amq-squad goal apply --project <project> --profile <profile> --session <session> --role <lead-role> --gate <topic> --yes --json
```

## Milestone Run: Claude Lead

Use a Claude lead when the lead is a Claude member in the configured team. The
operator path is the same AMQ-first flow; the lead handle and role come from the
team profile.

```sh
amq-squad status --project <project> --profile <profile> --session <session>
amq-squad operator status --project <project> --profile <profile> --session <session> --json
amq-squad operator directive --project <project> --profile <profile> --session <session> --to <lead-handle> --subject "<directive>" --body "<directive body>" --json
```

Use `operator answer` only for `gate/<topic>` decisions. Use
`operator directive` for steering data such as priority changes or requested
next checks.

## CLI-Only Operator Flow

For an operator who is not working inside an agent pane, the CLI loop is:

```sh
amq-squad team profiles --project <project> --json
amq-squad status --project <project>
amq-squad status --project <project> --profile <profile> --session <session> --json
amq-squad next --project <project> --profile <profile> --session <session> --json
```

If `next` returns a gate action, inspect before answering:

```sh
amq-squad thread --project <project> --profile <profile> --session <session> --id gate/<topic> --include-body
amq-squad operator answer --project <project> --profile <profile> --session <session> --gate <topic> --to <lead-handle> --approved --reason "<reason>"
```

If `next` reports idle with exit code 1, there is no current operator action for
that scoped profile/session.

## Multi-Run Global Orchestrator Board

When one `global_orchestrator` conversation owns more than one active or
recently active run, keep a compact board in the conversation and update it
after every poll, gate answer, spawn, stop, final report, or recovery action.

Minimum fields:

| Field | What to record |
| --- | --- |
| Name / repo | Short run label and target repo or project root. |
| Profile / session | Exact namespace for commands. |
| Lead / pane | Visible lead role/handle and pane id when known. |
| State | `running`, `gated`, `blocked`, `paused`, `stale`, `done`, or `closed`. |
| Last checked / next poll | Absolute check time and the next poll or wake source. |
| Gate / blocker | Current `gate/<topic>`, operator decision, or blocker. |
| Last action / next action | Last step taken and one concrete next action. |
| Polling commands | Exact commands for the next check. |

For `poll_required=true`, use deterministic commands such as:

```sh
amq-squad monitor --project <project> --profile <profile> --session <session> --once --json
amq-squad status --project <project> --profile <profile> --session <session> --json
amq-squad operator status --project <project> --profile <profile> --session <session> --json
amq-squad next --project <project> --profile <profile> --session <session> --json
```

Demote finished workstreams to `closed` with `next action: none - closed` so
they stop competing with `gated`, `blocked`, or `stale` rows. Recovery should
use native amq-squad paths first: inspect `status`/`monitor`/gates/tasks,
re-nudge queued work with `dispatch` or drain-only `send`, resume stale agents
from `status --json.actions[]` or `resume --json`, and mark native `/goal`
blockers as `paused`. Raw `tmux send-keys Enter` is a recorded last resort only
after operator direction or when native recovery is unavailable.

## Command Primitive Decision Table

When steering a live squad, choose the command family by intent:

| Intent | Use | Why |
| --- | --- | --- |
| Supervise a run | `amq-squad status`, `operator status`, `operator watch`, `next`, `task`, `collect` | These commands resolve the project/profile/session and show the squad model. Use `collect` for lead-side reports when raw AMQ would say `refusing collect` of a `lead-owned mailbox`; it follows the #322 collect-vs-drain contract. |
| Tell the visible lead something now | `amq-squad send --project <project> --profile <profile> --session <session> --role <lead-role> --body "..."` | This is live tmux pane delivery to the recorded agent pane. It is **not** a durable AMQ protocol message: no `--kind`, no `--thread`, no mailbox receipt. |
| Assign durable work and wake a recipient | `amq-squad dispatch --project <project> --profile <profile> --session <session> --role <role> --kind todo --subject "..." --body "..."` | Dispatch sends a durable AMQ task to the resolved workstream root and wakes or nudges the agent to drain it. This is the usual lead-to-worker path. |
| Read or write AMQ mailboxes directly | Raw `amq send/read/drain/thread` only from the correct coop/session shell, or with an explicit `--root`. From an external pane, prefer `amq-squad amq ...`. | Raw AMQ is mailbox plumbing, not squad routing. If the profile/session root is wrong, you can reproduce #328-style namespace mistakes: `implicit default-profile mutation`, `legacy/default session root`, or `refusing before write`. |

Typical orchestrated flow: the operator uses `amq-squad send` or
`operator directive` to steer the visible lead; the lead uses `task`,
`dispatch`, and `collect` to coordinate workers. Do not use raw AMQ from an
external operator pane unless the mailbox root is explicit.

Ambiguous from an external pane:

```sh
amq send --session <session> --to <worker-handle> --thread p2p/<lead>__<worker> \
  --kind todo --subject "Task" --body "..."
```

Root-resolving alternatives:

```sh
amq-squad amq send --project <project> --profile <profile> --session <session> \
  --to <worker-handle> --thread p2p/<lead>__<worker> \
  --kind todo --subject "Task" --body "..."

amq send --root <project>/.agent-mail/<profile>/<session> \
  --to <worker-handle> --thread p2p/<lead>__<worker> \
  --kind todo --subject "Task" --body "..."
```

## Issue Or Dogfood Run

Start with a dry run, then confirm delivery:

```sh
amq-squad goal start --project <project> --profile <profile> --session <session> --goal "<issue or dogfood goal>" --dry-run --json
amq-squad goal start --project <project> --profile <profile> --session <session> --goal "<issue or dogfood goal>" --yes --json
```

Monitor with a read-only command:

```sh
amq-squad operator watch --project <project> --profile <profile> --session <session> --once
amq-squad next --project <project> --profile <profile> --session <session>
```

Send a directive when the operator needs to steer the lead:

```sh
amq-squad operator directive --project <project> --profile <profile> --session <session> --to <lead-handle> --subject "<directive>" --body "<body>"
```

Answer approval gates with `operator answer`; do not treat p2p prose such as
"pending operator" or "manual approval" as an approval.

## Common Failures

### Topology Or Launch Fault

Inspect the scoped status JSON and use the emitted repair command:

```sh
amq-squad status --project <project> --profile <profile> --session <session> --json
```

Repair fault objects include a `remedy` action. Copy the `remedy.command` only
after verifying it targets the intended profile and session.

### Version Or Skill Skew

Run the doctor against the same profile and project, or inspect the scoped
status JSON:

```sh
amq-squad doctor --project <project> --profile <profile> --json
amq-squad status --project <project> --profile <profile> --session <session> --json
```

Read `data.versions`: it names the running binary, the `amq-squad` on `PATH`,
Codex and Claude plugin-cache manifests, and the skill marker where detectable.
Fix reported binary, AMQ, plugin, or skill mismatches before launching more
agents; `up` repeats detectable version-alignment warnings before launch.

### Missing Live Lead

Check the scoped status and resume plan:

```sh
amq-squad status --project <project> --profile <profile> --session <session> --json
amq-squad resume --project <project> --profile <profile> --session <session> --json
```

Use the exact resume command from the JSON actions when possible. External lead
panes are operator-owned; do not close or kill them as part of managed worker
teardown.

### Missing Approval Gate

`goal apply` requires a real operator `APPROVED:` answer on the matching
`gate/<topic>` thread. Inspect the gate and answer it before applying:

```sh
amq-squad thread --project <project> --profile <profile> --session <session> --id gate/<topic> --include-body
amq-squad operator answer --project <project> --profile <profile> --session <session> --gate <topic> --to <lead-handle> --approved --reason "<reason>"
amq-squad goal apply --project <project> --profile <profile> --session <session> --role <lead-role> --gate <topic> --yes --json
```

## FAQ

**Visible lead vs operator:** The visible lead owns the goal execution in an
agent pane. The operator is the human-facing AMQ participant, usually `user`,
and answers gates or sends directives. The operator is not a runnable agent.

**Poll vs watch:** `operator status` is a read-only snapshot. `operator poll`
reads the operator workload and may claim a loop lease unless `--readonly` is
used. `operator watch` repeats the poll loop on an interval.

**Profile vs session:** A profile selects the team configuration. A session
selects the workstream. In projects with named profiles, pass both values on
every lifecycle, status, operator, goal, and repair command.

**Gate answer vs p2p prose:** A gate answer is an AMQ `answer` message on
`gate/<topic>` with a subject such as `APPROVED:` or `DENIED:`. P2P prose is
evidence only; it does not authorize `goal apply`, merge, release, teardown, or
external side effects.

**Bounded self-operator setup:** For a fresh exact session, use
`amq-squad run start --operator-mode self_operator --self-operator-lead cto --self-operator-allow merge ...`.
No allowlist is inferred. Existing profiles are authoritative; change them only
with `amq-squad team operator set`. Spawn, releases, tags, publishing, external
sends, and destructive filesystem actions remain human-only. A self-approved
merge must be executed by a different verified actor. `self_approved` and
`human_only_gate` notifications are attention-only and never satisfy
`verify action`.

`team operator set` fails closed when invoked from any tmux pane if the target
project has no resolvable AMQ root. This is correct but can be rough during
first-time setup: initialize the project namespace first, then run the policy
command from a manual/non-agent control plane (outside the squad's agent
panes).

**When to use `goal apply`:** Use it only after the matching gate has a real
operator `APPROVED:` answer and the visible lead has a native goal binding.
`goal apply` verifies both before delivering.

**How to stop cleanly:** Resolve the exact profile/session first, then use the
scoped stop command from status JSON when available:

```sh
amq-squad status --project <project> --profile <profile> --session <session> --json
amq-squad stop --project <project> --profile <profile> --session <session> --all --close-panes
```
