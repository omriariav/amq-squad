# Migrating to amq-squad 2.0.0

2.0.0 is a breaking release. The breaking surface is intentionally small and
mechanical: a set of long-deprecated verbs is removed, and the Go module path
gains the `/v2` suffix that Go requires for a v2+ module. Your on-disk team and
session state does **not** need to be migrated.

This guide covers everything you have to change.

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

## 4. Shell completion

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
