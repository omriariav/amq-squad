# Readiness rows

Readiness runs against what preparation actually wrote, not against the plan. A row
is `ready`, `blocked`, or `drifted`, and a blocked row carries a `fix` field naming
the exact command, scoped to your project and profile.

Read the fix field first. It is generated from your roster, so its role names and
paths are yours rather than placeholders.

| Row | Blocks when | Where to look |
|---|---|---|
| `profile` | the roster is absent, or drifted from the accepted manifest | `stages.md` |
| `member:<role>` | binary, model, effort, task ownership or tool identity differ from accepted | the row's accepted-vs-current values |
| `tool_policy:<role>` | capability sources or materialized policy differ from the audited set | regenerate the role policy |
| `worktree_isolation` | 2+ mutation-capable members share one directory without a recorded exception | `worktrees.md` |
| `bootstrap:<role>` | the role file, brief, rules, or goal binding cannot be resolved | the named path |
| `goal_binding` | no actionable binding for the visible lead | `stages.md` |
| `prepared_manifest` | the accepted generation is missing or superseded | re-run `--prepare` |

## Two properties worth relying on

**Readiness fails closed.** A condition it cannot verify is a blocker, not a warning.
That is deliberate: an unverifiable isolation or policy claim is exactly the case where
proceeding is expensive to undo.

**Readiness checks what spawn checks.** A row that reports ready is checked by the same
predicate the launch path uses, so ready-then-dead-at-spawn is not a state you should
see. If you do see it, that is a bug worth reporting with both outputs.

## Reading `--readiness-json`

Prefer the JSON projection in scripts and pass-through, and print it verbatim. It is
schema-versioned; the human table is not a contract.
