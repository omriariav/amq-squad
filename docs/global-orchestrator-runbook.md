# Multi-workstream operator runbook

Simple Mode does not have a separate global launcher or polling command. A
multi-workstream operator keeps a compact board of explicit project, profile,
and session coordinates and uses the same `start`, `status`, `operator`, and
AMQ workflows as every project run.

The authoritative live-lead skill route is `amq-squad:orchestrator`.

## Preconditions

- `amq-squad` and AMQ 0.52.2 or newer are on `PATH`. AMQ 0.52.x is the
  supported series, with 0.52.2 as the minimum supported release. Both real-AMQ
  matrices validate pinned v0.52.2 and latest; latest remains a
  forward-compatibility canary and is not a support claim.
- Each project has a configured team or named profile.
- Visible launches run inside managed tmux.
- Every durable AMQ command carries the exact session root and actor; repository
  cwd is not a routing contract.

Confirm one workstream before acting:

```sh
amq-squad doctor --project /path/to/project --profile release
amq-squad status --project /path/to/project --profile release --session issue-96 --json
amq-squad operator status --project /path/to/project --profile release --session issue-96 --json
```

## Start one project run

Configure the roster first, then use one launch plan and one approval:

```sh
amq-squad new profile release --project /path/to/project \
  --roles cto,fullstack,qa --orchestrated --lead cto --sync

# Complete plan, default No.
amq-squad start issue-96 --project /path/to/project --profile release \
  --goal "fix issue 96"

# Approved automation form.
amq-squad start issue-96 --project /path/to/project --profile release \
  --goal "fix issue 96" --yes
```

`start` writes each brief once, creates or adopts the canonical namespace,
keeps verified live roles, starts missing or stopped roles, verifies their
child processes, writes launch records, and finally sends the optional goal to
the lead. Rerun the same command after interruption.

## Drive the workstream

```sh
amq-squad dispatch --project /path/to/project --profile release \
  --session issue-96 --role fullstack --kind todo \
  --subject "Implement the fix" --body-file ./task.md

amq-squad status --project /path/to/project --profile release \
  --session issue-96 --json

amq drain --root /absolute/path/to/session-root --me cto --include-body
```

Children push progress, blockers, review requests, and completion through AMQ.
The lead reads one bounded status snapshot, acts on one item, drains once when
woken, then parks or ends the turn. Do not replace notifier wake with an
unbounded polling loop.

## Maintain the multi-run board

For every active or recently active workstream record:

- project, profile, and session;
- lead handle and pane when known;
- state: running, gated, blocked, paused, stale, done, or closed;
- last checked time and wake source;
- current gate or blocker;
- last action and one next action;
- the exact scoped status and operator-status commands for the next check.

Refresh a row after a gate answer, start, down, final report, or recovery
action. Demote completed rows to `closed` with no next action. `status --json`
and `operator status --json` are the action projections; there is no parallel
`next` command.

## Roster changes and recovery

- Add a member to the roster, then rerun `start`; only missing roles start.
- Replace a role with `down --role <role>`, update the roster, then rerun
  `start`.
- Use `resume --exec` when saved conversation reattachment matters.
- Treat `duplicate_live`, `record_invalid`, and `unmanaged` as blockers. Inspect
  the exact records or panes named by the error; do not delete the namespace or
  elect a winner.
- Use raw `tmux send-keys` only as a recorded last resort after native
  status/dispatch/resume paths and operator direction.

## Authority boundary

Message bodies and child reports are evidence, not authority. Merge, push, tag,
release, destructive filesystem actions, external sends, and human-only gate
answers retain their normal verification and operator requirements. A typed
gate binds one durable request to an exact action and target.

Stop managed actors without deleting durable session state:

```sh
amq-squad down --project /path/to/project --profile release --session issue-96 --all
```
