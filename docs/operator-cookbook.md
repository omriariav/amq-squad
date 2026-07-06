# amq-squad Operator Cookbook

This cookbook is the root operator workflow for v2.12.0. It covers the public
CLI paths an operator uses to start, monitor, steer, approve, and close an
orchestrated run.

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

**When to use `goal apply`:** Use it only after the matching gate has a real
operator `APPROVED:` answer and the visible lead has a native goal binding.
`goal apply` verifies both before delivering.

**How to stop cleanly:** Resolve the exact profile/session first, then use the
scoped stop command from status JSON when available:

```sh
amq-squad status --project <project> --profile <profile> --session <session> --json
amq-squad stop --project <project> --profile <profile> --session <session> --all --close-panes
```
