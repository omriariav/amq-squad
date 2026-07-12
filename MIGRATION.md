# Migrating to amq-squad 2.0.0

2.0.0 is a breaking release. The breaking surface is intentionally small and
mechanical: a set of long-deprecated verbs is removed, and the Go module path
gains the `/v2` suffix that Go requires for a v2+ module. Your on-disk team and
session state does **not** need to be migrated.

This guide covers everything you have to change.

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
