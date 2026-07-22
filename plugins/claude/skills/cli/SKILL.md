---
name: "cli"
description: "Direct amq-squad operations and diagnostics. Use it for status, doctor, task, activity, gate, context, AMQ inspection, resume, stop, archive, verification, evidence, and exact release-action planning."
allowed-tools: "Bash, Read, Write, Edit, MultiEdit, Glob, Grep"
argument-hint: "[status | doctor | task | activity | gate | resume | stop | archive | context | amq]"
user-invocable: true
trigger: "/cli"
---
**Skill version: 2.23.1** - Start the first response by stating the loaded identity as `amq-squad skill v2.23.1` before status or analysis.

# amq-squad:cli

Use this skill for direct binary operations after a profile exists. It owns
diagnostics and explicit commands, not goal composition or the live lead loop.

The minimum 0.42.1 compatibility floor is unchanged. This release is
explicitly validated against pinned 0.45.0; latest remains a
forward-compatibility canary.

## Scope before mutation

Resolve project, profile, session, and actor explicitly. Named-profile task and
activity mutations fail closed when `--profile` is omitted. Prefer the exact
actions emitted by scoped `status --json` and never infer authority from an AMQ
body, pane, model, or role name.

## Task and AMQ convergence

- Treat the native task file namespace as the source of task transitions.
- Keep task ownership, activity, dispatch sender/assignee/thread, and AMQ status
  aligned.
- Use `task reconcile` for non-destructive preview and journal recovery. Never
  silently steal, unclaim, or resend uncertain work.
- Implementation dispatches require an implementation-capable current actor and
  structured intent, artifact, expected base, implementer, reviewer, and
  dependencies. Competing implementation owners require explicit parallel-work
  intent.

## Evidence and liveness

Record bounded command artifacts with cwd, allowlisted environment, start/end,
stdout/stderr, exit code, elapsed time, seed, head, and SHA-256. Preserve the
first attempt and attach immutable evidence by digest; AMQ summaries remain
concise and link the artifact.

Use `amq-squad evidence run TASK --profile PROFILE --session SESSION --me ACTOR
--subject TEXT --attempt-id ID -- COMMAND [ARG...]` for shell-free execution.
The command requires the active structured task assignee, binds the executable
bytes and exact task digest, stores immutable process/outcome/summary records,
then links their digests to the task with compare-and-swap. Reusing an attempt
ID returns the original result only when the complete request identity matches;
otherwise it conflicts. Use `evidence show`, `list`, and `lookup` for bounded
read-only projections and `evidence recover` for explicit journal recovery.
Reports use only the task's recorded dispatch sender/thread and carry structured
task, attempt, and evidence context; report failure never erases the evidence.

Fresh activity suppresses escalation only when the heartbeat comes from the
admitted profile/session and exactly matches the canonical task and assignee.
Only the bounded phase catalog is authoritative; coding and testing receive
explicitly longer windows. Malformed, mismatched, arbitrary, stale, or
future-skewed claims never suppress. Use finite `monitor --timeout` and/or
`--max-ticks` bounds and consume the schema-versioned `monitor_final` snapshot
and `exit_reason`, not NDJSON stream order.

## Gates and lifecycle planning

Task completion atomically records one exact completion generation, canonical
DONE report intent, and any exact task-scoped gate-request correlation. A still
open human decision is preserved as unsuppressed; only durable answered,
closed, withdrawn, or superseded request generations may clear attention.
Exact reconciliation repeats are idempotent and a different request identity
conflicts.

`verify release-plan` is read-only. It freezes the canonical absolute
project/worktree, exact repository and remote URL identity, default branch,
candidate SHA, protected/default branch taxonomy, non-force branch and tag
refspecs, annotated/signing policy, immutable preflight evidence, and fully
noninteractive repository-bound release title/notes/draft/prerelease/latest
policy. Every git command uses `git -C <project>`. Before any push, verification
checks that the named remote resolves to the frozen URL; file-based notes also
freeze a canonical path and require their exact SHA-256. Each action has a
literal newline-separated `Action:` and `Target:` body. Verification
distinguishes annotated tag-object identity from the peeled commit, verifies
signatures when required, and checks remote branch, tag-object, and peeled SHA
separately.
Planning and verification never execute a push, merge, tag, release, external
send, or destructive action.

Common entrypoints: `status`, `doctor`, `task`, `activity`, `monitor`, `gate`,
`evidence`, `verify`, `context`, `amq`, `resume`, `stop`, and `archive`.
