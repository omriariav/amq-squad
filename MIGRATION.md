# Migrating to amq-squad 2.0.0

2.0.0 is a breaking release. The breaking surface is intentionally small and
mechanical: a set of long-deprecated verbs is removed, and the Go module path
gains the `/v2` suffix that Go requires for a v2+ module. Your on-disk team and
session state does **not** need to be migrated.

This guide covers everything you have to change.

## What's new in 2.28.0: Simple Mode

2.28 removes the preparation, readiness, digest, receipt, bootstrap-ack, and
parallel-observation command layers. Launch has one command and one default-No
approval: run `amq-squad start` to inspect the complete plan, then repeat the
same coordinates with `--yes` only after approval. Rerun `start` after an
interruption; it keeps verified live roles and reconciles missing or stopped
ones without deleting the namespace.

The nine core workflows are `start`, `status`, `send`, `task`, `goal`, `gate`,
`verify`, `down`, and `doctor`. The public CLI also retains census-approved
setup, restore, utility, and diagnostic exceptions. The complete retained list
and rationale live in the
[v2.28.0 release notes](docs/v2.28.0-release-notes.md).

Removed commands have no aliases. Update scripts and runbooks mechanically:

| Removed v2.27 surface | v2.28 replacement |
|---|---|
| `context`, `history`, `console`, `monitor`, `activity` | Use scoped `status --json` and `doctor`; inspect durable mail with exact-root raw AMQ commands. |
| `collect`, `threads`, `thread` | Use `amq drain/list/read/thread --root <exact-root> --me <handle> ...`. |
| `notify`, `notifications` | Use `status`, `operator status`, and typed `gate` workflows. |
| `bootstrap ack` | Removed with no replacement; process/pane liveness and actual work are the launch evidence. |
| `brief` | Edit the resolved active workstream brief directly, then rerun `start`. |
| `prune-panes`, `rm`, `archive` | Use `down` and inspect with `status`/`doctor`; durable session data is not deleted automatically. |
| `fork` | Select a fresh explicit session in the roster and run `start`. |
| `review-worktree` | Use the retained `worktree` workflow and repository workspace-safety rules. |
| `tmux-harness` | Run the relevant project test directly in an operator-created disposable tmux server. |
| `next` | Use `status --json` or `operator status --json` action projections. |

Task automation must use the flat `task add`, `task claim`, `task done`, and
`task list` lifecycle. Progress, blockers, review requests, and completion are
ordinary AMQ messages, not a second delivery or reconciliation state machine.
Existing v2.27 launch records remain compatibility input; old prepared and
receipt directories are ignored and are not auto-deleted.

## What's new in 2.28.1: AMQ 0.52.2 floor

**Action required.** Upgrade AMQ to 0.52.2 or newer before upgrading
amq-squad. Every 0.49.x, 0.50.x, and 0.51.x release is now below the supported
floor and is rejected fail-closed. Run `amq upgrade`, then stop and
resume/relaunch agents so their parent shells receive the complete current AMQ
identity tuple. Confirm the result with `amq-squad doctor`.

Upstream AMQ introduced a wake-doorbell retry ladder in v0.49.11 that re-rings
the doorbell on a backoff while an inbox stays undrained; busy Ink-based CLIs
(Claude Code, Codex) queue every injected repetition and flush them as
duplicate "AMQ doorbell" bursts when the turn ends
([avivsinai/agent-message-queue#426](https://github.com/avivsinai/agent-message-queue/issues/426)).
AMQ v0.52.2 bounds this to at most 4 reminders per unchanged inbox cohort
before parking, with a lifetime cap of 8 per continuously-undrained cohort
(upstream PR #428). Both real-AMQ matrices now run pinned `v0.52.2` and
`latest` only.

Upstream #424 (`--retry-until injected` presentation acknowledgement) and #422
(macOS EINTR kqueue watcher fix) remain open and are not adopted in this
release; amq-squad picks them up in v2.29.0 when AMQ 0.53.0 ships
(tracked on #654).

## What's new in 2.26.0: AMQ 0.51.1 floor

**Action required.** Upgrade AMQ to 0.51.1 or newer before upgrading
amq-squad. Every 0.49.x, 0.50.x, and 0.51.0 release is now below the supported
floor and is rejected rather than run through a compatibility path. Run
`amq upgrade`, then stop and resume/relaunch agents so their parent shells
receive the complete current AMQ identity tuple. Confirm the result with
`amq-squad doctor`.

The real-AMQ queue and wake matrices now run pinned `v0.51.1` and `latest`
only. Supported launches always use canonical exact-root authority and the
current wake flags; the pre-0.51 capability and root/session shims have been
removed. See [the AMQ 0.51.x assessment](docs/amq-0.51.x-assessment.md) for the
upstream-change mapping and the `amq wake check` evaluation.

## What's new in 2.25.1: AMQ 0.49.9 floor

**Action required if you pin AMQ below 0.49.9.** 2.25.1 raises the floor again:
AMQ 0.49.9 is now the minimum supported release, and the whole 0.49.0 through
0.49.8 range is no longer supported. Releases below 0.49.9 are rejected rather
than degraded, so upgrade `amq` before upgrading amq-squad. Run `amq upgrade`,
then `amq-squad doctor` to confirm the version check passes.

**The pinned CI lanes changed with it.** Both real-AMQ matrices now exercise
pinned `v0.49.9` and `latest` only; the `v0.49.0` and `v0.49.1` lanes are
retired along with the floor they proved. `latest` remains a
forward-compatibility canary and is not a support claim. The pinned lane is the
one that records what was actually proved: the version assertion is skipped when
the requested version is literally `latest`.

**Why the jump.** AMQ 0.49.7 (upstream `fab4c76`) changed `doctor` to audit the
reserved operator handle implicitly, so a mailbox repair now reports created
paths for the `user` roster entry alongside the mailbox you actually repaired.
Verification against 0.49.8 and 0.49.9 found the two releases indistinguishable
for this behaviour. Supporting a single current release instead of a floor plus
a tested range is what the raised floor buys.

**Existing session-root repair.** AMQ 0.49.8 introduced canonical authority
for exact roots and refuses writes when the selected root has no
`meta/config.json`. Older amq-squad sessions did not create that file. Bare
`amq-squad doctor` now detects a missing or stale exact-root authority config
without changing it. Repair one selected profile/workstream explicitly:

```sh
amq-squad doctor --fix-amq-root
```

The repair writes the configured member roster plus the enabled operator
handle atomically, then asks AMQ to materialize missing mailboxes.
`amq-squad resume --exec` performs the same repair automatically before
restarting watchers or agents and reports every created or written path.
`team.json` and goal-attempt schemas remain unchanged; only the AMQ session
root gains its authority config and any missing mailbox directories.

**Known upstream doorbell limitation.** AMQ owns the fixed coop-wake doorbell
text, which still says to run bare `amq drain --include-body`. From a repository
cwd whose `.amqrc` names a different initialized root, AMQ 0.49.8+ can refuse
that bare command even after the session root is repaired. amq-squad cannot
rewrite the upstream doorbell. Use the exact `--root` command in the generated
bootstrap routing block (the AMQ refusal also prints rooted recovery guidance).
amq-squad-owned wake and dispatch fallback prompts are rooted.

## What's new in 2.25.0: claim-once goal resume, AMQ 0.49.0 floor

**Action required if you pin AMQ below 0.49.0.** 2.25.0 raises the floor: AMQ
0.49.0 is the minimum supported release and compatibility is tested through
0.49.1. Releases older than 0.49.0 are rejected rather than degraded, so
upgrade `amq` before upgrading amq-squad. `latest` is exercised in CI as a
forward-compatibility canary and is not a support claim.

**No schema migration.** `team.json` and the goal-attempt record schema are
unchanged. The recovery-transition record's new fields are `omitempty`, so a
record written by an earlier version continues to serialise without them and
continues to be read as a legacy redelivery record — absence means legacy, and
a field a kind requires is refused on write rather than defaulted.

**One new refusal to be aware of.** A namespace holding a native recovery
claim now refuses to migrate until that claim is consumed, because the claim
key is derived from the namespace and rewriting profile/session without
recomputing it would leave a record that reads as absent — and absent means
"no prior claim", which is a second delivery. This replaces silent corruption
with an explicit refusal; consume or resolve the claim, then migrate.

Paused native `/goal` runs can now be resumed automatically under `SafeAuto`
policy via `goal supervise-resume`, with `--dry-run` as a side-effect-free
inspection path. Worker commands are delivered as the pane's own process
instead of being typed, so a long command can no longer be truncated into a
partial launch reported as success. `resume` previews now consult the
predicate admission enforces. Launch ids are stamped at capture so incomplete
records are distinguished from genuine identity mismatches.

See [the v2.25.0 release notes](docs/v2.25.0-release-notes.md) for the
complete issue-to-behavior map, the safety-evidence summary, and residual
risks.

## What's new in 2.24.0: squad reuse, enforced worktree isolation, advisory model routing

2.24.0 keeps the AMQ 0.42.1 compatibility floor; AMQ 0.46 delivery-model
features are version-gated and optional. `team.json` schema is unchanged —
every new field (`SharedCwdException`, catalog routing metadata) is additive
and optional, and existing profiles need no migration.

`run start --from-profile` clones an existing profile's roster into a new
session-pinned profile; `--no-session-pin` and `team member update` make
unpinned templates and in-place member maintenance first-class. A durable
WorktreePlan store backs a deterministic `worktree`
plan/materialize/activate/handoff/cleanup CLI, alongside a planning-level
fail-closed readiness gate for squads where 2+ mutation-capable developers
would otherwise share one working directory — record an explicit
`team shared-cwd-exception set "<reason>"` if that is intentional. Advisory
task-aware model/effort routing surfaces recommendations in the wizard and
preparation review; the operator always overrides. The NOC console's action
palette is wired through `internal/act` with staged preview. `verify
rebind`/`verify merge` prove tree or scoped-patch identity across a review
rebuild before accepting a carried review. Every scheduled `tmux run-shell -b`
helper is now silent and zero-exit by construction.

See [the v2.24.0 release notes](docs/v2.24.0-release-notes.md) for the
complete issue-to-behavior map and residual risks.

## What's new in 2.23.1: verified staged runtime identity

2.23.1 keeps the AMQ 0.42.1 compatibility floor and is validated against AMQ
0.45.0 and requires Go 1.25.12. Prepared staged launch now uses an explicit
immutable claim and the parent-owned `team member launch ROLE --claim ID`
transaction. Admission and replacement are lead-only; launch requires the
exact authorizing actor, and an exclusive reservation makes every claim
single-use before topology mutation. Runtime actions require one verified live
identity, and native terminals are bound to the live process controlling TTY.
The safe, idempotent `control_continue` pause-recovery action repeats the exact
pane-scoped continue once after an observed silent no-op; it never retries
topology. Command evidence follows real Git/Go command and subcommand option
boundaries for supported `-C` forms, while unknown wrappers fail closed.
Existing legacy preparations are not upgraded in place.

See [the v2.23.1 runtime migration guide](docs/v2.23.1-runtime-migration.md)
for staged admission, canonical recovery, tmux control-client recovery, and
command-evidence compatibility details. The verified #505 terminal root cause
and pause-recovery boundary are documented in
[the staged iTerm2 harness](docs/issue-505-staged-iterm2-harness.md).

## What's new in 2.20.0: AMQ 0.42.1 identity pins

amq-squad 2.20.0 requires **amq 0.42.1+**. This is the first supported AMQ
release for the complete injected identity contract. Upgrade AMQ, then stop
and resume/relaunch every agent so its parent shell is rebuilt; a child command
cannot repair stale inherited variables.

- Default-profile sessions use `AM_ROOT=AM_BASE_ROOT/AM_SESSION`, a non-empty
  `AM_SESSION`, and `AM_ME`.
- Named-profile sessions use their exact root with `AM_ROOT=AM_BASE_ROOT` and
  omit `AM_SESSION` entirely.

Run `amq-squad doctor` before `resume --exec` or `agent resume`. If it reports
a legacy or inconsistent AMQ identity pin, stop and relaunch instead of relying
on a bare child command. Until then, use the explicitly scoped
`amq-squad amq ... --project ... --profile ... --session ...` wrapper for
control-plane operations.

## What's new in 2.1.0 (additive; nothing to migrate)

2.1.0 ("orchestrator dogfood hardening") only adds commands and fixes — it
removes nothing and changes no on-disk format. New surface:

- **`amq-squad dispatch --session S --role R --kind todo --subject … --body-file ./task.md`**
  — the deterministic lead→child dispatch: a durable AMQ send to the
  workstream's resolved root PLUS a drain-only pane nudge, in one root-correct
  command. Use it instead of hand-rolling `amq send` + a manual nudge from a lead.
  Prefer `--body-file FILE` or `--body-file -` for code, commands, backticks,
  and `$()` syntax; the caller shell expands inline `--body` before execution.
- **`amq-squad amq send|reply|drain|watch|list|read|thread`** — root-resolving
  passthroughs so an EXTERNAL lead (a human-driven session with no `AM_ROOT`)
  reaches `.agent-mail/<session>` instead of the default `.agent-mail`.
- **`amq-squad resume --role a,b`** — resume only a subset of members.
- **`amq-squad rm|archive --stop-agents`** — one-command teardown of a live
  squad (SIGTERM the agents, close their panes, then remove). Plain `--force`
  now also names any live agents it leaves running.

Reliability fixes: `status` no longer reports `pane_alive:true` for a closed
pane; teardown never closes a pane whose id was reused by another agent; the
dispatch wake is pane-precise and submits cleanly on freshly-spawned panes; the
board ages cold ghost records out of its health rollup; `new profile`/`doctor`
flag a stale shared `team-rules.md` roster.

## 1. Module path: add `/v2`

Go requires a `/vN` suffix on the module path for v2 and later, so v1 and v2
resolve to distinct modules.

**Install:**

```sh
# before (1.x)
go install github.com/omriariav/amq-squad/cmd/amq-squad@latest

# 2.0+
go install github.com/omriariav/amq-squad/v2/cmd/amq-squad@latest
```

**If you import amq-squad as a library**, update every import prefix:

```go
// before
import "github.com/omriariav/amq-squad/internal/team"

// 2.0+
import "github.com/omriariav/amq-squad/v2/internal/team"
```

(amq-squad's packages are `internal/`, so this only affects a fork or vendored
copy; the public consumer is the `amq-squad` binary.)

Nothing else about the binary's name, flags, or behavior changes from the
rename — `amq-squad version` still reports the same way.

## 2. Removed verbs

This table records the 2.0 migration as shipped. Commands named here may have
been removed since; see the v2.28 table above for current replacements.

Six verbs that were deprecated through the 1.x line are removed. Invoking one
now returns a **usage error (exit 1)** — a clear "unknown command", not a
silent success and no migration hint. Switch to the replacement:

| Removed verb | Use instead |
| --- | --- |
| `amq-squad down` | `amq-squad stop` |
| `amq-squad launch <binary>` | `amq-squad agent up <binary>` |
| `amq-squad restore` (print mode) | `amq-squad history` |
| `amq-squad restore --exec --role R` | `amq-squad agent resume R` |
| `amq-squad list` | `amq-squad status` (live) or `amq-squad history` (records) |
| `amq-squad team show` | `amq-squad up --dry-run` |
| `amq-squad team launch` | `amq-squad up` |
| `amq-squad team launch --fresh --session X` | `amq-squad fork --from <current> --as X` |

The replacement command shapes are unchanged from 1.x — only the deprecated
aliases are gone. The top-level `amq-squad --help` also lists this mapping
under "Removed in 2.0".

### `stop` exit-code reminder

`stop` (the replacement for `down`) performs the SIGTERM teardown and exits
`0`, or `3` on a partial run (some agents stopped, some failed). It preserves
all on-disk state, so the session stays resumable with `amq-squad resume`.

## 3. No team.json migration

The `team.json` schema is unchanged (still schema v3). Team configs written by
the 1.x line load as-is under 2.0 — there is no rewrite or conversion step.
The mutable-roster commands (`team member add/rm/list`) and the native task
store (`task ...`, stored under `.amq-squad/tasks/`) are additive and do not
alter the `team.json` shape.

## 4. Teardown now closes tmux panes

`rm` and `archive` now **close the torn-down agents' tmux panes by default**
(the session is being removed, so its panes are dead weight). Panes of agents
still considered live are never touched. Pass `--keep-panes` to leave them.

`stop` is unchanged by default (it keeps panes so final output stays readable
and `resume` re-creates them); pass `--close-panes` to close them on stop too.

Only panes amq-squad recorded are ever touched, and only for agents it believes
are down — so this is safe, but if a workflow relied on dead panes lingering
after `rm`/`archive`, add `--keep-panes`.

## 5. Check for version skew

amq-squad launches every agent into a shell that calls bare `amq-squad`
(resolved via `PATH`). If you run a different build than the one on `PATH`,
spawned agents silently use the `PATH` version. `amq-squad doctor` now warns on
this skew — run it after upgrading and align the two.

## 6. Shell completion

Regenerate your shell completion so the removed verbs stop being suggested:

```sh
amq-squad completion bash   # or zsh / fish
```

## Quick checklist

- [ ] Reinstall from the `/v2` path (`go install …/v2/cmd/amq-squad@latest`).
- [ ] Replace any `down` calls with `stop`.
- [ ] Replace any `launch` / `restore` / `list` / `team show` / `team launch`
      calls per the table above.
- [ ] Update import prefixes to `/v2` if you vendor or fork the source.
- [ ] Regenerate shell completion.
- [ ] No action needed for existing `team.json` / session state.
